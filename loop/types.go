package loop

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/guangshinhaha/shinfactory/adapter"
	"github.com/guangshinhaha/shinfactory/config"
	"github.com/guangshinhaha/shinfactory/dispatcher"
	"github.com/guangshinhaha/shinfactory/event"
	"github.com/guangshinhaha/shinfactory/internal/orderx"
	"github.com/guangshinhaha/shinfactory/internal/reducer"
	"github.com/guangshinhaha/shinfactory/internal/state"
	"github.com/guangshinhaha/shinfactory/internal/loopreg"
	"github.com/guangshinhaha/shinfactory/internal/statusfile"
	"github.com/guangshinhaha/shinfactory/internal/taskreg"
	"github.com/guangshinhaha/shinfactory/mise"
	"github.com/guangshinhaha/shinfactory/monitor"
	loopruntime "github.com/guangshinhaha/shinfactory/runtime"
	"github.com/guangshinhaha/shinfactory/worktree"
)

type State string

const (
	StateRunning  State = "running"
	StatePaused   State = "paused"
	StateDraining State = "draining"
	StateIdle     State = "idle"
)

// Type aliases — orderx is canonical.
type Stage = orderx.Stage
type Order = orderx.Order
type OrdersFile = orderx.OrdersFile

// Re-export orderx status constants for in-package use.
const (
	StageStatusPending   = orderx.StageStatusPending
	StageStatusActive    = orderx.StageStatusActive
	StageStatusMerging   = orderx.StageStatusMerging
	StageStatusCompleted = orderx.StageStatusCompleted
	StageStatusFailed    = orderx.StageStatusFailed
	StageStatusCancelled = orderx.StageStatusCancelled
)

const (
	OrderStatusActive    = orderx.OrderStatusActive
	OrderStatusCompleted = orderx.OrderStatusCompleted
	OrderStatusFailed    = orderx.OrderStatusFailed
)

type StageResultStatus string

const (
	StageResultCompleted StageResultStatus = "completed"
	StageResultFailed    StageResultStatus = "failed"
	StageResultCancelled StageResultStatus = "cancelled"
)

type ControlCommand struct {
	ID       string    `json:"id"`
	Action   string    `json:"action"`
	OrderID  string    `json:"order_id,omitempty"`
	Name     string    `json:"name,omitempty"`
	Target   string    `json:"target,omitempty"`
	Prompt   string    `json:"prompt,omitempty"`
	Value    string    `json:"value,omitempty"`
	TaskKey  string    `json:"task_key,omitempty"`
	Provider string    `json:"provider,omitempty"`
	Model    string    `json:"model,omitempty"`
	Skill    string    `json:"skill,omitempty"`
	At       time.Time `json:"at,omitempty"`
}

type ControlAck struct {
	ID      string             `json:"id"`
	Action  string             `json:"action"`
	Status  string             `json:"status"`
	Message string             `json:"message,omitempty"`
	Failure *ControlAckFailure `json:"failure,omitempty"`
	At      time.Time          `json:"at"`
}

type StageResult struct {
	OrderID      string
	StageIndex   int
	Attempt      int
	Status       StageResultStatus
	SessionID    string
	Generation   uint64
	IsSchedule   bool
	IsBootstrap  bool
	WorktreeName string
	WorktreePath string
	Error        error
	CompletedAt  time.Time
}

// cookIdentity holds the fields shared across all cook handle types.
type cookIdentity struct {
	orderID    string
	stageIndex int
	stage      Stage
	plan       []string
}

type cookHandle struct {
	cookIdentity
	orderStatus       orderx.OrderStatus
	session           loopruntime.SessionHandle
	worktreeName      string
	worktreePath      string
	attempt           int
	generation        uint64
	startedAt         time.Time
	displayName       string // stable kitchen name, preserved across retries
	dispatchedRuntime string // actual runtime used (may differ from stage.Runtime after fallback)
}

// pendingReviewCook is a completed cook waiting for human merge/reject.
type pendingReviewCook struct {
	cookIdentity
	worktreeName string
	worktreePath string
	sessionID    string
	reason       string
}

type WorktreeManager interface {
	Create(name string, opts ...worktree.CreateOpts) error
	Merge(name string, opts ...worktree.MergeOpts) error
	MergeRemoteBranch(branch string) error
	Cleanup(name string, opts ...worktree.CleanupOpts) error
	HasUnmergedCommits(name string) (bool, error)
}

type AdapterRunner interface {
	Run(ctx context.Context, adapterName, action string, options adapter.RunOptions) (string, error)
}

type MiseBuilder interface {
	Build(ctx context.Context, activeSummary mise.ActiveSummary, recentHistory []mise.HistoryItem) (mise.Brief, []string, bool, error)
}

type Monitor interface {
	RunOnce(ctx context.Context) ([]monitor.SessionMeta, error)
}

type Dependencies struct {
	Runtimes       map[string]loopruntime.Runtime
	Worktree       WorktreeManager
	Lander         Lander // nil uses the git and gh CLIs; only read in landing mode "pr"
	Adapter        AdapterRunner
	Mise           MiseBuilder
	Monitor        Monitor
	Registry       taskreg.Registry
	EventSink      dispatcher.SessionEventSink
	Logger         *slog.Logger
	Now            func() time.Time
	OrdersFile     string
	OrdersNextFile string
	StatusFile     string
}

type Loop struct {
	projectDir  string
	runtimeDir  string
	config      config.Config
	registry    taskreg.Registry
	registryErr error
	deps        Dependencies
	logger      *slog.Logger
	events      *event.LoopEventWriter

	// Components — field-grouping sub-structs.
	cooks         cookTracker
	cmds          cmdProcessor
	completionBuf completionBuffer
	evidence      evidenceTracker
	wake          chan struct{} // a background task finished; Run should cycle

	state             State
	registryStale     atomic.Bool
	registryFailCount int

	watcherWG          sync.WaitGroup
	shutdownOnce       sync.Once
	watcherCount       atomic.Int64
	// watchedDone maps a watched session ID to its Done channel, from the
	// watcher's start until its result is queued, so a drain can tell a
	// finished session whose result is in flight from one still running.
	watchedDone sync.Map
	dispatchGeneration atomic.Uint64

	bootstrapAttempts  int
	bootstrapExhausted bool
	bootstrapInFlight  *cookHandle

	orders               OrdersFile
	ordersLoaded         bool
	schedulePromoted     bool      // set when consumeOrdersNext promotes after a schedule dispatch
	scheduleNothingUntil time.Time // cooldown: suppress schedule re-spawn until this time
	lastPromotionError   string    // latest scheduler-output validation issue to inject into next schedule prompt
	droppedOrderIDs      []string  // unready order ids dropped since the last schedule prompt; survives clean promotions

	activeSummary  mise.ActiveSummary
	recentHistory  []mise.HistoryItem
	mergeQueue     *MergeQueue
	publishedState atomic.Pointer[LoopState]

	lastStatus statusfile.Status

	announceVersion string
	announcer       *loopreg.Registrar // nil until Announce; the machine-wide registry entry

	// V2 canonical state — event-sourced pipeline.
	canonical       state.State
	canonicalLoaded bool
	effectLedger    *reducer.EffectLedger
	eventCounter    atomic.Uint64

	limitHolds    usageLimitHolds
	limitRequeues usageLimitRequeues

	reconciledFailures []reconciledFailure
	lastMiseWarnings   []string
	backlogRetry       *backlogRetry // pending transient backlog failure; nil when synced
	lastChecksPoll     time.Time // last time parked PRs were re-checked for merge
	// checkWaitSince is when each parked order's PR first waited on GitHub's
	// mergeability or a red check's re-run; in memory, so a restart restarts
	// the wait.
	checkWaitSince map[string]time.Time
	checkWaitKind  map[string]string

	lastLoopFailure *LoopFailureEnvelope

	// Test hooks — nil in production. These allow tests to simulate crashes
	// at specific points in the state-persistence pipeline.
	TestFlushBarrier      func() // called between file writes in flushState()
	TestControlAckBarrier func() // called between command processing and ack write in processControlCommands()
}

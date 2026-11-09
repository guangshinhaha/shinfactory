package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guangshinhaha/shinfactory/adapter"
	"github.com/guangshinhaha/shinfactory/config"
	"github.com/guangshinhaha/shinfactory/dispatcher"
	"github.com/guangshinhaha/shinfactory/event"
	"github.com/guangshinhaha/shinfactory/internal/shellx"
	"github.com/guangshinhaha/shinfactory/internal/state"
	"github.com/guangshinhaha/shinfactory/internal/taskreg"
	"github.com/guangshinhaha/shinfactory/internal/testutil/fakeorca"
	"github.com/guangshinhaha/shinfactory/mise"
	loopruntime "github.com/guangshinhaha/shinfactory/runtime"
	"github.com/guangshinhaha/shinfactory/skill"
	"github.com/guangshinhaha/shinfactory/worktree"
)

// These fixtures run the real orca dispatcher and runtime against a fake `orca`
// CLI (internal/testutil/fakeorca), so nothing touches a real Orca.

// gitWorktrees is a WorktreeManager that makes real linked git worktrees, which
// the orca dispatcher requires, and records merges like fakeWorktree.
type gitWorktrees struct {
	fakeWorktree
	projectDir string
}

func (g *gitWorktrees) Create(name string, _ ...worktree.CreateOpts) error {
	g.created = append(g.created, name)
	return runGit(g.projectDir, "worktree", "add", "-b", name, filepath.Join(g.projectDir, ".worktrees", name))
}

func runGit(dir string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", dir,
		"-c", "user.name=fixture", "-c", "user.email=fixture@example.com"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}
	return nil
}

type orcaEnv struct {
	*integrationEnv
	fake  *fakeorca.Fake
	clock time.Time
	wts   *gitWorktrees
	cfg   config.Config
}

func orcaRegistry() taskreg.Registry {
	mk := func(name string) skill.SkillMeta {
		return skill.SkillMeta{Name: name, Path: "/skills/" + name, Frontmatter: skill.Frontmatter{Schedule: "fixture"}}
	}
	return taskreg.NewFromSkills([]skill.SkillMeta{mk("schedule"), mk("execute"), mk("quality"), mk("debrief")})
}

func newOrcaEnv(t *testing.T, orders OrdersFile) *orcaEnv {
	t.Helper()
	fake := fakeorca.New(t)

	projectDir := t.TempDir()
	if err := runGit(projectDir, "init", "-q", "-b", "main"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if err := runGit(projectDir, "commit", "-q", "--allow-empty", "-m", "init"); err != nil {
		t.Fatalf("git commit: %v", err)
	}
	runtimeDir := filepath.Join(projectDir, ".noodle")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ordersPath := filepath.Join(runtimeDir, "orders.json")
	if err := writeOrdersAtomic(ordersPath, orders); err != nil {
		t.Fatal(err)
	}

	cfg := localLandingConfig()
	cfg.Mode = "auto"
	cfg.Runtime.Default = "orca"
	cfg.Runtime.Orca.LimitRetrySeconds = 60

	wts := &gitWorktrees{projectDir: projectDir}
	env := &orcaEnv{fake: fake, clock: time.Now(), wts: wts, cfg: cfg}
	env.integrationEnv = &integrationEnv{wt: &wts.fakeWorktree, ordersPath: ordersPath, runtimeDir: runtimeDir, projectDir: projectDir}
	env.loop = env.newLoop()
	return env
}

// newLoop builds a loop over the env's directories with a fresh orca
// dispatcher, the way a restarted factory process would.
func (e *orcaEnv) newLoop() *Loop {
	od := dispatcher.NewOrcaDispatcher(dispatcher.OrcaDispatcherConfig{
		ProjectDir:   e.projectDir,
		RuntimeDir:   e.runtimeDir,
		Bin:          e.fake.Bin,
		RunID:        e.cfg.Runtime.Orca.RunID,
		PollInterval: 5 * time.Millisecond,
	})
	return New(e.projectDir, "noodle", e.cfg, Dependencies{
		Runtimes: map[string]loopruntime.Runtime{
			"process": newMockRuntime(),
			"orca":    loopruntime.NewOrcaRuntime(od, e.runtimeDir, 0),
		},
		Worktree:       e.wts,
		Adapter:        &fakeAdapterRunner{},
		Mise:           &fakeMise{brief: mise.Brief{Backlog: []adapter.BacklogItem{{ID: "42", Title: "t", Status: "open"}}}},
		Monitor:        fakeMonitor{},
		Registry:       orcaRegistry(),
		Now:            func() time.Time { return e.clock },
		OrdersFile:     e.ordersPath,
		OrdersNextFile: filepath.Join(e.runtimeDir, "orders-next.json"),
	})
}

// restart replaces the loop with a new one over the same project and runs the
// startup reconcile, as a restarted factory process does.
func (e *orcaEnv) restart(t *testing.T) {
	t.Helper()
	e.loop = e.newLoop()
	if err := e.loop.reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile after restart: %v", err)
	}
}

// cycleUntil runs loop cycles until cond holds. Orca workers settle on a
// background poll, so completions land between cycles.
func (e *orcaEnv) cycleUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := e.loop.Cycle(context.Background()); err != nil {
			t.Fatalf("cycle while waiting for %s: %v", what, err)
		}
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s\norca calls:\n%s", what, strings.Join(e.fake.Calls(), "\n"))
}

func (e *orcaEnv) orderStages(t *testing.T, orderID string) []string {
	t.Helper()
	of := e.readOrders(t)
	for _, o := range of.Orders {
		if o.ID == orderID {
			var out []string
			for _, s := range o.Stages {
				out = append(out, s.TaskKey+":"+string(s.Status))
			}
			return out
		}
	}
	return nil
}

func (e *orcaEnv) writeOrdersNext(t *testing.T, stages ...string) {
	t.Helper()
	var wire []map[string]string
	for _, s := range stages {
		wire = append(wire, map[string]string{"do": s, "with": "claude", "model": "claude-sonnet-5-5"})
	}
	data, _ := json.Marshal(map[string]any{"orders": []any{map[string]any{"id": "42", "title": "orca fixture", "stages": wire}}})
	if err := os.WriteFile(e.loop.deps.OrdersNextFile, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func (e *orcaEnv) activeSessionID(t *testing.T, orderID string) string {
	t.Helper()
	cook := e.loop.cooks.activeCooksByOrder[orderID]
	if cook == nil {
		t.Fatalf("no active cook for order %q", orderID)
	}
	return cook.session.ID()
}

func (e *orcaEnv) emitSessionEvent(t *testing.T, sessionID string, typ event.EventType, message string) {
	t.Helper()
	w, err := event.NewEventWriter(e.runtimeDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]string{"message": message})
	if err := w.Append(context.Background(), event.Event{Type: typ, Payload: payload, SessionID: sessionID, Timestamp: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
}

func scheduleOnlyOrders() OrdersFile {
	return OrdersFile{Orders: []Order{scheduleOrder("")}}
}

// settleLatest ends the most recently launched fake worker.
func (e *orcaEnv) settleLatest(t *testing.T, outcome string) string {
	t.Helper()
	id := e.fake.LastDispatch(t)
	e.fake.Settle(t, id, outcome)
	return id
}

// Done when 2: one order goes schedule -> execute -> quality -> debrief on
// orca, each stage a visible Orca worker launched with the factory's provider
// and model, in its own worktree.
func TestOrcaRuntimeRunsOrderAcrossStages(t *testing.T) {
	env := newOrcaEnv(t, scheduleOnlyOrders())

	env.cycleUntil(t, "schedule worker started", func() bool { return len(env.fake.Starts()) == 1 })
	if got := env.fake.Starts()[0]; !strings.Contains(got, "--worktree path:"+env.projectDir) || !strings.Contains(got, "--agent claude") {
		t.Fatalf("schedule worker-start = %q, want the project dir worktree and claude agent", got)
	}
	env.writeOrdersNext(t, "execute", "quality", "debrief")
	env.settleLatest(t, "succeeded")

	for i, stage := range []string{"execute", "quality", "debrief"} {
		want := i + 2
		env.cycleUntil(t, stage+" worker started", func() bool { return len(env.fake.Starts()) == want })
		start := env.fake.Starts()[want-1]
		for _, frag := range []string{"--agent claude", "--model claude-sonnet-5-5", "--worktree path:" + filepath.Join(env.projectDir, ".worktrees", "42-"+string(rune('0'+i))+"-"+stage)} {
			if !strings.Contains(start, frag) {
				t.Fatalf("%s worker-start = %q, missing %q", stage, start, frag)
			}
		}
		env.settleLatest(t, "succeeded")
	}
	env.cycleUntil(t, "order completed", func() bool { return env.orderStages(t, "42") == nil })

	// Once the order is done the factory schedules again, so there may be a fifth start.
	if got := len(env.fake.Starts()); got < 4 {
		t.Fatalf("worker-start calls = %d, want at least 4 (schedule, execute, quality, debrief)", got)
	}
	if len(env.wt.merged) == 0 {
		t.Fatalf("no stage worktree was merged; merged=%v", env.wt.merged)
	}
	spec := env.fake.Spec(t, 2)
	if !strings.Contains(spec, "NOODLE_PROJECT_DIR="+shellx.Quote(env.projectDir)) || !strings.Contains(spec, "--session ") {
		t.Fatalf("execute spec lacks the session identity the skills read:\n%s", spec)
	}
}

// A stage on orca still reports through the session event log: stage_yield
// turns a worker that ended badly into a completed stage, stage_fail rejects
// one the worker called a success.
func TestOrcaRuntimeStageEventsDecideTheResult(t *testing.T) {
	orders := OrdersFile{Orders: []Order{{
		ID: "42", Title: "events", Status: OrderStatusActive,
		Stages: []Stage{
			{TaskKey: "execute", Skill: "execute", Provider: "claude", Model: "claude-sonnet-5-5", Status: StageStatusPending},
			{TaskKey: "quality", Skill: "quality", Provider: "claude", Model: "claude-sonnet-5-5", Status: StageStatusPending},
		},
	}}}
	env := newOrcaEnv(t, orders)

	env.cycleUntil(t, "execute started", func() bool { return len(env.fake.Starts()) == 1 })
	sid := env.activeSessionID(t, "42")
	env.emitSessionEvent(t, sid, event.EventStageYield, "built it")
	env.settleLatest(t, "failed") // worker died after yielding
	env.cycleUntil(t, "quality started", func() bool { return len(env.fake.Starts()) == 2 })
	if got := env.orderStages(t, "42"); got[0] != "execute:completed" {
		t.Fatalf("stages = %v, want execute completed by stage_yield", got)
	}

	sid = env.activeSessionID(t, "42")
	env.emitSessionEvent(t, sid, event.EventStageFail, "Rejected: Done when missed")
	env.settleLatest(t, "succeeded")
	env.cycleUntil(t, "quality rejected", func() bool {
		of := env.readOrders(t)
		return len(of.Orders) == 1 && of.Orders[0].Status == OrderStatusFailed
	})
}

// Done when 3: a launch refused for usage drains the loop (nothing fails, no
// fallback to process), and the stage launches once a launch succeeds again.
func TestOrcaRuntimeDrainsOnLaunchLimitAndResumes(t *testing.T) {
	orders := OrdersFile{Orders: []Order{{
		ID: "42", Title: "limit at launch", Status: OrderStatusActive,
		Stages: []Stage{{TaskKey: "execute", Skill: "execute", Provider: "claude", Model: "claude-sonnet-5-5", Status: StageStatusPending}},
	}}}
	env := newOrcaEnv(t, orders)
	env.fake.SetLimit(t, true)
	process := env.loop.deps.Runtimes["process"].(*mockRuntime)

	for i := 0; i < 3; i++ {
		if err := env.loop.Cycle(context.Background()); err != nil {
			t.Fatalf("cycle: %v", err)
		}
	}
	if got := env.orderStages(t, "42"); len(got) != 1 || got[0] != "execute:pending" {
		t.Fatalf("stages = %v, want execute still pending while the limit holds", got)
	}
	if len(process.calls) != 0 {
		t.Fatalf("process runtime got %d dispatches; a usage limit must not fall back", len(process.calls))
	}
	if n := len(env.fake.Starts()); n != 1 {
		t.Fatalf("worker-start attempts during the hold = %d, want 1 (the hold stops retries)", n)
	}

	// The limit resets and the hold lapses: the next launch is the probe.
	env.fake.SetLimit(t, false)
	env.clock = env.clock.Add(2 * time.Minute)
	env.cycleUntil(t, "stage launched after reset", func() bool { return len(env.fake.Starts()) == 2 })
	env.settleLatest(t, "succeeded")
	env.cycleUntil(t, "order completed", func() bool { return env.orderStages(t, "42") == nil })
}

// Done when 3, mid-run: a worker that dies on a limit is requeued, not failed,
// and resumes in the same worktree after the hold.
func TestOrcaRuntimeRequeuesStageWhoseWorkerDiedOnLimit(t *testing.T) {
	orders := OrdersFile{Orders: []Order{{
		ID: "42", Title: "limit mid-run", Status: OrderStatusActive,
		Stages: []Stage{{TaskKey: "execute", Skill: "execute", Provider: "claude", Model: "claude-sonnet-5-5", Status: StageStatusPending}},
	}}}
	env := newOrcaEnv(t, orders)

	env.cycleUntil(t, "execute started", func() bool { return len(env.fake.Starts()) == 1 })
	id := env.fake.LastDispatch(t)
	env.fake.SetTranscript(t, id, "Claude usage limit reached.")
	env.fake.Settle(t, id, "failed")

	env.cycleUntil(t, "stage requeued", func() bool {
		got := env.orderStages(t, "42")
		return len(got) == 1 && got[0] == "execute:pending" && env.loop.cooks.activeCooksByOrder["42"] == nil
	})
	if of := env.readOrders(t); of.Orders[0].Status != OrderStatusActive {
		t.Fatalf("order status = %q, want active (a limit is not a failure)", of.Orders[0].Status)
	}
	for i := 0; i < 3; i++ {
		_ = env.loop.Cycle(context.Background())
	}
	if n := len(env.fake.Starts()); n != 1 {
		t.Fatalf("worker-start calls while held = %d, want 1", n)
	}

	env.clock = env.clock.Add(2 * time.Minute)
	env.cycleUntil(t, "stage relaunched", func() bool { return len(env.fake.Starts()) == 2 })
	if first, second := env.fake.Starts()[0], env.fake.Starts()[1]; wtOf(first) != wtOf(second) {
		t.Fatalf("relaunch used worktree %q, want the same worktree %q", wtOf(second), wtOf(first))
	}
	env.settleLatest(t, "succeeded")
	env.cycleUntil(t, "order completed", func() bool { return env.orderStages(t, "42") == nil })
}

func wtOf(startCall string) string {
	_, after, _ := strings.Cut(startCall, "--worktree ")
	field, _, _ := strings.Cut(after, " ")
	return field
}

// Done when 4: orca is never required. With the orca CLI missing, a config that
// defaults to orca resolves to process and orca is not offered.
func TestOrcaRuntimeAbsentFallsBackToProcess(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	cfg := localLandingConfig()
	cfg.Runtime.Default = "orca"
	for _, name := range cfg.AvailableRuntimes() {
		if name == "orca" {
			t.Fatalf("orca offered without the orca CLI on PATH: %v", cfg.AvailableRuntimes())
		}
	}
	l := &Loop{config: cfg, deps: Dependencies{Runtimes: map[string]loopruntime.Runtime{"process": newMockRuntime()}}}
	if got := l.stageRuntime(Stage{}); got != "process" {
		t.Fatalf("stageRuntime with orca unwired = %q, want process", got)
	}
	if got := l.stageRuntime(Stage{Runtime: "process"}); got != "process" {
		t.Fatalf("explicit process stage = %q, want process", got)
	}
}

// The safety fact for "runs exactly as today": no config other than a default of
// orca changes which runtime a stage gets. A sprites default still leaves a
// stage without a runtime on process, as before.
func TestStageRuntimeKeepsLegacyDefaultsForOtherRuntimes(t *testing.T) {
	for _, def := range []string{"", "process", "sprites", "cursor"} {
		cfg := localLandingConfig()
		cfg.Runtime.Default = def
		l := &Loop{config: cfg, deps: Dependencies{Runtimes: map[string]loopruntime.Runtime{"process": newMockRuntime()}}}
		if got := l.stageRuntime(Stage{}); got != "process" {
			t.Errorf("default %q: stage without runtime = %q, want process", def, got)
		}
		if got := l.stageRuntime(Stage{Runtime: "sprites"}); got != "sprites" {
			t.Errorf("default %q: explicit sprites stage = %q, want sprites", def, got)
		}
	}
}

// A worker that crashes with text that merely resembles a limit message is a
// failed stage, not a held runtime.
func TestOrcaRuntimeCrashMentioningLimitWordFailsTheStage(t *testing.T) {
	orders := OrdersFile{Orders: []Order{{
		ID: "42", Title: "crash", Status: OrderStatusActive,
		Stages: []Stage{{TaskKey: "execute", Skill: "execute", Provider: "claude", Model: "claude-sonnet-5-5", Status: StageStatusPending}},
	}}}
	env := newOrcaEnv(t, orders)
	env.cycleUntil(t, "execute started", func() bool { return len(env.fake.Starts()) == 1 })
	id := env.fake.LastDispatch(t)
	env.fake.SetTranscript(t, id, "Panic: TestRateLimiter failed, rate limit not enforced; limit reached check crashed")
	env.fake.Settle(t, id, "failed")
	env.cycleUntil(t, "stage failed", func() bool {
		of := env.readOrders(t)
		return len(of.Orders) == 1 && of.Orders[0].Status == OrderStatusFailed
	})
	if _, held := env.loop.limitHolds.heldUntil("orca", env.clock); held {
		t.Fatal("runtime held after an ordinary crash")
	}
}

func oneStageOrder() OrdersFile {
	return OrdersFile{Orders: []Order{{
		ID: "42", Title: "one stage", Status: OrderStatusActive,
		Stages: []Stage{{TaskKey: "execute", Skill: "execute", Provider: "claude", Model: "claude-sonnet-5-5", Status: StageStatusPending}},
	}}}
}

// Done when 1: the factory uses its own Orca run. It creates one, passes it on
// every worker-start, and follows its workers inside that run only.
func TestOrcaRuntimeUsesItsOwnRun(t *testing.T) {
	env := newOrcaEnv(t, scheduleOnlyOrders())
	env.cycleUntil(t, "schedule worker started", func() bool { return len(env.fake.Starts()) == 1 })
	env.writeOrdersNext(t, "execute")
	env.settleLatest(t, "succeeded")
	env.cycleUntil(t, "execute worker started", func() bool { return len(env.fake.Starts()) == 2 })
	env.settleLatest(t, "succeeded")
	env.cycleUntil(t, "order completed", func() bool { return env.orderStages(t, "42") == nil })

	runs := env.fake.Runs()
	if len(runs) != 1 {
		t.Fatalf("runs created = %v, want exactly one for the whole loop", runs)
	}
	for _, start := range env.fake.Starts() {
		if !strings.Contains(start, "--run "+runs[0]+" ") {
			t.Errorf("worker-start = %q, want --run %s", start, runs[0])
		}
	}
	for _, call := range env.fake.Calls() {
		if strings.HasPrefix(call, "orchestration worker-list") && !strings.Contains(call, "--run "+runs[0]) {
			t.Errorf("worker-list = %q, want it scoped to --run %s", call, runs[0])
		}
	}
}

// A run id in .noodle.toml is used as given: nothing is created.
func TestOrcaRuntimeUsesConfiguredRun(t *testing.T) {
	env := newOrcaEnv(t, oneStageOrder())
	env.cfg.Runtime.Orca.RunID = "run_from_config"
	env.loop = env.newLoop()
	env.cycleUntil(t, "worker started", func() bool { return len(env.fake.Starts()) == 1 })
	if got := env.fake.Starts()[0]; !strings.Contains(got, "--run run_from_config ") {
		t.Fatalf("worker-start = %q, want --run run_from_config", got)
	}
	if runs := env.fake.Runs(); len(runs) != 0 {
		t.Fatalf("runs created = %v, want none when run_id is configured", runs)
	}
}

// Done when 2: a restarted loop stops the worker its previous process left
// running, relaunches the stage in the same worktree and the same run, and
// does not create a second run.
func TestOrcaRuntimeRestartStopsOrphanedWorker(t *testing.T) {
	env := newOrcaEnv(t, oneStageOrder())
	// The restart injects the schedule order; room for it beside the stage.
	env.cfg.Concurrency.MaxConcurrency = 3
	env.loop = env.newLoop()
	env.cycleUntil(t, "worker started", func() bool { return len(env.fake.Starts()) == 1 })
	orphan := env.fake.LastDispatch(t)
	if got := env.fake.Worker(t, orphan); got != "in_progress" {
		t.Fatalf("worker %s = %q before restart, want in_progress", orphan, got)
	}

	// The factory process dies and a new one starts over the same project.
	env.restart(t)
	stageStarts := func() []string {
		var out []string
		for _, start := range env.fake.Starts() {
			if strings.Contains(start, ".worktrees/42-0-execute") {
				out = append(out, start)
			}
		}
		return out
	}
	env.cycleUntil(t, "stage relaunched", func() bool { return len(stageStarts()) == 2 })

	if got := env.fake.Worker(t, orphan); got == "in_progress" {
		t.Fatalf("orphaned worker %s still running after restart", orphan)
	}
	first, second := stageStarts()[0], stageStarts()[1]
	if wtOf(first) != wtOf(second) {
		t.Fatalf("relaunch worktree = %q, want the kept worktree %q", wtOf(second), wtOf(first))
	}
	if runs := env.fake.Runs(); len(runs) != 1 || !strings.Contains(second, "--run "+runs[0]+" ") {
		t.Fatalf("runs = %v, relaunch = %q; want the one run reused", runs, second)
	}
}

// Recovery only touches workers the factory launched: a worker someone else
// started in a shared, configured run is left running.
func TestOrcaRuntimeRestartLeavesForeignWorkersAlone(t *testing.T) {
	env := newOrcaEnv(t, oneStageOrder())
	env.cfg.Runtime.Orca.RunID = "run_shared"
	env.cfg.Concurrency.MaxConcurrency = 3 // the restart injects the schedule order; room for it beside the stage
	env.loop = env.newLoop()
	env.cycleUntil(t, "worker started", func() bool { return len(env.fake.Starts()) == 1 })
	ours := env.fake.LastDispatch(t)
	if out, err := exec.Command(env.fake.Bin, "orchestration", "worker-start", "--run", "run_shared").CombinedOutput(); err != nil {
		t.Fatalf("start foreign worker: %v: %s", err, out)
	}
	foreign := env.fake.LastDispatch(t)

	env.restart(t)
	env.cycleUntil(t, "stage relaunched", func() bool {
		n := 0
		for _, start := range env.fake.Starts() {
			if strings.Contains(start, ".worktrees/42-0-execute") {
				n++
			}
		}
		return n == 2
	})
	if got := env.fake.Worker(t, ours); got == "in_progress" {
		t.Fatalf("our orphan %s still running", ours)
	}
	if got := env.fake.Worker(t, foreign); got != "in_progress" {
		t.Fatalf("foreign worker %s = %q, want untouched", foreign, got)
	}
}

// Done when 3: the hold waits until the reset time in the error, not the retry
// interval, and the first launch after that clears it.
func TestOrcaRuntimeHoldWaitsUntilParsedResetTime(t *testing.T) {
	env := newOrcaEnv(t, oneStageOrder())
	env.clock = time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	env.fake.SetLimitMessage(t, true, "Claude usage limit reached. Your limit will reset at 3pm (UTC).")

	for i := 0; i < 2; i++ {
		if err := env.loop.Cycle(context.Background()); err != nil {
			t.Fatalf("cycle: %v", err)
		}
	}
	until, held := env.loop.limitHolds.heldUntil("orca", env.clock)
	want := time.Date(2026, 11, 9, 15, 0, 0, 0, time.UTC).Add(usageLimitResetSlack)
	if !held || !until.Equal(want) {
		t.Fatalf("hold until %v (held=%v), want %v from the 3pm in the error", until, held, want)
	}

	// Past the 60s retry interval but before the reset: still no launch attempt.
	env.clock = env.clock.Add(4 * time.Hour)
	env.fake.SetLimit(t, false)
	for i := 0; i < 3; i++ {
		_ = env.loop.Cycle(context.Background())
	}
	if n := len(env.fake.Starts()); n != 1 {
		t.Fatalf("worker-start attempts before the reset = %d, want 1", n)
	}

	env.clock = env.clock.Add(time.Hour + 2*time.Minute)
	env.cycleUntil(t, "launch after reset", func() bool { return len(env.fake.Starts()) == 2 })
	if _, held := env.loop.limitHolds.heldUntil("orca", env.clock); held {
		t.Fatal("hold still set after a successful launch")
	}
}

// Done when 4: a relaunch after a usage limit starts a clean attempt. The
// cancelled run's completion time and error don't carry over.
func TestOrcaRuntimeRelaunchClearsStaleAttemptFields(t *testing.T) {
	env := newOrcaEnv(t, oneStageOrder())
	env.cycleUntil(t, "execute started", func() bool { return len(env.fake.Starts()) == 1 })
	id := env.fake.LastDispatch(t)
	env.fake.SetTranscript(t, id, "Claude usage limit reached.")
	env.fake.Settle(t, id, "failed")
	env.cycleUntil(t, "stage requeued", func() bool {
		got := env.orderStages(t, "42")
		return len(got) == 1 && got[0] == "execute:pending" && env.loop.cooks.activeCooksByOrder["42"] == nil
	})
	cancelled := env.loop.canonical.Orders["42"].Stages[0].Attempts[0]
	if cancelled.Error == "" || cancelled.CompletedAt.IsZero() {
		t.Fatalf("cancelled attempt = %+v, want error and completion time set (the fixture's premise)", cancelled)
	}

	env.clock = env.clock.Add(2 * time.Minute)
	env.cycleUntil(t, "stage relaunched", func() bool { return len(env.fake.Starts()) == 2 })
	attempts := env.loop.canonical.Orders["42"].Stages[0].Attempts
	if len(attempts) != 1 {
		t.Fatalf("attempts = %+v, want the relaunch to reuse the attempt record", attempts)
	}
	got := attempts[0]
	if got.Error != "" || !got.CompletedAt.IsZero() || got.ExitCode != nil || got.Status != state.AttemptRunning {
		t.Fatalf("relaunched attempt = %+v, want running with no stale error, completion time or exit code", got)
	}
}

// Done when 5: a worker that finished successfully but printed a usage-limit
// message is a success. It holds nothing and the next stage launches at once.
func TestOrcaRuntimeSucceededWorkerPrintingLimitIsNotHeld(t *testing.T) {
	orders := OrdersFile{Orders: []Order{{
		ID: "42", Title: "mentions limits", Status: OrderStatusActive,
		Stages: []Stage{
			{TaskKey: "execute", Skill: "execute", Provider: "claude", Model: "claude-sonnet-5-5", Status: StageStatusPending},
			{TaskKey: "quality", Skill: "quality", Provider: "claude", Model: "claude-sonnet-5-5", Status: StageStatusPending},
		},
	}}}
	env := newOrcaEnv(t, orders)
	env.cycleUntil(t, "execute started", func() bool { return len(env.fake.Starts()) == 1 })
	id := env.fake.LastDispatch(t)
	env.fake.SetTranscript(t, id, "Done. Note: a teammate saw Claude usage limit reached. Your limit will reset at 3pm (UTC) earlier.")
	env.fake.Settle(t, id, "succeeded")

	env.cycleUntil(t, "quality started with no clock advance", func() bool { return len(env.fake.Starts()) == 2 })
	if _, held := env.loop.limitHolds.heldUntil("orca", env.clock); held {
		t.Fatal("runtime held after a successful worker")
	}
	if got := env.orderStages(t, "42"); got[0] != "execute:completed" {
		t.Fatalf("stages = %v, want execute completed", got)
	}
}

// Issue #72: a configured orca default must win over a runtime the scheduler
// set by habit; only a stage that states a reason keeps its own runtime.
func TestStageRuntimeOrcaDefaultWinsUnlessReasonStated(t *testing.T) {
	cfg := localLandingConfig()
	cfg.Runtime.Default = "orca"
	l := &Loop{config: cfg, deps: Dependencies{Runtimes: map[string]loopruntime.Runtime{
		"process": newMockRuntime(), "orca": newMockRuntime(),
	}}}
	reason := map[string]json.RawMessage{"runtime_reason": json.RawMessage(`"needs host docker"`)}
	cases := []struct {
		name  string
		stage Stage
		want  string
	}{
		{"no runtime", Stage{}, "orca"},
		{"habitual process", Stage{Runtime: "process"}, "orca"},
		{"process with reason", Stage{Runtime: "process", Extra: reason}, "process"},
		{"blank reason", Stage{Runtime: "process", Extra: map[string]json.RawMessage{"runtime_reason": json.RawMessage(`"  "`)}}, "orca"},
	}
	for _, tc := range cases {
		if got := l.stageRuntime(tc.stage); got != tc.want {
			t.Errorf("%s: stageRuntime = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A worker whose output merely mentions rate_limit_error is a real failure.
func TestOrcaRuntimeBareRateLimitErrorFailsTheStage(t *testing.T) {
	env := newOrcaEnv(t, oneStageOrder())
	env.cycleUntil(t, "execute started", func() bool { return len(env.fake.Starts()) == 1 })
	id := env.fake.LastDispatch(t)
	env.fake.SetTranscript(t, id, "Tests failed: expected rate_limit_error from the mock API")
	env.fake.Settle(t, id, "failed")
	env.cycleUntil(t, "stage failed", func() bool {
		of := env.readOrders(t)
		return len(of.Orders) == 1 && of.Orders[0].Status == OrderStatusFailed
	})
	if _, held := env.loop.limitHolds.heldUntil("orca", env.clock); held {
		t.Fatal("runtime held by a failure that only mentions rate_limit_error")
	}
}

// A stage that keeps dying on the limit is requeued a bounded number of times,
// then fails instead of relaunching forever.
func TestOrcaRuntimeUsageLimitRequeuesAreBounded(t *testing.T) {
	env := newOrcaEnv(t, oneStageOrder())
	const bound = 2
	env.loop.limitRequeues.cap = bound
	for i := 1; i <= bound; i++ {
		env.cycleUntil(t, "execute started", func() bool { return len(env.fake.Starts()) == i })
		id := env.fake.LastDispatch(t)
		env.fake.SetTranscript(t, id, "Claude usage limit reached.")
		env.fake.Settle(t, id, "failed")
		env.cycleUntil(t, "stage requeued", func() bool {
			got := env.orderStages(t, "42")
			return len(got) == 1 && got[0] == "execute:pending" && env.loop.cooks.activeCooksByOrder["42"] == nil
		})
		env.clock = env.clock.Add(2 * time.Minute)
	}
	n := bound + 1
	env.cycleUntil(t, "execute started again", func() bool { return len(env.fake.Starts()) == n })
	id := env.fake.LastDispatch(t)
	env.fake.SetTranscript(t, id, "Claude usage limit reached.")
	env.fake.Settle(t, id, "failed")
	env.cycleUntil(t, "stage failed", func() bool {
		of := env.readOrders(t)
		return len(of.Orders) == 1 && of.Orders[0].Status == OrderStatusFailed
	})
}

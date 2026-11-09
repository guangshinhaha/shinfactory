package loop

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/guangshinhaha/shinfactory/dispatcher"
	"github.com/guangshinhaha/shinfactory/internal/state"
	"github.com/guangshinhaha/shinfactory/internal/stringx"
)

// usageLimitHolds records runtimes whose account is out of usage. While a
// runtime is held the loop launches nothing on it: stages stay pending instead
// of failing. When the hold lapses the next launch is the probe: it clears the
// hold if it succeeds and renews it if the limit is still there.
type usageLimitHolds struct {
	mu    sync.Mutex
	until map[string]time.Time
}

// heldUntil returns when the runtime's hold lapses, and whether it is held at now.
func (h *usageLimitHolds) heldUntil(runtime string, now time.Time) (time.Time, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	until, ok := h.until[stringx.Normalize(runtime)]
	return until, ok && now.Before(until)
}

func (h *usageLimitHolds) hold(runtime string, until time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.until == nil {
		h.until = map[string]time.Time{}
	}
	h.until[stringx.Normalize(runtime)] = until
}

// release clears the runtime's hold and reports whether there was one.
func (h *usageLimitHolds) release(runtime string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	key := stringx.Normalize(runtime)
	_, was := h.until[key]
	delete(h.until, key)
	return was
}

// maxUsageLimitRequeues bounds how often one stage may die on a usage limit
// and be requeued. A stage that keeps dying that way (for instance one whose
// own output trips the detector) fails instead of relaunching forever.
const maxUsageLimitRequeues = 5

// usageLimitRequeueCap is the requeue bound; tests lower it to stay quick.
func (l *Loop) usageLimitRequeueCap() int {
	if l.limitRequeues.cap > 0 {
		return l.limitRequeues.cap
	}
	return maxUsageLimitRequeues
}

// usageLimitRequeues counts consecutive requeues per stage: a stage that ends
// any other way starts its count again.
type usageLimitRequeues struct {
	mu  sync.Mutex
	n   map[string]int
	cap int // overrides maxUsageLimitRequeues when positive
}

func (r *usageLimitRequeues) bump(orderID string, stageIndex int) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.n == nil {
		r.n = map[string]int{}
	}
	key := fmt.Sprintf("%s/%d", orderID, stageIndex)
	r.n[key]++
	return r.n[key]
}

// reset forgets the stage's requeues; its last run did not die on the limit.
func (r *usageLimitRequeues) reset(orderID string, stageIndex int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.n, fmt.Sprintf("%s/%d", orderID, stageIndex))
}

// usageLimitResetSlack keeps the probe launch from landing in the second the
// limit resets, when the account may not have refilled yet.
const usageLimitResetSlack = time.Minute

// stageRuntime resolves the runtime an order stage launches on: its own, else
// process, as before the orca runtime existed. A configured default of "orca"
// wins over the stage's own runtime unless the stage states
// extra.runtime_reason, so a habitual scheduler "process" cannot hide cooks.
func (l *Loop) stageRuntime(stage Stage) string {
	name := stringx.Normalize(stage.Runtime)
	if stringx.Normalize(l.config.Runtime.Default) == "orca" {
		// A configured orca default wins over a runtime the scheduler set by
		// habit; the stage keeps its own only with a stated runtime_reason.
		if strings.TrimSpace(legacyExtraString(stage.Extra, "runtime_reason")) == "" {
			name = ""
		}
	} else if name == "" {
		name = "process"
	}
	return l.dispatchRuntime(name)
}

// dispatchRuntime resolves a request's runtime: its own, else the configured
// default, else process. A configured "orca" that is not wired (no orca CLI)
// resolves to process, so the factory runs as it does without the orca runtime.
// A sensitive repo never uses orca (when process is wired): orca cannot enforce routing.
func (l *Loop) dispatchRuntime(requested string) string {
	name := stringx.Normalize(requested)
	if name == "" {
		name = stringx.Normalize(l.config.Runtime.Default)
	}
	if name == "" {
		name = "process"
	}
	if name == "orca" && l.config.Routing.Sensitive && l.deps.Runtimes["process"] != nil {
		// Orca starts the agent, so the factory cannot pass it tool denials or a PATH shim.
		return "process"
	}
	if name == "orca" && l.deps.Runtimes["orca"] == nil && l.deps.Runtimes["process"] != nil {
		return "process"
	}
	return name
}

func (l *Loop) runtimeHeld(runtimeName string) bool {
	until, held := l.limitHolds.heldUntil(runtimeName, l.deps.Now())
	if held {
		l.logger.Debug("runtime held by usage limit", "runtime", runtimeName, "until", until)
	}
	return held
}

func (l *Loop) holdRuntime(runtimeName, reason string) {
	now := l.deps.Now()
	until := now.Add(l.config.Runtime.Orca.LimitRetry())
	source := "retry interval"
	if reset, ok := dispatcher.ParseUsageLimitReset(reason, now); ok {
		until = reset.Add(usageLimitResetSlack)
		source = "reset time in the error"
	}
	l.limitHolds.hold(runtimeName, until)
	l.logger.Warn("runtime out of usage; holding launches",
		"runtime", runtimeName, "until", until, "until_from", source, "reason", reason)
}

func (l *Loop) releaseRuntime(runtimeName string) {
	if l.limitHolds.release(runtimeName) {
		l.logger.Info("runtime launched again after usage limit; resuming", "runtime", runtimeName)
	}
}

// requeueAfterUsageLimit handles a stage whose worker died on a usage limit:
// hold the runtime and put the stage back to pending. The worktree is kept, so
// the relaunch resumes from its commits instead of starting over.
func (l *Loop) requeueAfterUsageLimit(cook *cookHandle) error {
	runtimeName := nonEmpty(cook.dispatchedRuntime, l.stageRuntime(cook.stage))
	reason := nonEmpty(cook.session.Outcome().Reason, "usage limit reached")
	l.holdRuntime(runtimeName, reason)
	// The account ran out, not the stage: the launch must not eat max_attempts.
	if order, ok := l.orderByID(cook.orderID); ok {
		l.refundStageLaunch(cook.orderID, spendTaskKey(cook.stage), order.Round)
	}
	if n := l.limitRequeues.bump(cook.orderID, cook.stageIndex); n > l.usageLimitRequeueCap() {
		l.limitRequeues.reset(cook.orderID, cook.stageIndex)
		return l.failStage(nil, cook, fmt.Sprintf("stage was requeued after the usage limit %d times: %s", n, reason))
	}
	if err := l.resetStageToPending(cook.orderID, cook.stageIndex, reason); err != nil {
		return err
	}
	l.logger.Warn("stage requeued after usage limit",
		"order", cook.orderID, "stage", cook.stageIndex, "session", cook.session.ID())
	return nil
}

// resetStageToPending returns a dispatched stage to pending in both the
// canonical state and orders.json, cancelling its in-flight attempt.
func (l *Loop) resetStageToPending(orderID string, stageIndex int, reason string) error {
	now := timeNowUTC(l.deps.Now)
	if node, ok := l.canonical.Orders[orderID]; ok && stageIndex < len(node.Stages) {
		stage := node.Stages[stageIndex]
		stage.Status = state.StagePending
		if n := len(stage.Attempts); n > 0 {
			last := &stage.Attempts[n-1]
			if last.Status == state.AttemptLaunching || last.Status == state.AttemptRunning {
				last.Status = state.AttemptCancelled
				last.CompletedAt = now
				last.Error = reason
			}
		}
		node.Stages[stageIndex] = stage
		node.Status = state.OrderPending
		node.UpdatedAt = now
		l.canonical.Orders[orderID] = node
	}
	if err := l.mutateOrdersState(func(orders *OrdersFile) (bool, error) {
		for oi := range orders.Orders {
			order := &orders.Orders[oi]
			if order.ID != orderID || stageIndex >= len(order.Stages) {
				continue
			}
			if order.Stages[stageIndex].Status != StageStatusActive {
				return false, nil
			}
			order.Stages[stageIndex].Status = StageStatusPending
			return true, nil
		}
		return false, nil
	}); err != nil {
		return err
	}
	return l.persistCanonicalCheckpoint()
}

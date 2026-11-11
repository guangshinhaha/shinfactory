package loop

import (
	"context"
	"sync"
)

// A repo's checks can run for minutes. They run in their own goroutine so the
// loop keeps handling other orders' completions and control commands; the stage
// is accepted or failed on the loop goroutine when the checks return.

// evidenceCheck is one execute stage whose checks are running.
type evidenceCheck struct {
	cook   *cookHandle
	cancel context.CancelFunc
}

// evidenceResult is a finished check, waiting for the loop goroutine.
type evidenceResult struct {
	check *evidenceCheck
	err   error // why the stage must not be accepted; nil = accepted
	// aborted means the checks never got a verdict (the loop is stopping or the
	// check was cancelled), so the stage is neither accepted nor failed.
	aborted bool
}

// evidenceTracker owns the running checks. checks is read and written only by
// the loop goroutine; done is the one field the check goroutines touch.
type evidenceTracker struct {
	checks map[string]*evidenceCheck // by order ID

	mu   sync.Mutex
	done []evidenceResult
}

func (t *evidenceTracker) running(orderID string) bool {
	_, ok := t.checks[orderID]
	return ok
}

func (t *evidenceTracker) push(r evidenceResult) {
	t.mu.Lock()
	t.done = append(t.done, r)
	t.mu.Unlock()
}

func (t *evidenceTracker) take() []evidenceResult {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := t.done
	t.done = nil
	return out
}

// needsAsyncEvidence reports whether accepting the stage means running repo
// checks, the slow part. Without declared checks the evidence is quick git
// reads, which stay inline.
func (l *Loop) needsAsyncEvidence(cook *cookHandle) bool {
	return gateStageKey(cook.stage) == gateBuildKey && cook.worktreePath != "" &&
		len(repoChecks(l.projectDir)) > 0
}

// startEvidenceCheck runs the stage's evidence off the loop goroutine. The order
// stays blocked from dispatch until finishEvidenceChecks handles the result.
func (l *Loop) startEvidenceCheck(ctx context.Context, order Order, cook *cookHandle) {
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithCancel(ctx)
	check := &evidenceCheck{cook: cook, cancel: cancel}
	l.cancelEvidenceCheck(cook.orderID)
	if l.evidence.checks == nil {
		l.evidence.checks = map[string]*evidenceCheck{}
	}
	l.evidence.checks[cook.orderID] = check
	l.logger.Info("execute evidence checks started", "order", cook.orderID, "session", cook.session.ID())
	go func() {
		defer cancel()
		err := l.checkExecuteEvidence(runCtx, order, cook)
		// A cancelled run says nothing about the work.
		l.evidence.push(evidenceResult{check: check, err: err, aborted: runCtx.Err() != nil})
		l.wakeLoop()
	}()
}

// wakeLoop asks Run for a cycle so a finished check is handled without waiting
// for the next poll.
func (l *Loop) wakeLoop() {
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

// cancelEvidenceCheck drops a running check for the order, if any, so its
// result is ignored. The loop goroutine only.
func (l *Loop) cancelEvidenceCheck(orderID string) {
	if check, ok := l.evidence.checks[orderID]; ok {
		check.cancel()
		delete(l.evidence.checks, orderID)
	}
}

// finishEvidenceChecks handles every check that has returned: a failure fails
// the stage the same way an agent's stage_fail does, a pass carries on to
// landing. The loop goroutine only.
func (l *Loop) finishEvidenceChecks(ctx context.Context) error {
	// Every result is handled even if one errors; the first error is returned.
	var firstErr error
	for _, res := range l.evidence.take() {
		cook := res.check.cook
		if l.evidence.checks[cook.orderID] != res.check {
			continue // cancelled or superseded
		}
		delete(l.evidence.checks, cook.orderID)
		if res.aborted || !l.stageStillActive(cook) {
			continue // no verdict, or the order moved on while the checks ran
		}
		var err error
		if res.err != nil {
			err = l.rejectStage(ctx, cook, res.err.Error())
		} else {
			err = l.completeAfterEvidence(ctx, cook)
		}
		if err != nil {
			if conflictErr := l.handleMergeConflict(cook, err); conflictErr != nil && firstErr == nil {
				firstErr = conflictErr
			}
		}
	}
	return firstErr
}

// stageStillActive reports whether the cook's stage is still the one running
// for its order: not removed, requeued or advanced by a control command.
func (l *Loop) stageStillActive(cook *cookHandle) bool {
	order, ok := l.orderByID(cook.orderID)
	if !ok || cook.stageIndex < 0 || cook.stageIndex >= len(order.Stages) {
		return false
	}
	return order.Stages[cook.stageIndex].Status == StageStatusActive
}

package loop

import (
	"context"

	loopruntime "github.com/guangshinhaha/shinfactory/runtime"
)

func (l *Loop) nextDispatchGeneration() uint64 {
	return l.dispatchGeneration.Add(1)
}

func (l *Loop) startSessionWatcher(ctx context.Context, cook *cookHandle, isBootstrap bool) {
	if cook == nil || cook.session == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	l.watcherWG.Add(1)
	l.watcherCount.Add(1)
	l.watchedDone.Store(cook.session.ID(), cook.session.Done())
	go func(sessionID string, handle *cookHandle, watcherCtx context.Context) {
		defer l.watcherWG.Done()
		defer l.watcherCount.Add(-1)

		<-handle.session.Done()
		result := StageResult{
			OrderID:      handle.orderID,
			StageIndex:   handle.stageIndex,
			Attempt:      handle.attempt,
			Status:       stageResultFromOutcome(handle.session.Outcome()),
			SessionID:    sessionID,
			Generation:   handle.generation,
			IsSchedule:   isScheduleStage(handle.stage),
			IsBootstrap:  isBootstrap,
			WorktreeName: handle.worktreeName,
			WorktreePath: handle.worktreePath,
			CompletedAt:  l.deps.Now(),
		}
		l.enqueueCompletion(watcherCtx, result)
		l.watchedDone.Delete(sessionID)
	}(cook.session.ID(), cook, ctx)
}

func stageResultFromOutcome(outcome loopruntime.SessionOutcome) StageResultStatus {
	switch outcome.Status {
	case loopruntime.StatusCompleted:
		return StageResultCompleted
	case loopruntime.StatusKilled, loopruntime.StatusCancelled:
		return StageResultCancelled
	default:
		return StageResultFailed
	}
}

// finishedSessionInFlight reports whether a watched session has finished but
// its watcher has not queued the result yet. That result is certain to arrive.
func (l *Loop) finishedSessionInFlight() bool {
	found := false
	l.watchedDone.Range(func(_, done any) bool {
		select {
		case <-done.(<-chan struct{}):
			found = true
			return false
		default:
			return true
		}
	})
	return found
}

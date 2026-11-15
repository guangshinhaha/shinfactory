package loop

import (
	"context"
	"sort"
	"time"

	"github.com/guangshinhaha/shinfactory/internal/stringx"
	"github.com/guangshinhaha/shinfactory/monitor"
)

// runMonitorPass refreshes session meta.json files and applies monitor-side
// repairs. Best-effort: monitoring issues should not stop scheduling.
func (l *Loop) runMonitorPass(ctx context.Context) {
	if l.deps.Monitor == nil {
		return
	}
	metas, err := l.deps.Monitor.RunOnce(ctx)
	if err != nil {
		l.logger.Warn("monitor run failed", "error", err)
		return
	}
	l.noteStuckSessions(metas)
}

// noteStuckSessions records the orders whose running session has shown no event
// or log output for [monitor] stuck_after_minutes, for `status` to report, and
// logs each stuck session once. A session that wakes up, or ends, is forgotten,
// so a later stall warns again.
func (l *Loop) noteStuckSessions(metas []monitor.SessionMeta) {
	limit := time.Duration(l.config.Monitor.StuckAfterMinutes) * time.Minute
	idle := make(map[string]int64, len(metas))
	for _, meta := range metas {
		if meta.Alive && meta.Status == monitor.SessionStatusRunning {
			idle[meta.SessionID] = meta.IdleSeconds
		}
	}
	var stuck []string
	seen := map[string]struct{}{}
	for orderID, cook := range l.cooks.activeCooksByOrder {
		if limit <= 0 || cook.session == nil {
			continue
		}
		secs, ok := idle[cook.session.ID()]
		if !ok || time.Duration(secs)*time.Second < limit {
			continue
		}
		stuck = append(stuck, orderID)
		seen[cook.session.ID()] = struct{}{}
		if _, warned := l.stuckWarned[cook.session.ID()]; !warned {
			l.logger.Warn("stage looks stuck: session shows no events",
				"order", orderID, "session", cook.session.ID(), "idle", (time.Duration(secs) * time.Second).String())
		}
	}
	sort.Strings(stuck)
	l.stuckOrders = stuck
	l.stuckWarned = seen
}

// enqueueTerminalActiveCompletions bridges monitor-derived terminal statuses
// back into loop stage completion handling when a process remains alive after
// writing a terminal canonical event.
func (l *Loop) enqueueTerminalActiveCompletions(ctx context.Context) error {
	for _, cook := range l.cooks.activeCooksByOrder {
		metaStatus, ok, err := l.readSessionStatus(cook.session.ID())
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		resultStatus, terminal := stageResultFromSessionMetaStatus(metaStatus)
		if !terminal {
			continue
		}
		l.logger.Info("session meta indicates terminal state; enqueueing completion",
			"order", cook.orderID,
			"session", cook.session.ID(),
			"status", metaStatus,
		)
		if resultStatus == StageResultCompleted && !isScheduleStage(cook.stage) {
			l.forwardToScheduler(cook, "session_repaired", "session emitted terminal result but stayed alive; auto-closing and advancing", nil)
		}
		l.enqueueCompletion(ctx, StageResult{
			OrderID:      cook.orderID,
			StageIndex:   cook.stageIndex,
			Attempt:      cook.attempt,
			Status:       resultStatus,
			SessionID:    cook.session.ID(),
			Generation:   cook.generation,
			IsSchedule:   isScheduleStage(cook.stage),
			WorktreeName: cook.worktreeName,
			WorktreePath: cook.worktreePath,
			CompletedAt:  l.deps.Now(),
		})
		_ = cook.session.ForceKill()
	}
	if l.bootstrapInFlight != nil {
		cook := l.bootstrapInFlight
		metaStatus, ok, err := l.readSessionStatus(cook.session.ID())
		if err != nil {
			return err
		}
		if ok {
			resultStatus, terminal := stageResultFromSessionMetaStatus(metaStatus)
			if terminal {
				l.logger.Info("bootstrap session meta indicates terminal state; enqueueing completion",
					"session", cook.session.ID(),
					"status", metaStatus,
				)
				l.enqueueCompletion(ctx, StageResult{
					OrderID:      cook.orderID,
					StageIndex:   cook.stageIndex,
					Attempt:      cook.attempt,
					Status:       resultStatus,
					SessionID:    cook.session.ID(),
					Generation:   cook.generation,
					IsSchedule:   isScheduleStage(cook.stage),
					IsBootstrap:  true,
					WorktreeName: cook.worktreeName,
					WorktreePath: cook.worktreePath,
					CompletedAt:  l.deps.Now(),
				})
				_ = cook.session.ForceKill()
			}
		}
	}
	return nil
}

func stageResultFromSessionMetaStatus(raw string) (StageResultStatus, bool) {
	switch stringx.Normalize(raw) {
	case "completed", "exited":
		return StageResultCompleted, true
	case "failed":
		return StageResultFailed, true
	case "killed", "cancelled", "canceled", "stopped":
		return StageResultCancelled, true
	default:
		return "", false
	}
}

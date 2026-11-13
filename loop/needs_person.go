package loop

import (
	"context"
	"strings"
	"time"

	"github.com/guangshinhaha/shinfactory/adapter"
	"github.com/guangshinhaha/shinfactory/internal/linestatus"
)

const (
	// unbuildablePrefix starts the stage_fail message of an issue that states
	// nothing observable to build against.
	unbuildablePrefix = "Unbuildable:"
	// stopMessageLimit bounds how much of a failure reason goes into the issue
	// comment.
	stopMessageLimit = 600
	// stopTimeout bounds the stop action's gh calls, which run on the loop's
	// goroutine.
	stopTimeout = 2 * time.Minute
)

// stopForPerson hands the order's issue to a person: the backlog adapter's
// "stop" action comments once with message and swaps the issue's ready label for needs-shin,
// so the backlog drops it until a person puts ready back.
// A backlog with no stop script (a todos.md backlog) has nobody to tell, so it
// is skipped. A failed stop is logged: the order is already failed and
// inert, and the next stop reports again.
func (l *Loop) stopForPerson(ctx context.Context, orderID, summary, detail string) {
	// A person now owns the order; when they put ready back it starts with
	// its one rebase again.
	l.clearRebased(orderID)
	message := strings.TrimSpace(summary + " " + truncateRunes(strings.TrimSpace(detail), stopMessageLimit))
	if l.config.Routing.Sensitive {
		// The comment lands on an issue other people can read; what the agent
		// saw in the work stays in the session log.
		message = strings.TrimSpace(summary + " Ask the factory chat for the details.")
	}
	ctx, cancel := context.WithTimeout(ctx, stopTimeout)
	defer cancel()
	_, err := l.deps.Adapter.Run(ctx, "backlog", "stop", adapter.RunOptions{
		Args:  []string{orderID},
		Stdin: []byte(message),
	})
	// The issue left the ready list: a cached backlog would still offer it to
	// the scheduler, which could replace the stopped order.
	l.requestSync()
	switch {
	case err == nil:
		l.markStopped(orderID)
		l.logger.Info("order stopped for a person", "order", orderID)
		if err := linestatus.RecordNeedsShin(l.runtimeDir, linestatus.NeedsShin{Order: orderID, Reason: message, At: l.deps.Now().UTC()}); err != nil {
			l.logger.Warn("could not record needs-shin for status", "order", orderID, "error", err)
		}
		l.clearOrderSpend(orderID)
		l.payStopDebt(orderID)
	case strings.Contains(err.Error(), `action "stop" script is not configured`):
		l.markStopped(orderID) // nobody to tell; repeating the call changes nothing
		l.payStopDebt(orderID)
		l.logger.Info("backlog has no stop action; nobody to tell", "order", orderID, "reason", summary)
	default:
		// The order is failed and silent until this lands: keep owing it.
		l.recordStopDebt(orderID, summary, detail)
		l.logger.Warn("could not hand stopped order to a person; will retry", "order", orderID, "error", err)
	}
}

// markStopped tells stopFailedOrder this failure already reached a person. A
// stop that failed is not marked, so the failure's own hand-off tries again.
func (l *Loop) markStopped(orderID string) {
	if l.stoppedForPerson == nil {
		l.stoppedForPerson = map[string]struct{}{}
	}
	l.stoppedForPerson[orderID] = struct{}{}
}

// stopFailedOrder hands an order that failed for good to a person: attempts
// ran out, or the failure is one a retry cannot fix. It is the only way a
// failed order may leave the factory's sight (see TestEveryOrderFailurePathEndsVisible).
// A stop the caller already made for this failure (a merge conflict, a landing
// stop) is not repeated.
func (l *Loop) stopFailedOrder(ctx context.Context, orderID, reason string) {
	if isScheduleOrderID(orderID) {
		return
	}
	if _, done := l.stoppedForPerson[orderID]; done {
		delete(l.stoppedForPerson, orderID)
		return
	}
	summary, detail := failureStopText(reason)
	l.stopForPerson(ctx, orderID, summary, detail)
	delete(l.stoppedForPerson, orderID)
}

func truncateRunes(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "…"
}

package loop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/guangshinhaha/shinfactory/adapter"
	"github.com/guangshinhaha/shinfactory/internal/linestatus"
)

const (
	// unbuildablePrefix starts the stage_fail message of an issue that states
	// nothing observable to build against.
	unbuildablePrefix = "Unbuildable:"
	// maxFailedRounds is how many rejected rounds an order gets before a person
	// is asked.
	maxFailedRounds = 2
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
	switch {
	case err == nil:
		l.logger.Info("order stopped for a person", "order", orderID)
		if err := linestatus.RecordNeedsShin(l.runtimeDir, linestatus.NeedsShin{Order: orderID, Reason: message, At: l.deps.Now().UTC()}); err != nil {
			l.logger.Warn("could not record needs-shin for status", "order", orderID, "error", err)
		}
		l.clearFailedRounds(orderID)
	case strings.Contains(err.Error(), `action "stop" script is not configured`):
		l.logger.Info("backlog has no stop action; nobody to tell", "order", orderID, "reason", summary)
	default:
		l.logger.Warn("could not hand stopped order to a person", "order", orderID, "error", err)
	}
}

// noteRejectedStage decides whether an agent-reported stage_fail needs a
// person. An unbuildable issue always does; any other rejection does once the
// order has failed maxFailedRounds rounds.
func (l *Loop) noteRejectedStage(ctx context.Context, orderID, reason string) {
	if detail, ok := strings.CutPrefix(reason, unbuildablePrefix); ok {
		l.stopForPerson(ctx, orderID, "The issue has no observable Done when, or something only a person can settle blocks it.", detail)
		return
	}
	if l.bumpFailedRounds(orderID) < maxFailedRounds {
		return
	}
	l.stopForPerson(ctx, orderID, "Two fix rounds failed. Decide whether to change the Done when, split the issue, or drop it, then put ready back. Last rejection:", reason)
}

// failedRoundsFile counts rejected rounds per order. A retry is a fresh order
// with the same ID, so the count lives beside orders.json, not in it.
func (l *Loop) failedRoundsFile() string {
	return filepath.Join(l.runtimeDir, "failed-rounds.json")
}

func (l *Loop) readFailedRounds() map[string]int {
	counts := map[string]int{}
	data, err := os.ReadFile(l.failedRoundsFile())
	if err != nil {
		return counts
	}
	if err := json.Unmarshal(data, &counts); err != nil {
		l.logger.Warn("failed-rounds.json unreadable; counting from zero", "error", err)
		return map[string]int{}
	}
	return counts
}

func (l *Loop) writeFailedRounds(counts map[string]int) {
	data, err := json.Marshal(counts)
	if err == nil {
		err = os.WriteFile(l.failedRoundsFile(), data, 0o644)
	}
	if err != nil {
		l.logger.Warn("could not record failed rounds", "error", err)
	}
}

// failedRoundsMu serializes the read-modify-write on failed-rounds.json: with
// two cooks, two rejections can land together.
var failedRoundsMu sync.Mutex

func (l *Loop) bumpFailedRounds(orderID string) int {
	failedRoundsMu.Lock()
	defer failedRoundsMu.Unlock()
	counts := l.readFailedRounds()
	counts[orderID]++
	l.writeFailedRounds(counts)
	return counts[orderID]
}

func (l *Loop) clearFailedRounds(orderID string) {
	failedRoundsMu.Lock()
	defer failedRoundsMu.Unlock()
	counts := l.readFailedRounds()
	if _, ok := counts[orderID]; !ok {
		return
	}
	delete(counts, orderID)
	l.writeFailedRounds(counts)
}

func truncateRunes(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "…"
}

package loop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/guangshinhaha/shinfactory/internal/filex"
)

// A failed order owes its issue a hand-off to a person. The debt is written to
// disk before anything that can fail or return early, and paid when the stop
// adapter succeeds (or the backlog has nobody to tell). An unpaid debt is
// retried every few cycles and survives a restart, so a gh blip or an early
// error return can no longer leave an order failed with `ready` still up and
// nobody told (#161).

// stopRetryInterval spaces retries of unpaid hand-offs, so a gh outage does
// not run the stop script on every cycle.
const stopRetryInterval = time.Minute

type stopDebt struct {
	Summary string    `json:"summary"`
	Detail  string    `json:"detail"`
	At      time.Time `json:"at"`
}

var stopDebtMu sync.Mutex

func (l *Loop) stopDebtFile() string {
	return filepath.Join(l.runtimeDir, "unsent-stops.json")
}

func (l *Loop) readStopDebts() map[string]stopDebt {
	debts := map[string]stopDebt{}
	data, err := os.ReadFile(l.stopDebtFile())
	if err != nil {
		return debts
	}
	if err := json.Unmarshal(data, &debts); err != nil {
		l.logger.Warn("unsent-stops.json unreadable; treating as empty", "error", err)
		return map[string]stopDebt{}
	}
	return debts
}

func (l *Loop) writeStopDebts(debts map[string]stopDebt) {
	data, err := json.Marshal(debts)
	if err == nil {
		err = filex.WriteFileAtomic(l.stopDebtFile(), data)
	}
	if err != nil {
		l.logger.Warn("could not record the unsent stop", "error", err)
	}
}

// failureStopText is the issue comment for an order that failed with reason.
func failureStopText(reason string) (summary, detail string) {
	if detail, ok := strings.CutPrefix(reason, unbuildablePrefix); ok {
		return "The issue has no observable Done when, or something only a person can settle blocks it.", detail
	}
	return "The order failed and the factory will not retry it. Fix the cause, then put ready back. Reason:", reason
}

// isScheduleOrderID reports whether id names the scheduler's own order, which
// has no issue behind it.
func isScheduleOrderID(id string) bool {
	id = strings.TrimSpace(id)
	return strings.EqualFold(id, scheduleOrderID) || strings.EqualFold(id, scheduleBootstrapOrderID)
}

// owePersonStop records that the order failed and its issue must be handed to
// a person. Call it before any state write or fallible step of a failure path.
func (l *Loop) owePersonStop(orderID, reason string) {
	if isScheduleOrderID(orderID) {
		return
	}
	if _, done := l.stoppedForPerson[orderID]; done {
		return // a stop for this failure already reached a person
	}
	summary, detail := failureStopText(reason)
	l.recordStopDebt(orderID, summary, detail)
}

func (l *Loop) recordStopDebt(orderID, summary, detail string) {
	stopDebtMu.Lock()
	defer stopDebtMu.Unlock()
	debts := l.readStopDebts()
	if _, owed := debts[orderID]; owed {
		return // keep the first, most specific reason
	}
	debts[orderID] = stopDebt{Summary: summary, Detail: truncateRunes(detail, stopMessageLimit), At: l.deps.Now().UTC()}
	l.writeStopDebts(debts)
}

func (l *Loop) payStopDebt(orderID string) {
	stopDebtMu.Lock()
	defer stopDebtMu.Unlock()
	debts := l.readStopDebts()
	if _, owed := debts[orderID]; !owed {
		return
	}
	delete(debts, orderID)
	l.writeStopDebts(debts)
}

// settleStopDebts retries hand-offs that have not reached a person. It runs on
// the loop goroutine at the top of a cycle, at most once per stopRetryInterval.
func (l *Loop) settleStopDebts(ctx context.Context) {
	now := l.deps.Now()
	if !l.lastStopRetry.IsZero() && now.Sub(l.lastStopRetry) < stopRetryInterval {
		return
	}
	stopDebtMu.Lock()
	debts := l.readStopDebts()
	stopDebtMu.Unlock()
	if len(debts) == 0 {
		return
	}
	l.lastStopRetry = now
	for orderID, debt := range debts {
		if _, active := l.cooks.activeCooksByOrder[orderID]; active {
			l.payStopDebt(orderID) // launched again (requeued): the failure is no longer current
			continue
		}
		l.stopForPerson(ctx, orderID, debt.Summary, debt.Detail)
	}
}

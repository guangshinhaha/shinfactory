package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/guangshinhaha/shinfactory/internal/ingest"
)

// orderSpend is what one order has used so far. A retry is a fresh order with
// the same ID, so the ledger lives beside orders.json, keyed by order ID.
type orderSpend struct {
	// Attempts counts launches per stage task key.
	Attempts map[string]int `json:"attempts"`
	// CostUSD sums the cost of the order's finished stages.
	CostUSD float64 `json:"cost_usd"`
}

func (s orderSpend) totalAttempts() int {
	total := 0
	for _, n := range s.Attempts {
		total += n
	}
	return total
}

// orderSpendMu serializes the read-modify-write on order-spend.json.
var orderSpendMu sync.Mutex

func (l *Loop) orderSpendFile() string {
	return filepath.Join(l.runtimeDir, "order-spend.json")
}

func (l *Loop) readOrderSpend() map[string]orderSpend {
	ledger := map[string]orderSpend{}
	data, err := os.ReadFile(l.orderSpendFile())
	if err != nil {
		return ledger
	}
	if err := json.Unmarshal(data, &ledger); err != nil {
		l.logger.Warn("order-spend.json unreadable; counting from zero", "error", err)
		return map[string]orderSpend{}
	}
	return ledger
}

func (l *Loop) writeOrderSpend(ledger map[string]orderSpend) {
	data, err := json.Marshal(ledger)
	if err == nil {
		err = os.WriteFile(l.orderSpendFile(), data, 0o644)
	}
	if err != nil {
		l.logger.Warn("could not record order spend", "error", err)
	}
}

func (l *Loop) orderSpendOf(orderID string) orderSpend {
	orderSpendMu.Lock()
	defer orderSpendMu.Unlock()
	return l.readOrderSpend()[orderID]
}

// recordStageLaunch counts one launch of the stage's task key against the order.
func (l *Loop) recordStageLaunch(orderID, taskKey string) {
	orderSpendMu.Lock()
	defer orderSpendMu.Unlock()
	ledger := l.readOrderSpend()
	spend := ledger[orderID]
	if spend.Attempts == nil {
		spend.Attempts = map[string]int{}
	}
	spend.Attempts[taskKey]++
	ledger[orderID] = spend
	l.writeOrderSpend(ledger)
}

// recordStageCost adds a finished stage's reported cost to the order.
func (l *Loop) recordStageCost(orderID string, costUSD float64) {
	if costUSD <= 0 {
		return
	}
	orderSpendMu.Lock()
	defer orderSpendMu.Unlock()
	ledger := l.readOrderSpend()
	spend := ledger[orderID]
	spend.CostUSD += costUSD
	ledger[orderID] = spend
	l.writeOrderSpend(ledger)
}

func (l *Loop) clearOrderSpend(orderID string) {
	orderSpendMu.Lock()
	defer orderSpendMu.Unlock()
	ledger := l.readOrderSpend()
	if _, ok := ledger[orderID]; !ok {
		return
	}
	delete(ledger, orderID)
	l.writeOrderSpend(ledger)
}

// overSpendCap reports why the order may not launch another attempt of the
// stage, or "" when it may. A cap of zero or less is off; config normalizes a configured zero to the
// default, so only an unconfigured Config value reaches that case.
func (l *Loop) overSpendCap(orderID, taskKey string) string {
	spend := l.orderSpendOf(orderID)
	caps := l.config.Spend
	if caps.MaxAttempts > 0 && spend.Attempts[taskKey] >= caps.MaxAttempts {
		return fmt.Sprintf("it already ran the %q stage %d times (cap %d)", taskKey, spend.Attempts[taskKey], caps.MaxAttempts)
	}
	if caps.MaxCostUSD > 0 && spend.CostUSD >= caps.MaxCostUSD {
		return fmt.Sprintf("its stages already cost $%.2f (cap $%.2f)", spend.CostUSD, caps.MaxCostUSD)
	}
	return ""
}

// stopOverCap fails the order without launching the stage and hands its issue
// to a person: one comment with what it spent and why it stopped, ready swapped
// for needs-shin.
func (l *Loop) stopOverCap(ctx context.Context, cand dispatchCandidate, stage Stage, why string) error {
	spend := l.orderSpendOf(cand.OrderID)
	reason := "spending cap reached: " + why
	if err := l.mutateOrdersState(func(orders *OrdersFile) (bool, error) {
		updated, err := failStage(*orders, cand.OrderID, reason)
		if err != nil {
			return false, err
		}
		*orders = updated
		return true, nil
	}); err != nil {
		return err
	}
	cook := &cookHandle{cookIdentity: cookIdentity{orderID: cand.OrderID, stageIndex: cand.StageIndex, stage: stage}}
	l.recordStageFailure(cook, reason, OrderFailureClassStageTerminal, nil)
	l.emitEvent(ingest.EventStageFailed, map[string]any{
		"order_id":    cand.OrderID,
		"stage_index": cand.StageIndex,
		"error":       reason,
	})
	l.classifyOrderHard("cycle.spend_cap", OrderFailureClassStageTerminal, cand.OrderID, cand.StageIndex, reason, nil)
	l.logger.Warn("order stopped at its spending cap", "order", cand.OrderID, "reason", why)

	summary := fmt.Sprintf("Spending cap reached: %s. So far it spent $%.2f over %d stage attempts. Raise [spend] in .noodle.toml or reshape the issue, then put ready back.",
		why, spend.CostUSD, spend.totalAttempts())
	if spend.CostUSD == 0 {
		summary += " No stage reported a cost (the Orca runtime reports none), so the cost cap did not apply; the attempt cap did."
	}
	l.stopForPerson(ctx, cand.OrderID, summary, "")
	return nil
}

func spendTaskKey(stage Stage) string {
	return strings.TrimSpace(nonEmpty(stage.TaskKey, stage.Skill))
}

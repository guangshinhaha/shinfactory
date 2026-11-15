package loop

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/guangshinhaha/shinfactory/adapter"
	"github.com/guangshinhaha/shinfactory/internal/orderx"
	"github.com/guangshinhaha/shinfactory/mise"
	loopruntime "github.com/guangshinhaha/shinfactory/runtime"
)

// A brief that changes only in history or tickets while a stage is in flight must not
// re-run the scheduler: its only possible amendment is rejected (#157).
func TestEnsureScheduleSkipsRerunWhileStageInFlightAndBacklogUnchanged(t *testing.T) {
	projectDir := t.TempDir()
	ordersPath := filepath.Join(projectDir, ".noodle", "orders.json")
	if err := writeOrdersAtomic(ordersPath, OrdersFile{}); err != nil {
		t.Fatalf("write orders: %v", err)
	}
	l := New(projectDir, "noodle", localLandingConfig(), Dependencies{
		Runtimes:   map[string]loopruntime.Runtime{"process": newMockRuntime()},
		Worktree:   &fakeWorktree{},
		Adapter:    &fakeAdapterRunner{},
		Mise:       &fakeMise{},
		Monitor:    fakeMonitor{},
		Registry:   testLoopRegistry(),
		Now:        time.Now,
		OrdersFile: ordersPath,
	})
	l.cooks.activeCooksByOrder["121"] = &cookHandle{
		cookIdentity: cookIdentity{orderID: "121", stage: Stage{TaskKey: "execute", Status: StageStatusActive}},
	}
	running := Order{ID: "121", Status: OrderStatusActive, Stages: []Stage{{TaskKey: "execute", Status: StageStatusActive}}}
	backlog := []adapter.BacklogItem{{ID: "121", Title: "a"}}

	inject := func(backlog []adapter.BacklogItem) bool {
		orders := OrdersFile{Orders: []Order{running}}
		if _, err := l.ensureScheduleIfNeeded(mise.Brief{Backlog: backlog}, &orders, true); err != nil {
			t.Fatalf("ensureScheduleIfNeeded: %v", err)
		}
		return hasScheduleOrder(orders)
	}

	if !inject(backlog) {
		t.Fatal("first mise change: want a schedule run")
	}
	if inject(backlog) {
		t.Fatal("same backlog while a stage is in flight: want no schedule run")
	}
	if !inject([]adapter.BacklogItem{{ID: "121", Title: "a"}, {ID: "122", Title: "b"}}) {
		t.Fatal("backlog changed: want a schedule run")
	}
	delete(l.cooks.activeCooksByOrder, "121")
	if !inject(backlog) {
		t.Fatal("stage finished: want a schedule run")
	}
}

// A scheduler session that fails leaves a failed "schedule" order in
// orders.json; nothing injected a new one, so a ready issue sat unscheduled
// until a person ran `control requeue schedule` (#205).
func TestEnsureScheduleRetriesFailedScheduleOrder(t *testing.T) {
	projectDir := t.TempDir()
	ordersPath := filepath.Join(projectDir, ".noodle", "orders.json")
	failedSchedule := scheduleOrder("")
	failedSchedule.Status = OrderStatusFailed
	failedSchedule.Stages[0].Status = StageStatusFailed
	failedOther := Order{ID: "38", Status: OrderStatusFailed, Stages: []Stage{{TaskKey: "execute", Status: StageStatusFailed}}}
	if err := writeOrdersAtomic(ordersPath, OrdersFile{Orders: []Order{failedSchedule, failedOther}}); err != nil {
		t.Fatalf("write orders: %v", err)
	}
	now := time.Now()
	l := New(projectDir, "noodle", localLandingConfig(), Dependencies{
		Runtimes:   map[string]loopruntime.Runtime{"process": newMockRuntime()},
		Worktree:   &fakeWorktree{},
		Adapter:    &fakeAdapterRunner{},
		Mise:       &fakeMise{},
		Monitor:    fakeMonitor{},
		Registry:   testLoopRegistry(),
		Now:        func() time.Time { return now },
		OrdersFile: ordersPath,
	})
	brief := mise.Brief{Backlog: []adapter.BacklogItem{{ID: "42", Title: "ready issue"}}}

	scheduleStatus := func() orderx.OrderStatus {
		t.Helper()
		orders, err := l.currentOrders()
		if err != nil {
			t.Fatalf("currentOrders: %v", err)
		}
		for _, o := range orders.Orders {
			if isScheduleOrder(o) {
				return o.Status
			}
		}
		t.Fatal("schedule order missing")
		return ""
	}

	orders, err := l.currentOrders()
	if err != nil {
		t.Fatalf("currentOrders: %v", err)
	}
	if err := l.retryFailedScheduleOrder(context.Background()); err != nil {
		t.Fatalf("retryFailedScheduleOrder: %v", err)
	}
	orders, _ = l.currentOrders()
	if _, err := l.ensureScheduleIfNeeded(brief, &orders, true); err != nil {
		t.Fatalf("ensureScheduleIfNeeded: %v", err)
	}
	if got := scheduleStatus(); got != OrderStatusActive {
		t.Fatalf("failed schedule order status = %q, want %q so a new scheduler session starts", got, OrderStatusActive)
	}

	// The scheduler fails again straight away: no hot loop, wait out the backoff.
	failAgain := func() {
		if err := l.mutateOrdersState(func(o *OrdersFile) (bool, error) {
			for i := range o.Orders {
				if isScheduleOrder(o.Orders[i]) {
					o.Orders[i].Status = OrderStatusFailed
					o.Orders[i].Stages[0].Status = StageStatusFailed
				}
			}
			return true, nil
		}); err != nil {
			t.Fatalf("fail schedule: %v", err)
		}
	}
	failAgain()
	orders, _ = l.currentOrders()
	if err := l.retryFailedScheduleOrder(context.Background()); err != nil {
		t.Fatalf("retryFailedScheduleOrder: %v", err)
	}
	orders, _ = l.currentOrders()
	if _, err := l.ensureScheduleIfNeeded(brief, &orders, true); err != nil {
		t.Fatalf("ensureScheduleIfNeeded: %v", err)
	}
	if got := scheduleStatus(); got != OrderStatusFailed {
		t.Fatalf("second failure inside backoff: status = %q, want it left failed", got)
	}
	now = now.Add(scheduleRetryBackoff + time.Second)
	orders, _ = l.currentOrders()
	if err := l.retryFailedScheduleOrder(context.Background()); err != nil {
		t.Fatalf("retryFailedScheduleOrder: %v", err)
	}
	orders, _ = l.currentOrders()
	if _, err := l.ensureScheduleIfNeeded(brief, &orders, true); err != nil {
		t.Fatalf("ensureScheduleIfNeeded: %v", err)
	}
	if got := scheduleStatus(); got != OrderStatusActive {
		t.Fatalf("after backoff: status = %q, want %q", got, OrderStatusActive)
	}
}

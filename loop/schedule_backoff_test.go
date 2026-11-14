package loop

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/guangshinhaha/shinfactory/adapter"
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

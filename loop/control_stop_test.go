package loop

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/guangshinhaha/shinfactory/config"
	loopruntime "github.com/guangshinhaha/shinfactory/runtime"
)

type failingKillSession struct {
	*mockSession
	err error
}

func (s *failingKillSession) ForceKill() error { return s.err }

func newStopTestLoop(t *testing.T, mode string, wt *fakeWorktree) *Loop {
	t.Helper()
	projectDir := t.TempDir()
	runtimeDir := filepath.Join(projectDir, ".noodle")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatalf("mkdir runtime: %v", err)
	}
	ordersPath := filepath.Join(runtimeDir, "orders.json")
	order := testOrder("42", "execute", "execute", "claude", "claude-sonnet-5-5")
	order.Stages[0].Status = StageStatusActive
	if err := writeOrdersAtomic(ordersPath, OrdersFile{Orders: []Order{order}}); err != nil {
		t.Fatalf("write orders: %v", err)
	}
	cfg := localLandingConfig()
	cfg.Landing.Mode = mode
	return New(projectDir, "noodle", cfg, Dependencies{
		Runtimes:   map[string]loopruntime.Runtime{"process": newMockRuntime()},
		Worktree:   wt,
		Adapter:    &fakeAdapterRunner{},
		Mise:       &fakeMise{},
		Monitor:    fakeMonitor{},
		Registry:   testLoopRegistry(),
		Now:        time.Now,
		OrdersFile: ordersPath,
	})
}

func stopTestCook(sess loopruntime.SessionHandle) *cookHandle {
	return &cookHandle{
		cookIdentity: cookIdentity{orderID: "42", stage: Stage{TaskKey: "execute", Skill: "execute"}},
		session:      sess,
		worktreeName: "42-0-execute",
		worktreePath: "/wt/42-0-execute",
		orderStatus:  OrderStatusActive,
	}
}

func TestStopKeepsWorktreeAndParksOrderForRequeue(t *testing.T) {
	for _, mode := range []string{config.LandingModeLocal, config.LandingModePR} {
		t.Run(mode, func(t *testing.T) {
			wt := &fakeWorktree{}
			l := newStopTestLoop(t, mode, wt)
			sess := &mockSession{id: "sess-stop", status: "running", done: make(chan struct{})}
			l.cooks.activeCooksByOrder["42"] = stopTestCook(sess)

			if err := l.controlStop("42-0-execute"); err != nil {
				t.Fatalf("stop: %v", err)
			}
			if sess.status != "killed" {
				t.Fatalf("session status = %q, want killed", sess.status)
			}
			if len(wt.cleaned) != 0 {
				t.Fatalf("worktrees cleaned = %v, want none: stop must keep the worktree and branch", wt.cleaned)
			}
			if _, ok := l.cooks.activeCooksByOrder["42"]; ok {
				t.Fatal("stopped cook still active")
			}
			orders, err := l.currentOrders()
			if err != nil {
				t.Fatal(err)
			}
			if got := orders.Orders[0].Status; got != OrderStatusFailed {
				t.Fatalf("order status = %q, want %q so it can be requeued", got, OrderStatusFailed)
			}
			if got := orders.Orders[0].Stages[0].Status; got != StageStatusFailed {
				t.Fatalf("stage status = %q, want %q", got, StageStatusFailed)
			}
			if err := l.controlRequeue("42"); err != nil {
				t.Fatalf("requeue after stop: %v", err)
			}
		})
	}
}

func TestStopFailureKeepsCookAndWorktree(t *testing.T) {
	wt := &fakeWorktree{}
	l := newStopTestLoop(t, config.LandingModeLocal, wt)
	sess := &failingKillSession{
		mockSession: &mockSession{id: "sess-live", status: "running", done: make(chan struct{})},
		err:         os.ErrDeadlineExceeded,
	}
	l.cooks.activeCooksByOrder["42"] = stopTestCook(sess)

	if err := l.controlStop("42-0-execute"); err == nil {
		t.Fatal("stop returned nil although the worker could not be stopped")
	}
	if _, ok := l.cooks.activeCooksByOrder["42"]; !ok {
		t.Fatal("cook dropped although its worker may still be live")
	}
	if len(wt.cleaned) != 0 {
		t.Fatalf("worktrees cleaned = %v, want none", wt.cleaned)
	}
	orders, err := l.currentOrders()
	if err != nil {
		t.Fatal(err)
	}
	if got := orders.Orders[0].Status; got != OrderStatusActive {
		t.Fatalf("order status = %q, want it left active", got)
	}
}

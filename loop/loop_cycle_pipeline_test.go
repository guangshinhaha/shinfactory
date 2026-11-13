package loop

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guangshinhaha/shinfactory/adapter"
	"github.com/guangshinhaha/shinfactory/internal/orderx"
	loopruntime "github.com/guangshinhaha/shinfactory/runtime"
)

func TestCancelSupersededActiveCooksCancelsChangedStage(t *testing.T) {
	projectDir := t.TempDir()
	runtimeDir := filepath.Join(projectDir, ".noodle")
	ordersPath := filepath.Join(runtimeDir, "orders.json")
	if err := writeOrdersAtomic(ordersPath, OrdersFile{}); err != nil {
		t.Fatalf("write orders: %v", err)
	}

	worktree := &fakeWorktree{}
	l := New(projectDir, "noodle", localLandingConfig(), Dependencies{
		Runtimes:   map[string]loopruntime.Runtime{"process": newMockRuntime()},
		Worktree:   worktree,
		Adapter:    &fakeAdapterRunner{},
		Mise:       &fakeMise{},
		Monitor:    fakeMonitor{},
		Registry:   testLoopRegistry(),
		Now:        time.Now,
		OrdersFile: ordersPath,
	})

	session := &mockSession{id: "sess-97", status: "running", done: make(chan struct{})}
	l.cooks.activeCooksByOrder["97"] = &cookHandle{
		cookIdentity: cookIdentity{
			orderID:    "97",
			stageIndex: 0,
			stage: Stage{
				TaskKey:  "execute",
				Prompt:   "old prompt",
				Provider: "codex",
				Model:    "gpt-5.4",
				Status:   StageStatusActive,
			},
		},
		session:      session,
		worktreeName: "97-0-execute",
		worktreePath: filepath.Join(projectDir, ".worktrees", "97-0-execute"),
	}

	orders := OrdersFile{
		Orders: []Order{{
			ID:     "97",
			Status: OrderStatusActive,
			Stages: []Stage{
				{TaskKey: "execute", Prompt: "new prompt", Provider: "codex", Model: "gpt-5.4", Status: StageStatusPending},
				{TaskKey: "adversarial-review", Provider: "claude", Model: "claude-opus-4-6", Status: StageStatusPending},
			},
		}},
	}

	l.cancelSupersededActiveCooks(orders)

	if _, ok := l.cooks.activeCooksByOrder["97"]; ok {
		t.Fatal("expected superseded active cook to be removed")
	}
	if got := session.Status(); got != "killed" {
		t.Fatalf("session status = %q, want killed", got)
	}
	if len(worktree.cleaned) != 1 || worktree.cleaned[0] != "97-0-execute" {
		t.Fatalf("cleaned worktrees = %v, want [97-0-execute]", worktree.cleaned)
	}
}

func TestCancelSupersededActiveCooksKeepsMatchingStage(t *testing.T) {
	projectDir := t.TempDir()
	runtimeDir := filepath.Join(projectDir, ".noodle")
	ordersPath := filepath.Join(runtimeDir, "orders.json")
	if err := writeOrdersAtomic(ordersPath, OrdersFile{}); err != nil {
		t.Fatalf("write orders: %v", err)
	}

	worktree := &fakeWorktree{}
	l := New(projectDir, "noodle", localLandingConfig(), Dependencies{
		Runtimes:   map[string]loopruntime.Runtime{"process": newMockRuntime()},
		Worktree:   worktree,
		Adapter:    &fakeAdapterRunner{},
		Mise:       &fakeMise{},
		Monitor:    fakeMonitor{},
		Registry:   testLoopRegistry(),
		Now:        time.Now,
		OrdersFile: ordersPath,
	})

	session := &mockSession{id: "sess-97", status: "running", done: make(chan struct{})}
	l.cooks.activeCooksByOrder["97"] = &cookHandle{
		cookIdentity: cookIdentity{
			orderID:    "97",
			stageIndex: 0,
			stage: Stage{
				TaskKey:  "execute",
				Prompt:   "steady prompt",
				Provider: "codex",
				Model:    "gpt-5.4",
				Status:   StageStatusActive,
			},
		},
		session:      session,
		worktreeName: "97-0-execute",
		worktreePath: filepath.Join(projectDir, ".worktrees", "97-0-execute"),
	}

	orders := OrdersFile{
		Orders: []Order{{
			ID:     "97",
			Status: OrderStatusActive,
			Stages: []Stage{
				{TaskKey: "execute", Prompt: "steady prompt", Provider: "codex", Model: "gpt-5.4", Status: StageStatusPending},
				{TaskKey: "adversarial-review", Provider: "claude", Model: "claude-opus-4-6", Status: StageStatusPending},
			},
		}},
	}

	l.cancelSupersededActiveCooks(orders)

	if _, ok := l.cooks.activeCooksByOrder["97"]; !ok {
		t.Fatal("expected matching active cook to stay running")
	}
	if got := session.Status(); got != "running" {
		t.Fatalf("session status = %q, want running", got)
	}
	if len(worktree.cleaned) != 0 {
		t.Fatalf("expected no worktree cleanup, got %v", worktree.cleaned)
	}
}

func newPromotionTestLoop(t *testing.T) (*Loop, string) {
	t.Helper()
	projectDir := t.TempDir()
	runtimeDir := filepath.Join(projectDir, ".noodle")
	ordersPath := filepath.Join(runtimeDir, "orders.json")
	if err := writeOrdersAtomic(ordersPath, OrdersFile{}); err != nil {
		t.Fatalf("write orders: %v", err)
	}
	l := New(projectDir, "noodle", localLandingConfig(), Dependencies{
		Runtimes:       map[string]loopruntime.Runtime{"process": newMockRuntime()},
		Worktree:       &fakeWorktree{},
		Adapter:        &fakeAdapterRunner{},
		Mise:           &fakeMise{},
		Monitor:        fakeMonitor{},
		Registry:       testLoopRegistry(),
		Now:            time.Now,
		OrdersFile:     ordersPath,
		OrdersNextFile: filepath.Join(runtimeDir, "orders-next.json"),
	})
	return l, runtimeDir
}

func promoteProposal(t *testing.T, l *Loop, ids []string, backlog []adapter.BacklogItem) {
	t.Helper()
	compact := orderx.CompactOrdersFile{}
	for _, id := range ids {
		compact.Orders = append(compact.Orders, orderx.CompactOrder{ID: id, Stages: []orderx.CompactStage{{Do: "execute"}}})
	}
	writeCompactOrders(t, l.deps.OrdersNextFile, compact)
	orders, err := l.currentOrders()
	if err != nil {
		t.Fatalf("currentOrders: %v", err)
	}
	result, err := consumeOrdersNext(l.deps.OrdersNextFile, orders, backlog)
	if err := l.handlePromotionResult(result, err); err != nil {
		t.Fatalf("handlePromotionResult: %v", err)
	}
}

func TestDroppedOrdersEmitOwnEventAndSurviveCleanPromotion(t *testing.T) {
	l, runtimeDir := newPromotionTestLoop(t)

	promoteProposal(t, l, []string{"chore-side-note"}, nil)
	data, err := os.ReadFile(filepath.Join(runtimeDir, "loop-events.ndjson"))
	if err != nil {
		t.Fatalf("read loop events: %v", err)
	}
	if !strings.Contains(string(data), `"type":"orders.unready"`) || !strings.Contains(string(data), "chore-side-note") {
		t.Fatalf("want orders.unready event naming the id, got %s", data)
	}
	if l.lastPromotionError != "" {
		t.Fatalf("dropped orders must not reuse lastPromotionError, got %q", l.lastPromotionError)
	}

	// A clean promotion in between must not erase the dropped ids.
	promoteProposal(t, l, []string{"7"}, []adapter.BacklogItem{{ID: "7"}})

	taskTypes := buildOrderTaskTypesPrompt(l.registry.All())
	first := buildSchedulePrompt("schedule", taskTypes, Order{}, "", runtimeDir, l.lastPromotionError, l.droppedOrderIDs, nil, nil)
	if got := strings.Count(first, "chore-side-note"); got != 1 {
		t.Fatalf("dropped id appears %d times in prompt, want once:\n%s", got, first)
	}
}

func TestProposalOfUnreadyAndScheduleEntersCooldown(t *testing.T) {
	l, _ := newPromotionTestLoop(t)
	now := time.Now()
	l.deps.Now = func() time.Time { return now }

	promoteProposal(t, l, []string{"chore-side-note", "schedule"}, nil)

	if !l.scheduleNothingUntil.After(now) {
		t.Fatalf("want empty-promotion cooldown after [unready, schedule], got %v", l.scheduleNothingUntil)
	}
}

func TestCancelSupersededCookKeepsCookWhenForceKillFails(t *testing.T) {
	for _, resume := range []bool{true, false} {
		t.Run(fmt.Sprintf("resume=%v", resume), func(t *testing.T) {
			projectDir := t.TempDir()
			ordersPath := filepath.Join(projectDir, ".noodle", "orders.json")
			if err := writeOrdersAtomic(ordersPath, OrdersFile{Orders: []Order{{
				ID:     "97",
				Status: OrderStatusActive,
				Stages: []Stage{{TaskKey: "execute", Status: StageStatusActive}},
			}}}); err != nil {
				t.Fatalf("write orders: %v", err)
			}
			worktree := &fakeWorktree{}
			l := New(projectDir, "noodle", localLandingConfig(), Dependencies{
				Runtimes:   map[string]loopruntime.Runtime{"process": newMockRuntime()},
				Worktree:   worktree,
				Adapter:    &fakeAdapterRunner{},
				Mise:       &fakeMise{},
				Monitor:    fakeMonitor{},
				Registry:   testLoopRegistry(),
				Now:        time.Now,
				OrdersFile: ordersPath,
			})
			session := &failingKillSession{mockSession: &mockSession{id: "sess-97", status: "running", done: make(chan struct{})}, err: errors.New("worker-stop failed")}
			cook := &cookHandle{
				cookIdentity: cookIdentity{
					orderID: "97", stageIndex: 0,
					stage: Stage{TaskKey: "execute", Status: StageStatusActive},
				},
				session:      session,
				worktreeName: "97-0-execute",
				worktreePath: filepath.Join(projectDir, ".worktrees", "97-0-execute"),
			}
			l.cooks.activeCooksByOrder["97"] = cook

			err := l.cancelSupersededCook("97", cook, resume)
			if err == nil {
				t.Fatal("cancelSupersededCook returned nil although the worker could not be stopped")
			}
			if l.cooks.activeCooksByOrder["97"] != cook {
				t.Fatal("cook was untracked while its worker may still be live")
			}
			if len(worktree.cleaned) != 0 {
				t.Fatalf("worktree cleaned %v while its worker may still be live", worktree.cleaned)
			}
			orders, err := l.currentOrders()
			if err != nil {
				t.Fatal(err)
			}
			if got := orders.Orders[0].Stages[0].Status; got != StageStatusActive {
				t.Fatalf("stage status = %q, want active (no respawn into the same worktree)", got)
			}
		})
	}
}

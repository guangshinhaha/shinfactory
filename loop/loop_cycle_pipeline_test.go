package loop

import (
	"encoding/json"
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

func readPromotionResult(t *testing.T, runtimeDir string) promotionResultFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(runtimeDir, "orders-next.result.json"))
	if err != nil {
		t.Fatalf("read orders-next.result.json: %v", err)
	}
	var got promotionResultFile
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parse orders-next.result.json: %v\n%s", err, data)
	}
	return got
}

func TestPromotionWritesResultFileForScheduleWorker(t *testing.T) {
	t.Run("promoted", func(t *testing.T) {
		l, runtimeDir := newPromotionTestLoop(t)
		promoteProposal(t, l, []string{"7"}, []adapter.BacklogItem{{ID: "7"}})
		got := readPromotionResult(t, runtimeDir)
		if got.Status != "promoted" {
			t.Fatalf("status = %q, want promoted", got.Status)
		}
	})
	t.Run("amendment rejected while stage in flight", func(t *testing.T) {
		l, runtimeDir := newPromotionTestLoop(t)
		existing := OrdersFile{Orders: []Order{{ID: "352", Status: OrderStatusActive, Stages: []Stage{
			{TaskKey: "execute", Status: StageStatusActive},
			{TaskKey: "quality", Status: StageStatusPending},
		}}}}
		if err := writeOrdersAtomic(l.deps.OrdersFile, existing); err != nil {
			t.Fatalf("write orders: %v", err)
		}
		writeCompactOrders(t, l.deps.OrdersNextFile, orderx.CompactOrdersFile{Orders: []orderx.CompactOrder{
			{ID: "352", Stages: []orderx.CompactStage{{Do: "execute"}, {Do: "adversarial-review"}}},
		}})
		orders, err := l.currentOrders()
		if err != nil {
			t.Fatalf("currentOrders: %v", err)
		}
		result, err := consumeOrdersNext(l.deps.OrdersNextFile, orders, []adapter.BacklogItem{{ID: "352"}})
		if err := l.handlePromotionResult(result, err); err != nil {
			t.Fatalf("handlePromotionResult: %v", err)
		}
		got := readPromotionResult(t, runtimeDir)
		if len(got.AmendRejected) != 1 || got.AmendRejected[0].Order != "352" ||
			!strings.Contains(got.AmendRejected[0].Reason, "stage in flight") {
			t.Fatalf("amend_rejected = %+v, want order 352 with stage-in-flight reason", got.AmendRejected)
		}
	})
	t.Run("dropped orders name no ready issue", func(t *testing.T) {
		l, runtimeDir := newPromotionTestLoop(t)
		promoteProposal(t, l, []string{"chore-side-note"}, nil)
		got := readPromotionResult(t, runtimeDir)
		if len(got.Unbuildable) != 1 || got.Unbuildable[0] != "chore-side-note" {
			t.Fatalf("unbuildable = %v, want [chore-side-note]", got.Unbuildable)
		}
	})
	t.Run("invalid proposal rejected", func(t *testing.T) {
		l, runtimeDir := newPromotionTestLoop(t)
		if err := os.WriteFile(l.deps.OrdersNextFile, []byte("{not json"), 0o644); err != nil {
			t.Fatal(err)
		}
		orders, _ := l.currentOrders()
		result, err := consumeOrdersNext(l.deps.OrdersNextFile, orders, nil)
		_ = l.handlePromotionResult(result, err)
		got := readPromotionResult(t, runtimeDir)
		if got.Status != "rejected" || !strings.Contains(got.Reason, "invalid orders-next.json") {
			t.Fatalf("result = %+v, want rejected with the validation reason", got)
		}
	})
}

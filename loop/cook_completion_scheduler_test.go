package loop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guangshinhaha/shinfactory/event"
	loopruntime "github.com/guangshinhaha/shinfactory/runtime"
)

func TestHandleCompletionFailureForwardsToScheduler(t *testing.T) {
	projectDir := t.TempDir()
	runtimeDir := filepath.Join(projectDir, ".noodle")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatalf("mkdir runtime: %v", err)
	}
	ordersPath := filepath.Join(runtimeDir, "orders.json")
	if err := writeOrdersAtomic(ordersPath, OrdersFile{
		Orders: []Order{{
			ID:     "order-1",
			Status: OrderStatusActive,
			Stages: []Stage{{
				TaskKey: "execute",
				Skill:   "execute",
				Status:  StageStatusActive,
			}},
		}},
	}); err != nil {
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

	schedulerController := &mockController{steerable: true}
	l.cooks.activeCooksByOrder["schedule"] = &cookHandle{
		cookIdentity: cookIdentity{
			orderID:    "schedule",
			stageIndex: 0,
			stage: Stage{
				TaskKey: "schedule",
				Skill:   "schedule",
			},
		},
		session: &steerableSession{
			mockSession: &mockSession{
				id:     "schedule-session",
				status: "running",
				done:   make(chan struct{}),
			},
			ctrl: schedulerController,
		},
	}

	failedCook := &cookHandle{
		cookIdentity: cookIdentity{
			orderID:    "order-1",
			stageIndex: 0,
			stage: Stage{
				TaskKey: "execute",
				Skill:   "execute",
			},
		},
		session: &mockSession{
			id:     "order-1-session",
			status: "failed",
			done:   make(chan struct{}),
		},
	}

	if err := l.handleCompletion(context.Background(), failedCook, StageResultFailed, "failed"); err != nil {
		t.Fatalf("handleCompletion: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		schedulerController.mu.Lock()
		sendCalls := schedulerController.sendCalls
		lastMessage := schedulerController.lastSentMessage
		schedulerController.mu.Unlock()
		if sendCalls > 0 {
			if !strings.Contains(lastMessage, "[stage_failed]") {
				t.Fatalf("scheduler message = %q, want stage_failed event", lastMessage)
			}
			if !strings.Contains(lastMessage, "order=order-1") {
				t.Fatalf("scheduler message = %q, want order id", lastMessage)
			}
			if !strings.Contains(lastMessage, "cook exited with status failed") {
				t.Fatalf("scheduler message = %q, want failure reason", lastMessage)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	schedulerController.mu.Lock()
	sendCalls := schedulerController.sendCalls
	schedulerController.mu.Unlock()
	if sendCalls == 0 {
		t.Fatal("expected scheduler notification on non-success completion")
	}

	updated, err := readOrders(ordersPath)
	if err != nil {
		t.Fatalf("read orders: %v", err)
	}
	if len(updated.Orders) != 1 {
		t.Fatalf("orders count = %d, want 1", len(updated.Orders))
	}
	if updated.Orders[0].Status != OrderStatusFailed {
		t.Fatalf("order status = %q, want %q", updated.Orders[0].Status, OrderStatusFailed)
	}
	if got := updated.Orders[0].Stages[0].Status; got != StageStatusFailed {
		t.Fatalf("stage status = %q, want %q", got, StageStatusFailed)
	}
}

func TestHandleCompletionCancelledClassifiesAsStageTerminal(t *testing.T) {
	projectDir := t.TempDir()
	runtimeDir := filepath.Join(projectDir, ".noodle")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatalf("mkdir runtime: %v", err)
	}
	ordersPath := filepath.Join(runtimeDir, "orders.json")
	if err := writeOrdersAtomic(ordersPath, OrdersFile{
		Orders: []Order{{
			ID:     "order-1",
			Status: OrderStatusActive,
			Stages: []Stage{{
				TaskKey: "execute",
				Skill:   "execute",
				Status:  StageStatusActive,
			}},
		}},
	}); err != nil {
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

	failedCook := &cookHandle{
		cookIdentity: cookIdentity{
			orderID:    "order-1",
			stageIndex: 0,
			stage: Stage{
				TaskKey: "execute",
				Skill:   "execute",
			},
		},
		session: &mockSession{
			id:     "order-1-session",
			status: "cancelled",
			done:   make(chan struct{}),
		},
	}

	if err := l.handleCompletion(context.Background(), failedCook, StageResultCancelled, "cancelled"); err != nil {
		t.Fatalf("handleCompletion: %v", err)
	}

	if l.lastLoopFailure == nil {
		t.Fatal("lastLoopFailure = nil, want classification recorded")
	}
	if l.lastLoopFailure.Class != CycleFailureClassOrderHard {
		t.Fatalf("lastLoopFailure class = %q, want %q", l.lastLoopFailure.Class, CycleFailureClassOrderHard)
	}
	if l.lastLoopFailure.OrderClass != OrderFailureClassStageTerminal {
		t.Fatalf("lastLoopFailure order class = %q, want %q", l.lastLoopFailure.OrderClass, OrderFailureClassStageTerminal)
	}
	if !strings.Contains(l.lastLoopFailure.Message, "cook cancelled with status cancelled") {
		t.Fatalf("lastLoopFailure message = %q, want cancelled failure wording", l.lastLoopFailure.Message)
	}
}

func TestHandleCompletionStageFailEventFailsCleanlyFinishedStage(t *testing.T) {
	projectDir := t.TempDir()
	runtimeDir := filepath.Join(projectDir, ".noodle")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatalf("mkdir runtime: %v", err)
	}
	ordersPath := filepath.Join(runtimeDir, "orders.json")
	if err := writeOrdersAtomic(ordersPath, OrdersFile{
		Orders: []Order{{
			ID:     "order-1",
			Status: OrderStatusActive,
			Stages: []Stage{
				{TaskKey: "quality", Skill: "quality", Status: StageStatusActive},
				{TaskKey: "debrief", Skill: "debrief", Status: StageStatusPending},
			},
		}},
	}); err != nil {
		t.Fatalf("write orders: %v", err)
	}

	worktrees := &fakeWorktree{}
	l := New(projectDir, "noodle", localLandingConfig(), Dependencies{
		Runtimes:   map[string]loopruntime.Runtime{"process": newMockRuntime()},
		Worktree:   worktrees,
		Adapter:    &fakeAdapterRunner{},
		Mise:       &fakeMise{},
		Monitor:    fakeMonitor{},
		Registry:   testLoopRegistry(),
		Now:        time.Now,
		OrdersFile: ordersPath,
	})

	const reason = "Rejected: running ./greet prints nothing, Done when wants hello"
	writer, err := event.NewEventWriter(runtimeDir, "order-1-session")
	if err != nil {
		t.Fatalf("event writer: %v", err)
	}
	payload, err := json.Marshal(event.StageFailPayload{Message: reason})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := writer.Append(context.Background(), event.Event{
		Type:      event.EventStageFail,
		SessionID: "order-1-session",
		Payload:   payload,
	}); err != nil {
		t.Fatalf("append stage_fail: %v", err)
	}

	cook := &cookHandle{
		cookIdentity: cookIdentity{
			orderID:    "order-1",
			stageIndex: 0,
			stage:      Stage{TaskKey: "quality", Skill: "quality"},
		},
		worktreeName: "quality-worktree",
		session: &mockSession{
			id:     "order-1-session",
			status: "completed",
			done:   make(chan struct{}),
		},
	}

	if err := l.handleCompletion(context.Background(), cook, StageResultCompleted, "completed"); err != nil {
		t.Fatalf("handleCompletion: %v", err)
	}

	updated, err := readOrders(ordersPath)
	if err != nil {
		t.Fatalf("read orders: %v", err)
	}
	if got := updated.Orders[0].Status; got != OrderStatusFailed {
		t.Fatalf("order status = %q, want %q", got, OrderStatusFailed)
	}
	if got := updated.Orders[0].Stages[0].Status; got != StageStatusFailed {
		t.Fatalf("quality stage status = %q, want %q", got, StageStatusFailed)
	}
	if got := updated.Orders[0].Stages[1].Status; got != StageStatusPending {
		t.Fatalf("debrief stage status = %q, want it never to start", got)
	}
	if _, parked := l.cooks.pendingReview["order-1"]; parked {
		t.Fatal("failed stage was parked for review")
	}

	// The scheduler reads loop events through mise, so the reason has to land there.
	events, err := os.ReadFile(filepath.Join(runtimeDir, "loop-events.ndjson"))
	if err != nil {
		t.Fatalf("read loop events: %v", err)
	}
	if !strings.Contains(string(events), reason) {
		t.Fatalf("loop events lack the reason:\n%s", events)
	}
}

func TestReadStageFailTreatsUnreadableLogAsFailure(t *testing.T) {
	projectDir := t.TempDir()
	sessionDir := filepath.Join(projectDir, ".noodle", "sessions", "s1")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir session: %v", err)
	}
	l := New(projectDir, "noodle", localLandingConfig(), Dependencies{
		Runtimes: map[string]loopruntime.Runtime{"process": newMockRuntime()},
		Worktree: &fakeWorktree{},
		Adapter:  &fakeAdapterRunner{},
		Mise:     &fakeMise{},
		Monitor:  fakeMonitor{},
		Registry: testLoopRegistry(),
		Now:      time.Now,
	})

	if got := l.readStageFail("s1"); got != nil {
		t.Fatalf("no log: got %#v, want nil", got)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "events.ndjson"), []byte("{truncated\n"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	got := l.readStageFail("s1")
	if got == nil || !strings.Contains(got.Message, "session event log unreadable") {
		t.Fatalf("corrupt log: got %#v, want an unreadable failure", got)
	}
}

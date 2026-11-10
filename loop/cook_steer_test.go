package loop

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/guangshinhaha/shinfactory/event"
	loopruntime "github.com/guangshinhaha/shinfactory/runtime"
)

type testSessionEventSink struct {
	events []event.Event
}

func (s *testSessionEventSink) PublishSessionEvent(_ string, ev event.Event) {
	s.events = append(s.events, ev)
}

func (s *testSessionEventSink) PublishSessionDelta(_ string, _ string, _ time.Time) {}

// mockController records calls and allows configurable behavior.
type mockController struct {
	mu              sync.Mutex
	steerable       bool
	interruptErr    error
	sendMessageErr  error
	interruptCalls  int
	sendCalls       int
	lastSentMessage string
}

func (c *mockController) Steerable() bool { return c.steerable }

func (c *mockController) Interrupt(_ context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.interruptCalls++
	return c.interruptErr
}

func (c *mockController) SendMessage(_ context.Context, prompt string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sendCalls++
	c.lastSentMessage = prompt
	return c.sendMessageErr
}

// steerableSession wraps a mockSession to return a controllable controller.
type steerableSession struct {
	*mockSession
	ctrl loopruntime.AgentController
}

func (s *steerableSession) Controller() loopruntime.AgentController { return s.ctrl }

func newSteerTestLoop(t *testing.T, rt *mockRuntime) *Loop {
	t.Helper()
	projectDir := t.TempDir()
	runtimeDir := filepath.Join(projectDir, ".noodle")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatalf("mkdir runtime: %v", err)
	}
	ordersPath := filepath.Join(runtimeDir, "orders.json")
	return New(projectDir, "noodle", localLandingConfig(), Dependencies{
		Runtimes:   map[string]loopruntime.Runtime{"process": rt},
		Worktree:   &fakeWorktree{},
		Adapter:    &fakeAdapterRunner{},
		Mise:       &fakeMise{},
		Monitor:    fakeMonitor{},
		Registry:   testLoopRegistry(),
		Now:        time.Now,
		OrdersFile: ordersPath,
	})
}

func TestSteerLiveInterruptAndRedirect(t *testing.T) {
	rt := newMockRuntime()
	l := newSteerTestLoop(t, rt)

	ctrl := &mockController{steerable: true}
	sess := &steerableSession{
		mockSession: &mockSession{id: "sess-live", status: "running", done: make(chan struct{})},
		ctrl:        ctrl,
	}
	l.cooks.activeCooksByOrder["order-1"] = &cookHandle{
		cookIdentity: cookIdentity{orderID: "order-1"},
		session:      sess,
		worktreeName: "wt-1",
	}

	if err := l.steer("wt-1", "change direction"); err != nil {
		t.Fatalf("steer: %v", err)
	}

	// steerLive runs async — wait for it.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ctrl.mu.Lock()
		sent := ctrl.sendCalls
		ctrl.mu.Unlock()
		if sent > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()

	if ctrl.interruptCalls != 1 {
		t.Fatalf("interrupt calls = %d, want 1", ctrl.interruptCalls)
	}
	if ctrl.sendCalls != 1 {
		t.Fatalf("send calls = %d, want 1", ctrl.sendCalls)
	}
	if ctrl.lastSentMessage != "change direction" {
		t.Fatalf("sent message = %q, want %q", ctrl.lastSentMessage, "change direction")
	}

	// Session should NOT have been killed — still running.
	if sess.status == "killed" {
		t.Fatal("steerable session was killed — should have been redirected")
	}
	// Cook should still be in the active map.
	if _, ok := l.cooks.activeCooksByOrder["order-1"]; !ok {
		t.Fatal("cook was removed from active map — should remain for live steer")
	}
}

func TestSteerNonSteerableFallsBackToRespawn(t *testing.T) {
	rt := newMockRuntime()
	l := newSteerTestLoop(t, rt)

	sess := &mockSession{id: "sess-noop", status: "running", done: make(chan struct{})}
	l.cooks.activeCooksByOrder["order-2"] = &cookHandle{
		cookIdentity: cookIdentity{
			orderID: "order-2",
			stage:   Stage{TaskKey: "execute", Prompt: "original", Provider: "claude", Model: "claude-sonnet-4-6"},
		},
		session:      sess,
		worktreeName: "wt-2",
		worktreePath: t.TempDir(),
		orderStatus:  OrderStatusActive,
	}

	err := l.steer("wt-2", "new direction")
	if err != nil {
		t.Fatalf("steer: %v", err)
	}

	// Original session should be killed.
	if sess.status != "killed" {
		t.Fatalf("session status = %q, want killed", sess.status)
	}
	// spawnCook re-adds the order with a new session — verify the session changed.
	newCook, ok := l.cooks.activeCooksByOrder["order-2"]
	if !ok {
		t.Fatal("respawned cook not in active map")
	}
	if newCook.session.ID() == "sess-noop" {
		t.Fatal("session was not respawned — same session ID")
	}
}

func TestSteerLiveInterruptFailureLogs(t *testing.T) {
	rt := newMockRuntime()
	l := newSteerTestLoop(t, rt)

	ctrl := &mockController{
		steerable:    true,
		interruptErr: errors.New("interrupt timeout"),
	}
	sess := &steerableSession{
		mockSession: &mockSession{id: "sess-fail", status: "running", done: make(chan struct{})},
		ctrl:        ctrl,
	}
	l.cooks.activeCooksByOrder["order-3"] = &cookHandle{
		cookIdentity: cookIdentity{orderID: "order-3"},
		session:      sess,
		worktreeName: "wt-3",
	}

	// steer returns nil (async), but the goroutine should handle the failure.
	if err := l.steer("wt-3", "redirect"); err != nil {
		t.Fatalf("steer: %v", err)
	}

	// Wait for async steer to complete.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ctrl.mu.Lock()
		calls := ctrl.interruptCalls
		ctrl.mu.Unlock()
		if calls > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()

	if ctrl.interruptCalls != 1 {
		t.Fatalf("interrupt calls = %d, want 1", ctrl.interruptCalls)
	}
	// SendMessage should NOT be called if interrupt failed.
	if ctrl.sendCalls != 0 {
		t.Fatalf("send calls = %d, want 0 (interrupt failed)", ctrl.sendCalls)
	}
}

func TestSteerSessionNotFound(t *testing.T) {
	rt := newMockRuntime()
	l := newSteerTestLoop(t, rt)

	err := l.steer("nonexistent", "hello")
	if err == nil {
		t.Fatal("expected error for missing session")
	}
	if err.Error() != "session not found" {
		t.Fatalf("error = %q, want 'session not found'", err.Error())
	}
}

func TestSteerScheduleTargetsActiveScheduleSession(t *testing.T) {
	rt := newMockRuntime()
	l := newSteerTestLoop(t, rt)

	ctrl := &mockController{steerable: true}
	sess := &steerableSession{
		mockSession: &mockSession{id: "sess-schedule", status: "running", done: make(chan struct{})},
		ctrl:        ctrl,
	}
	l.cooks.activeCooksByOrder[ScheduleTaskKey()] = &cookHandle{
		cookIdentity: cookIdentity{
			orderID: ScheduleTaskKey(),
			stage: Stage{
				TaskKey: ScheduleTaskKey(),
			},
		},
		session: sess,
	}

	if err := l.steer(ScheduleTaskKey(), "focus on auth bugs"); err != nil {
		t.Fatalf("steer: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ctrl.mu.Lock()
		sent := ctrl.sendCalls
		ctrl.mu.Unlock()
		if sent > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()

	if ctrl.interruptCalls != 1 {
		t.Fatalf("interrupt calls = %d, want 1", ctrl.interruptCalls)
	}
	if ctrl.sendCalls != 1 {
		t.Fatalf("send calls = %d, want 1", ctrl.sendCalls)
	}
	if ctrl.lastSentMessage != "focus on auth bugs" {
		t.Fatalf("sent message = %q, want %q", ctrl.lastSentMessage, "focus on auth bugs")
	}
}

func TestSteerScheduleNonSteerableReschedulesWithoutRespawn(t *testing.T) {
	rt := newMockRuntime()
	l := newSteerTestLoop(t, rt)

	sess := &mockSession{id: "sess-schedule-noop", status: "running", done: make(chan struct{})}
	l.cooks.activeCooksByOrder[ScheduleTaskKey()] = &cookHandle{
		cookIdentity: cookIdentity{
			orderID: ScheduleTaskKey(),
			stage: Stage{
				TaskKey: ScheduleTaskKey(),
			},
		},
		session: sess,
	}

	if err := l.steer(ScheduleTaskKey(), "prioritize auth hardening"); err != nil {
		t.Fatalf("steer: %v", err)
	}

	if sess.status == "killed" {
		t.Fatal("schedule session was killed, want reschedule-only behavior")
	}
	if len(rt.calls) != 0 {
		t.Fatalf("dispatch calls = %d, want 0", len(rt.calls))
	}

	orders, err := readOrders(filepath.Join(l.runtimeDir, "orders.json"))
	if err != nil {
		t.Fatalf("read orders: %v", err)
	}
	if len(orders.Orders) != 1 {
		t.Fatalf("orders count = %d, want 1", len(orders.Orders))
	}
	if orders.Orders[0].Rationale != "Chef steer: prioritize auth hardening" {
		t.Fatalf("rationale = %q", orders.Orders[0].Rationale)
	}
}

func TestSteerRecordsUserPromptEvent(t *testing.T) {
	rt := newMockRuntime()
	l := newSteerTestLoop(t, rt)
	sink := &testSessionEventSink{}
	l.deps.EventSink = sink

	ctrl := &mockController{steerable: true}
	sess := &steerableSession{
		mockSession: &mockSession{id: "sess-user-event", status: "running", done: make(chan struct{})},
		ctrl:        ctrl,
	}
	l.cooks.activeCooksByOrder["order-user-event"] = &cookHandle{
		cookIdentity: cookIdentity{orderID: "order-user-event"},
		session:      sess,
		worktreeName: "wt-user-event",
	}

	if err := l.steer("wt-user-event", "please rewrite tests"); err != nil {
		t.Fatalf("steer: %v", err)
	}

	reader := event.NewEventReader(l.runtimeDir)
	events, err := reader.ReadSession("sess-user-event", event.EventFilter{
		Types: map[event.EventType]struct{}{
			event.EventAction: {},
		},
	})
	if err != nil {
		t.Fatalf("read session events: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected at least one action event")
	}

	var payload struct {
		Tool    string `json:"tool"`
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal(events[len(events)-1].Payload, &payload); err != nil {
		t.Fatalf("decode action payload: %v", err)
	}
	if payload.Tool != "user" {
		t.Fatalf("tool = %q, want user", payload.Tool)
	}
	if payload.Summary != "please rewrite tests" {
		t.Fatalf("summary = %q, want %q", payload.Summary, "please rewrite tests")
	}
	if len(sink.events) == 0 {
		t.Fatal("expected user steer event to be published to sink")
	}
}

func TestSteerSerializesConcurrentSteers(t *testing.T) {
	rt := newMockRuntime()
	l := newSteerTestLoop(t, rt)

	var callOrder atomic.Int64
	var firstSendStart, secondSendStart atomic.Int64

	ctrl := &mockController{steerable: true}
	// Override SendMessage to track ordering.
	slowCtrl := &serializedController{
		mockController: ctrl,
		sendDelay:      50 * time.Millisecond,
		callOrder:      &callOrder,
		firstStart:     &firstSendStart,
		secondStart:    &secondSendStart,
	}

	sess := &steerableSession{
		mockSession: &mockSession{id: "sess-serial", status: "running", done: make(chan struct{})},
		ctrl:        slowCtrl,
	}
	l.cooks.activeCooksByOrder["order-s"] = &cookHandle{
		cookIdentity: cookIdentity{orderID: "order-s"},
		session:      sess,
		worktreeName: "wt-serial",
	}

	// Launch two steers concurrently.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = l.steer("wt-serial", "first")
	}()
	go func() {
		defer wg.Done()
		_ = l.steer("wt-serial", "second")
	}()
	wg.Wait()

	// Wait for both async operations.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		slowCtrl.mu.Lock()
		sends := slowCtrl.sendCalls
		slowCtrl.mu.Unlock()
		if sends >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Both steers should have completed.
	slowCtrl.mu.Lock()
	sends := slowCtrl.sendCalls
	slowCtrl.mu.Unlock()
	if sends != 2 {
		t.Fatalf("send calls = %d, want 2", sends)
	}

	// Verify serialization: second send should start after first finishes.
	first := firstSendStart.Load()
	second := secondSendStart.Load()
	if first == 0 || second == 0 {
		t.Fatal("expected both sends to have recorded timestamps")
	}
	// The sends should be separated by at least the delay.
	diff := second - first
	if diff < 0 {
		diff = -diff
	}
	if diff < int64(40*time.Millisecond) {
		t.Fatalf("sends overlapped: diff=%v, want >= 40ms", time.Duration(diff))
	}
}

// serializedController wraps mockController with a delay to test serialization.
type serializedController struct {
	*mockController
	sendDelay   time.Duration
	callOrder   *atomic.Int64
	firstStart  *atomic.Int64
	secondStart *atomic.Int64
}

func (c *serializedController) SendMessage(ctx context.Context, prompt string) error {
	seq := c.callOrder.Add(1)
	now := time.Now().UnixNano()
	if seq == 1 {
		c.firstStart.Store(now)
	} else {
		c.secondStart.Store(now)
	}
	time.Sleep(c.sendDelay)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.sendCalls++
	c.lastSentMessage = prompt
	return c.sendMessageErr
}

// Steering the last allowed attempt resumes it: the respawn is the same attempt,
// so the spend cap must not kill it, and the attempt count stays put.
func TestSteerRespawnOnTheLastAllowedAttemptIsNotStoppedByTheSpendCap(t *testing.T) {
	rt := newMockRuntime()
	l := newSteerTestLoop(t, rt)
	for i := 0; i < l.config.Spend.MaxAttempts; i++ {
		l.recordStageLaunch("order-2", "execute", 0)
	}

	sess := &mockSession{id: "sess-last", status: "running", done: make(chan struct{})}
	l.cooks.activeCooksByOrder["order-2"] = &cookHandle{
		cookIdentity: cookIdentity{
			orderID: "order-2",
			stage:   Stage{TaskKey: "execute", Prompt: "original", Provider: "claude", Model: "claude-sonnet-4-6"},
		},
		session:      sess,
		worktreeName: "wt-2",
		worktreePath: t.TempDir(),
		orderStatus:  OrderStatusActive,
	}

	if err := l.steer("wt-2", "new direction"); err != nil {
		t.Fatalf("steer: %v", err)
	}
	newCook, ok := l.cooks.activeCooksByOrder["order-2"]
	if !ok || newCook.session.ID() == "sess-last" {
		t.Fatal("steer on the last allowed attempt did not respawn the cook")
	}
	if got := l.orderSpendOf("order-2").Attempts["execute"]; got != l.config.Spend.MaxAttempts {
		t.Fatalf("execute attempts = %d, want %d: a steer is not a new attempt", got, l.config.Spend.MaxAttempts)
	}
}

func orcaCook(orderID string, stage Stage, sess loopruntime.SessionHandle) *cookHandle {
	return &cookHandle{
		cookIdentity:      cookIdentity{orderID: orderID, stage: stage},
		session:           sess,
		worktreeName:      "wt-" + orderID,
		worktreePath:      "/does/not/matter",
		orderStatus:       OrderStatusActive,
		dispatchedRuntime: "orca",
	}
}

func assertNoSteerEffects(t *testing.T, l *Loop, rt *mockRuntime, sess *mockSession, sessionID string) {
	t.Helper()
	if sess.status == "killed" {
		t.Fatal("session was killed by a refused steer")
	}
	if len(rt.calls) != 0 {
		t.Fatalf("dispatch calls = %d, want 0", len(rt.calls))
	}
	if _, err := os.Stat(filepath.Join(l.runtimeDir, "orders.json")); !os.IsNotExist(err) {
		t.Fatalf("orders.json touched by a refused steer (stat err = %v)", err)
	}
	events, err := event.NewEventReader(l.runtimeDir).ReadSession(sessionID, event.EventFilter{})
	if err != nil {
		t.Fatalf("read session events: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("refused steer wrote %d session events, want 0 (current_action must not become the steer text)", len(events))
	}
}

func TestSteerRefusedOnOrcaAgentTarget(t *testing.T) {
	rt := newMockRuntime()
	l := newSteerTestLoop(t, rt)
	sess := &mockSession{id: "sess-orca", status: "running", done: make(chan struct{})}
	l.cooks.activeCooksByOrder["order-orca"] = orcaCook("order-orca", Stage{TaskKey: "execute"}, sess)

	err := l.steer("wt-order-orca", "new direction")
	if err == nil || !strings.Contains(err.Error(), "Open in Orca") {
		t.Fatalf("steer err = %v, want a refusal that points to Open in Orca", err)
	}
	if got := l.cooks.activeCooksByOrder["order-orca"]; got == nil || got.session.ID() != "sess-orca" {
		t.Fatal("refused steer replaced or removed the active cook")
	}
	assertNoSteerEffects(t, l, rt, sess, "sess-orca")
}

func TestSteerRefusedOnOrcaScheduleTarget(t *testing.T) {
	rt := newMockRuntime()
	l := newSteerTestLoop(t, rt)
	sess := &mockSession{id: "sess-orca-schedule", status: "running", done: make(chan struct{})}
	l.cooks.activeCooksByOrder[ScheduleTaskKey()] = orcaCook(ScheduleTaskKey(), Stage{TaskKey: ScheduleTaskKey()}, sess)

	err := l.steer(ScheduleTaskKey(), "prioritize auth hardening")
	if err == nil || !strings.Contains(err.Error(), "Open in Orca") {
		t.Fatalf("steer err = %v, want a refusal that points to Open in Orca", err)
	}
	assertNoSteerEffects(t, l, rt, sess, "sess-orca-schedule")
}

// A steer resumes the same attempt, so it must never run the retry reset that
// discards uncommitted work.
func TestSteerRespawnKeepsUncommittedWorkOnRetriedStage(t *testing.T) {
	rt := newMockRuntime()
	l := newSteerTestLoop(t, rt)
	stage := Stage{TaskKey: "execute", Prompt: "original", Provider: "claude", Model: "claude-sonnet-4-6"}
	name := l.cookWorktreeName("order-retry", 0, stage.TaskKey)
	wt := l.worktreePath(name)
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", wt}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	tracked := filepath.Join(wt, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("committed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "base")
	if err := os.WriteFile(tracked, []byte("edited, uncommitted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	untracked := filepath.Join(wt, "new.txt")
	if err := os.WriteFile(untracked, []byte("untracked work\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sess := &mockSession{id: "sess-retry", status: "running", done: make(chan struct{})}
	l.cooks.activeCooksByOrder["order-retry"] = &cookHandle{
		cookIdentity: cookIdentity{orderID: "order-retry", stage: stage},
		session:      sess,
		worktreeName: name,
		worktreePath: wt,
		orderStatus:  OrderStatusActive,
		attempt:      1,
	}

	if err := l.steer(name, "new direction"); err != nil {
		t.Fatalf("steer: %v", err)
	}
	if got := l.cooks.activeCooksByOrder["order-retry"]; got == nil || got.session.ID() == "sess-retry" {
		t.Fatal("steer did not respawn the cook")
	}
	if b, _ := os.ReadFile(tracked); string(b) != "edited, uncommitted\n" {
		t.Fatalf("tracked edit lost across steer respawn: %q", b)
	}
	if _, err := os.Stat(untracked); err != nil {
		t.Fatalf("untracked file lost across steer respawn: %v", err)
	}
}

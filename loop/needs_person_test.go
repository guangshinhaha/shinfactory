package loop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guangshinhaha/shinfactory/adapter"
	"github.com/guangshinhaha/shinfactory/config"
	"github.com/guangshinhaha/shinfactory/event"
	loopruntime "github.com/guangshinhaha/shinfactory/runtime"
	"github.com/guangshinhaha/shinfactory/worktree"
)

// stubStopGH puts a gh first on PATH that logs every call and remembers whether the
// issue carries the needs-shin label, so the real backlog-stop script runs
// against it without touching GitHub.
func stubStopGH(t *testing.T) (logPath string) {
	t.Helper()
	dir := t.TempDir()
	logPath = filepath.Join(dir, "gh.log")
	stub := `#!/bin/sh
printf '%s\n' "$*" >> "` + logPath + `"
case "$1 $2" in
  "issue view") [ -f "` + dir + `/labelled" ] && echo true || echo false ;;
  "issue edit") case "$*" in *--add-label*) touch "` + dir + `/labelled" ;; esac ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

// githubStopRunner wires the real GitHub adapter scripts, so the loop's stop
// goes through the same code a repo using them runs.
func githubStopRunner(t *testing.T, projectDir string) *adapter.Runner {
	t.Helper()
	scripts, err := filepath.Abs(filepath.Join("..", "defaults", "adapters", "github"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.Adapters = map[string]config.AdapterConfig{"backlog": {Scripts: map[string]string{
		"stop": filepath.Join(scripts, "backlog-stop"),
		"done": filepath.Join(scripts, "backlog-done"),
	}}}
	return adapter.NewRunner(projectDir, cfg)
}

func ghLog(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

type stopEnv struct {
	loop       *Loop
	runtimeDir string
	ordersPath string
	log        string
}

func newStopEnv(t *testing.T) *stopEnv {
	t.Helper()
	log := stubStopGH(t)
	projectDir := t.TempDir()
	runtimeDir := filepath.Join(projectDir, ".noodle")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ordersPath := filepath.Join(runtimeDir, "orders.json")
	l := New(projectDir, "noodle", localLandingConfig(), Dependencies{
		Runtimes:   map[string]loopruntime.Runtime{"process": newMockRuntime()},
		Worktree:   &fakeWorktree{},
		Adapter:    githubStopRunner(t, projectDir),
		Mise:       &fakeMise{},
		Monitor:    fakeMonitor{},
		Registry:   testLoopRegistry(),
		Now:        time.Now,
		OrdersFile: ordersPath,
	})
	return &stopEnv{loop: l, runtimeDir: runtimeDir, ordersPath: ordersPath, log: log}
}

// reject runs one round: a fresh active order for issue 7 whose quality stage
// reports stage_fail with message.
func (e *stopEnv) reject(t *testing.T, round int, message string) {
	t.Helper()
	if err := writeOrdersAtomic(e.ordersPath, OrdersFile{Orders: []Order{{
		ID: "7", Status: OrderStatusActive,
		Stages: []Stage{{TaskKey: "quality", Skill: "quality", Status: StageStatusActive}},
	}}}); err != nil {
		t.Fatal(err)
	}
	e.loop.syncCanonicalOrderFromLegacy(mustOrder(t, e.ordersPath))
	sid := "round-" + string(rune('0'+round))
	writer, err := event.NewEventWriter(e.runtimeDir, sid)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(event.StageFailPayload{Message: message})
	if err := writer.Append(context.Background(), event.Event{Type: event.EventStageFail, SessionID: sid, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	cook := &cookHandle{
		cookIdentity: cookIdentity{orderID: "7", stage: Stage{TaskKey: "quality", Skill: "quality"}},
		worktreeName: "w",
		session:      &mockSession{id: sid, status: "completed", done: make(chan struct{})},
	}
	if err := e.loop.handleCompletion(context.Background(), cook, StageResultCompleted, "completed"); err != nil {
		t.Fatalf("handleCompletion: %v", err)
	}
}

func mustOrder(t *testing.T, path string) Order {
	t.Helper()
	orders, err := readOrders(path)
	if err != nil || len(orders.Orders) == 0 {
		t.Fatalf("read orders: %v", err)
	}
	return orders.Orders[0]
}

// Stop 1: an issue with no observable Done when. The first rejection stops it.
func TestStopForPersonWhenTheIssueIsUnbuildable(t *testing.T) {
	env := newStopEnv(t)
	env.reject(t, 1, "Unbuildable: the issue says \"make it better\" with nothing to check. Shin, what should running it print?")

	log := ghLog(t, env.log)
	if strings.Count(log, "issue comment 7") != 1 ||
		!strings.Contains(log, "issue comment 7 --body shinfactory: stopped. Unbuildable: the issue says") {
		t.Fatalf("want one comment on issue 7 carrying the reason:\n%s", log)
	}
	if !strings.Contains(log, "issue edit 7 --add-label needs-shin") {
		t.Fatalf("issue 7 not labelled needs-shin:\n%s", log)
	}
}

// Stop 2: the first rejected round retries quietly, the second asks a person
// once, and a third rejection while the label is up adds no second comment.
func TestStopForPersonAfterTwoFailedRounds(t *testing.T) {
	env := newStopEnv(t)

	env.reject(t, 1, "Rejected: Done when missed, ./greet printed nothing.")
	if log := ghLog(t, env.log); strings.Contains(log, "issue comment") || strings.Contains(log, "needs-shin") {
		t.Fatalf("one failed round must not reach a person:\n%s", log)
	}

	env.reject(t, 2, "Rejected: ./greet still prints nothing.")
	log := ghLog(t, env.log)
	if strings.Count(log, "issue comment 7") != 1 ||
		!strings.Contains(log, "Two fix rounds failed. Last rejection: Rejected: ./greet still prints nothing.") {
		t.Fatalf("want one comment after round 2 naming the decision:\n%s", log)
	}
	if !strings.Contains(log, "issue edit 7 --add-label needs-shin") {
		t.Fatalf("issue 7 not labelled needs-shin:\n%s", log)
	}

	env.reject(t, 3, "Rejected: again.")
	env.reject(t, 4, "Rejected: and again.")
	if n := strings.Count(ghLog(t, env.log), "issue comment 7"); n != 1 {
		t.Fatalf("comments = %d, want exactly 1 while the label is up", n)
	}
}

// Stop 3: a merge conflict nobody can resolve in the loop.
func TestStopForPersonOnAnUnresolvableMergeConflict(t *testing.T) {
	logPath := stubStopGH(t)
	orders := OrdersFile{Orders: []Order{{
		ID: "7", Title: "conflict", Status: OrderStatusActive,
		Stages: []Stage{{TaskKey: "execute", Skill: "execute", Provider: "claude", Model: "claude-opus-4-6", Status: StageStatusPending}},
	}}}
	env := newIntegrationEnv(t, orders, func(ic *integrationCfg) {
		ic.cfg.Mode = "auto"
		ic.mergeErr = &worktree.MergeConflictError{Branch: "noodle/7"}
	})
	env.loop.deps.Adapter = githubStopRunner(t, env.projectDir)
	seedCanonicalFromOrders(env.loop, orders)

	if err := env.loop.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	env.completeSessions("completed")
	if err := env.loop.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}

	log := ghLog(t, logPath)
	if strings.Count(log, "issue comment 7") != 1 || !strings.Contains(log, "Merge conflict the factory cannot resolve") {
		t.Fatalf("want one comment on issue 7 naming the conflict:\n%s", log)
	}
	if !strings.Contains(log, "issue edit 7 --add-label needs-shin") {
		t.Fatalf("issue 7 not labelled needs-shin:\n%s", log)
	}
}

// A backlog with no stop action (todos.md) has nobody to tell: the stop is
// skipped and the order's failure stands.
func TestStopForPersonSkipsABacklogWithoutAStopAction(t *testing.T) {
	env := newStopEnv(t)
	cfg := config.DefaultConfig()
	cfg.Adapters = map[string]config.AdapterConfig{"backlog": {Scripts: map[string]string{"sync": "true"}}}
	env.loop.deps.Adapter = adapter.NewRunner(t.TempDir(), cfg)

	env.reject(t, 1, "Unbuildable: nothing observable.")
	if got := mustOrder(t, env.ordersPath).Status; got != OrderStatusFailed {
		t.Fatalf("order status = %q, want failed", got)
	}
	if log := ghLog(t, env.log); log != "" {
		t.Fatalf("gh was called for a backlog with no stop action:\n%s", log)
	}
}

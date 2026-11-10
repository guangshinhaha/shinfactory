package loop

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/guangshinhaha/shinfactory/internal/linestatus"
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
  "issue view") if [ -f "` + dir + `/labelled" ]; then echo needs-shin; fi; echo ready ;;
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
		!strings.Contains(log, "issue comment 7 --body shinfactory: stopped. The issue has no observable Done when, or something only a person can settle blocks it. the issue says") {
		t.Fatalf("want one comment on issue 7 carrying the reason:\n%s", log)
	}
	if !strings.Contains(log, "issue edit 7 --remove-label ready --add-label needs-shin") {
		t.Fatalf("issue 7 not labelled needs-shin:\n%s", log)
	}
	// `shinfactory status` lists the stop from this record: the backlog no longer sees the issue.
	rep, err := linestatus.Read(env.runtimeDir)
	if err != nil || len(rep.NeedsShin) != 1 || rep.NeedsShin[0].Order == "" {
		t.Fatalf("status needs_shin = %+v, err = %v", rep.NeedsShin, err)
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
		!strings.Contains(log, "Last rejection: Rejected: ./greet still prints nothing.") {
		t.Fatalf("want one comment after round 2 naming the decision:\n%s", log)
	}
	if !strings.Contains(log, "issue edit 7 --remove-label ready --add-label needs-shin") {
		t.Fatalf("issue 7 not labelled needs-shin:\n%s", log)
	}

	env.reject(t, 3, "Rejected: again.")
	env.reject(t, 4, "Rejected: and again.")
	if n := strings.Count(ghLog(t, env.log), "issue comment 7"); n != 1 {
		t.Fatalf("comments = %d, want exactly 1 while the label is up", n)
	}
}

// An old rejection must not combine with a much later one: only rounds inside
// failedRoundsWindow count toward the stop.
func TestFailedRoundsExpireAfterTheWindow(t *testing.T) {
	env := newStopEnv(t)
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	env.loop.deps.Now = func() time.Time { return now }

	if got := env.loop.bumpFailedRounds("7"); got != 1 {
		t.Fatalf("first rejection counted %d, want 1", got)
	}
	now = now.Add(failedRoundsWindow - time.Minute)
	if got := env.loop.bumpFailedRounds("7"); got != 2 {
		t.Fatalf("rejection inside the window counted %d, want 2", got)
	}
	now = now.Add(failedRoundsWindow + time.Minute)
	if got := env.loop.bumpFailedRounds("7"); got != 1 {
		t.Fatalf("rejection after the window counted %d, want 1 (the old rounds expired)", got)
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
	if strings.Count(log, "issue comment 7") != 1 || !strings.Contains(log, "A merge conflict the factory cannot resolve") {
		t.Fatalf("want one comment on issue 7 naming the conflict:\n%s", log)
	}
	if !strings.Contains(log, "issue edit 7 --remove-label ready --add-label needs-shin") {
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

// A sensitive repo's issue comment says what kind of stop it is and leaves the
// agent's own words out.
func TestStopForPersonKeepsDetailOutOfCommentsForSensitiveRepos(t *testing.T) {
	env := newStopEnv(t)
	env.loop.config.Routing.Sensitive = true
	env.reject(t, 1, "Unbuildable: student Tan Ah Kow (S1234567A) in fixtures/roster.csv")

	log := ghLog(t, env.log)
	if strings.Contains(log, "S1234567A") || strings.Contains(log, "roster.csv") {
		t.Fatalf("agent detail reached the issue comment:\n%s", log)
	}
	if !strings.Contains(log, "issue comment 7 --body shinfactory: stopped.") || !strings.Contains(log, "needs-shin") {
		t.Fatalf("sensitive stop still has to comment and label:\n%s", log)
	}
}

// spendCapEnv sets up a pending order 7 whose stage the cycle would launch,
// with caps from tweak and the real backlog-stop script on a stub gh.
func spendCapEnv(t *testing.T, tweak func(*config.SpendConfig)) (*integrationEnv, string) {
	t.Helper()
	logPath := stubStopGH(t)
	orders := OrdersFile{Orders: []Order{{
		ID: "7", Title: "runaway", Status: OrderStatusActive,
		Stages: []Stage{{TaskKey: "execute", Skill: "execute", Provider: "claude", Model: "claude-opus-4-6", Status: StageStatusPending}},
	}}}
	env := newIntegrationEnv(t, orders, func(ic *integrationCfg) {
		ic.cfg.Mode = "auto"
		tweak(&ic.cfg.Spend)
	})
	env.loop.deps.Adapter = githubStopRunner(t, env.projectDir)
	seedCanonicalFromOrders(env.loop, orders)
	return env, logPath
}

func assertSpendStop(t *testing.T, env *integrationEnv, logPath, wantReason string) {
	t.Helper()
	log := ghLog(t, logPath)
	if strings.Count(log, "issue comment 7") != 1 || !strings.Contains(log, wantReason) || !strings.Contains(log, "Spending cap reached") {
		t.Fatalf("want one comment on issue 7 saying %q:\n%s", wantReason, log)
	}
	if !strings.Contains(log, "issue edit 7 --remove-label ready --add-label needs-shin") {
		t.Fatalf("issue 7 not labelled needs-shin:\n%s", log)
	}
	if got := mustOrder(t, env.loop.deps.OrdersFile).Status; got != OrderStatusFailed {
		t.Fatalf("order status = %q, want failed", got)
	}
	if got := len(env.loop.cooks.activeCooksByOrder); got != 0 {
		t.Fatalf("active cooks = %d, want 0: the capped stage must not launch", got)
	}
}

// Cap 1: an order that already ran its stage max_attempts times stops.
func TestOrderStopsAtTheAttemptCap(t *testing.T) {
	env, logPath := spendCapEnv(t, func(s *config.SpendConfig) { s.MaxAttempts = 3; s.MaxCostUSD = 1000 })
	for i := 0; i < 3; i++ {
		env.loop.recordStageLaunch("7", "execute", 0)
	}
	if err := env.loop.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertSpendStop(t, env, logPath, `it already ran the "execute" stage 3 times this round (cap 3)`)
	if !strings.Contains(ghLog(t, logPath), "the cost cap did not apply") {
		t.Fatal("a stop with no reported cost must say the cost cap did not apply")
	}
}

// Cap 2: an order whose finished stages cost the ceiling stops.
func TestOrderStopsAtTheCostCap(t *testing.T) {
	env, logPath := spendCapEnv(t, func(s *config.SpendConfig) { s.MaxAttempts = 10; s.MaxCostUSD = 5 })
	env.loop.recordStageLaunch("7", "execute", 0)
	env.loop.recordStageCost("7", 3.25)
	env.loop.recordStageCost("7", 2)
	if err := env.loop.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertSpendStop(t, env, logPath, "its stages already cost $5.25 (cap $5.00)")
}

// Under both caps the stage launches and the ledger counts the attempt.
func TestOrderUnderTheCapsLaunches(t *testing.T) {
	env, logPath := spendCapEnv(t, func(s *config.SpendConfig) { s.MaxAttempts = 3; s.MaxCostUSD = 5 })
	env.loop.recordStageLaunch("7", "execute", 0)
	env.loop.recordStageCost("7", 1)
	if err := env.loop.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if log := ghLog(t, logPath); log != "" {
		t.Fatalf("an order under its caps reached a person:\n%s", log)
	}
	if got := env.loop.orderSpendOf("7").Attempts["execute"]; got != 2 {
		t.Fatalf("execute attempts = %d, want 2 after the launch", got)
	}
}

// A quality-reject fix round starts the attempt count over; cost carries on.
func TestFixRoundResetsAttemptsKeepsCost(t *testing.T) {
	env, _ := spendCapEnv(t, func(s *config.SpendConfig) { s.MaxAttempts = 3; s.MaxCostUSD = 1000 })
	for i := 0; i < 3; i++ {
		env.loop.recordStageLaunch("7", "execute", 0)
	}
	env.loop.recordStageCost("7", 2)
	if why := env.loop.overSpendCap("7", "execute", 0); why == "" {
		t.Fatal("round 0 at 3 attempts must be over the cap")
	}
	if why := env.loop.overSpendCap("7", "execute", 1); why != "" {
		t.Fatalf("a new fix round must not inherit the old round's attempts: %s", why)
	}
	env.loop.recordStageLaunch("7", "execute", 1)
	spend := env.loop.orderSpendOf("7")
	if spend.Attempts["execute"] != 1 || spend.CostUSD != 2 {
		t.Fatalf("after a new round: attempts=%d cost=%v, want 1 and 2", spend.Attempts["execute"], spend.CostUSD)
	}
}

// The ledger is written through a temp file and rename, so a crash mid-write
// cannot leave a truncated file that resets every count.
func TestOrderSpendWriteLeavesNoTempFiles(t *testing.T) {
	env, _ := spendCapEnv(t, func(s *config.SpendConfig) {})
	env.loop.recordStageLaunch("7", "execute", 0)
	entries, err := os.ReadDir(env.loop.runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp.") {
			t.Fatalf("leftover temp file %s", e.Name())
		}
	}
	if got := env.loop.orderSpendOf("7").Attempts["execute"]; got != 1 {
		t.Fatalf("attempts = %d, want 1", got)
	}
}

// A skipped order is abandoned; its ledger entry goes with it.
func TestSkipClearsOrderSpend(t *testing.T) {
	env, _ := spendCapEnv(t, func(s *config.SpendConfig) {})
	env.loop.recordStageLaunch("7", "execute", 0)
	env.loop.recordStageCost("7", 1)
	if err := env.loop.controlSkip("7"); err != nil {
		t.Fatal(err)
	}
	if spend := env.loop.orderSpendOf("7"); spend.CostUSD != 0 || spend.totalAttempts() != 0 {
		t.Fatalf("spend after skip = %+v, want empty", spend)
	}
}

// A stage killed before it finishes still charges what it spent.
func TestKilledStageCostReachesTheLedger(t *testing.T) {
	env, _ := spendCapEnv(t, func(s *config.SpendConfig) {})
	cook := &cookHandle{
		cookIdentity: cookIdentity{orderID: "7", stage: Stage{TaskKey: "execute", Skill: "execute"}},
		session:      &mockSession{id: "s1", done: make(chan struct{}), cost: 4.5},
	}
	env.loop.recordStageLaunch("7", "execute", 0)
	env.loop.trackCookCompleted(cook, StageResult{Status: StageResultCancelled})
	if got := env.loop.orderSpendOf("7").CostUSD; got != 4.5 {
		t.Fatalf("cost = %v, want 4.5", got)
	}
}

// A retry back at round 0 must not wipe attempts already used in a later round,
// and a cleared order must not be recreated by a late cost.
func TestSpendLedgerIgnoresLowerRoundsAndClearedOrders(t *testing.T) {
	env, _ := spendCapEnv(t, func(s *config.SpendConfig) { s.MaxAttempts = 2 })
	env.loop.recordStageLaunch("7", "execute", 2)
	env.loop.recordStageLaunch("7", "execute", 2)
	if why := env.loop.overSpendCap("7", "execute", 0); why == "" {
		t.Fatal("a round-0 retry must still see the round-2 attempts")
	}
	env.loop.clearOrderSpend("7")
	env.loop.recordStageCost("7", 3)
	if spend := env.loop.orderSpendOf("7"); spend.CostUSD != 0 {
		t.Fatalf("cost after clear = %v, want 0", spend.CostUSD)
	}
}

// A requeue starts the order over, so it must not stay stopped by the spend
// that stopped it the first time.
func TestControlRequeueClearsTheOrdersSpend(t *testing.T) {
	h := newGateHarness(t, "none", "execute")
	h.ln.mergeErr = errors.New("boom")
	h.next(t)
	h.failedWith(t, "ship error")
	h.l.recordStageLaunch("42", "execute", 0)
	if h.l.orderSpendOf("42").totalAttempts() == 0 {
		t.Fatal("fixture recorded no spend")
	}
	if err := h.l.controlRequeue("42"); err != nil {
		t.Fatal(err)
	}
	if n := h.l.orderSpendOf("42").totalAttempts(); n != 0 {
		t.Fatalf("attempts after requeue = %d, want 0", n)
	}
}

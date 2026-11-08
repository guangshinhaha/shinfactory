package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guangshinhaha/shinfactory/adapter"
	"github.com/guangshinhaha/shinfactory/config"
	"github.com/guangshinhaha/shinfactory/dispatcher"
	"github.com/guangshinhaha/shinfactory/event"
	"github.com/guangshinhaha/shinfactory/internal/taskreg"
	"github.com/guangshinhaha/shinfactory/internal/testutil/fakeorca"
	"github.com/guangshinhaha/shinfactory/mise"
	loopruntime "github.com/guangshinhaha/shinfactory/runtime"
	"github.com/guangshinhaha/shinfactory/skill"
	"github.com/guangshinhaha/shinfactory/worktree"
)

// These fixtures run the real orca dispatcher and runtime against a fake `orca`
// CLI (internal/testutil/fakeorca), so nothing touches a real Orca.

// gitWorktrees is a WorktreeManager that makes real linked git worktrees, which
// the orca dispatcher requires, and records merges like fakeWorktree.
type gitWorktrees struct {
	fakeWorktree
	projectDir string
}

func (g *gitWorktrees) Create(name string, _ ...worktree.CreateOpts) error {
	g.created = append(g.created, name)
	return runGit(g.projectDir, "worktree", "add", "-b", name, filepath.Join(g.projectDir, ".worktrees", name))
}

func runGit(dir string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", dir,
		"-c", "user.name=fixture", "-c", "user.email=fixture@example.com"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}
	return nil
}

type orcaEnv struct {
	*integrationEnv
	fake  *fakeorca.Fake
	clock time.Time
	wts   *gitWorktrees
}

func orcaRegistry() taskreg.Registry {
	mk := func(name string) skill.SkillMeta {
		return skill.SkillMeta{Name: name, Path: "/skills/" + name, Frontmatter: skill.Frontmatter{Schedule: "fixture"}}
	}
	return taskreg.NewFromSkills([]skill.SkillMeta{mk("schedule"), mk("execute"), mk("quality"), mk("debrief")})
}

func newOrcaEnv(t *testing.T, orders OrdersFile) *orcaEnv {
	t.Helper()
	fake := fakeorca.New(t)

	projectDir := t.TempDir()
	if err := runGit(projectDir, "init", "-q", "-b", "main"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if err := runGit(projectDir, "commit", "-q", "--allow-empty", "-m", "init"); err != nil {
		t.Fatalf("git commit: %v", err)
	}
	runtimeDir := filepath.Join(projectDir, ".noodle")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ordersPath := filepath.Join(runtimeDir, "orders.json")
	if err := writeOrdersAtomic(ordersPath, orders); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	cfg.Mode = "auto"
	cfg.Runtime.Default = "orca"
	cfg.Runtime.Orca.LimitRetrySeconds = 60

	od := dispatcher.NewOrcaDispatcher(dispatcher.OrcaDispatcherConfig{
		ProjectDir:   projectDir,
		RuntimeDir:   runtimeDir,
		Bin:          fake.Bin,
		PollInterval: 5 * time.Millisecond,
	})
	wts := &gitWorktrees{projectDir: projectDir}
	env := &orcaEnv{fake: fake, clock: time.Now(), wts: wts}
	l := New(projectDir, "noodle", cfg, Dependencies{
		Runtimes: map[string]loopruntime.Runtime{
			"process": newMockRuntime(),
			"orca":    loopruntime.NewDispatcherRuntime("orca", od, runtimeDir),
		},
		Worktree:       wts,
		Adapter:        &fakeAdapterRunner{},
		Mise:           &fakeMise{brief: mise.Brief{Backlog: []adapter.BacklogItem{{ID: "1", Title: "t", Status: "open"}}}},
		Monitor:        fakeMonitor{},
		Registry:       orcaRegistry(),
		Now:            func() time.Time { return env.clock },
		OrdersFile:     ordersPath,
		OrdersNextFile: filepath.Join(runtimeDir, "orders-next.json"),
	})
	env.integrationEnv = &integrationEnv{loop: l, wt: &wts.fakeWorktree, ordersPath: ordersPath, runtimeDir: runtimeDir, projectDir: projectDir}
	return env
}

// cycleUntil runs loop cycles until cond holds. Orca workers settle on a
// background poll, so completions land between cycles.
func (e *orcaEnv) cycleUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := e.loop.Cycle(context.Background()); err != nil {
			t.Fatalf("cycle while waiting for %s: %v", what, err)
		}
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s\norca calls:\n%s", what, strings.Join(e.fake.Calls(), "\n"))
}

func (e *orcaEnv) orderStages(t *testing.T, orderID string) []string {
	t.Helper()
	of := e.readOrders(t)
	for _, o := range of.Orders {
		if o.ID == orderID {
			var out []string
			for _, s := range o.Stages {
				out = append(out, s.TaskKey+":"+string(s.Status))
			}
			return out
		}
	}
	return nil
}

func (e *orcaEnv) writeOrdersNext(t *testing.T, stages ...string) {
	t.Helper()
	var wire []map[string]string
	for _, s := range stages {
		wire = append(wire, map[string]string{"do": s, "with": "claude", "model": "claude-sonnet-5-5"})
	}
	data, _ := json.Marshal(map[string]any{"orders": []any{map[string]any{"id": "42", "title": "orca fixture", "stages": wire}}})
	if err := os.WriteFile(e.loop.deps.OrdersNextFile, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func (e *orcaEnv) activeSessionID(t *testing.T, orderID string) string {
	t.Helper()
	cook := e.loop.cooks.activeCooksByOrder[orderID]
	if cook == nil {
		t.Fatalf("no active cook for order %q", orderID)
	}
	return cook.session.ID()
}

func (e *orcaEnv) emitSessionEvent(t *testing.T, sessionID string, typ event.EventType, message string) {
	t.Helper()
	w, err := event.NewEventWriter(e.runtimeDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]string{"message": message})
	if err := w.Append(context.Background(), event.Event{Type: typ, Payload: payload, SessionID: sessionID, Timestamp: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
}

func scheduleOnlyOrders() OrdersFile {
	return OrdersFile{Orders: []Order{scheduleOrder(config.DefaultConfig(), "")}}
}

// settleLatest ends the most recently launched fake worker.
func (e *orcaEnv) settleLatest(t *testing.T, outcome string) string {
	t.Helper()
	id := e.fake.LastDispatch(t)
	e.fake.Settle(t, id, outcome)
	return id
}

// Done when 2: one order goes schedule -> execute -> quality -> debrief on
// orca, each stage a visible Orca worker launched with the factory's provider
// and model, in its own worktree.
func TestOrcaRuntimeRunsOrderAcrossStages(t *testing.T) {
	env := newOrcaEnv(t, scheduleOnlyOrders())

	env.cycleUntil(t, "schedule worker started", func() bool { return len(env.fake.Starts()) == 1 })
	if got := env.fake.Starts()[0]; !strings.Contains(got, "--worktree path:"+env.projectDir) || !strings.Contains(got, "--agent claude") {
		t.Fatalf("schedule worker-start = %q, want the project dir worktree and claude agent", got)
	}
	env.writeOrdersNext(t, "execute", "quality", "debrief")
	env.settleLatest(t, "succeeded")

	for i, stage := range []string{"execute", "quality", "debrief"} {
		want := i + 2
		env.cycleUntil(t, stage+" worker started", func() bool { return len(env.fake.Starts()) == want })
		start := env.fake.Starts()[want-1]
		for _, frag := range []string{"--agent claude", "--model claude-sonnet-5-5", "--worktree path:" + filepath.Join(env.projectDir, ".worktrees", "42-"+string(rune('0'+i))+"-"+stage)} {
			if !strings.Contains(start, frag) {
				t.Fatalf("%s worker-start = %q, missing %q", stage, start, frag)
			}
		}
		env.settleLatest(t, "succeeded")
	}
	env.cycleUntil(t, "order completed", func() bool { return env.orderStages(t, "42") == nil })

	// Once the order is done the factory schedules again, so there may be a fifth start.
	if got := len(env.fake.Starts()); got < 4 {
		t.Fatalf("worker-start calls = %d, want at least 4 (schedule, execute, quality, debrief)", got)
	}
	if len(env.wt.merged) == 0 {
		t.Fatalf("no stage worktree was merged; merged=%v", env.wt.merged)
	}
	spec := env.fake.Spec(t, 2)
	if !strings.Contains(spec, "NOODLE_PROJECT_DIR="+env.projectDir) || !strings.Contains(spec, "--session ") {
		t.Fatalf("execute spec lacks the session identity the skills read:\n%s", spec)
	}
}

// A stage on orca still reports through the session event log: stage_yield
// turns a worker that ended badly into a completed stage, stage_fail rejects
// one the worker called a success.
func TestOrcaRuntimeStageEventsDecideTheResult(t *testing.T) {
	orders := OrdersFile{Orders: []Order{{
		ID: "42", Title: "events", Status: OrderStatusActive,
		Stages: []Stage{
			{TaskKey: "execute", Skill: "execute", Provider: "claude", Model: "claude-sonnet-5-5", Status: StageStatusPending},
			{TaskKey: "quality", Skill: "quality", Provider: "claude", Model: "claude-sonnet-5-5", Status: StageStatusPending},
		},
	}}}
	env := newOrcaEnv(t, orders)

	env.cycleUntil(t, "execute started", func() bool { return len(env.fake.Starts()) == 1 })
	sid := env.activeSessionID(t, "42")
	env.emitSessionEvent(t, sid, event.EventStageYield, "built it")
	env.settleLatest(t, "failed") // worker died after yielding
	env.cycleUntil(t, "quality started", func() bool { return len(env.fake.Starts()) == 2 })
	if got := env.orderStages(t, "42"); got[0] != "execute:completed" {
		t.Fatalf("stages = %v, want execute completed by stage_yield", got)
	}

	sid = env.activeSessionID(t, "42")
	env.emitSessionEvent(t, sid, event.EventStageFail, "Rejected: Done when missed")
	env.settleLatest(t, "succeeded")
	env.cycleUntil(t, "quality rejected", func() bool {
		of := env.readOrders(t)
		return len(of.Orders) == 1 && of.Orders[0].Status == OrderStatusFailed
	})
}

// Done when 3: a launch refused for usage drains the loop (nothing fails, no
// fallback to process), and the stage launches once a launch succeeds again.
func TestOrcaRuntimeDrainsOnLaunchLimitAndResumes(t *testing.T) {
	orders := OrdersFile{Orders: []Order{{
		ID: "42", Title: "limit at launch", Status: OrderStatusActive,
		Stages: []Stage{{TaskKey: "execute", Skill: "execute", Provider: "claude", Model: "claude-sonnet-5-5", Status: StageStatusPending}},
	}}}
	env := newOrcaEnv(t, orders)
	env.fake.SetLimit(t, true)
	process := env.loop.deps.Runtimes["process"].(*mockRuntime)

	for i := 0; i < 3; i++ {
		if err := env.loop.Cycle(context.Background()); err != nil {
			t.Fatalf("cycle: %v", err)
		}
	}
	if got := env.orderStages(t, "42"); len(got) != 1 || got[0] != "execute:pending" {
		t.Fatalf("stages = %v, want execute still pending while the limit holds", got)
	}
	if len(process.calls) != 0 {
		t.Fatalf("process runtime got %d dispatches; a usage limit must not fall back", len(process.calls))
	}
	if n := len(env.fake.Starts()); n != 1 {
		t.Fatalf("worker-start attempts during the hold = %d, want 1 (the hold stops retries)", n)
	}

	// The limit resets and the hold lapses: the next launch is the probe.
	env.fake.SetLimit(t, false)
	env.clock = env.clock.Add(2 * time.Minute)
	env.cycleUntil(t, "stage launched after reset", func() bool { return len(env.fake.Starts()) == 2 })
	env.settleLatest(t, "succeeded")
	env.cycleUntil(t, "order completed", func() bool { return env.orderStages(t, "42") == nil })
}

// Done when 3, mid-run: a worker that dies on a limit is requeued, not failed,
// and resumes in the same worktree after the hold.
func TestOrcaRuntimeRequeuesStageWhoseWorkerDiedOnLimit(t *testing.T) {
	orders := OrdersFile{Orders: []Order{{
		ID: "42", Title: "limit mid-run", Status: OrderStatusActive,
		Stages: []Stage{{TaskKey: "execute", Skill: "execute", Provider: "claude", Model: "claude-sonnet-5-5", Status: StageStatusPending}},
	}}}
	env := newOrcaEnv(t, orders)

	env.cycleUntil(t, "execute started", func() bool { return len(env.fake.Starts()) == 1 })
	id := env.fake.LastDispatch(t)
	env.fake.SetTranscript(t, id, "Claude usage limit reached. Your limit will reset at 3pm.")
	env.fake.Settle(t, id, "failed")

	env.cycleUntil(t, "stage requeued", func() bool {
		got := env.orderStages(t, "42")
		return len(got) == 1 && got[0] == "execute:pending" && env.loop.cooks.activeCooksByOrder["42"] == nil
	})
	if of := env.readOrders(t); of.Orders[0].Status != OrderStatusActive {
		t.Fatalf("order status = %q, want active (a limit is not a failure)", of.Orders[0].Status)
	}
	for i := 0; i < 3; i++ {
		_ = env.loop.Cycle(context.Background())
	}
	if n := len(env.fake.Starts()); n != 1 {
		t.Fatalf("worker-start calls while held = %d, want 1", n)
	}

	env.clock = env.clock.Add(2 * time.Minute)
	env.cycleUntil(t, "stage relaunched", func() bool { return len(env.fake.Starts()) == 2 })
	if first, second := env.fake.Starts()[0], env.fake.Starts()[1]; wtOf(first) != wtOf(second) {
		t.Fatalf("relaunch used worktree %q, want the same worktree %q", wtOf(second), wtOf(first))
	}
	env.settleLatest(t, "succeeded")
	env.cycleUntil(t, "order completed", func() bool { return env.orderStages(t, "42") == nil })
}

func wtOf(startCall string) string {
	_, after, _ := strings.Cut(startCall, "--worktree ")
	field, _, _ := strings.Cut(after, " ")
	return field
}

// Done when 4: orca is never required. With the orca CLI missing, a config that
// defaults to orca resolves to process and orca is not offered.
func TestOrcaRuntimeAbsentFallsBackToProcess(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	cfg := config.DefaultConfig()
	cfg.Runtime.Default = "orca"
	for _, name := range cfg.AvailableRuntimes() {
		if name == "orca" {
			t.Fatalf("orca offered without the orca CLI on PATH: %v", cfg.AvailableRuntimes())
		}
	}
	l := &Loop{config: cfg, deps: Dependencies{Runtimes: map[string]loopruntime.Runtime{"process": newMockRuntime()}}}
	if got := l.stageRuntime(""); got != "process" {
		t.Fatalf("stageRuntime with orca unwired = %q, want process", got)
	}
	if got := l.stageRuntime("process"); got != "process" {
		t.Fatalf("explicit process stage = %q, want process", got)
	}
}

// The safety fact for "runs exactly as today": no config other than a default of
// orca changes which runtime a stage gets. A sprites default still leaves a
// stage without a runtime on process, as before.
func TestStageRuntimeKeepsLegacyDefaultsForOtherRuntimes(t *testing.T) {
	for _, def := range []string{"", "process", "sprites", "cursor"} {
		cfg := config.DefaultConfig()
		cfg.Runtime.Default = def
		l := &Loop{config: cfg, deps: Dependencies{Runtimes: map[string]loopruntime.Runtime{"process": newMockRuntime()}}}
		if got := l.stageRuntime(""); got != "process" {
			t.Errorf("default %q: stage without runtime = %q, want process", def, got)
		}
		if got := l.stageRuntime("sprites"); got != "sprites" {
			t.Errorf("default %q: explicit sprites stage = %q, want sprites", def, got)
		}
	}
}

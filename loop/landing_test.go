package loop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/guangshinhaha/shinfactory/adapter"
	"github.com/guangshinhaha/shinfactory/config"
	"github.com/guangshinhaha/shinfactory/mise"
	loopruntime "github.com/guangshinhaha/shinfactory/runtime"
)

type fakeLander struct {
	pushes   []string // worktree dirs pushed
	specs    []PRSpec
	merged   []PullRequest
	pushErr  error
	mergeErr error
}

func (f *fakeLander) PushAndEnsureDraft(_ context.Context, dir string, spec PRSpec) (PullRequest, error) {
	f.pushes = append(f.pushes, dir)
	f.specs = append(f.specs, spec)
	if f.pushErr != nil {
		return PullRequest{}, f.pushErr
	}
	return PullRequest{URL: "https://example.test/pr/1", Branch: "b", IsDraft: len(f.merged) == 0}, nil
}

func (f *fakeLander) MarkReadyAndMerge(_ context.Context, _ string, pr PullRequest) error {
	if f.mergeErr != nil {
		return f.mergeErr
	}
	f.merged = append(f.merged, pr)
	return nil
}

type landingHarness struct {
	l  *Loop
	rt *mockRuntime
	wt *fakeWorktree
	ar *fakeAdapterRunner
	ln *fakeLander
}

func newLandingHarness(t *testing.T, runMode string, stages int) *landingHarness {
	t.Helper()
	projectDir := t.TempDir()
	runtimeDir := filepath.Join(projectDir, ".noodle")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	order := testOrder("42", "execute", "execute", "claude", "claude-opus-4-6")
	order.Title = "Land through a PR"
	for i := 1; i < stages; i++ {
		order.Stages = append(order.Stages, Stage{TaskKey: "reflect", Skill: "reflect", Provider: "claude", Model: "claude-opus-4-6", Status: StageStatusPending})
	}
	ordersPath := writeTestOrders(t, runtimeDir, OrdersFile{Orders: []Order{order}})
	h := &landingHarness{rt: newMockRuntime(), wt: &fakeWorktree{}, ar: &fakeAdapterRunner{}, ln: &fakeLander{}}
	cfg := config.DefaultConfig()
	cfg.Mode = runMode
	cfg.Landing.Mode = config.LandingModePR
	h.l = New(projectDir, "noodle", cfg, Dependencies{
		Runtimes:   map[string]loopruntime.Runtime{"process": h.rt},
		Worktree:   h.wt,
		Adapter:    h.ar,
		Lander:     h.ln,
		OrdersFile: ordersPath,
		Mise:       &fakeMise{brief: mise.Brief{Backlog: []adapter.BacklogItem{{ID: "1", Title: "t", Status: "open"}}}},
		Monitor:    fakeMonitor{},
		Registry:   testLoopRegistry(),
		Now:        time.Now,
	})
	return h
}

// runStage spawns the next stage and completes it.
func (h *landingHarness) runStage(t *testing.T, n int) {
	t.Helper()
	if err := h.l.Cycle(context.Background()); err != nil {
		t.Fatalf("spawn cycle: %v", err)
	}
	if len(h.rt.sessions) != n {
		t.Fatalf("sessions = %d, want %d", len(h.rt.sessions), n)
	}
	h.rt.sessions[n-1].complete("completed")
	if err := h.l.Cycle(context.Background()); err != nil {
		t.Fatalf("completion cycle: %v", err)
	}
}

func TestPRLandingPushesDraftAfterExecuteAndMergesAfterLastStage(t *testing.T) {
	h := newLandingHarness(t, "auto", 2)

	h.runStage(t, 1)
	if len(h.ln.pushes) != 1 {
		t.Fatalf("pushes after execute = %d, want 1 (draft PR opened right after execute)", len(h.ln.pushes))
	}
	if len(h.ln.merged) != 0 {
		t.Fatal("PR merged before the last stage")
	}
	if len(h.wt.merged) != 0 {
		t.Fatalf("local worktree merges = %v, want none in PR mode", h.wt.merged)
	}
	if got := h.ln.specs[0].Title; got != "Land through a PR" {
		t.Fatalf("PR title = %q", got)
	}

	h.runStage(t, 2)
	if len(h.ln.pushes) != 2 {
		t.Fatalf("pushes after second stage = %d, want 2 (later stages push to the same PR)", len(h.ln.pushes))
	}
	if h.ln.pushes[0] != h.ln.pushes[1] {
		t.Fatalf("stages used different worktrees: %v", h.ln.pushes)
	}
	if len(h.ln.merged) != 1 {
		t.Fatalf("PR merges = %d, want 1 after the last stage", len(h.ln.merged))
	}
	if len(h.wt.merged) != 0 {
		t.Fatalf("local worktree merges = %v", h.wt.merged)
	}
	if len(h.wt.cleaned) != 1 {
		t.Fatalf("worktree cleanups = %v, want the order worktree removed once", h.wt.cleaned)
	}
	if len(h.ar.doneCalls) != 1 || h.ar.doneCalls[0] != "42" {
		t.Fatalf("backlog done calls = %#v", h.ar.doneCalls)
	}
	if h.specs0Closes(t) == false {
		t.Fatal("PR body should reference the numeric issue")
	}
}

func (h *landingHarness) specs0Closes(t *testing.T) bool {
	return len(h.ln.specs) > 0 && contains(h.ln.specs[0].Body, "Refs #42")
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestPRLandingSupervisedParksInsteadOfMergingThenApproveMerges(t *testing.T) {
	h := newLandingHarness(t, "supervised", 1)

	h.runStage(t, 1)
	if len(h.ln.pushes) != 1 {
		t.Fatalf("pushes = %d, want the draft PR opened even in supervised mode", len(h.ln.pushes))
	}
	if len(h.ln.merged) != 0 {
		t.Fatal("supervised mode merged without approval")
	}
	if _, ok := h.l.cooks.pendingReview["42"]; !ok {
		t.Fatal("final stage should park for review in supervised mode")
	}

	if err := h.l.controlMerge("42"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if len(h.ln.merged) != 1 {
		t.Fatalf("PR merges after approval = %d, want 1", len(h.ln.merged))
	}
	if len(h.ar.doneCalls) != 1 {
		t.Fatalf("backlog done calls = %#v", h.ar.doneCalls)
	}
}

func TestPRLandingPushFailureFailsStageAndKeepsWorktree(t *testing.T) {
	h := newLandingHarness(t, "auto", 1)
	h.ln.pushErr = errors.New("remote rejected")

	h.runStage(t, 1)
	if len(h.ln.merged) != 0 {
		t.Fatal("merged despite push failure")
	}
	if len(h.wt.cleaned) != 0 {
		t.Fatalf("worktree cleaned after failure: %v", h.wt.cleaned)
	}
	if len(h.ar.doneCalls) != 0 {
		t.Fatalf("order marked done despite push failure: %v", h.ar.doneCalls)
	}
}

func TestPRLandingMergeRefusedFailsStageWithoutDone(t *testing.T) {
	h := newLandingHarness(t, "auto", 1)
	h.ln.mergeErr = errors.New("required check pending")

	h.runStage(t, 1)
	if len(h.ar.doneCalls) != 0 {
		t.Fatalf("order marked done though merge was refused: %v", h.ar.doneCalls)
	}
	if len(h.wt.cleaned) != 0 {
		t.Fatalf("worktree cleaned though merge was refused: %v", h.wt.cleaned)
	}
}

func TestLocalLandingStillMergesLocally(t *testing.T) {
	h := newLandingHarness(t, "auto", 1)
	h.l.config.Landing.Mode = config.LandingModeLocal
	h.runStage(t, 1)
	if len(h.wt.merged) != 1 || len(h.ln.pushes) != 0 {
		t.Fatalf("local mode: merged=%v pushes=%v", h.wt.merged, h.ln.pushes)
	}
}

func TestPRLandingApprovingNonFinalStageDoesNotShip(t *testing.T) {
	h := newLandingHarness(t, "auto", 2)
	h.runStage(t, 1)
	// Park the first stage as a blocking stage message would.
	cook := &cookHandle{
		cookIdentity: cookIdentity{orderID: "42", stageIndex: 0, stage: Stage{TaskKey: "execute", Skill: "execute"}},
		worktreeName: "42-0-task",
		worktreePath: h.l.worktreePath("42-0-task"),
	}
	if err := h.l.approvePRShip(cook); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if len(h.ln.merged) != 0 || len(h.wt.cleaned) != 0 {
		t.Fatalf("non-final approval shipped: merged=%d cleaned=%v", len(h.ln.merged), h.wt.cleaned)
	}
}

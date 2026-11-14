package loop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	deadline bool // the last MarkReadyAndMerge context had a deadline
	pending  int  // MarkReadyAndMerge returns ErrChecksPending this many times first
	// pendingDetail, when set, makes those errors say what the PR waits on.
	pendingDetail string
	// conflicts makes MarkReadyAndMerge return ErrPRConflict this many times.
	conflicts int
	// rebaseErr is returned by RebaseOntoBase; rebases counts the calls.
	rebaseErr error
	rebases   int
	// verdicts are the bodies PostVerdict received; verdictErr fails it.
	verdicts   []string
	verdictErr error
	// verdictTries counts PostVerdict calls, failed ones included.
	verdictTries int
	// probeFiles is what ConflictingFiles reports.
	probeFiles []string
	// closedPR is what BranchPRClosed reports; closedPRChecks counts the calls.
	closedPR       bool
	closedPRChecks int
	closedHeads    []string // the closed PRs' heads; a placeholder when nil
	// lostVerdicts makes that many PostVerdict calls vanish: they succeed, but
	// no verdict is on the PR afterwards. missingVerdict is true until a
	// verdict lands, and makes MarkReadyAndMerge wait on verdict-check.
	lostVerdicts   int
	missingVerdict bool
}

func (f *fakeLander) BranchPRClosed(context.Context, string, string) ([]string, error) {
	f.closedPRChecks++
	if !f.closedPR {
		return nil, nil
	}
	if f.closedHeads != nil {
		return f.closedHeads, nil
	}
	return []string{"0000000000000000000000000000000000000000"}, nil
}

func (f *fakeLander) RebaseOntoBase(context.Context, string, string) error {
	f.rebases++
	return f.rebaseErr
}

func (f *fakeLander) PostVerdict(_ context.Context, _ string, _ PullRequest, body string) error {
	f.verdictTries++
	if f.verdictErr != nil {
		return f.verdictErr
	}
	if f.lostVerdicts > 0 {
		f.lostVerdicts--
		return nil
	}
	f.missingVerdict = false
	f.verdicts = append(f.verdicts, body)
	return nil
}

func (f *fakeLander) ConflictingFiles(context.Context, string, string) ([]string, error) {
	return f.probeFiles, nil
}

func (f *fakeLander) PushAndEnsureDraft(_ context.Context, dir string, spec PRSpec) (PullRequest, error) {
	f.pushes = append(f.pushes, dir)
	f.specs = append(f.specs, spec)
	if f.pushErr != nil {
		return PullRequest{}, f.pushErr
	}
	return PullRequest{URL: "https://example.test/pr/1", Branch: spec.Branch, IsDraft: len(f.merged) == 0}, nil
}

func (f *fakeLander) MarkReadyAndMerge(ctx context.Context, _ string, pr PullRequest) error {
	_, f.deadline = ctx.Deadline()
	if f.conflicts > 0 {
		f.conflicts--
		return ErrPRConflict
	}
	if f.pending > 0 {
		f.pending--
		if f.pendingDetail != "" {
			return pendingError{url: pr.URL, detail: f.pendingDetail}
		}
		return ErrChecksPending
	}
	if f.missingVerdict {
		return pendingError{url: pr.URL, detail: redCheckDetail("verdict-check / verdict-check"), noVerdict: true}
	}
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
	order.Tier = "none" // these tests are about landing, not the review gate
	for i := 1; i < stages; i++ {
		order.Stages = append(order.Stages, Stage{TaskKey: "reflect", Skill: "reflect", Provider: "claude", Model: "claude-opus-4-6", Status: StageStatusPending})
	}
	ordersPath := writeTestOrders(t, runtimeDir, OrdersFile{Orders: []Order{order}})
	h := &landingHarness{rt: newMockRuntime(), wt: &fakeWorktree{}, ar: &fakeAdapterRunner{}, ln: &fakeLander{}}
	cfg := config.DefaultConfig()
	cfg.Mode = runMode
	cfg.Landing.Mode = config.LandingModePR
	cfg.Landing.CloseIssues = true
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
		t.Fatal("PR body should close the numeric GitHub issue")
	}
}

func (h *landingHarness) specs0Closes(t *testing.T) bool {
	return len(h.ln.specs) > 0 && contains(h.ln.specs[0].Body, "Closes #42")
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

// Gantry's checks start on the final push. The order must wait for them, not
// fail, and the loop merges by itself once they pass.
func TestPRLandingWaitsForChecksThenMerges(t *testing.T) {
	h := newLandingHarness(t, "auto", 1)
	h.ln.pending = 2

	h.runStage(t, 1)
	review, parked := h.l.cooks.pendingReview["42"]
	if !parked || !strings.HasPrefix(review.reason, awaitingChecksPrefix) {
		t.Fatalf("pending review = %+v (parked=%v), want parked waiting for checks", review, parked)
	}
	if len(h.ar.doneCalls) != 0 || len(h.wt.cleaned) != 0 {
		t.Fatalf("order finished while checks were pending: done=%v cleaned=%v", h.ar.doneCalls, h.wt.cleaned)
	}
	if o, _ := h.l.orderByID("42"); o.Status == OrderStatusFailed {
		t.Fatal("order failed while checks were pending")
	}

	// Within the poll interval nothing is retried.
	h.l.lastChecksPoll = time.Now()
	if err := h.l.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.ln.pending != 1 {
		t.Fatalf("retried inside the poll interval: pending left = %d", h.ln.pending)
	}

	// Checks still running: stays parked.
	h.l.lastChecksPoll = time.Time{}
	if err := h.l.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, still := h.l.cooks.pendingReview["42"]; !still || len(h.ln.merged) != 0 {
		t.Fatalf("parked=%v merged=%d, want still waiting", still, len(h.ln.merged))
	}

	// Checks pass: the loop merges and finishes the order unasked.
	h.l.lastChecksPoll = time.Time{}
	if err := h.l.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(h.ln.merged) != 1 {
		t.Fatalf("merges = %d, want 1 once checks passed", len(h.ln.merged))
	}
	if _, still := h.l.cooks.pendingReview["42"]; still {
		t.Fatal("still parked after merging")
	}
	if len(h.ar.doneCalls) != 1 || len(h.wt.cleaned) != 1 {
		t.Fatalf("done=%v cleaned=%v, want both once merged", h.ar.doneCalls, h.wt.cleaned)
	}
}

// The refreshed reason must survive the projection reload, not live in memory.
func TestPRLandingRefreshedWaitReasonIsPersisted(t *testing.T) {
	for _, tc := range []struct{ mode, prefix string }{
		{"auto", awaitingChecksPrefix},
		{"supervised", approvedAwaitingChecksPrefix},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			h := newLandingHarness(t, tc.mode, 1)
			h.ln.pending = 1
			h.ln.pendingDetail = "lint pending"
			h.runStage(t, 1)
			if tc.mode == "supervised" {
				// Park as approved while checks run.
				h.ln.pending = 1
				if err := h.l.controlMerge("42"); err != nil {
					t.Fatal(err)
				}
			}
			h.ln.pending = 1
			h.ln.pendingDetail = "test pending"
			h.l.lastChecksPoll = time.Time{}
			if err := h.l.Cycle(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := h.l.syncPendingReviewProjection(); err != nil {
				t.Fatal(err)
			}
			canon := h.l.canonical.Orders["42"].Stages[0]
			if len(canon.Attempts) != 1 {
				t.Fatalf("attempts after re-park = %d, want 1 (re-park adds none)", len(canon.Attempts))
			}
			got := h.l.cooks.pendingReview["42"].reason
			if !strings.HasPrefix(got, tc.prefix) || !strings.Contains(got, "test pending") {
				t.Fatalf("reason after reload = %q, want %q prefix and the refreshed detail", got, tc.prefix)
			}
		})
	}
}

// Without auto mode, a waiting PR nobody approved must not merge by itself.
func TestPRLandingSupervisedDoesNotMergeUnapprovedWaitingPR(t *testing.T) {
	h := newLandingHarness(t, "supervised", 1)
	h.l.cooks.pendingReview["42"] = &pendingReviewCook{
		cookIdentity: cookIdentity{orderID: "42", stage: Stage{TaskKey: "execute", Skill: "execute"}},
		reason:       awaitingChecksPrefix + " (https://example.test/pr/1): lint pending",
	}
	h.l.lastChecksPoll = time.Time{}
	if err := h.l.retryAwaitingChecks(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(h.ln.pushes) != 0 || len(h.ln.merged) != 0 {
		t.Fatalf("pushes=%d merges=%d, want an unapproved PR left alone", len(h.ln.pushes), len(h.ln.merged))
	}
}

func TestPRLandingWaitingPRRefusedAfterChecksStaysParkedWithReason(t *testing.T) {
	h := newLandingHarness(t, "auto", 1)
	h.ln.pending = 1
	h.runStage(t, 1)

	h.ln.mergeErr = errors.New("a required check failed")
	h.l.lastChecksPoll = time.Time{}
	if err := h.l.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	review, parked := h.l.cooks.pendingReview["42"]
	if !parked || !strings.Contains(review.reason, "a required check failed") {
		t.Fatalf("pending review = %+v (parked=%v), want parked with the refusal", review, parked)
	}
	if len(h.ar.doneCalls) != 0 {
		t.Fatal("order marked done though the merge was refused")
	}
	// A refusal is not retried by itself: a human decides.
	h.ln.mergeErr = nil
	h.l.lastChecksPoll = time.Time{}
	if err := h.l.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(h.ln.merged) != 0 {
		t.Fatal("retried a refused merge without a human")
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

// A network blip while a parked PR retries is not a merge refusal: the order
// keeps its waiting reason and merges on a later poll.
func TestPRLandingWaitingPRTransientErrorKeepsWaitingAndRetries(t *testing.T) {
	h := newLandingHarness(t, "auto", 1)
	clock := setClock(h.l)
	h.ln.pending = 1
	h.runStage(t, 1)
	waiting := h.l.cooks.pendingReview["42"].reason

	h.ln.mergeErr = errors.New("gh pr merge: HTTP 502: Bad Gateway")
	h.l.lastChecksPoll = time.Time{}
	if err := h.l.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	review, parked := h.l.cooks.pendingReview["42"]
	if !parked || review.reason != waiting || strings.Contains(review.reason, "merge refused") {
		t.Fatalf("pending review = %+v (parked=%v), want still parked as %q", review, parked, waiting)
	}

	h.ln.mergeErr = nil
	h.l.lastChecksPoll = time.Time{}
	clock.now = clock.now.Add(10 * time.Second) // past the outage backoff
	if err := h.l.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(h.ln.merged) != 1 {
		t.Fatalf("merged=%d, want the retry to merge once the blip clears", len(h.ln.merged))
	}
}

// localLandingConfig is the default config with landing mode "local". Most
// loop tests exercise the local merge pipeline; PR landing tests set "pr".
func localLandingConfig() config.Config {
	cfg := config.DefaultConfig()
	cfg.Landing.Mode = config.LandingModeLocal
	return cfg
}

// Approval while checks run must not ask for a second yes: the order parks as
// approved and the loop merges it, even in supervised mode.
func TestPRLandingApprovalWhileChecksPendingMergesWithoutSecondApproval(t *testing.T) {
	h := newLandingHarness(t, "supervised", 1)
	h.runStage(t, 1)
	h.ln.pending = 1
	if err := h.l.controlMerge("42"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	review, parked := h.l.cooks.pendingReview["42"]
	if !parked || !strings.HasPrefix(review.reason, approvedAwaitingChecksPrefix) {
		t.Fatalf("pending review = %+v (parked=%v), want parked as approved and waiting", review, parked)
	}
	if len(h.ln.merged) != 0 {
		t.Fatal("merged while checks were pending")
	}
	h.l.lastChecksPoll = time.Time{}
	if err := h.l.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(h.ln.merged) != 1 || len(h.ar.doneCalls) != 1 {
		t.Fatalf("merged=%d done=%v, want the loop to finish the approved order", len(h.ln.merged), h.ar.doneCalls)
	}
}

// Marking the issue done is not bounded by the ship deadline.
func TestPRLandingMarkDoneRunsOutsideTheShipDeadline(t *testing.T) {
	h := newLandingHarness(t, "supervised", 1)
	h.runStage(t, 1)
	if err := h.l.controlMerge("42"); err != nil {
		t.Fatal(err)
	}
	if len(h.ar.doneCalls) != 1 || h.ar.doneHadDeadline {
		t.Fatalf("done=%v hadDeadline=%v, want done without the ship deadline", h.ar.doneCalls, h.ar.doneHadDeadline)
	}
	if !h.ln.deadline {
		t.Fatal("the merge itself lost its deadline")
	}
}

// GitHub that never computes mergeability must not park an order forever.
func TestParkedPRWithUnknownMergeabilityStopsAfterTheTimeout(t *testing.T) {
	h := newGateHarness(t, "none", "execute")
	h.ln.pending = 1000
	h.ln.pendingDetail = unknownMergeabilityDetail
	h.next(t)
	if _, ok := h.l.cooks.pendingReview["42"]; !ok {
		t.Fatal("order did not park")
	}
	clock := time.Now()
	h.l.deps.Now = func() time.Time { return clock }
	poll := func() {
		t.Helper()
		h.l.lastChecksPoll = time.Time{}
		if err := h.l.Cycle(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	poll() // first sight of the unknown state
	clock = clock.Add(unknownMergeabilityTimeout - time.Minute)
	poll()
	if _, ok := h.l.cooks.pendingReview["42"]; !ok || h.order(t).Status == OrderStatusFailed {
		t.Fatal("order stopped before the timeout")
	}
	clock = clock.Add(2 * time.Minute)
	poll()
	h.failedWith(t, "mergeability unknown past the timeout")
	if !strings.Contains(actionNeeded(t, h), "mergeability") {
		t.Fatalf("action_needed = %q, want the stuck PR named", actionNeeded(t, h))
	}
	if len(h.ar.stopCalls) != 1 {
		t.Fatalf("stop calls = %v, want the person told once", h.ar.stopCalls)
	}
}

// #129: a required check that never turns green stops the order with a reason
// that names the check, not a bare merge error.
func TestParkedPRWithAStuckRedCheckStopsNamingTheCheck(t *testing.T) {
	h := newGateHarness(t, "none", "execute")
	h.ln.pending = 1000
	h.ln.pendingDetail = redCheckDetail("verdict-check / verdict-check")
	h.next(t)
	if _, ok := h.l.cooks.pendingReview["42"]; !ok {
		t.Fatal("order did not park")
	}
	clock := time.Now()
	h.l.deps.Now = func() time.Time { return clock }
	poll := func() {
		t.Helper()
		h.l.lastChecksPoll = time.Time{}
		if err := h.l.Cycle(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	poll()
	clock = clock.Add(checkWaitTimeout - time.Minute)
	poll()
	if _, ok := h.l.cooks.pendingReview["42"]; !ok || h.order(t).Status == OrderStatusFailed {
		t.Fatal("order stopped before the timeout")
	}
	clock = clock.Add(2 * time.Minute)
	poll()
	h.failedWith(t, "verdict-check / verdict-check")
	if !strings.Contains(actionNeeded(t, h), "verdict-check / verdict-check") {
		t.Fatalf("action_needed = %q, want the check named", actionNeeded(t, h))
	}
}

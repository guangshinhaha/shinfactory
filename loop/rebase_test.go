package loop

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func actionNeeded(t *testing.T, h *gateHarness) string {
	t.Helper()
	orders, err := h.l.currentOrders()
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(orders.ActionNeeded, "\n")
}

func TestConflictAtShipRebasesOnceThenReviewsAndVerifiesTheNewHead(t *testing.T) {
	h := newGateHarness(t, "high", "execute", "quality", "verify")
	h.ln.conflicts = 1
	h.next(t)
	h.review(t, 0, "none")
	h.next(t)
	h.verdict(t, 0, VerdictPass, fixtureHead)
	h.next(t) // verify completes, ship hits the conflict

	if h.ln.rebases != 1 {
		t.Fatalf("rebases = %d, want 1", h.ln.rebases)
	}
	if len(h.ln.merged) != 0 {
		t.Fatal("PR merged although it conflicted")
	}
	o := h.order(t)
	if o.Status != OrderStatusActive || o.Round != 0 {
		t.Fatalf("order = %s round %d, want active, same fix round", o.Status, o.Round)
	}
	if o.Stages[0].Status != StageStatusCompleted {
		t.Fatalf("execute status = %s, want completed (a rebase does not redo the build)", o.Stages[0].Status)
	}

	// Review and verify run again on the rebased head, then the PR lands.
	h.review(t, 0, "none")
	h.next(t)
	h.verdict(t, 0, VerdictPass, fixtureHead)
	h.next(t)
	if len(h.ln.merged) != 1 {
		t.Fatalf("merges = %d, want 1 after the rebased head passed review and verify", len(h.ln.merged))
	}
	if h.ln.rebases != 1 {
		t.Fatalf("rebases = %d, want still 1", h.ln.rebases)
	}
}

func TestRebaseConflictStopsTheOrderNamingTheFiles(t *testing.T) {
	h := newGateHarness(t, "medium", "execute", "quality")
	h.ln.conflicts = 1
	h.ln.rebaseErr = &RebaseConflictError{Files: []string{"brain/index.md", "README.md"}}
	h.next(t)
	h.review(t, 0, "none")
	h.next(t)

	h.failedWith(t, "rebase conflict")
	if got := actionNeeded(t, h); !strings.Contains(got, "brain/index.md, README.md") {
		t.Fatalf("action_needed = %q, want the conflicting files", got)
	}
	if h.ln.rebases != 1 {
		t.Fatalf("rebases = %d, want 1", h.ln.rebases)
	}
	// Nothing retries it.
	for i := 0; i < 3; i++ {
		if err := h.l.Cycle(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if h.ln.rebases != 1 || len(h.rt.sessions) != 2 {
		t.Fatalf("stopped order was retried: rebases=%d sessions=%d", h.ln.rebases, len(h.rt.sessions))
	}
}

func TestSecondConflictAfterTheRebaseStopsTheOrder(t *testing.T) {
	h := newGateHarness(t, "medium", "execute", "quality")
	h.ln.conflicts = 2
	h.ln.probeFiles = []string{"go.sum"}
	h.next(t)
	h.review(t, 0, "none")
	h.next(t) // first conflict: rebase, review again
	h.review(t, 0, "none")
	h.next(t) // second conflict: stop

	h.failedWith(t, "second conflict")
	if h.ln.rebases != 1 {
		t.Fatalf("rebases = %d, want exactly 1", h.ln.rebases)
	}
	if got := actionNeeded(t, h); !strings.Contains(got, "go.sum") {
		t.Fatalf("action_needed = %q, want the conflicting file", got)
	}
}

func TestConflictWithoutReviewStageShipsTheRebasedHead(t *testing.T) {
	h := newGateHarness(t, "none", "execute")
	h.ln.conflicts = 1
	h.next(t)
	if h.ln.rebases != 1 || len(h.ln.merged) != 1 {
		t.Fatalf("rebases=%d merged=%d, want 1 rebase then 1 merge", h.ln.rebases, len(h.ln.merged))
	}
}

func TestRebaseCountIsWrittenAtomicallyAndSurvivesAReread(t *testing.T) {
	h := newGateHarness(t, "none", "execute")
	if err := h.l.bumpRebased("a"); err != nil {
		t.Fatal(err)
	}
	if err := h.l.bumpRebased("b"); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(h.l.runtimeDir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Fatalf("temp file %s left behind", e.Name())
		}
	}
	if h.l.rebaseCount("a") != 1 || h.l.rebaseCount("b") != 1 {
		t.Fatal("counts lost between writes")
	}
}

func TestAPersonStopClearsTheRebaseCount(t *testing.T) {
	h := newGateHarness(t, "medium", "execute", "quality")
	h.ln.conflicts = 1
	h.next(t)
	h.review(t, 0, "none")
	h.next(t) // conflict: rebase, review again
	id := h.order(t).ID
	if h.l.rebaseCount(id) != 1 {
		t.Fatal("rebase not counted")
	}
	h.review(t, 0, "none")
	h.ln.mergeErr = errors.New("boom")
	h.next(t) // non-conflict ship error after the rebase fails the stage and stops for a person
	if n := h.l.rebaseCount(id); n != 0 {
		t.Fatalf("rebase count = %d after the ship failure stopped for a person, want 0", n)
	}
}

func TestRejectingAParkedOrderClearsItsRebaseCount(t *testing.T) {
	h := newGateHarness(t, "none", "execute")
	h.ln.pending = 5
	h.next(t) // ship waits on checks: the order parks
	if _, ok := h.l.cooks.pendingReview["42"]; !ok {
		t.Fatal("order did not park")
	}
	if err := h.l.bumpRebased("42"); err != nil {
		t.Fatal(err)
	}
	if err := h.l.controlReject("42"); err != nil {
		t.Fatal(err)
	}
	if n := h.l.rebaseCount("42"); n != 0 {
		t.Fatalf("rebase count = %d after reject, want 0", n)
	}
}

// readOnlyRuntimeDir makes writes beside orders.json fail until the test ends.
func readOnlyRuntimeDir(t *testing.T, h *gateHarness) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	if err := os.Chmod(h.l.runtimeDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(h.l.runtimeDir, 0o755) })
}

func TestStopConflictedStillStopsThePersonWhenTheFlagWriteFails(t *testing.T) {
	h := newGateHarness(t, "none", "execute")
	h.ln.pending = 5
	h.next(t)
	pending := h.l.cooks.pendingReview["42"]
	cook := &cookHandle{cookIdentity: pending.cookIdentity}
	readOnlyRuntimeDir(t, h)

	err := h.l.stopConflicted(t.Context(), cook, []string{"go.sum"}, "test")
	if err == nil {
		t.Fatal("stopConflicted returned nil although the flag write failed")
	}
	if len(h.ar.stopCalls) != 1 || h.ar.stopCalls[0] != "42" {
		t.Fatalf("stop calls = %v, want the person told once about order 42", h.ar.stopCalls)
	}
}

func TestFailedReopenOfAParkedOrderRestoresItsPendingReviewAndStages(t *testing.T) {
	h := newGateHarness(t, "medium", "execute", "quality")
	h.next(t)
	h.review(t, 0, "none")
	h.ln.pending = 5
	h.next(t) // review passes, ship waits on checks: parked
	pending, ok := h.l.cooks.pendingReview["42"]
	if !ok {
		t.Fatal("order did not park")
	}
	cook := &cookHandle{cookIdentity: pending.cookIdentity}
	before := h.l.canonical.Orders["42"].Stages[0].Status
	readOnlyRuntimeDir(t, h)

	stopped, err := h.l.onShipConflict(t.Context(), cook, PullRequest{URL: "u"}, true)
	if !stopped || err == nil {
		t.Fatalf("stopped=%v err=%v, want a stop with the reopen error", stopped, err)
	}
	if _, ok := h.l.canonical.PendingReviews["42"]; !ok {
		t.Fatal("parked entry lost after the failed reopen")
	}
	if got := h.l.canonical.Orders["42"].Stages[0].Status; got != before {
		t.Fatalf("stage 0 status = %s after the failed reopen, want %s", got, before)
	}
}

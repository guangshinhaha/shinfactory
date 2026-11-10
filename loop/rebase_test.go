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

func TestFailedStageClearsTheRebaseCountSoARetryGetsItsRebase(t *testing.T) {
	h := newGateHarness(t, "medium", "execute", "quality")
	h.ln.conflicts = 1
	h.next(t)
	h.review(t, 0, "none")
	h.next(t) // conflict: rebase, review again
	if h.l.rebaseCount(h.order(t).ID) != 1 {
		t.Fatal("rebase not counted")
	}
	h.review(t, 0, "none")
	h.ln.mergeErr = errors.New("boom")
	h.next(t) // non-conflict ship error after the rebase fails the stage
	if n := h.l.rebaseCount(h.order(t).ID); n != 0 {
		t.Fatalf("rebase count = %d after a failed stage, want 0", n)
	}
}

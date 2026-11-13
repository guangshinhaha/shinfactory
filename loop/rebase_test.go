package loop

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guangshinhaha/shinfactory/adapter"
	"github.com/guangshinhaha/shinfactory/internal/orderx"
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

func TestFailedStageHandsToAPersonWhichClearsTheRebaseCount(t *testing.T) {
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
	h.next(t) // non-conflict ship error after the rebase fails the stage
	if n := h.stops(); n != 1 {
		t.Fatalf("stop calls = %d, want the failed order handed to a person once", n)
	}
	// The person owns the order now; putting ready back starts it with its one rebase again.
	if n := h.l.rebaseCount(id); n != 0 {
		t.Fatalf("rebase count = %d after the hand-off, want 0", n)
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

// repropose has the scheduler propose order 42 again while the backlog still lists it
// as ready, as it does when the stop could not remove the label.
func (h *gateHarness) repropose(t *testing.T, ready bool) {
	t.Helper()
	backlog := []adapter.BacklogItem{}
	if ready {
		backlog = append(backlog, adapter.BacklogItem{ID: "42", Title: "t", Status: "open"})
	}
	h.l.deps.Mise.(*fakeMise).brief.Backlog = backlog
	writeCompactOrders(t, h.l.deps.OrdersNextFile, orderx.CompactOrdersFile{
		Orders: []orderx.CompactOrder{{ID: "42", Stages: []orderx.CompactStage{{Do: "execute"}}}},
	})
	if err := h.l.Cycle(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func countLoopEvents(t *testing.T, h *gateHarness, typ string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(h.l.runtimeDir, "loop-events.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), `"type":"`+typ+`"`)
}

func TestConflictedOrderIsNotReplannedWhileItsStopIsUnsent(t *testing.T) {
	h := newGateHarness(t, "medium", "execute", "quality")
	h.l.deps.OrdersNextFile = filepath.Join(h.l.runtimeDir, "orders-next.json")
	h.ln.conflicts = 1
	h.ln.rebaseErr = &RebaseConflictError{Files: []string{"README.md"}}
	h.ar.stopErr = errors.New("GraphQL: API rate limit already exceeded")
	h.next(t)
	h.review(t, 0, "none")
	h.next(t)
	h.failedWith(t, "rebase conflict")
	sessions := len(h.rt.sessions)

	// The stop failed, so the issue is still ready and the scheduler keeps proposing it.
	for i := 0; i < 5; i++ {
		h.repropose(t, true)
	}
	if h.ln.rebases != 1 || len(h.rt.sessions) != sessions {
		t.Fatalf("conflicted order re-ran: rebases=%d (want 1), new sessions=%d (want 0)", h.ln.rebases, len(h.rt.sessions)-sessions)
	}
	if got := h.order(t).Status; got != OrderStatusFailed {
		t.Fatalf("order status = %s, want failed until a person puts ready back", got)
	}
	if got := countLoopEvents(t, h, "order.failed"); got != 1 {
		t.Fatalf("order.failed events = %d, want 1", got)
	}

	// The stop lands once gh recovers: the issue loses ready, and the order stays put.
	h.ar.stopErr = nil
	h.l.lastStopRetry = time.Time{}
	tries := len(h.ar.stopCalls)
	h.repropose(t, false)
	h.repropose(t, false)
	if landed := len(h.ar.stopCalls) - tries; landed != 1 {
		t.Fatalf("stop calls after gh recovered = %d, want exactly 1", landed)
	}
	if h.ln.rebases != 1 || len(h.rt.sessions) != sessions {
		t.Fatalf("order ran after its stop landed: rebases=%d new sessions=%d", h.ln.rebases, len(h.rt.sessions)-sessions)
	}
}

package loop

import (
	"testing"
)

// #186: verdict-check is red because the head has no verdict (the comment was
// lost). Retrying ship cannot fix that; the verify stage has to run again.
func TestShipWithNoVerdictAtHeadRerunsVerifyOnceThenShips(t *testing.T) {
	h := newGateHarness(t, "high", "execute", "quality", "verify")
	h.ln.lostVerdicts = 1
	h.ln.missingVerdict = true
	h.next(t)
	h.review(t, 0, "none")
	h.next(t)
	h.verdict(t, 0, VerdictPass, fixtureHead)
	h.next(t) // verify finishes; its verdict never reaches the PR; ship finds verdict-check red

	if len(h.ln.merged) != 0 {
		t.Fatal("merged with no verdict at the head")
	}
	if got := h.order(t).Stages[2].Status; got != StageStatusPending && got != StageStatusActive {
		t.Fatalf("verify stage status = %q, want it reopened", got)
	}
	// The rerun verifier posts the verdict this time, and the order ships.
	h.verdict(t, 0, VerdictPass, fixtureHead)
	h.next(t)
	if len(h.ln.merged) != 1 {
		t.Fatalf("merged = %d, want the order shipped after the verify rerun", len(h.ln.merged))
	}
	if h.ln.verdictTries != 2 || len(h.rt.sessions) != 4 {
		t.Fatalf("verdict posts = %d, sessions = %d; want verify run twice, no more", h.ln.verdictTries, len(h.rt.sessions))
	}
}

// The rerun happens once per head. A verdict that is still missing after it
// is not retried again: the order waits and stops like any stuck red check.
func TestMissingVerdictAfterOneVerifyRerunIsNotRerunAgain(t *testing.T) {
	h := newGateHarness(t, "high", "execute", "quality", "verify")
	h.ln.lostVerdicts = 100
	h.ln.missingVerdict = true
	h.next(t)
	h.review(t, 0, "none")
	h.next(t)
	h.verdict(t, 0, VerdictPass, fixtureHead)
	h.next(t)
	h.verdict(t, 0, VerdictPass, fixtureHead)
	h.next(t) // the rerun's verdict is lost too

	for i := 0; i < 5; i++ {
		h.l.lastChecksPoll = h.l.lastChecksPoll.Add(-checksPollInterval * 2)
		if err := h.l.Cycle(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if len(h.rt.sessions) != 4 {
		t.Fatalf("sessions = %d, want 4: verify reran more than once for one head", len(h.rt.sessions))
	}
	if _, ok := h.l.cooks.pendingReview["42"]; !ok {
		t.Fatal("order did not park to wait on the red check")
	}
	if len(h.ln.merged) != 0 {
		t.Fatal("merged with no verdict at the head")
	}
}

// A FAIL from the rerun takes the normal rework path.
func TestFailFromVerifyRerunFollowsTheReworkPath(t *testing.T) {
	h := newGateHarness(t, "high", "execute", "quality", "verify")
	h.ln.lostVerdicts = 1
	h.ln.missingVerdict = true
	h.next(t)
	h.review(t, 0, "none")
	h.next(t)
	h.verdict(t, 0, VerdictPass, fixtureHead)
	h.next(t)
	h.verdict(t, 0, VerdictFail, fixtureHead)
	h.next(t)

	if o := h.order(t); o.Round != 1 || o.Stages[0].Status != StageStatusActive {
		t.Fatalf("round = %d, stages = %v; want execute rerunning in round 1", o.Round, o.Stages)
	}
	if len(h.ln.merged) != 0 {
		t.Fatal("merged after a FAIL")
	}
}

// The same wait found while the PR is parked (checks were still running at the
// first ship) also reruns verify.
func TestParkedShipFindingNoVerdictRerunsVerify(t *testing.T) {
	h := newGateHarness(t, "high", "execute", "quality", "verify")
	h.ln.lostVerdicts = 1
	h.ln.missingVerdict = true
	h.ln.pending = 1
	h.ln.pendingDetail = "checks are still running"
	h.next(t)
	h.review(t, 0, "none")
	h.next(t)
	h.verdict(t, 0, VerdictPass, fixtureHead)
	h.next(t)
	if _, ok := h.l.cooks.pendingReview["42"]; !ok {
		t.Fatal("order did not park on running checks")
	}
	h.l.lastChecksPoll = h.l.lastChecksPoll.Add(-checksPollInterval * 2)
	if err := h.l.Cycle(t.Context()); err != nil { // the poll finds verdict-check red, no verdict
		t.Fatal(err)
	}
	if _, ok := h.l.cooks.pendingReview["42"]; ok {
		t.Fatal("order stayed parked instead of rerunning verify")
	}
	h.verdict(t, 0, VerdictPass, fixtureHead)
	h.next(t)
	if len(h.ln.merged) != 1 {
		t.Fatalf("merged = %d, want shipped after the verify rerun", len(h.ln.merged))
	}
}

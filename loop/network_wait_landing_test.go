package loop

import (
	"context"
	"strings"
	"testing"
	"time"
)

func setClock(l *Loop) *fakeClock {
	clock := &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	l.deps.Now = clock.Now
	return clock
}

func parkedForNetwork(l *Loop, orderID string) bool {
	p, ok := l.cooks.pendingReview[orderID]
	return ok && strings.HasPrefix(p.reason, awaitingNetworkPrefix)
}

// cycleFor runs cycles a second apart until done reports true or d passes. It
// fails the test if Cycle ever returns an error: a network error must not end start.
func cycleFor(t *testing.T, l *Loop, clock *fakeClock, d time.Duration, done func() bool) bool {
	t.Helper()
	for end := clock.now.Add(d); clock.now.Before(end); clock.now = clock.now.Add(time.Second) {
		if err := l.Cycle(context.Background()); err != nil {
			t.Fatalf("Cycle error = %v, want a network error to leave the loop running", err)
		}
		if done() {
			return true
		}
	}
	return false
}

// A network error in a PR call after the cook finished must not fail the
// order: it parks, the loop waits with backoff, and the completion resumes.
func TestPRCallNetworkErrorsParkTheStageAndResume(t *testing.T) {
	sites := []struct {
		name string
		down func(h *landingHarness, err error)
		up   func(h *landingHarness)
	}{
		{"push and open draft PR", func(h *landingHarness, err error) { h.ln.pushErr = err }, func(h *landingHarness) { h.ln.pushErr = nil }},
		{"ship PR", func(h *landingHarness, err error) { h.ln.mergeErr = err }, func(h *landingHarness) { h.ln.mergeErr = nil }},
	}
	for _, site := range sites {
		for _, class := range networkErrorClasses {
			t.Run(site.name+"/"+class.name, func(t *testing.T) {
				h := newLandingHarness(t, "auto", 1)
				clock := setClock(h.l)
				site.down(h, class.err)

				h.runStage(t, 1) // the completion hits the network error
				if !parkedForNetwork(h.l, "42") {
					t.Fatalf("pending = %#v, want the stage parked for the network, not failed", h.l.cooks.pendingReview)
				}
				if h.l.outage == nil || h.l.stateLabel() != "waiting-network" {
					t.Fatalf("outage=%v state=%q, want waiting-network", h.l.outage, h.l.stateLabel())
				}
				// Down for 12 minutes: the stage stays parked, nothing merges.
				cycleFor(t, h.l, clock, 12*time.Minute, func() bool { return false })
				if !parkedForNetwork(h.l, "42") || len(h.ln.merged) != 0 {
					t.Fatalf("after the outage window: parked=%v merged=%d, want still parked", parkedForNetwork(h.l, "42"), len(h.ln.merged))
				}
				if got := h.l.outage.attempts; got < 3 {
					t.Fatalf("outage attempts = %d, want the completion retried with backoff", got)
				}

				site.up(h)
				if !cycleFor(t, h.l, clock, 10*time.Minute, func() bool { return len(h.ln.merged) == 1 }) {
					t.Fatal("PR never merged after the network came back")
				}
				if parkedForNetwork(h.l, "42") {
					t.Fatal("stage still parked for the network after it completed")
				}
				cycleFor(t, h.l, clock, time.Minute, func() bool { return h.l.outage == nil })
				if h.l.outage != nil {
					t.Fatal("outage kept after recovery")
				}
				if len(h.ar.doneCalls) != 1 {
					t.Fatalf("backlog done calls = %v, want the order finished once", h.ar.doneCalls)
				}
			})
		}
	}
}

func TestVerdictCommentNetworkErrorWaitsAndPostsOnce(t *testing.T) {
	h := newGateHarness(t, "high", "execute", "quality", "verify")
	clock := setClock(h.l)
	verdictPostBackoff = 0
	h.ln.verdictErr = networkErrorClasses[4].err
	h.next(t)
	h.review(t, 0, "none")
	h.next(t)
	h.verdict(t, 0, VerdictPass, fixtureHead)
	h.next(t)
	if !parkedForNetwork(h.l, "42") {
		t.Fatalf("pending = %#v, want the verify stage parked for the network, not failed", h.l.cooks.pendingReview)
	}
	cycleFor(t, h.l, clock, 12*time.Minute, func() bool { return false })
	if !parkedForNetwork(h.l, "42") || len(h.ln.merged) != 0 {
		t.Fatal("want the stage parked and nothing merged while the network is down")
	}
	h.ln.verdictErr = nil
	if !cycleFor(t, h.l, clock, 10*time.Minute, func() bool { return len(h.ln.merged) == 1 }) {
		t.Fatal("PR never merged after the network came back")
	}
	if len(h.ln.verdicts) != 1 {
		t.Fatalf("verdict comments = %d, want the verdict posted once", len(h.ln.verdicts))
	}
}

func TestRebaseFetchNetworkErrorWaitsAndRebasesOnce(t *testing.T) {
	h := newLandingHarness(t, "auto", 1)
	clock := setClock(h.l)
	h.ln.conflicts = 1
	h.ln.rebaseErr = networkErrorClasses[0].err
	h.runStage(t, 1)
	if !parkedForNetwork(h.l, "42") {
		t.Fatalf("pending = %#v, want the stage parked for the network, not failed", h.l.cooks.pendingReview)
	}
	if got := h.l.rebaseCount("42"); got != 0 {
		t.Fatalf("rebase count = %d, want the rebase that never ran taken back", got)
	}
	h.ln.rebaseErr = nil
	if !cycleFor(t, h.l, clock, 10*time.Minute, func() bool { return len(h.ln.merged) == 1 }) {
		t.Fatal("PR never merged after the network came back")
	}
}

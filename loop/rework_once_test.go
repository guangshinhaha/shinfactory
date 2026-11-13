package loop

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/guangshinhaha/shinfactory/event"
)

// stageFail queues a stage_fail event for the session that dispatches next, as
// a reviewer or verifier agent emits it when it rejects the work.
func (h *gateHarness) stageFail(t *testing.T, message string) {
	t.Helper()
	h.pending = append(h.pending, func() {
		sid := h.rt.sessions[h.n].id
		w, err := event.NewEventWriter(h.l.runtimeDir, sid)
		if err != nil {
			t.Fatal(err)
		}
		payload, _ := json.Marshal(event.StageFailPayload{Message: message})
		if err := w.Append(context.Background(), event.Event{Type: event.EventStageFail, SessionID: sid, Payload: payload}); err != nil {
			t.Fatal(err)
		}
	})
}

// finishRework completes the execute stage a rejection already redispatched.
func (h *gateHarness) finishRework(t *testing.T) {
	t.Helper()
	h.rt.sessions[h.n].complete("completed")
	h.n++
	if err := h.l.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.settleEvidence(t)
}

// One rejection then a pass merges: the reviewer's findings reach the rework
// prompt, and the same review and verify stages run again on the new head.
func TestQualityRejectionReworksOnceThenMerges(t *testing.T) {
	h := newGateHarness(t, "high", "execute", "quality", "verify")
	h.next(t) // execute
	h.stageFail(t, "Rejected: ./greet prints nothing, Done when wants hello")
	h.next(t) // quality rejects -> back to execute

	o := h.order(t)
	if o.Status == OrderStatusFailed || o.Round != 1 {
		t.Fatalf("after one rejection: status %q round %d, want a rework in round 1", o.Status, o.Round)
	}
	if n := h.stops(); n != 0 {
		t.Fatalf("person told %d times after the first rejection, want 0", n)
	}
	// Redispatch happened in that cycle; the rework prompt carries the findings.
	if len(h.rt.calls) != 3 || !strings.Contains(h.rt.calls[2].Prompt, "./greet prints nothing") {
		t.Fatalf("rework prompt = %v, want the reviewer's findings", h.rt.calls[len(h.rt.calls)-1].Prompt)
	}
	if h.rt.calls[2].WorktreePath != h.rt.calls[0].WorktreePath {
		t.Fatal("rework did not reuse the order's worktree")
	}
	h.finishRework(t)
	h.review(t, 1, "none")
	h.next(t)
	h.verdict(t, 1, VerdictPass, fixtureHead)
	h.next(t)
	if len(h.ln.merged) != 1 {
		t.Fatalf("merges = %d, want 1 after the rework passed", len(h.ln.merged))
	}
	if n := h.stops(); n != 0 {
		t.Fatalf("person told %d times for an order that merged", n)
	}
}

// A verifier's stage_fail takes the same road as a quality one.
func TestVerifyRejectionReworksOnce(t *testing.T) {
	h := newGateHarness(t, "high", "execute", "quality", "verify")
	h.next(t)
	h.review(t, 0, "none")
	h.next(t)
	h.stageFail(t, "Rejected: login redirects to /500")
	h.next(t)
	if o := h.order(t); o.Status == OrderStatusFailed || o.Round != 1 {
		t.Fatalf("status %q round %d, want a rework in round 1", o.Status, o.Round)
	}
	if !strings.Contains(h.rt.calls[len(h.rt.calls)-1].Prompt, "login redirects to /500") {
		t.Fatal("verifier's findings missing from the rework prompt")
	}
}

// Two rejections send the issue back once, with both reasons in the comment.
func TestSecondRejectionStopsWithBothReasons(t *testing.T) {
	h := newGateHarness(t, "high", "execute", "quality", "verify")
	h.next(t) // execute
	h.stageFail(t, "Rejected: first problem")
	h.next(t) // quality rejects; execute redispatched
	h.finishRework(t)
	h.stageFail(t, "Rejected: second problem")
	h.next(t) // quality rejects again

	h.failedWith(t, "second rejection")
	if n := h.stops(); n != 1 {
		t.Fatalf("stop adapter ran %d times, want exactly 1", n)
	}
	msg := h.ar.stopMessages[len(h.ar.stopMessages)-1]
	for _, want := range []string{"first problem", "second problem"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("stop comment = %q, want it to hold %q", msg, want)
		}
	}
}

// A findings file in review.md is a rejection too, and counts with stage_fail:
// one of each is two rejections.
func TestFindingsFileAndStageFailShareOneReworkBudget(t *testing.T) {
	h := newGateHarness(t, "medium", "execute", "quality")
	h.next(t)
	h.review(t, 0, "\n- file finding")
	h.next(t)
	if o := h.order(t); o.Round != 1 || o.Status == OrderStatusFailed {
		t.Fatalf("status %q round %d, want rework", o.Status, o.Round)
	}
	if !strings.Contains(h.rt.calls[len(h.rt.calls)-1].Prompt, "file finding") {
		t.Fatalf("findings text missing from the rework prompt: %d calls, last %q", len(h.rt.calls), h.rt.calls[len(h.rt.calls)-1].Prompt)
	}
	h.finishRework(t)
	h.stageFail(t, "Rejected: stage_fail problem")
	h.next(t)
	h.failedWith(t, "second rejection")
}

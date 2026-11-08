package loop

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/guangshinhaha/shinfactory/event"
)

func TestTmpBlockingMessageApproveMergesMidOrder(t *testing.T) {
	h := newLandingHarness(t, "supervised", 2)
	if err := h.l.Cycle(context.Background()); err != nil { t.Fatal(err) }
	sid := h.rt.sessions[0].ID()
	w, _ := event.NewEventWriter(h.l.runtimeDir, sid)
	p, _ := json.Marshal(event.StageMessagePayload{Message: "need human decision before stage 2"})
	_ = w.Append(context.Background(), event.Event{Type: event.EventStageMessage, SessionID: sid, Payload: p})
	h.rt.sessions[0].complete("completed")
	if err := h.l.Cycle(context.Background()); err != nil { t.Fatal(err) }
	t.Logf("pushes=%d merged=%d parked=%v", len(h.ln.pushes), len(h.ln.merged), h.l.cooks.pendingReview["42"] != nil)
	if err := h.l.controlMerge("42"); err != nil { t.Fatal(err) }
	t.Logf("after approve: pushes=%d merged=%d cleaned=%v done=%v", len(h.ln.pushes), len(h.ln.merged), h.wt.cleaned, h.ar.doneCalls)
	o, _ := h.l.orderByID("42")
	for i, s := range o.Stages { t.Logf("stage %d status=%s", i, s.Status) }
}

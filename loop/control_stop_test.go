package loop

import (
	"context"
	"testing"
)

// #225: stopping a non-steerable cook (an Orca worker) killed the session but
// left the order's stage active, so the order kept its slot and no cycle ever
// finished or relaunched it.
func TestControlStopEndsTheOrderAndFreesItsSlot(t *testing.T) {
	h := newGateHarness(t, "none", "execute")
	if err := h.l.Cycle(context.Background()); err != nil {
		t.Fatalf("dispatch cycle: %v", err)
	}
	if len(h.rt.sessions) != 1 {
		t.Fatalf("sessions = %d, want 1 dispatched", len(h.rt.sessions))
	}
	sessionID := h.rt.sessions[0].ID()

	if err := h.l.controlStop(sessionID); err != nil {
		t.Fatalf("controlStop: %v", err)
	}
	if err := h.l.Cycle(context.Background()); err != nil {
		t.Fatalf("cycle after stop: %v", err)
	}

	o := h.order(t)
	if o.Status == OrderStatusActive {
		t.Fatalf("order status = %q after control stop, want it out of active", o.Status)
	}
	for _, s := range o.Stages {
		if s.Status == StageStatusActive {
			t.Fatalf("stage %s still active after control stop; it holds the order's slot", s.TaskKey)
		}
	}
	if len(h.rt.sessions) != 1 {
		t.Fatalf("sessions = %d, want no relaunch after a stop", len(h.rt.sessions))
	}
}

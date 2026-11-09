package loop

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/guangshinhaha/shinfactory/internal/linestatus"
)

// The coordinator's path, end to end on a running loop: read the line with
// linestatus (what `shinfactory status --json` prints), send a control through the
// queue (what `shinfactory control` does), and read the ack and the line again.
func TestCoordinatorStatusDrainResumeOnARunningLoop(t *testing.T) {
	logger, _ := newTestLogger()
	tc := newTestLoop(t, logger)
	orders := OrdersFile{Orders: []Order{testOrder("item-1", "execute", "execute", "claude", "claude-opus-4-6")}}
	if err := writeOrdersAtomic(tc.ordersPath, orders); err != nil {
		t.Fatalf("write orders: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- tc.loop.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		// Shutdown waits for live sessions; let the fake ones end.
		tc.runtime.mu.Lock()
		for _, s := range tc.runtime.sessions {
			s.complete("failed")
		}
		tc.runtime.mu.Unlock()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("loop did not stop")
		}
	})

	waitForState := func(want string) linestatus.Report {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		var rep linestatus.Report
		var lastErr error
		for time.Now().Before(deadline) {
			r, err := linestatus.Read(tc.runtimeDir)
			if err != nil {
				lastErr = err
			}
			if err == nil {
				rep = r
				if r.LoopState == want && runningHas(r, "item-1") {
					return r
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("status never showed loop_state=%s with item-1 running; last = %+v, err = %v", want, rep, lastErr)
		return rep
	}
	send := func(action string) ControlAck {
		t.Helper()
		id := fmt.Sprintf("cli-test-%s", action)
		if err := AppendControlCommand(tc.runtimeDir, ControlCommand{ID: id, Action: action, At: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
		actx, acancel := context.WithTimeout(ctx, 10*time.Second)
		defer acancel()
		ack, err := WaitControlAck(actx, tc.runtimeDir, id)
		if err != nil {
			t.Fatal(err)
		}
		if ack.Status != "ok" {
			t.Fatalf("%s ack = %+v", action, ack)
		}
		return ack
	}

	before := waitForState("running")
	if !runningHas(before, "item-1") {
		t.Fatalf("running = %+v", before.Running)
	}
	send("drain")
	if got := waitForState("draining"); !runningHas(got, "item-1") {
		t.Fatalf("draining line = %+v", got)
	}
	send("resume")
	waitForState("running")
}

func runningHas(r linestatus.Report, id string) bool {
	for _, o := range r.Running {
		if o.ID == id {
			return true
		}
	}
	return false
}

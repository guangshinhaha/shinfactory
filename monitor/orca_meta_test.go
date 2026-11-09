package monitor

import (
	"context"
	"testing"
	"time"

	"github.com/guangshinhaha/shinfactory/dispatcher"
	"github.com/guangshinhaha/shinfactory/internal/testutil/fakeorca"
)

func TestOrcaSessionMetaCarriesRuntimeModelAndOutcome(t *testing.T) {
	for _, tc := range []struct {
		outcome string
		want    SessionStatus
	}{{"succeeded", SessionStatusExited}, {"failed", SessionStatusFailed}} {
		t.Run(tc.outcome, func(t *testing.T) { checkOrcaMeta(t, tc.outcome, tc.want) })
	}
}

func checkOrcaMeta(t *testing.T, outcome string, want SessionStatus) {
	t.Helper()
	fake := fakeorca.New(t)
	dir := t.TempDir()
	rt := dir + "/.noodle"
	d := dispatcher.NewOrcaDispatcher(dispatcher.OrcaDispatcherConfig{
		ProjectDir: dir, RuntimeDir: rt, Bin: fake.Bin, PollInterval: 5 * time.Millisecond,
	})
	s, err := d.Dispatch(context.Background(), dispatcher.DispatchRequest{
		Name: "42-0-execute", Prompt: "do it", Provider: "claude", Model: "claude-sonnet-5-5",
		WorktreePath: dir, AllowPrimaryCheckout: true, Runtime: "orca",
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	fake.Settle(t, fake.LastDispatch(t), outcome)
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("not done")
	}
	metas, err := NewMonitor(rt).RunOnce(context.Background())
	if err != nil || len(metas) != 1 {
		t.Fatalf("RunOnce: %v %v", metas, err)
	}
	m := metas[0]
	t.Logf("meta: %+v", m)
	if m.Runtime != "orca" || m.Model != "claude-sonnet-5-5" {
		t.Errorf("runtime/model = %q/%q", m.Runtime, m.Model)
	}
	if m.Status != want || m.Alive {
		t.Errorf("status = %q alive=%v, want %q", m.Status, m.Alive, want)
	}
	if m.DurationSeconds < 0 {
		t.Errorf("duration")
	}
}

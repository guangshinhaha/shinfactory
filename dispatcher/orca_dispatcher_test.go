package dispatcher

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/guangshinhaha/shinfactory/internal/testutil/fakeorca"
)

func orcaTestDispatcher(t *testing.T, fake *fakeorca.Fake) (*OrcaDispatcher, string) {
	t.Helper()
	dir := t.TempDir()
	d := NewOrcaDispatcher(OrcaDispatcherConfig{
		ProjectDir:   dir,
		RuntimeDir:   dir + "/.noodle",
		Bin:          fake.Bin,
		PollInterval: 5 * time.Millisecond,
	})
	return d, dir
}

func orcaTestRequest(dir string) DispatchRequest {
	return DispatchRequest{
		Name: "42-0-execute", Prompt: "do it", Provider: "claude", Model: "claude-sonnet-5-5",
		WorktreePath: dir, AllowPrimaryCheckout: true, Runtime: "orca",
	}
}

func waitDone(t *testing.T, s Session) SessionOutcome {
	t.Helper()
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("session did not finish")
		return SessionOutcome{}
	}
	// A poll still mid-call would write into the fake's state dir while
	// t.TempDir cleanup removes it.
	if orca, ok := s.(*orcaSession); ok {
		select {
		case <-orca.pollDone:
		case <-time.After(5 * time.Second):
			t.Fatal("orca poll did not stop")
		}
	}
	return s.Outcome()
}

func TestOrcaDispatchLaunchesWorkerWithChosenProviderAndModel(t *testing.T) {
	fake := fakeorca.New(t)
	d, dir := orcaTestDispatcher(t, fake)

	s, err := d.Dispatch(context.Background(), orcaTestRequest(dir))
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	start := fake.Starts()
	if len(start) != 1 {
		t.Fatalf("worker-start calls = %v, want 1", start)
	}
	for _, frag := range []string{"--agent claude", "--model claude-sonnet-5-5", "--worktree path:" + dir, "--json"} {
		if !strings.Contains(start[0], frag) {
			t.Errorf("worker-start = %q, missing %q", start[0], frag)
		}
	}
	fake.Settle(t, fake.LastDispatch(t), "succeeded")
	if out := waitDone(t, s); out.Status != StatusCompleted {
		t.Fatalf("outcome = %+v, want completed", out)
	}
}

func TestOrcaDispatchLaunchesZcodeWorker(t *testing.T) {
	fake := fakeorca.New(t)
	d, dir := orcaTestDispatcher(t, fake)

	req := orcaTestRequest(dir)
	req.Provider = "zcode"
	req.Model = "GLM-5.3"
	s, err := d.Dispatch(context.Background(), req)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	start := fake.Starts()
	if len(start) != 1 {
		t.Fatalf("worker-start calls = %v, want 1", start)
	}
	for _, frag := range []string{"--agent zcode", "--model GLM-5.3"} {
		if !strings.Contains(start[0], frag) {
			t.Errorf("worker-start = %q, missing %q", start[0], frag)
		}
	}
	fake.Settle(t, fake.LastDispatch(t), "succeeded")
	if out := waitDone(t, s); out.Status != StatusCompleted {
		t.Fatalf("outcome = %+v, want completed", out)
	}
}

func TestOrcaDispatchRejectsUnknownProvider(t *testing.T) {
	fake := fakeorca.New(t)
	d, dir := orcaTestDispatcher(t, fake)

	req := orcaTestRequest(dir)
	req.Provider = "cursor"
	if _, err := d.Dispatch(context.Background(), req); err == nil {
		t.Fatal("expected an error for a provider orca has no agent for")
	}
	if n := len(fake.Starts()); n != 0 {
		t.Fatalf("worker-start calls = %d, want 0 (rejected before launch)", n)
	}
}

func TestOrcaDispatchReportsUsageLimitAtLaunch(t *testing.T) {
	fake := fakeorca.New(t)
	fake.SetLimit(t, true)
	d, dir := orcaTestDispatcher(t, fake)

	_, err := d.Dispatch(context.Background(), orcaTestRequest(dir))
	if !IsUsageLimit(err) {
		t.Fatalf("dispatch error = %v, want a usage-limit error", err)
	}
}

func TestOrcaDispatchOtherLaunchFailureIsNotAUsageLimit(t *testing.T) {
	fake := fakeorca.New(t)
	d, dir := orcaTestDispatcher(t, fake)
	req := orcaTestRequest(dir)
	req.Provider = "gemini"

	_, err := d.Dispatch(context.Background(), req)
	if err == nil || IsUsageLimit(err) {
		t.Fatalf("dispatch error = %v, want a plain provider error", err)
	}
}

func TestOrcaSessionFailureWithoutLimitTextIsAPlainFailure(t *testing.T) {
	fake := fakeorca.New(t)
	d, dir := orcaTestDispatcher(t, fake)
	s, err := d.Dispatch(context.Background(), orcaTestRequest(dir))
	if err != nil {
		t.Fatal(err)
	}
	// The fake's user message mentions limits; only the worker's own output counts.
	id := fake.LastDispatch(t)
	fake.SetTranscript(t, id, "go build failed: undefined: foo")
	fake.Settle(t, id, "failed")
	out := waitDone(t, s)
	if out.Status != StatusFailed || out.UsageLimit {
		t.Fatalf("outcome = %+v, want failed without UsageLimit", out)
	}
}

func TestOrcaSessionTerminateStopsWorker(t *testing.T) {
	fake := fakeorca.New(t)
	d, dir := orcaTestDispatcher(t, fake)
	s, err := d.Dispatch(context.Background(), orcaTestRequest(dir))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Terminate(); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	if out := waitDone(t, s); out.Status != StatusKilled {
		t.Fatalf("outcome = %+v, want killed", out)
	}
	if !strings.Contains(strings.Join(fake.Calls(), "\n"), "worker-stop --dispatch "+fake.LastDispatch(t)) {
		t.Fatalf("worker-stop not called: %v", fake.Calls())
	}
}

func TestIsUsageLimitText(t *testing.T) {
	for text, want := range map[string]bool{
		"Claude usage limit reached. Your limit will reset at 3pm": true,
		"5-hour limit reached":                                   true,
		"Credit balance is too low":                              true,
		"go test: TestRateLimiter FAIL: rate limit not enforced": false,
		"max line length limit reached in golangci-lint":         false,
		"error: I reached your context limit":                    false,
		"go build failed: undefined: foo":                        false,
		"":                                                       false,
	} {
		if got := IsUsageLimitText(text); got != want {
			t.Errorf("IsUsageLimitText(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestOrcaSpecQuotesProjectDir(t *testing.T) {
	d := NewOrcaDispatcher(OrcaDispatcherConfig{ProjectDir: "/Users/a b/My Projects/x"})
	spec := d.buildSpec("sess-1", DispatchRequest{Prompt: "p"}, "")
	if !strings.Contains(spec, "NOODLE_PROJECT_DIR='/Users/a b/My Projects/x' shinfactory") {
		t.Fatalf("spec does not shell-quote the project dir:\n%s", spec)
	}
}

// Real Orca lists a finished worker as state "succeeded" with no outcome field.
func TestOrcaWorkerRowShapes(t *testing.T) {
	for name, tc := range map[string]struct {
		w       orcaWorker
		settled bool
		success bool
	}{
		"succeeded, no outcome": {orcaWorker{State: "succeeded"}, true, true},
		"stopped, failed":       {orcaWorker{State: "stopped", Outcome: "failed"}, true, false},
		"ready, in progress":    {orcaWorker{State: "ready", Outcome: "in_progress"}, false, false},
		"stopped, no outcome":   {orcaWorker{State: "stopped"}, true, false},
	} {
		if tc.w.settled() != tc.settled || tc.w.succeeded() != tc.success {
			t.Errorf("%s: settled=%v succeeded=%v, want %v %v", name, tc.w.settled(), tc.w.succeeded(), tc.settled, tc.success)
		}
	}
}

func TestOrcaSessionFindsWorkerBeyondFirstListPage(t *testing.T) {
	fake := fakeorca.New(t)
	d, dir := orcaTestDispatcher(t, fake)
	real := d.run
	d.run = func(ctx context.Context, bin string, args ...string) ([]byte, error) {
		if len(args) > 1 && args[1] == "worker-list" {
			return []byte(`{"ok":true,"result":{"workers":[]}}`), nil // worker is past the first page
		}
		return real(ctx, bin, args...)
	}
	s, err := d.Dispatch(context.Background(), orcaTestRequest(dir))
	if err != nil {
		t.Fatal(err)
	}
	fake.Settle(t, fake.LastDispatch(t), "succeeded")
	if out := waitDone(t, s); out.Status != StatusCompleted {
		t.Fatalf("outcome = %+v, want completed via worker-show", out)
	}
}

func TestOrcaDispatchRetriesUnknownWorktreeThenLaunches(t *testing.T) {
	fake := fakeorca.New(t)
	fake.RefuseUnknownWorktree(t, 2)
	d, dir := orcaTestDispatcher(t, fake)
	d.selectorBackoff = time.Millisecond

	if _, err := d.Dispatch(context.Background(), orcaTestRequest(dir)); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if n := len(fake.Starts()); n != 3 {
		t.Fatalf("worker-start calls = %d, want 3 (two refusals, then success)", n)
	}
}

func TestOrcaDispatchGivesUpOnPersistentUnknownWorktree(t *testing.T) {
	fake := fakeorca.New(t)
	fake.RefuseUnknownWorktree(t, 1000)
	d, dir := orcaTestDispatcher(t, fake)
	d.selectorBackoff = time.Millisecond

	_, err := d.Dispatch(context.Background(), orcaTestRequest(dir))
	if err == nil || !strings.Contains(err.Error(), "selector_not_found") {
		t.Fatalf("dispatch error = %v, want one naming selector_not_found", err)
	}
}

func TestOrcaDispatchRecoversRunAfterConsumerFenced(t *testing.T) {
	fake := fakeorca.New(t)
	d, dir := orcaTestDispatcher(t, fake)

	first, err := d.Dispatch(context.Background(), orcaTestRequest(dir))
	if err != nil {
		t.Fatalf("first dispatch: %v", err)
	}
	fake.Settle(t, fake.LastDispatch(t), "succeeded")
	waitDone(t, first)

	// The host sleeps; Orca fences the old run's consumer.
	fake.FenceConsumer(t, 1)
	s, err := d.Dispatch(context.Background(), orcaTestRequest(dir))
	if err != nil {
		t.Fatalf("dispatch after consumer_fenced = %v, want a recovered run", err)
	}
	fake.Settle(t, fake.LastDispatch(t), "succeeded")
	waitDone(t, s)

	runs := fake.Runs()
	if len(runs) != 2 {
		t.Fatalf("runs = %v, want a new run created after the fence", runs)
	}
	if got := fake.RunOf(t, fake.LastDispatch(t)); got != runs[1] {
		t.Fatalf("worker run = %s, want the new run %s", got, runs[1])
	}
	if rec := d.recordedRun(); rec != runs[1] {
		t.Fatalf("recorded run = %s, want %s so a restart finds it", rec, runs[1])
	}
}

func TestOrcaDispatchGivesUpWhenConsumerStaysFenced(t *testing.T) {
	fake := fakeorca.New(t)
	d, dir := orcaTestDispatcher(t, fake)
	fake.FenceConsumer(t, 100)

	_, err := d.Dispatch(context.Background(), orcaTestRequest(dir))
	if err == nil || !strings.Contains(err.Error(), "consumer_fenced") {
		t.Fatalf("dispatch error = %v, want consumer_fenced surfaced after one recovery", err)
	}
}

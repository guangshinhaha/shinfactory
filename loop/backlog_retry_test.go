package loop

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guangshinhaha/shinfactory/mise"
	loopruntime "github.com/guangshinhaha/shinfactory/runtime"
)

// fakeClock is the loop's only time seam for backlog retries: tests advance it
// instead of waiting, so a minutes-long outage runs instantly.
type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func backlogRetryLoop(t *testing.T, m *fakeMise) (*Loop, *fakeClock) {
	t.Helper()
	clock := &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	return New(t.TempDir(), "noodle", localLandingConfig(), Dependencies{
		Runtimes: map[string]loopruntime.Runtime{"process": newMockRuntime()},
		Worktree: &fakeWorktree{},
		Adapter:  &fakeAdapterRunner{},
		Mise:     m,
		Monitor:  fakeMonitor{},
		Registry: testLoopRegistry(),
		Now:      clock.Now,
	}), clock
}

// cycleUntilDone calls buildCycleBrief, advancing the clock a second per call
// like the poll ticker, until it settles. It returns the elapsed virtual time
// and the number of calls that returned while still waiting.
func cycleUntilDone(t *testing.T, l *Loop, clock *fakeClock) (running bool, waits int, elapsed time.Duration, err error) {
	t.Helper()
	start := clock.now
	for i := 0; i < 100000; i++ {
		_, _, running, _, err = l.buildCycleBrief(context.Background())
		if err != nil || running {
			return running, waits, clock.now.Sub(start), err
		}
		waits++
		clock.now = clock.now.Add(time.Second)
	}
	t.Fatal("buildCycleBrief never settled")
	return
}

var errTransientGitHub = errors.New(`run adapter backlog.sync: gh: error connecting to api.github.com: dial tcp: lookup api.github.com: no such host: exit status 1`)

func TestBuildCycleBriefRetriesTransientBacklogError(t *testing.T) {
	m := &fakeMise{results: []fakeMiseResult{
		{err: errTransientGitHub},
		{err: errors.New("run adapter backlog.sync: gh: HTTP 502: Bad Gateway: exit status 1")},
		{brief: mise.Brief{}},
	}}
	l, clock := backlogRetryLoop(t, m)

	running, _, _, err := cycleUntilDone(t, l, clock)
	if err != nil {
		t.Fatalf("buildCycleBrief error = %v, want the transient errors retried", err)
	}
	if !running || m.calls != 3 {
		t.Fatalf("running=%v calls=%d, want running after 3 sync attempts", running, m.calls)
	}
	if l.backlogRetry != nil {
		t.Fatal("retry state kept after recovery, want it cleared")
	}
}

// The cycle must return while waiting so control commands and status stamps
// run; a failing sync is not re-run before its backoff has passed.
func TestBuildCycleBriefReturnsWhileWaitingToRetry(t *testing.T) {
	m := &fakeMise{err: errTransientGitHub}
	l, clock := backlogRetryLoop(t, m)

	_, warnings, running, _, err := l.buildCycleBrief(context.Background())
	if err != nil || running {
		t.Fatalf("running=%v err=%v, want a non-running cycle that returns at once", running, err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "retrying in 1s") {
		t.Fatalf("warnings = %q, want a waiting-state warning", warnings)
	}
	// Half a second later the backoff has not passed: no new sync call.
	clock.now = clock.now.Add(500 * time.Millisecond)
	if _, _, _, _, err := l.buildCycleBrief(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.calls != 1 {
		t.Fatalf("calls = %d, want 1 (still inside the backoff)", m.calls)
	}
}

func TestCycleStampsWaitingStateDuringBacklogOutage(t *testing.T) {
	m := &fakeMise{err: errTransientGitHub}
	l, _ := backlogRetryLoop(t, m)
	l.deps.StatusFile = filepath.Join(t.TempDir(), "status.json")

	if err := l.Cycle(context.Background()); err != nil {
		t.Fatalf("Cycle error = %v, want the outage reported as a waiting state", err)
	}
	if len(l.lastStatus.Warnings) != 1 || !strings.Contains(l.lastStatus.Warnings[0], "backlog sync failing") {
		t.Fatalf("status warnings = %q, want the waiting state stamped", l.lastStatus.Warnings)
	}
}

func TestBuildCycleBriefStopsOnPersistentBacklogError(t *testing.T) {
	m := &fakeMise{err: errTransientGitHub}
	l, clock := backlogRetryLoop(t, m)

	_, _, elapsed, err := cycleUntilDone(t, l, clock)
	if err == nil || !strings.Contains(err.Error(), "still failing after") {
		t.Fatalf("error = %v, want a persistent-failure message naming the attempts", err)
	}
	// Default window is five minutes, counted from the first failure.
	if elapsed < 5*time.Minute || elapsed > 5*time.Minute+time.Second {
		t.Fatalf("elapsed = %s, want the 5m default window", elapsed)
	}
}

func TestBuildCycleBriefDoesNotRetryHardBacklogError(t *testing.T) {
	m := &fakeMise{err: errors.New("run adapter backlog.sync: gh: HTTP 401: Bad credentials: exit status 1")}
	l, _ := backlogRetryLoop(t, m)

	if _, _, _, _, err := l.buildCycleBrief(context.Background()); err == nil {
		t.Fatal("buildCycleBrief error = nil, want the auth failure surfaced")
	}
	if m.calls != 1 {
		t.Fatalf("calls = %d, want 1 (auth failure is not transient)", m.calls)
	}
}

// A host sleep keeps the network down far longer than the old ~7s window
// (1s+2s+4s). The fake network fails for four virtual minutes, then recovers.
func TestBuildCycleBriefSurvivesLongNetworkOutage(t *testing.T) {
	var calls int
	l, clock := backlogRetryLoop(t, &fakeMise{})
	outage := 4 * time.Minute
	end := clock.now.Add(outage)
	l.deps.Mise = outageMise{down: func() bool { return clock.now.Before(end) }, calls: &calls}
	l.config.Backlog.RetryWindowSeconds = 600

	running, _, elapsed, err := cycleUntilDone(t, l, clock)
	if err != nil {
		t.Fatalf("buildCycleBrief error = %v, want the loop to ride out a %s outage", err, outage)
	}
	if !running || elapsed < outage {
		t.Fatalf("running=%v elapsed=%s, want running only after the outage ended", running, elapsed)
	}
}

func TestBuildCycleBriefHonorsConfiguredWindow(t *testing.T) {
	m := &fakeMise{err: errTransientGitHub}
	l, clock := backlogRetryLoop(t, m)
	l.config.Backlog.RetryWindowSeconds = 20

	_, _, elapsed, err := cycleUntilDone(t, l, clock)
	if err == nil {
		t.Fatal("error = nil, want a persistent-failure error after the window")
	}
	if elapsed < 20*time.Second || elapsed > 21*time.Second {
		t.Fatalf("elapsed = %s, want the configured 20s window", elapsed)
	}
}

// Time spent inside a slow sync call counts against the window.
func TestBuildCycleBriefWindowCountsTimeInsideSync(t *testing.T) {
	var calls int
	l, clock := backlogRetryLoop(t, &fakeMise{})
	l.deps.Mise = slowFailingMise{clock: clock, took: 15 * time.Second, calls: &calls}
	l.config.Backlog.RetryWindowSeconds = 20

	_, _, _, err := cycleUntilDone(t, l, clock)
	if err == nil {
		t.Fatal("error = nil, want the window spent")
	}
	if calls > 3 {
		t.Fatalf("calls = %d, want the 15s sync time to eat the 20s window", calls)
	}
}

func TestIsTransientBacklogErrorCoversDNSAndClientTimeouts(t *testing.T) {
	for _, text := range []string{
		`Get "https://api.github.com/repos": dial tcp: lookup api.github.com: no such host`,
		`getaddrinfo ENOTFOUND api.github.com`,
		`Get "https://api.github.com/issues": context deadline exceeded (Client.Timeout exceeded while awaiting headers)`,
	} {
		if !isTransientBacklogError(errors.New(text)) {
			t.Errorf("%q not classed transient", text)
		}
	}
}

// outageMise fails with a transient error while down() reports true.
type outageMise struct {
	down  func() bool
	calls *int
}

func (o outageMise) Build(context.Context, mise.ActiveSummary, []mise.HistoryItem) (mise.Brief, []string, bool, error) {
	*o.calls++
	if o.down() {
		return mise.Brief{}, nil, false, errTransientGitHub
	}
	return mise.Brief{}, nil, false, nil
}

// slowFailingMise advances the clock by took inside each failing sync call.
type slowFailingMise struct {
	clock *fakeClock
	took  time.Duration
	calls *int
}

func (s slowFailingMise) Build(context.Context, mise.ActiveSummary, []mise.HistoryItem) (mise.Brief, []string, bool, error) {
	*s.calls++
	s.clock.now = s.clock.now.Add(s.took)
	return mise.Brief{}, nil, false, errTransientGitHub
}

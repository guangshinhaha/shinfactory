package loop

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/guangshinhaha/shinfactory/mise"
	loopruntime "github.com/guangshinhaha/shinfactory/runtime"
)

// backlogRetryLoop wires a loop whose retry sleeps are virtual: they add to
// *slept instead of waiting, so a minutes-long outage runs instantly.
func backlogRetryLoop(t *testing.T, m *fakeMise) (*Loop, *time.Duration) {
	t.Helper()
	slept := new(time.Duration)
	return New(t.TempDir(), "noodle", localLandingConfig(), Dependencies{
		Runtimes:            map[string]loopruntime.Runtime{"process": newMockRuntime()},
		Worktree:            &fakeWorktree{},
		Adapter:             &fakeAdapterRunner{},
		Mise:                m,
		Monitor:             fakeMonitor{},
		Registry:            testLoopRegistry(),
		BacklogRetryBackoff: time.Second,
		BacklogRetrySleep: func(_ context.Context, d time.Duration) error {
			*slept += d
			return nil
		},
	}), slept
}

var errTransientGitHub = errors.New(`run adapter backlog.sync: gh: error connecting to api.github.com: dial tcp: lookup api.github.com: no such host: exit status 1`)

func TestBuildCycleBriefRetriesTransientBacklogError(t *testing.T) {
	m := &fakeMise{results: []fakeMiseResult{
		{err: errTransientGitHub},
		{err: errors.New("run adapter backlog.sync: gh: HTTP 502: Bad Gateway: exit status 1")},
		{brief: mise.Brief{}},
	}}
	l, _ := backlogRetryLoop(t, m)

	_, _, running, _, err := l.buildCycleBrief(context.Background())
	if err != nil {
		t.Fatalf("buildCycleBrief error = %v, want the transient errors retried", err)
	}
	if !running || m.calls != 3 {
		t.Fatalf("running=%v calls=%d, want running after 3 sync attempts", running, m.calls)
	}
}

func TestBuildCycleBriefStopsOnPersistentBacklogError(t *testing.T) {
	m := &fakeMise{err: errTransientGitHub}
	l, slept := backlogRetryLoop(t, m)

	_, _, _, _, err := l.buildCycleBrief(context.Background())
	if err == nil || !strings.Contains(err.Error(), "still failing after") {
		t.Fatalf("error = %v, want a persistent-failure message naming the attempts", err)
	}
	// Default window is five minutes; the loop must spend all of it waiting.
	if *slept != 5*time.Minute {
		t.Fatalf("slept = %s, want the full 5m default window", *slept)
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
	l, slept := backlogRetryLoop(t, &fakeMise{})
	outage := 4 * time.Minute
	l.deps.Mise = outageMise{down: func() bool { return *slept < outage }, calls: &calls}
	l.config.Backlog.RetryWindowSeconds = 600

	_, _, running, _, err := l.buildCycleBrief(context.Background())
	if err != nil {
		t.Fatalf("buildCycleBrief error = %v, want the loop to ride out a %s outage", err, outage)
	}
	if !running || *slept < outage {
		t.Fatalf("running=%v slept=%s, want running only after the outage ended", running, *slept)
	}
}

func TestBuildCycleBriefHonorsConfiguredWindow(t *testing.T) {
	m := &fakeMise{err: errTransientGitHub}
	l, slept := backlogRetryLoop(t, m)
	l.config.Backlog.RetryWindowSeconds = 20

	if _, _, _, _, err := l.buildCycleBrief(context.Background()); err == nil {
		t.Fatal("error = nil, want a persistent-failure error after the window")
	}
	if *slept != 20*time.Second {
		t.Fatalf("slept = %s, want the configured 20s window", *slept)
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

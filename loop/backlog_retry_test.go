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

func backlogRetryLoop(t *testing.T, m *fakeMise) *Loop {
	t.Helper()
	return New(t.TempDir(), "noodle", localLandingConfig(), Dependencies{
		Runtimes:            map[string]loopruntime.Runtime{"process": newMockRuntime()},
		Worktree:            &fakeWorktree{},
		Adapter:             &fakeAdapterRunner{},
		Mise:                m,
		Monitor:             fakeMonitor{},
		Registry:            testLoopRegistry(),
		BacklogRetryBackoff: time.Millisecond,
	})
}

var errTransientGitHub = errors.New(`run adapter backlog.sync: gh: error connecting to api.github.com: dial tcp: lookup api.github.com: no such host: exit status 1`)

func TestBuildCycleBriefRetriesTransientBacklogError(t *testing.T) {
	m := &fakeMise{results: []fakeMiseResult{
		{err: errTransientGitHub},
		{err: errors.New("run adapter backlog.sync: gh: HTTP 502: Bad Gateway: exit status 1")},
		{brief: mise.Brief{}},
	}}
	l := backlogRetryLoop(t, m)

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
	l := backlogRetryLoop(t, m)

	_, _, _, _, err := l.buildCycleBrief(context.Background())
	if err == nil || !strings.Contains(err.Error(), "still failing after") {
		t.Fatalf("error = %v, want a persistent-failure message naming the attempts", err)
	}
	if m.calls != backlogSyncAttempts {
		t.Fatalf("calls = %d, want %d attempts before stopping", m.calls, backlogSyncAttempts)
	}
}

func TestBuildCycleBriefDoesNotRetryHardBacklogError(t *testing.T) {
	m := &fakeMise{err: errors.New("run adapter backlog.sync: gh: HTTP 401: Bad credentials: exit status 1")}
	l := backlogRetryLoop(t, m)

	if _, _, _, _, err := l.buildCycleBrief(context.Background()); err == nil {
		t.Fatal("buildCycleBrief error = nil, want the auth failure surfaced")
	}
	if m.calls != 1 {
		t.Fatalf("calls = %d, want 1 (auth failure is not transient)", m.calls)
	}
}

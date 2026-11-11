package loop

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/guangshinhaha/shinfactory/internal/statusfile"
	"github.com/guangshinhaha/shinfactory/mise"
	loopruntime "github.com/guangshinhaha/shinfactory/runtime"
)

// networkErrorClasses are the failures from the issue's Done-when list, as the
// adapter, gh and git report them.
var networkErrorClasses = []struct {
	name string
	err  error
}{
	{"dns", errors.New("fatal: unable to access 'https://github.com/x/y.git/': Could not resolve host: github.com")},
	{"connection refused", &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}},
	{"connection reset", fmt.Errorf("read tcp 10.0.0.1:1->140.82.0.1:443: %w", syscall.ECONNRESET)},
	{"timeout", errors.New(`Post "https://api.github.com/graphql": dial tcp: i/o timeout`)},
	{"address exhaustion", errors.New(`Post https://api.github.com/graphql: read tcp 10.0.0.1:1->140.82.0.1:443: can't assign requested address`)},
	{"github 5xx", errors.New("gh: HTTP 502: Bad Gateway (https://api.github.com/graphql)")},
}

type networkWaitFixture struct {
	l      *Loop
	clock  *fakeClock
	wt     *fakeWorktree
	mise   *fakeMise
	status string
	logs   *bytes.Buffer
}

func newNetworkWaitFixture(t *testing.T) *networkWaitFixture {
	t.Helper()
	projectDir := t.TempDir()
	runtimeDir := filepath.Join(projectDir, ".noodle")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeOrdersAtomic(filepath.Join(runtimeDir, "orders.json"), OrdersFile{
		Orders: []Order{testOrder("42", "execute", "execute", "claude", "claude-opus-4-6")},
	}); err != nil {
		t.Fatal(err)
	}
	f := &networkWaitFixture{
		clock:  &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		wt:     &fakeWorktree{},
		mise:   &fakeMise{},
		status: filepath.Join(t.TempDir(), "status.json"),
		logs:   &bytes.Buffer{},
	}
	f.l = New(projectDir, "noodle", localLandingConfig(), Dependencies{
		Runtimes:   map[string]loopruntime.Runtime{"process": newMockRuntime()},
		Worktree:   f.wt,
		Adapter:    &fakeAdapterRunner{},
		Mise:       f.mise,
		Monitor:    fakeMonitor{},
		Registry:   testLoopRegistry(),
		Now:        f.clock.Now,
		StatusFile: f.status,
		Logger:     slog.New(slog.NewTextHandler(f.logs, nil)),
	})
	return f
}

func (f *networkWaitFixture) loopState(t *testing.T) string {
	t.Helper()
	s, err := statusfile.Read(f.status)
	if err != nil {
		t.Fatal(err)
	}
	return s.LoopState
}

func (f *networkWaitFixture) cycle(t *testing.T) {
	t.Helper()
	if err := f.l.Cycle(context.Background()); err != nil {
		t.Fatalf("Cycle error = %v, want a network error to leave the loop running", err)
	}
}

// outageInjectors fail one cycle step with err while down() is true.
var outageInjectors = []struct {
	name   string
	inject func(f *networkWaitFixture, err error, down func() bool)
}{
	{"backlog sync", func(f *networkWaitFixture, err error, down func() bool) {
		f.l.deps.Mise = outageMiseWith{down: down, err: err}
	}},
	{"worktree creation", func(f *networkWaitFixture, err error, down func() bool) {
		f.wt.createErrFn = func() error {
			if down() {
				return fmt.Errorf("resolve origin default branch: %w", err)
			}
			return nil
		}
	}},
}

func TestCycleWaitsOutNetworkErrorsAndResumes(t *testing.T) {
	for _, inj := range outageInjectors {
		for _, class := range networkErrorClasses {
			t.Run(inj.name+"/"+class.name, func(t *testing.T) {
				f := newNetworkWaitFixture(t)
				end := f.clock.now.Add(12 * time.Minute) // longer than the old 5m window
				inj.inject(f, class.err, func() bool { return f.clock.now.Before(end) })

				for f.clock.now.Before(end) {
					f.cycle(t)
					if got := f.loopState(t); got != "waiting-network" {
						t.Fatalf("at %s loop state = %q, want waiting-network", f.clock.now.Sub(end)+12*time.Minute, got)
					}
					f.clock.now = f.clock.now.Add(time.Second)
				}
				// Network is back; the next due attempt must resume on its own.
				for i := 0; i < 400 && f.loopState(t) == "waiting-network"; i++ {
					f.cycle(t)
					f.clock.now = f.clock.now.Add(time.Second)
				}
				if got := f.loopState(t); got != "running" {
					t.Fatalf("loop state after recovery = %q, want running", got)
				}
				if f.l.outage != nil {
					t.Fatal("outage state kept after recovery")
				}
			})
		}
	}
}

func TestNetworkOutageBacksOffToFiveMinutesAndWarnsOncePerOutage(t *testing.T) {
	f := newNetworkWaitFixture(t)
	end := f.clock.now.Add(time.Hour)
	f.l.deps.Mise = outageMiseWith{down: func() bool { return f.clock.now.Before(end) }, err: networkErrorClasses[0].err}

	attempts := 0
	var last time.Time
	var maxGap time.Duration
	for f.clock.now.Before(end) {
		beforeAttempts := 0
		if f.l.outage != nil {
			beforeAttempts = f.l.outage.attempts
		}
		f.cycle(t)
		if o := f.l.outage; o != nil && o.attempts != beforeAttempts {
			attempts++
			if !last.IsZero() && f.clock.now.Sub(last) > maxGap {
				maxGap = f.clock.now.Sub(last)
			}
			last = f.clock.now
		}
		f.clock.now = f.clock.now.Add(time.Second)
	}
	if maxGap > 5*time.Minute+time.Second || maxGap < 4*time.Minute {
		t.Fatalf("longest gap between attempts = %s, want backoff capped at 5m", maxGap)
	}
	if attempts > 30 {
		t.Fatalf("%d attempts in an hour, want capped backoff", attempts)
	}
	if got := strings.Count(f.logs.String(), "level=WARN"); got != 1 {
		t.Fatalf("%d warnings for one outage, want 1:\n%s", got, f.logs.String())
	}
}

func TestNonNetworkErrorsStillEndTheCycle(t *testing.T) {
	f := newNetworkWaitFixture(t)
	f.wt.createErr = errors.New("fatal: not a git repository")
	if err := f.l.Cycle(context.Background()); err == nil {
		t.Fatal("Cycle error = nil, want a non-network failure to stay fatal")
	}
}

func TestIsNetworkErrorClassifiesStructuredAndTextErrors(t *testing.T) {
	for _, class := range networkErrorClasses {
		if !isNetworkError(class.err) {
			t.Errorf("%s not classed as a network error: %v", class.name, class.err)
		}
	}
	for _, text := range []string{"gh: HTTP 401: Bad credentials", "fatal: not a git repository", "permission denied"} {
		if isNetworkError(errors.New(text)) {
			t.Errorf("%q classed as a network error", text)
		}
	}
}

// outageMiseWith fails with err while down() is true.
type outageMiseWith struct {
	down func() bool
	err  error
}

func (o outageMiseWith) Build(context.Context, mise.ActiveSummary, []mise.HistoryItem) (mise.Brief, []string, bool, error) {
	if o.down() {
		return mise.Brief{}, nil, false, o.err
	}
	return mise.Brief{}, nil, false, nil
}

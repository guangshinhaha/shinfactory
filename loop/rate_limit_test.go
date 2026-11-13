package loop

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// installFakeGh puts a `gh` shell script first on PATH. The script body gets
// $DIR, a scratch directory it may keep counters in.
func installFakeGh(t *testing.T, body string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake gh is a POSIX shell script")
	}
	bin := t.TempDir()
	script := "#!/bin/sh\nDIR='" + t.TempDir() + "'\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// rateLimitJSON is `gh api rate_limit` output with the GraphQL window spent.
func rateLimitJSON(coreReset, graphqlReset time.Time, graphqlRemaining int) string {
	return fmt.Sprintf(`{"resources":{`+
		`"core":{"limit":5000,"used":10,"remaining":4990,"reset":%d},`+
		`"graphql":{"limit":5000,"used":%d,"remaining":%d,"reset":%d}}}`,
		coreReset.Unix(), 5000-graphqlRemaining, graphqlRemaining, graphqlReset.Unix())
}

// rateLimitErrors are the failures from the issue's Done-when list, as gh and
// the adapter report them. wait is the delay the error itself names (0 = the
// reset comes from `gh api rate_limit`).
var rateLimitErrors = []struct {
	name string
	err  error
	wait time.Duration
}{
	{"graphql primary", errors.New(`run adapter backlog.sync command "defaults/adapters/github/backlog-sync": GraphQL: API rate limit already exceeded for user ID 12345. (): exit status 1`), 0},
	{"graphql primary exceeded", errors.New(`run adapter backlog.sync command "x": GraphQL: API rate limit exceeded for user ID 12345.: exit status 1`), 0},
	{"rest primary 403", errors.New("gh: API rate limit exceeded for user ID 12345. If you reach out to GitHub Support for help, please include the request ID ABCD. (HTTP 403)"), 0},
	{"rest secondary 403 with retry-after", errors.New("gh: You have exceeded a secondary rate limit. Please wait a few minutes before you try again. (HTTP 403)\nretry-after: 90"), 90 * time.Second},
	{"rest 429 with retry-after", errors.New("gh: HTTP 429: Too Many Requests (https://api.github.com/repos/x/y/pulls)\nRetry-After: 45"), 45 * time.Second},
}

func TestCycleWaitsOutGitHubRateLimitUntilReset(t *testing.T) {
	for _, tc := range rateLimitErrors {
		t.Run(tc.name, func(t *testing.T) {
			f := newNetworkWaitFixture(t)
			reset := f.clock.now.Add(20 * time.Minute)
			installFakeGh(t, "cat <<'EOF'\n"+rateLimitJSON(f.clock.now.Add(time.Hour), reset, 0)+"\nEOF")
			f.mise.err = tc.err
			until := reset
			if tc.wait > 0 {
				until = f.clock.now.Add(tc.wait)
			}

			f.cycle(t)
			if got := f.loopState(t); got != "waiting-network" {
				t.Fatalf("loop state = %q, want waiting-network (the #162 wait state)", got)
			}
			for f.clock.now.Add(time.Second).Before(until) {
				f.clock.now = f.clock.now.Add(time.Second)
				f.cycle(t)
			}
			if f.mise.calls != 1 {
				t.Fatalf("backlog synced %d times before the limit reset, want 1 (no retry until reset)", f.mise.calls)
			}

			f.mise.err = nil
			f.clock.now = until.Add(2 * time.Second)
			f.cycle(t)
			if f.mise.calls != 2 {
				t.Fatalf("backlog synced %d times after the reset, want a retry (2)", f.mise.calls)
			}
			if got := f.loopState(t); got != "running" {
				t.Fatalf("loop state after the reset = %q, want running", got)
			}
		})
	}
}

func TestCycleRateLimitWithoutResetInfoStillWaits(t *testing.T) {
	f := newNetworkWaitFixture(t)
	installFakeGh(t, "echo 'gh: not signed in' >&2; exit 1")
	f.mise.err = rateLimitErrors[0].err

	f.cycle(t)
	if got := f.loopState(t); got != "waiting-network" {
		t.Fatalf("loop state = %q, want waiting-network when the reset lookup fails", got)
	}
	f.clock.now = f.clock.now.Add(30 * time.Second)
	f.cycle(t)
	if f.mise.calls != 1 {
		t.Fatalf("backlog synced %d times within 30s of a rate limit, want 1", f.mise.calls)
	}
	f.clock.now = f.clock.now.Add(40 * time.Second)
	f.cycle(t)
	if f.mise.calls != 2 {
		t.Fatalf("backlog synced %d times after the fallback minute, want a retry (2)", f.mise.calls)
	}
}

func TestRateLimitErrorsAreNotCredentialOrMissingRepoErrors(t *testing.T) {
	f := newNetworkWaitFixture(t)
	f.mise.err = errors.New("gh: Bad credentials (HTTP 401)")
	if err := f.l.Cycle(t.Context()); err == nil {
		t.Fatal("Cycle with a 401 returned nil, want it to stay fatal")
	}
}

func idleFixture(t *testing.T) *networkWaitFixture {
	t.Helper()
	f := newNetworkWaitFixture(t)
	if err := writeOrdersAtomic(filepath.Join(f.l.runtimeDir, "orders.json"), OrdersFile{}); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestIdleLoopSyncsBacklogLessOften(t *testing.T) {
	f := idleFixture(t)
	for i := 0; i < 60; i++ { // one minute of one-second cycles
		f.cycle(t)
		f.clock.now = f.clock.now.Add(time.Second)
	}
	if f.mise.calls > 2 {
		t.Fatalf("idle loop synced the backlog %d times in a minute, want at most 2", f.mise.calls)
	}
	before := f.mise.calls
	for i := 0; i < 600; i++ { // ten more minutes: it still looks
		f.cycle(t)
		f.clock.now = f.clock.now.Add(time.Second)
	}
	if f.mise.calls == before {
		t.Fatal("idle loop never synced again in ten minutes, want a slow poll")
	}
}

// invalidatingMise counts how often the loop drops the builder's cached sync.
type invalidatingMise struct {
	fakeMise
	invalidated int
}

func (m *invalidatingMise) InvalidateBacklog() { m.invalidated++ }

func TestControlActionsInvalidateTheCachedSync(t *testing.T) {
	f := newNetworkWaitFixture(t)
	im := &invalidatingMise{}
	f.l.deps.Mise = im
	f.cycle(t)
	f.clock.now = f.clock.now.Add(time.Second)
	if err := f.l.controlEnqueue(ControlCommand{OrderID: "43", Prompt: "do it"}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if im.invalidated != 1 {
		t.Fatalf("enqueue invalidated the cached sync %d times, want 1", im.invalidated)
	}
	if err := f.l.mutateOrdersState(func(o *OrdersFile) (bool, error) {
		o.Orders[0].Status = OrderStatusFailed
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.l.controlRequeue("42"); err != nil {
		t.Fatalf("requeue: %v", err)
	}
	if im.invalidated != 2 {
		t.Fatalf("requeue invalidated the cached sync %d times, want 2 in total", im.invalidated)
	}
}

func TestControlActionEndsTheIdleSkip(t *testing.T) {
	f := idleFixture(t)
	f.cycle(t) // an empty sync: the loop is idle
	f.clock.now = f.clock.now.Add(time.Second)
	f.cycle(t)
	if f.mise.calls != 1 {
		t.Fatalf("idle loop synced %d times, want the second cycle to skip", f.mise.calls)
	}
	f.l.requestSync()
	f.clock.now = f.clock.now.Add(time.Second)
	f.cycle(t)
	if f.mise.calls != 2 {
		t.Fatalf("synced %d times after a control action, want a sync on the next cycle", f.mise.calls)
	}
}

func TestRateLimitResumeKeepsANearResetShort(t *testing.T) {
	f := newNetworkWaitFixture(t)
	now := f.clock.now
	soon := now.Add(5 * time.Second)
	installFakeGh(t, "cat <<'EOF'\n"+rateLimitJSON(soon, soon, 0)+"\nEOF")
	if got := f.l.rateLimitResume(t.Context(), rateLimitErrors[0].err); got.After(now.Add(7 * time.Second)) {
		t.Fatalf("a reset 5s away resumes at +%s, want about 6s", got.Sub(now))
	}
}

func TestRateLimitResumeBacksOffWhenResetAlreadyPassed(t *testing.T) {
	f := newNetworkWaitFixture(t)
	now := f.clock.now
	stale := now.Add(-time.Minute)
	installFakeGh(t, "cat <<'EOF'\n"+rateLimitJSON(stale, stale, 0)+"\nEOF")
	for _, c := range rateLimitErrors {
		if got := f.l.rateLimitResume(t.Context(), c.err); got.Before(now.Add(30 * time.Second)) {
			t.Fatalf("stale reset resumes at +%s, want at least 30s", got.Sub(now))
		}
	}
}

func TestBacklogSyncLogsGraphQLCostInDebug(t *testing.T) {
	f := newNetworkWaitFixture(t)
	installFakeGh(t, `n=$(cat "$DIR/n" 2>/dev/null || echo 0); n=$((n+1)); echo $n >"$DIR/n"; used=$((n*7))
echo "{\"resources\":{\"core\":{\"limit\":5000,\"used\":1,\"remaining\":4999,\"reset\":9999999999},\"graphql\":{\"limit\":5000,\"used\":$used,\"remaining\":$((5000-used)),\"reset\":9999999999}}}"`)
	var logs bytes.Buffer
	f.l.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	f.l.deps.Logger = f.l.logger

	f.cycle(t)
	out := logs.String()
	if !strings.Contains(out, "backlog sync graphql cost") || !strings.Contains(out, "cost=7") {
		t.Fatalf("debug log has no GraphQL cost line with cost=7:\n%s", out)
	}
}

func TestBacklogSyncSkipsCostLookupOutsideDebug(t *testing.T) {
	f := newNetworkWaitFixture(t)
	marker := filepath.Join(t.TempDir(), "called")
	installFakeGh(t, "touch '"+marker+"'; exit 1")
	f.cycle(t)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("gh ran during a normal info-level sync, want the cost lookup only in debug")
	}
}

func TestIdleSkipDoesNotHideDueNetworkRetry(t *testing.T) {
	f := idleFixture(t)
	f.cycle(t) // an empty sync succeeds: the loop is idle
	f.clock.now = f.clock.now.Add(10 * time.Second)
	f.mise.err = networkErrorClasses[0].err
	f.l.lastSyncAt = time.Time{} // the next look is due, and it fails on the network
	f.cycle(t)
	if f.l.outage == nil {
		t.Fatal("network failure opened no outage")
	}
	f.l.lastSyncAt = f.clock.now // a stale idle marker must not mask the due retry
	f.l.lastSyncEmpty = true
	f.mise.err = nil
	calls := f.mise.calls
	f.clock.now = f.clock.now.Add(2 * time.Second)
	f.cycle(t)
	if f.mise.calls == calls {
		t.Fatal("idle skip swallowed the due retry; outage never re-probed")
	}
	if f.l.outage != nil {
		t.Fatal("outage still open after the retry succeeded")
	}
}

func TestRateLimitDuringNetworkBackoffExtendsTheWait(t *testing.T) {
	f := newNetworkWaitFixture(t)
	reset := f.clock.now.Add(10 * time.Minute)
	installFakeGh(t, "cat <<'EOF'\n"+rateLimitJSON(reset.Add(time.Hour), reset, 0)+"\nEOF")
	f.mise.err = networkErrorClasses[0].err
	f.cycle(t) // network outage, 1s backoff
	f.mise.err = rateLimitErrors[0].err
	f.clock.now = f.clock.now.Add(time.Second)
	f.cycle(t) // the retry hits the limit
	f.clock.now = f.clock.now.Add(5 * time.Minute)
	f.cycle(t)
	if f.mise.calls != 2 {
		t.Fatalf("backlog synced %d times while rate limited, want 2 (none until the reset)", f.mise.calls)
	}
}

func TestRateLimitResumeUsesTheFailedWindowAndCapsRetryAfter(t *testing.T) {
	f := newNetworkWaitFixture(t)
	now := f.clock.now
	gqlReset, coreReset := now.Add(10*time.Minute), now.Add(55*time.Minute)
	installFakeGh(t, "cat <<'EOF'\n"+fmt.Sprintf(`{"resources":{"core":{"used":5000,"remaining":0,"reset":%d},"graphql":{"used":5000,"remaining":0,"reset":%d}}}`, coreReset.Unix(), gqlReset.Unix())+"\nEOF")

	if got := f.l.rateLimitResume(t.Context(), rateLimitErrors[0].err); got.After(gqlReset.Add(2 * time.Second)) {
		t.Fatalf("GraphQL limit waits until %s, want the GraphQL reset %s, not the later REST one", got, gqlReset)
	}
	if got := f.l.rateLimitResume(t.Context(), rateLimitErrors[2].err); got.Before(coreReset) {
		t.Fatalf("REST limit waits until %s, want the REST reset %s", got, coreReset)
	}
	huge := errors.New("gh: HTTP 429: Too Many Requests\nretry-after: 86400")
	if got := f.l.rateLimitResume(t.Context(), huge); got.After(now.Add(time.Hour + 2*time.Second)) {
		t.Fatalf("retry-after 86400 waits until %s, want it capped at one hour", got)
	}
}

// A backlog stop or done the loop makes itself must drop the cached sync, or the
// scheduler can re-propose a stopped or finished issue from a stale backlog
// (the #175 failure).
func TestBacklogStopInvalidatesTheCachedSync(t *testing.T) {
	f := newNetworkWaitFixture(t)
	im := &invalidatingMise{}
	f.l.deps.Mise = im
	f.l.stopForPerson(t.Context(), "42", "needs a person", "")
	if im.invalidated != 1 {
		t.Fatalf("stop invalidated the cached sync %d times, want 1", im.invalidated)
	}
}

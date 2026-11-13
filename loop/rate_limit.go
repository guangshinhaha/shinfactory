package loop

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	// rateLimitFallbackWait is how long the loop waits when a rate-limit error
	// names no reset and `gh api rate_limit` cannot say. GitHub asks secondary
	// limit clients to wait at least a minute.
	rateLimitFallbackWait = time.Minute
	// rateLimitSlack is added to a reset time so the retry lands after it.
	rateLimitSlack = time.Second
	// rateLimitLookupTimeout bounds one `gh api rate_limit` call.
	rateLimitLookupTimeout = 15 * time.Second
	// maxRateLimitWait caps a retry-after: rate-limit windows last an hour.
	maxRateLimitWait = time.Hour
	// idleSyncInterval spaces backlog syncs while there is nothing to do: no
	// ready issues, no orders and no active cooks. Every sync costs GraphQL
	// points from a limit that all loops, workers and the coordinator share.
	idleSyncInterval = 5 * time.Minute
	// minRateLimitWait floors the wait after a rate limit: a reset time already
	// in the past (a stale or just-rolled window) must not retry after a second.
	minRateLimitWait = 30 * time.Second
)

// rateLimitMarkers are lowercase fragments of GitHub rate-limit failures: the
// GraphQL "API rate limit exceeded" message, REST 403 primary and secondary
// limits, and 429.
var rateLimitMarkers = []string{
	"api rate limit", "secondary rate limit", "abuse detection",
	"http 429", "too many requests",
}

var retryAfterPattern = regexp.MustCompile(`(?i)retry[- ]after:?\s*(\d+)`)

// isRateLimitError reports whether err is a GitHub primary or secondary rate
// limit. It ends when the window resets, so the loop waits instead of exiting.
func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	for _, marker := range rateLimitMarkers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// isWaitableError is a failure the loop waits out rather than ends on.
func isWaitableError(err error) bool {
	return isNetworkError(err) || isRateLimitError(err)
}

// retryAfter reads a retry-after delay out of err's text.
func retryAfter(err error) (time.Duration, bool) {
	m := retryAfterPattern.FindStringSubmatch(err.Error())
	if m == nil {
		return 0, false
	}
	secs, convErr := strconv.Atoi(m[1])
	if convErr != nil || secs < 0 {
		return 0, false
	}
	return time.Duration(secs) * time.Second, true
}

type rateWindow struct {
	Used      int   `json:"used"`
	Remaining int   `json:"remaining"`
	Reset     int64 `json:"reset"`
}

type rateLimits struct {
	Resources struct {
		Core    rateWindow `json:"core"`
		GraphQL rateWindow `json:"graphql"`
	} `json:"resources"`
}

// fetchRateLimits runs `gh api rate_limit`, which GitHub does not count
// against any limit.
func fetchRateLimits(ctx context.Context, dir string) (rateLimits, error) {
	ctx, cancel := context.WithTimeout(ctx, rateLimitLookupTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "api", "rate_limit")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return rateLimits{}, err
	}
	var rl rateLimits
	err = json.Unmarshal(out, &rl)
	return rl, err
}

// rateLimitResume is when to retry after a rate-limit error: the error's
// retry-after, else the reset of the exhausted window from `gh api
// rate_limit`, else a fixed minute.
func (l *Loop) rateLimitResume(ctx context.Context, err error) time.Time {
	now := l.deps.Now()
	if d, ok := retryAfter(err); ok {
		return now.Add(min(d, maxRateLimitWait) + rateLimitSlack)
	}
	rl, lookupErr := fetchRateLimits(ctx, l.projectDir)
	if lookupErr != nil {
		if l.logger != nil {
			l.logger.Warn("rate limit reset unknown; waiting a minute", "error", lookupErr)
		}
		return now.Add(rateLimitFallbackWait)
	}
	// The window that failed: GraphQL when the error says so, else REST. When
	// that one is not empty, the other if it is.
	windows := []rateWindow{rl.Resources.Core, rl.Resources.GraphQL}
	if strings.Contains(strings.ToLower(err.Error()), "graphql") {
		windows = []rateWindow{rl.Resources.GraphQL, rl.Resources.Core}
	}
	var reset time.Time
	for _, w := range windows {
		if w.Remaining == 0 && w.Reset > 0 {
			reset = time.Unix(w.Reset, 0)
			break
		}
	}
	if reset.IsZero() { // no window is empty: a secondary limit with no header
		return now.Add(rateLimitFallbackWait)
	}
	if !reset.After(now) { // the window already rolled over: don't retry after a second
		return now.Add(minRateLimitWait)
	}
	return reset.Add(rateLimitSlack)
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// syncIdle reports whether the backlog sync may be skipped because the loop
// has nothing to do: the last sync found no ready issues and nothing is
// running, queued or waiting to be promoted. It skips for idleSyncInterval.
func (l *Loop) syncIdle() bool {
	if l.outage != nil || l.syncRequested || l.lastSyncAt.IsZero() || !l.lastSyncEmpty ||
		l.deps.Now().Sub(l.lastSyncAt) >= idleSyncInterval ||
		l.activeSummary.Total > 0 || len(l.cooks.pendingReview) > 0 {
		return false
	}
	if _, err := os.Stat(l.deps.OrdersNextFile); err == nil {
		return false
	}
	orders, err := l.currentOrders()
	return err == nil && len(orders.Orders) == 0
}

// requestSync makes the next cycle read the backlog again: a control action
// (enqueue, requeue) changed what it should show. It ends the idle skip and
// drops the builder's cached sync, which a busy loop reuses for the configured
// interval (backlog.sync_interval_seconds).
func (l *Loop) requestSync() {
	l.syncRequested = true
	if inv, ok := l.deps.Mise.(backlogInvalidator); ok {
		inv.InvalidateBacklog()
	}
}

// graphQLUsed reads the GraphQL points used in the current window, for the
// debug cost line. ok is false when the lookup fails.
func (l *Loop) graphQLUsed(ctx context.Context) (rateWindow, bool) {
	rl, err := fetchRateLimits(ctx, l.projectDir)
	if err != nil {
		return rateWindow{}, false
	}
	return rl.Resources.GraphQL, true
}

// logSyncCost logs the GraphQL points one backlog sync used, from the window
// counter before and after. The counter is the whole user's, so a concurrent
// worker's calls land in the figure too; it is an upper bound on the sync.
func (l *Loop) logSyncCost(before, after rateWindow) {
	l.logger.Debug("backlog sync graphql cost",
		"cost", after.Used-before.Used,
		"window_used", after.Used,
		"remaining", after.Remaining,
		"resets_at", time.Unix(after.Reset, 0).UTC().Format(time.RFC3339),
		"includes_other_actors", true)
}

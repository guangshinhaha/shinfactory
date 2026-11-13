package loop

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/guangshinhaha/shinfactory/internal/ingest"
)

const (
	initialNetworkBackoff = time.Second
	// maxNetworkBackoff caps the doubling: a long outage (a Mac asleep, a
	// provider incident) is probed every five minutes until it ends.
	maxNetworkBackoff = 5 * time.Minute

	// loopStateWaitingNetwork is the status loop state while an outage is open.
	loopStateWaitingNetwork = "waiting-network"
)

// networkMarkers are lowercase fragments of network and 5xx failures that reach
// the loop as text: gh, curl and git report them on stderr.
var networkMarkers = []string{
	"no such host", "temporary failure in name resolution", "could not resolve host",
	"connection refused", "connection reset", "i/o timeout", "operation timed out",
	"connection timed out", "tls handshake", "network is unreachable", "no route to host",
	"error connecting to", "can't assign requested address",
	"failed to connect to", "the remote end hung up",
	"http 500", "http 502", "http 503", "http 504",
	"500 internal server error", "bad gateway", "service unavailable", "gateway timeout",
	"enotfound", "client.timeout exceeded", "timeout awaiting response headers",
}

// isNetworkError reports whether err is a network-class failure (DNS, refused or
// reset connection, timeout, address exhaustion, GitHub 5xx) that ends when the
// network does. Anything else, such as bad credentials or a missing repo, is not.
func isNetworkError(err error) bool {
	if err == nil {
		return false
	}
	// A bare deadline (context or file) is not evidence about the network.
	var netErr net.Error
	if errors.As(err, &netErr) &&
		!errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	for _, errno := range []syscall.Errno{
		syscall.ECONNREFUSED, syscall.ECONNRESET, syscall.EADDRNOTAVAIL,
		syscall.ETIMEDOUT, syscall.ENETUNREACH, syscall.EHOSTUNREACH,
	} {
		if errors.Is(err, errno) {
			return true
		}
	}
	text := strings.ToLower(err.Error())
	for _, marker := range networkMarkers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// networkOutage tracks one network outage across cycles. The loop never sleeps
// inside a cycle: it records when the next try is due and returns, so control
// commands and status stamps keep running while the network is down.
type networkOutage struct {
	since       time.Time
	nextAttempt time.Time
	attempts    int
	backoff     time.Duration
	rateLimited bool // waiting for a GitHub rate-limit reset, not a network backoff
}

func (o *networkOutage) warning(now time.Time) string {
	if o.rateLimited {
		return fmt.Sprintf("GitHub rate limit reached (attempt %d); retrying in %s",
			o.attempts, o.nextAttempt.Sub(now).Round(time.Second))
	}
	return fmt.Sprintf("network unreachable (attempt %d); retrying in %s",
		o.attempts, o.nextAttempt.Sub(now).Round(time.Second))
}

// outageDue reports whether a network-dependent step may run: false while an
// outage is open and its backoff has not passed.
func (l *Loop) outageDue() bool {
	return l.outage == nil || !l.deps.Now().Before(l.outage.nextAttempt)
}

// recordNetworkFailure opens an outage, or extends the open one, when err is
// network-class or a GitHub rate limit. A network error backs off with a longer
// delay each time; a rate limit waits until the reset. It logs one warning per
// outage and returns the status warning to show; ok is false for any other
// error.
func (l *Loop) recordNetworkFailure(ctx context.Context, step string, err error) (warning string, ok bool) {
	limited := isRateLimitError(err)
	if !limited && !isNetworkError(err) {
		return "", false
	}
	now := l.deps.Now()
	o := l.outage
	if o == nil {
		o = &networkOutage{since: now, backoff: initialNetworkBackoff}
		l.outage = o
		if l.logger != nil && !limited {
			l.logger.Warn("network error; waiting for the network and retrying with backoff",
				"step", step, "cause", err)
		}
	}
	due := o.attempts == 0 || !now.Before(o.nextAttempt)
	switch {
	case limited && (due || !o.rateLimited):
		// A rate limit seen mid-backoff still extends the wait: retrying after
		// a second would hit the limit again.
		if due {
			o.attempts++
		}
		o.rateLimited = true
		o.nextAttempt = maxTime(o.nextAttempt, l.rateLimitResume(ctx, err))
		if l.logger != nil {
			l.logger.Warn("GitHub rate limit; waiting for the reset",
				"step", step, "retry_at", o.nextAttempt.UTC().Format(time.RFC3339))
		}
	case !limited && due:
		o.attempts++
		o.rateLimited = false
		o.nextAttempt = now.Add(o.backoff)
		o.backoff = min(o.backoff*2, maxNetworkBackoff)
	}
	return o.warning(now), true
}

// awaitingNetworkPrefix marks a stage parked because a PR call (push, merge,
// verdict post, rebase fetch) hit a network error after the cook finished.
// retryNetworkParked re-runs the stage's completion once the backoff passes.
const awaitingNetworkPrefix = "waiting for the network"

// failStageOrWaitNetwork fails the stage, unless err is network-class: then the
// stage parks (the order, its worktree and its session are kept) and the loop
// retries its completion after the outage backoff. reason names the failed step.
func (l *Loop) failStageOrWaitNetwork(ctx context.Context, cook *cookHandle, reason string, err error) error {
	if _, ok := l.recordNetworkFailure(ctx, cook.orderID+": "+reason, err); !ok {
		return l.failStage(ctx, cook, reason+": "+err.Error())
	}
	if l.networkReparked == nil {
		l.networkReparked = map[string]bool{}
	}
	l.networkReparked[cook.orderID] = true
	return l.parkPendingReview(cook, awaitingNetworkPrefix+" ("+reason+"): "+err.Error())
}

func (l *Loop) hasNetworkParked() bool {
	for _, p := range l.cooks.pendingReview {
		if strings.HasPrefix(p.reason, awaitingNetworkPrefix) {
			return true
		}
	}
	return false
}

// retryNetworkParked re-runs the completion of each stage parked for the
// network, once the outage backoff has passed. A call that fails on the network
// again re-parks the stage (refreshing the outage); any other result unparks it.
func (l *Loop) retryNetworkParked(ctx context.Context) error {
	if !l.config.Landing.PR() || !l.outageDue() {
		return nil
	}
	var waiting []*pendingReviewCook
	for _, p := range l.cooks.pendingReview {
		if strings.HasPrefix(p.reason, awaitingNetworkPrefix) {
			waiting = append(waiting, p)
		}
	}
	slices.SortFunc(waiting, func(a, b *pendingReviewCook) int { return strings.Compare(a.orderID, b.orderID) })
	for _, pending := range waiting {
		cook := &cookHandle{
			cookIdentity: pending.cookIdentity,
			orderStatus:  OrderStatusActive,
			worktreeName: pending.worktreeName,
			worktreePath: pending.worktreePath,
			session:      &adoptedSession{id: pending.sessionID, status: "completed"},
		}
		delete(l.networkReparked, cook.orderID)
		if err := l.completeAfterEvidence(ctx, cook); err != nil {
			return err
		}
		if l.networkReparked[cook.orderID] {
			continue // still down: parked again with a longer backoff
		}
		if cur, ok := l.cooks.pendingReview[cook.orderID]; ok && strings.HasPrefix(cur.reason, awaitingNetworkPrefix) {
			if err := l.emitEventChecked(ingest.EventStageReviewApproved, map[string]any{
				"order_id":    cook.orderID,
				"stage_index": cook.stageIndex,
				"reason":      "network is back; completion retried",
			}); err != nil {
				return err
			}
			if err := l.syncPendingReviewProjection(); err != nil {
				return err
			}
		}
	}
	return nil
}

// clearNetworkOutage ends the open outage once a cycle got through every
// network step.
func (l *Loop) clearNetworkOutage() {
	o := l.outage
	if o == nil || l.hasNetworkParked() {
		return
	}
	l.outage = nil
	if l.logger != nil {
		l.logger.Info("network is back; resuming", "outage", l.deps.Now().Sub(o.since).Round(time.Second).String(), "attempts", o.attempts)
	}
}

// stateLabel is the loop state as shown in status and snapshots: the lifecycle
// state, or waiting-network while a running loop waits out an outage.
func (l *Loop) stateLabel() string {
	if l.outage != nil && l.state == StateRunning {
		return loopStateWaitingNetwork
	}
	return string(l.state)
}

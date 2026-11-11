package loop

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
	"time"
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
	"unexpected eof", "error connecting to", "can't assign requested address",
	"broken pipe", "failed to connect to", "the remote end hung up", "rpc failed",
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
	var netErr net.Error
	if errors.As(err, &netErr) {
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
}

func (o *networkOutage) warning(now time.Time) string {
	return fmt.Sprintf("network unreachable (attempt %d); retrying in %s",
		o.attempts, o.nextAttempt.Sub(now).Round(time.Second))
}

// outageDue reports whether a network-dependent step may run: false while an
// outage is open and its backoff has not passed.
func (l *Loop) outageDue() bool {
	return l.outage == nil || !l.deps.Now().Before(l.outage.nextAttempt)
}

// recordNetworkFailure opens an outage, or extends the open one with a longer
// backoff, when err is network-class. It logs one warning per outage and
// returns the status warning to show; ok is false for any other error.
func (l *Loop) recordNetworkFailure(step string, err error) (warning string, ok bool) {
	if !isNetworkError(err) {
		return "", false
	}
	now := l.deps.Now()
	o := l.outage
	if o == nil {
		o = &networkOutage{since: now, backoff: initialNetworkBackoff}
		l.outage = o
		if l.logger != nil {
			l.logger.Warn("network error; waiting for the network and retrying with backoff",
				"step", step, "cause", err)
		}
	}
	if o.attempts == 0 || !now.Before(o.nextAttempt) {
		o.attempts++
		o.nextAttempt = now.Add(o.backoff)
		o.backoff = min(o.backoff*2, maxNetworkBackoff)
	}
	return o.warning(now), true
}

// clearNetworkOutage ends the open outage once a cycle got through every
// network step.
func (l *Loop) clearNetworkOutage() {
	o := l.outage
	if o == nil {
		return
	}
	l.outage = nil
	if l.logger != nil {
		l.logger.Info("network is back; resuming", "outage", l.deps.Now().Sub(o.since).Round(time.Second).String(), "attempts", o.attempts)
	}
}

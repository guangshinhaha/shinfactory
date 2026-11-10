package loop

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// A session that already finished has a result in flight. A watcher goroutine
// that is slow to queue it (a loaded CI runner) must not make the drain return
// early and the cycle miss the completion.
func TestDrainCompletionsWaitsForAFinishedSessionsResult(t *testing.T) {
	l := newStopEnv(t).loop

	done := make(chan struct{})
	close(done)
	l.watchedDone.Store("slow-session", (<-chan struct{})(done))
	l.watcherCount.Add(1)
	t.Cleanup(func() { l.watcherCount.Add(-1) })

	var queued atomic.Bool
	go func() {
		time.Sleep(50 * time.Millisecond) // far past the 2ms poll for a running session
		queued.Store(true)
		l.completionBuf.completions <- StageResult{IsBootstrap: true}
		l.watchedDone.Delete("slow-session")
	}()

	if err := l.drainCompletions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !queued.Load() {
		t.Fatal("drain returned before the finished session's result was queued")
	}
}

// A session still running has nothing in flight: the drain keeps its short poll.
func TestDrainCompletionsDoesNotWaitOnARunningSession(t *testing.T) {
	l := newStopEnv(t).loop
	l.watchedDone.Store("running-session", (<-chan struct{})(make(chan struct{})))
	l.watcherCount.Add(1)
	t.Cleanup(func() { l.watcherCount.Add(-1) })

	start := time.Now()
	if err := l.drainCompletions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("drain took %v with only a running session", elapsed)
	}
}

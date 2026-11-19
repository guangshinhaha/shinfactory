package loop

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

// An idle cycle must not write the files that trigger the next cycle. A cycle
// that rewrites orders.json or control.ndjson wakes the runtime watcher, which
// queues another cycle at once, so an idle loop never waits for its poll tick
// (#188: ~1.5 cores per idle loop).
func TestIdleLoopDoesNotTriggerItsOwnCycles(t *testing.T) {
	logger, _ := newTestLogger()
	tc := newTestLoop(t, logger)
	// A running loop always has an empty control queue file.
	if err := os.WriteFile(filepath.Join(tc.runtimeDir, "control.ndjson"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- tc.loop.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		// Shutdown waits for live sessions; let the fake ones end.
		tc.runtime.mu.Lock()
		for _, sess := range tc.runtime.sessions {
			sess.complete("failed")
		}
		tc.runtime.mu.Unlock()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("loop did not stop")
		}
	})

	// Let startup and the first cycles settle, then watch the idle loop.
	time.Sleep(1500 * time.Millisecond)
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()
	if err := watcher.Add(tc.runtimeDir); err != nil {
		t.Fatal(err)
	}
	var triggers atomic.Int32
	go func() {
		for ev := range watcher.Events {
			if shouldTriggerRuntimeCycle(ev.Name) {
				triggers.Add(1)
				t.Logf("idle loop wrote trigger file: %s %s", ev.Name, ev.Op)
			}
		}
	}()

	time.Sleep(3500 * time.Millisecond) // three poll ticks
	if got := triggers.Load(); got != 0 {
		t.Fatalf("idle loop wrote cycle-trigger files %d times in 3.5s, want 0", got)
	}
}

package linestatus

import (
	"os"
	"path/filepath"
	"time"

	"github.com/guangshinhaha/shinfactory/internal/lockfile"
)

// StaleAfter is how long a running loop may go without a state write, while
// orders run, before status calls it stale.
const StaleAfter = 10 * time.Minute

// ResolveLoopState turns the loop state the loop last wrote into what is true
// now. The files outlive the loop, so the lock decides first: a dead lock pid
// is "stopped". A live loop that says "running" yet has written no state for
// StaleAfter while orders run is "stale", with the age of its last write. A
// runtime with no lock file keeps the recorded state.
func ResolveLoopState(runtimeDir, raw string, ordersRunning bool, now time.Time) (state string, staleFor time.Duration) {
	if lockfile.Holder(filepath.Join(runtimeDir, "noodle.lock")) == lockfile.HolderDead {
		return "stopped", 0
	}
	if raw == "running" && ordersRunning {
		if age := now.Sub(lastStateWrite(runtimeDir)); age >= StaleAfter {
			return "stale", age.Round(time.Second)
		}
	}
	return raw, 0
}

// lastStateWrite is the newest modification time among the files the loop
// writes as it works. Zero when it has written none.
func lastStateWrite(runtimeDir string) time.Time {
	var newest time.Time
	for _, name := range []string{"status.json", "state.snapshot.json"} {
		if info, err := os.Stat(filepath.Join(runtimeDir, name)); err == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	return newest
}

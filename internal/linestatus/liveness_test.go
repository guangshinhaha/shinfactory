package linestatus

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/guangshinhaha/shinfactory/internal/statusfile"
)

// deadPID returns the pid of a process that has exited.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.ProcessState.Pid()
}

func writeLock(t *testing.T, dir string, pid int) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "noodle.lock"), []byte(fmt.Sprintf("%d\n", pid)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func ageWrites(t *testing.T, dir string, age time.Duration) {
	t.Helper()
	old := time.Now().Add(-age)
	for _, name := range []string{"status.json", "state.snapshot.json"} {
		if err := os.Chtimes(filepath.Join(dir, name), old, old); err != nil {
			t.Fatal(err)
		}
	}
}

func setLoopState(t *testing.T, dir, loopState string) {
	t.Helper()
	if err := statusfile.WriteAtomic(filepath.Join(dir, "status.json"), statusfile.Status{
		Active: []string{"o-run"}, LoopState: loopState, Mode: "auto", MaxConcurrency: 3,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestReadReportsDeadLockPidAsStopped(t *testing.T) {
	dir := writeRuntime(t)
	setLoopState(t, dir, "running")
	writeLock(t, dir, deadPID(t))
	rep, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if rep.LoopState != "stopped" {
		t.Fatalf("loop_state = %q, want stopped", rep.LoopState)
	}
}

func TestReadReportsQuietLiveLoopWithRunningOrderAsStale(t *testing.T) {
	dir := writeRuntime(t)
	setLoopState(t, dir, "running")
	writeLock(t, dir, os.Getpid())
	ageWrites(t, dir, 25*time.Minute)
	rep, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if rep.LoopState != "stale" {
		t.Fatalf("loop_state = %q, want stale", rep.LoopState)
	}
	age, err := time.ParseDuration(rep.StaleFor)
	if err != nil || age < 25*time.Minute || age > 26*time.Minute {
		t.Fatalf("stale_for = %q, want about 25m", rep.StaleFor)
	}
}

func TestReadReportsHealthyLoopAsRunning(t *testing.T) {
	dir := writeRuntime(t)
	setLoopState(t, dir, "running")
	writeLock(t, dir, os.Getpid())
	rep, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if rep.LoopState != "running" || rep.StaleFor != "" {
		t.Fatalf("loop_state = %q stale_for = %q, want running", rep.LoopState, rep.StaleFor)
	}
}

func TestQuietLiveLoopWithNoRunningOrderIsNotStale(t *testing.T) {
	dir := writeRuntime(t)
	setLoopState(t, dir, "running")
	writeLock(t, dir, os.Getpid())
	ageWrites(t, dir, time.Hour)
	if got, _ := ResolveLoopState(dir, "running", false, time.Now()); got != "running" {
		t.Fatalf("loop_state = %q, want running", got)
	}
}

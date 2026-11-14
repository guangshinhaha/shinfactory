package loopreg

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func fixedNow(t *time.Time) func() time.Time { return func() time.Time { return *t } }

func entry(projectDir string) Entry {
	return Entry{ProjectDir: projectDir, LoopState: "running", Mode: "auto", SlotsBusy: 1, SlotsMax: 3, NeedsYou: 2, Version: "1.2.3"}
}

func TestRegistrarWriteUpdateRemove(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "loops")
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	r := NewRegistrar(dir, "/work/alpha", fixedNow(&now))

	if err := r.Publish(entry("/work/alpha")); err != nil {
		t.Fatal(err)
	}
	got, err := List(dir)
	if err != nil || len(got) != 1 {
		t.Fatalf("List = %v, %v; want one entry", got, err)
	}
	e := got[0]
	if e.Repo != "alpha" || e.PID != os.Getpid() || e.Port != 0 || e.SlotsMax != 3 || e.NeedsYou != 2 || e.Version != "1.2.3" || e.Stale {
		t.Fatalf("entry = %+v", e)
	}
	if !e.UpdatedAt.Equal(now) {
		t.Fatalf("updated_at = %v, want %v", e.UpdatedAt, now)
	}

	// The port arrives after the server binds and rewrites the file.
	now = now.Add(time.Minute)
	if err := r.SetPort(3001); err != nil {
		t.Fatal(err)
	}
	got, _ = List(dir)
	if got[0].Port != 3001 || !got[0].UpdatedAt.Equal(now) {
		t.Fatalf("after SetPort: %+v", got[0])
	}

	// A state change rewrites; the same state does not touch updated_at.
	later := now.Add(time.Minute)
	now = later
	changed := entry("/work/alpha")
	changed.LoopState = "paused"
	if err := r.Publish(changed); err != nil {
		t.Fatal(err)
	}
	now = later.Add(time.Hour)
	if err := r.Publish(changed); err != nil {
		t.Fatal(err)
	}
	got, _ = List(dir)
	if got[0].LoopState != "paused" || got[0].Port != 3001 || !got[0].UpdatedAt.Equal(later) {
		t.Fatalf("after update: %+v (want updated_at %v)", got[0], later)
	}

	if err := r.Remove(); err != nil {
		t.Fatal(err)
	}
	if got, _ = List(dir); len(got) != 0 {
		t.Fatalf("file survived Remove: %+v", got)
	}
	// A late cycle must not resurrect the entry.
	if err := r.Publish(changed); err != nil {
		t.Fatal(err)
	}
	if got, _ = List(dir); len(got) != 0 {
		t.Fatalf("Publish after Remove wrote %+v", got)
	}
	if err := r.Remove(); err != nil {
		t.Fatalf("second Remove: %v", err)
	}
}

func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=NONE")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.ProcessState.Pid()
}

func TestListMarksDeadPIDStale(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	r := NewRegistrar(dir, "/work/live", fixedNow(&now))
	if err := r.Publish(entry("/work/live")); err != nil {
		t.Fatal(err)
	}
	dead := NewRegistrar(dir, "/work/dead", fixedNow(&now))
	if err := dead.Publish(entry("/work/dead")); err != nil {
		t.Fatal(err)
	}
	// Rewrite the dead loop's file with a pid that has exited.
	dead.mu.Lock()
	e := dead.last
	dead.mu.Unlock()
	e.PID = deadPID(t)
	data := []byte(`{"repo":"dead","project_dir":"/work/dead","pid":` + strconv.Itoa(e.PID) + `}`)
	if err := os.WriteFile(filepath.Join(dir, "dead.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := List(dir)
	if err != nil || len(got) != 2 {
		t.Fatalf("List = %+v, %v", got, err)
	}
	if got[0].Repo != "dead" || !got[0].Stale {
		t.Fatalf("dead loop = %+v, want stale", got[0])
	}
	if got[1].Repo != "live" || got[1].Stale {
		t.Fatalf("live loop = %+v, want not stale", got[1])
	}
}

func TestTwoLoopsSameRepoNameDoNotClobber(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	a := NewRegistrar(dir, "/one/app", fixedNow(&now))
	if err := a.Publish(entry("/one/app")); err != nil {
		t.Fatal(err)
	}
	b := NewRegistrar(dir, "/two/app", fixedNow(&now))
	if err := b.Publish(entry("/two/app")); err != nil {
		t.Fatal(err)
	}
	got, _ := List(dir)
	if len(got) != 2 || got[0].ProjectDir == got[1].ProjectDir {
		t.Fatalf("List = %+v, want both loops", got)
	}
	if err := a.Remove(); err != nil {
		t.Fatal(err)
	}
	got, _ = List(dir)
	if len(got) != 1 || got[0].ProjectDir != "/two/app" {
		t.Fatalf("after removing one: %+v", got)
	}
}

func TestListMissingDirAndTornFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")
	if got, err := List(missing); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("missing dir: %v, %v; want empty non-nil", got, err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := List(dir); err != nil || len(got) != 0 {
		t.Fatalf("torn file: %v, %v", got, err)
	}
}

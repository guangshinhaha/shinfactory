package loop

import (
	"testing"
	"time"

	"github.com/guangshinhaha/shinfactory/internal/linestatus"
	"github.com/guangshinhaha/shinfactory/internal/loopreg"
)

func TestLoopAnnouncesStateAndRemovesEntryOnShutdown(t *testing.T) {
	logger, _ := newTestLogger()
	tc := newTestLoop(t, logger)
	l := tc.loop
	dir := t.TempDir()
	l.Announce(dir, "9.9.9")

	// Nothing is written until the first status stamp.
	if got, _ := loopreg.List(dir); len(got) != 0 {
		t.Fatalf("entry written before the first stamp: %+v", got)
	}
	if err := l.stampStatus(); err != nil {
		t.Fatalf("stampStatus: %v", err)
	}
	got, err := loopreg.List(dir)
	if err != nil || len(got) != 1 {
		t.Fatalf("List = %+v, %v; want one entry", got, err)
	}
	e := got[0]
	if e.ProjectDir != l.projectDir || e.LoopState != string(StateRunning) || e.Version != "9.9.9" ||
		e.SlotsBusy != 0 || e.SlotsMax != l.config.Concurrency.MaxConcurrency || e.NeedsYou != 0 || e.Stale {
		t.Fatalf("entry = %+v", e)
	}

	// A port reported after the server binds, and a loop-state change, both land.
	l.SetPort(4123)
	if err := linestatus.RecordNeedsShin(l.runtimeDir, linestatus.NeedsShin{Order: "7", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	l.setState(StatePaused)
	if err := l.stampStatus(); err != nil {
		t.Fatalf("stampStatus: %v", err)
	}
	got, _ = loopreg.List(dir)
	if got[0].Port != 4123 || got[0].LoopState != string(StatePaused) || got[0].NeedsYou != 1 {
		t.Fatalf("after change: %+v", got[0])
	}

	l.Shutdown()
	if got, _ = loopreg.List(dir); len(got) != 0 {
		t.Fatalf("entry survived Shutdown: %+v", got)
	}
}

func TestLoopWithoutAnnounceWritesNoRegistry(t *testing.T) {
	logger, _ := newTestLogger()
	tc := newTestLoop(t, logger)
	if err := tc.loop.stampStatus(); err != nil {
		t.Fatal(err)
	}
	tc.loop.SetPort(1) // must not panic without a registrar
}

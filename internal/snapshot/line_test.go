package snapshot

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/guangshinhaha/shinfactory/internal/reducer"
	"github.com/guangshinhaha/shinfactory/internal/state"
	"github.com/guangshinhaha/shinfactory/loop"
)

// An orca-runtime order lives in the factory's own state: the in-memory
// projection can lag it, and the stage model is only in the spawn record.
func TestLoadSnapshotReadsOrcaOrderFromFactoryState(t *testing.T) {
	dir := t.TempDir()
	st := state.State{
		Mode: state.RunModeAuto,
		Orders: map[string]state.OrderNode{
			"22": {OrderID: "22", Sequence: 1, Title: "Order 22", Status: state.OrderActive, Stages: []state.StageNode{
				{StageIndex: 0, TaskKey: "execute", Status: state.StageRunning,
					Attempts: []state.AttemptNode{{SessionID: "sess-x", Status: state.AttemptRunning}}},
			}},
		},
	}
	if err := reducer.WriteSnapshotAtomic(filepath.Join(dir, "state.snapshot.json"), reducer.BuildSnapshot(st, nil, time.Now())); err != nil {
		t.Fatal(err)
	}
	sd := filepath.Join(dir, "sessions", "sess-x")
	if err := os.MkdirAll(sd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sd, "orca.json"), []byte(`{"dispatch_id":"ctx_8f03"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sd, "spawn.json"), []byte(`{"provider":"claude","model":"haiku"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// The loop's published state has not caught up: no orders at all.
	snap, err := LoadSnapshot(dir, time.Now(), loop.LoopState{})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Orders) != 1 || snap.Orders[0].ID != "22" {
		t.Fatalf("orders = %+v, want order 22", snap.Orders)
	}
	got := snap.Orders[0].Stages[0]
	if got.Model != "haiku" || got.Provider != "claude" || got.SessionID != "sess-x" || got.DispatchID != "ctx_8f03" {
		t.Fatalf("stage = %+v", got)
	}
	if got.Status != "active" {
		t.Fatalf("stage status = %q, want active", got.Status)
	}
	if len(snap.ActiveOrderIDs) != 1 || snap.ActiveOrderIDs[0] != "22" {
		t.Fatalf("active order ids = %v", snap.ActiveOrderIDs)
	}
}

// A state file the line cannot parse must not blank the dashboard.
func TestLoadSnapshotDegradesWhenLineStateIsUnreadable(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "status.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	snap, err := LoadSnapshot(dir, time.Now(), loop.LoopState{})
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	if len(snap.Warnings) != 1 {
		t.Fatalf("warnings = %v, want one", snap.Warnings)
	}
}

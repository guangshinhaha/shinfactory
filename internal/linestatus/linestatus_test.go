package linestatus

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/guangshinhaha/shinfactory/internal/reducer"
	"github.com/guangshinhaha/shinfactory/internal/state"
	"github.com/guangshinhaha/shinfactory/internal/statusfile"
)

func writeRuntime(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := statusfile.WriteAtomic(filepath.Join(dir, "status.json"), statusfile.Status{
		Active: []string{"o-run"}, LoopState: "draining", Mode: "auto", MaxConcurrency: 3,
	}); err != nil {
		t.Fatal(err)
	}
	st := state.State{
		Mode: state.RunModeAuto,
		Orders: map[string]state.OrderNode{
			"o-run": {OrderID: "o-run", Sequence: 1, Title: "Running one", Status: state.OrderActive, Stages: []state.StageNode{
				{StageIndex: 0, TaskKey: "execute", Provider: "claude", Model: "sonnet", Status: state.StageCompleted},
				{StageIndex: 1, TaskKey: "quality", Provider: "claude", Model: "opus", Status: state.StageRunning,
					Attempts: []state.AttemptNode{{SessionID: "sess-old", Status: state.AttemptFailed}, {SessionID: "sess-1", Status: state.AttemptRunning}}},
			}},
			"o-queued": {OrderID: "o-queued", Sequence: 2, Title: "Queued one", Status: state.OrderActive, Stages: []state.StageNode{
				{TaskKey: "execute", Status: state.StagePending},
			}},
			"o-park": {OrderID: "o-park", Sequence: 3, Title: "Parked one", Status: state.OrderActive, Stages: []state.StageNode{
				{TaskKey: "execute", Status: state.StageReview},
			}},
			"o-done": {OrderID: "o-done", Sequence: 4, Status: state.OrderCompleted},
		},
		PendingReviews: map[string]state.PendingReviewNode{"o-park": {OrderID: "o-park", Reason: "review"}},
	}
	if err := reducer.WriteSnapshotAtomic(filepath.Join(dir, "state.snapshot.json"), reducer.BuildSnapshot(st, nil, time.Now())); err != nil {
		t.Fatal(err)
	}
	sd := filepath.Join(dir, "sessions", "sess-1")
	if err := os.MkdirAll(sd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sd, "orca.json"), []byte(`{"dispatch_id":"ctx_abc"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestReadReportsLineState(t *testing.T) {
	dir := writeRuntime(t)
	if err := RecordNeedsShin(dir, NeedsShin{Order: "o-stuck", Reason: "two fix rounds failed", At: time.Unix(0, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	// o-live was stopped for a person, then put back: it must not show as needing Shin.
	if err := RecordNeedsShin(dir, NeedsShin{Order: "o-queued", Reason: "old"}); err != nil {
		t.Fatal(err)
	}

	rep, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Mode != "auto" || rep.Concurrency != 3 || rep.LoopState != "draining" {
		t.Fatalf("line header = %+v", rep)
	}
	if len(rep.Running) != 1 || rep.Running[0].ID != "o-run" {
		t.Fatalf("running = %+v", rep.Running)
	}
	stages := rep.Running[0].Stages
	if len(stages) != 2 || stages[1].Model != "opus" || stages[1].SessionID != "sess-1" || stages[1].DispatchID != "ctx_abc" {
		t.Fatalf("stages = %+v", stages)
	}
	if len(rep.Queued) != 1 || rep.Queued[0].ID != "o-queued" {
		t.Fatalf("queued = %+v", rep.Queued)
	}
	if len(rep.Parked) != 1 || rep.Parked[0].ID != "o-park" || rep.Parked[0].Reason != "review" {
		t.Fatalf("parked = %+v", rep.Parked)
	}
	if len(rep.NeedsShin) != 1 || rep.NeedsShin[0].Order != "o-stuck" {
		t.Fatalf("needs_shin = %+v", rep.NeedsShin)
	}
}

func TestReadEmptyRuntimeIsAnIdleLine(t *testing.T) {
	rep, err := Read(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Running == nil || rep.Queued == nil || rep.Parked == nil || rep.NeedsShin == nil {
		t.Fatalf("lists must encode as [], got %+v", rep)
	}
}

func TestRecordNeedsShinKeepsOnePerOrder(t *testing.T) {
	dir := t.TempDir()
	for _, reason := range []string{"first", "second"} {
		if err := RecordNeedsShin(dir, NeedsShin{Order: "o1", Reason: reason}); err != nil {
			t.Fatal(err)
		}
	}
	items, err := readNeedsShin(dir)
	if err != nil || len(items) != 1 || items[0].Reason != "second" {
		t.Fatalf("items = %+v, err = %v", items, err)
	}
}

func TestReadTreatsAnEmptySnapshotAsAnIdleLine(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "state.snapshot.json"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := Read(dir)
	if err != nil || len(rep.Running) != 0 {
		t.Fatalf("rep = %+v, err = %v", rep, err)
	}
}

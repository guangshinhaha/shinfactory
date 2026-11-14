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
				{StageIndex: 1, TaskKey: "quality", Status: state.StageRunning,
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
	// The snapshot carries no model for the running stage; the spawn record does.
	if err := os.WriteFile(filepath.Join(sd, "spawn.json"), []byte(`{"provider":"claude","model":"opus"}`), 0o644); err != nil {
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

func TestMergeNeedsShinBacklogIsAuthoritative(t *testing.T) {
	rep := Report{NeedsShin: []NeedsShin{
		{Order: "7", Reason: "two rounds failed"},
		{Order: "9", Reason: "settled by a person"},
	}}
	rep.MergeNeedsShin([]NeedsShin{
		{Order: "16", Title: "refused", URL: "u16"},
		{Order: "7", Title: "seven", URL: "u7"},
	})
	if len(rep.NeedsShin) != 2 {
		t.Fatalf("needs_shin = %+v, want 7 and 16 only", rep.NeedsShin)
	}
	if got := rep.NeedsShin[0]; got.Order != "7" || got.Reason != "two rounds failed" || got.Title != "seven" || got.URL != "u7" {
		t.Fatalf("recorded entry = %+v, want its reason plus the backlog title and url", got)
	}
	if got := rep.NeedsShin[1]; got.Order != "16" || got.Title != "refused" {
		t.Fatalf("backlog-only entry = %+v", got)
	}
}

// A failed order is inert, so the report must name it and why it failed.
func TestReadListsFailedOrdersWithReason(t *testing.T) {
	dir := t.TempDir()
	st := state.State{Orders: map[string]state.OrderNode{
		"schedule": {OrderID: "schedule", Sequence: 1, Status: state.OrderFailed, Stages: []state.StageNode{
			{TaskKey: "schedule", Status: state.StageFailed,
				Attempts: []state.AttemptNode{{Status: state.AttemptFailed, Error: "cook cancelled with status cancelled"}}},
		}},
	}}
	if err := reducer.WriteSnapshotAtomic(filepath.Join(dir, "state.snapshot.json"), reducer.BuildSnapshot(st, nil, time.Now())); err != nil {
		t.Fatal(err)
	}
	rep, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Failed) != 1 || rep.Failed[0].ID != "schedule" || rep.Failed[0].Reason != "cook cancelled with status cancelled" {
		t.Fatalf("Failed = %+v", rep.Failed)
	}
}

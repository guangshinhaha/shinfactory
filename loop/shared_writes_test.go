package loop

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/guangshinhaha/shinfactory/adapter"
	"github.com/guangshinhaha/shinfactory/mise"
)

func writesStage(paths ...string) Stage {
	raw, _ := json.Marshal(paths)
	return Stage{TaskKey: "execute", Skill: "execute", Provider: "claude", Model: "m", Status: StageStatusPending,
		Extra: map[string]json.RawMessage{stageWritesKey: raw}}
}

func TestStageWritesNormalizesPathsAndIgnoresBadValues(t *testing.T) {
	got := stageWrites(writesStage("./brain/index.md", "brain//index.md", " ", "."))
	want := []string{"brain/index.md", "brain/index.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stageWrites = %v, want %v", got, want)
	}
	bad := Stage{Extra: map[string]json.RawMessage{stageWritesKey: json.RawMessage(`"brain/index.md"`)}}
	if got := stageWrites(bad); got != nil {
		t.Fatalf("a non-list value gave %v, want none", got)
	}
}

func TestTwoStagesWritingOneSharedFileNeverRunTogether(t *testing.T) {
	h := newGateHarness(t, "none", "execute")
	a := Order{ID: "a", Status: OrderStatusActive, Stages: []Stage{writesStage("brain/index.md")}}
	b := Order{ID: "b", Status: OrderStatusActive, Stages: []Stage{writesStage("brain/index.md")}}
	c := Order{ID: "c", Status: OrderStatusActive, Stages: []Stage{writesStage("other.md")}}
	cands := []dispatchCandidate{
		{OrderID: "a", Stage: a.Stages[0]}, {OrderID: "b", Stage: b.Stages[0]}, {OrderID: "c", Stage: c.Stages[0]},
	}

	got := h.l.withoutWriteClashes(cands)
	if len(got) != 2 || got[0].OrderID != "a" || got[1].OrderID != "c" {
		t.Fatalf("batch kept %v, want a and c (b waits for a's file)", got)
	}

	// A stage already running holds its files across cycles.
	h.l.cooks.activeCooksByOrder["a"] = &cookHandle{cookIdentity: cookIdentity{orderID: "a", stage: a.Stages[0]}}
	got = h.l.withoutWriteClashes(cands[1:])
	if len(got) != 1 || got[0].OrderID != "c" {
		t.Fatalf("with a running, kept %v, want only c", got)
	}

	// Once it finishes the waiting stage goes.
	delete(h.l.cooks.activeCooksByOrder, "a")
	got = h.l.withoutWriteClashes(cands[1:])
	if len(got) != 2 || got[0].OrderID != "b" {
		t.Fatalf("after a finished, kept %v, want b and c", got)
	}
}

func TestCycleDispatchesOnlyOneOfTwoOrdersWritingTheSameFile(t *testing.T) {
	h := newGateHarness(t, "none", "execute")
	h.l.config.Concurrency.MaxConcurrency = 2
	h.l.deps.Mise = &fakeMise{brief: mise.Brief{Backlog: []adapter.BacklogItem{
		{ID: "1", Title: "t", Status: "open"}, {ID: "2", Title: "t", Status: "open"},
	}}}
	two := OrdersFile{Orders: []Order{
		{ID: "1", Status: OrderStatusActive, Tier: "none", Stages: []Stage{writesStage("brain/index.md")}},
		{ID: "2", Status: OrderStatusActive, Tier: "none", Stages: []Stage{writesStage("brain/index.md")}},
	}}
	writeTestOrders(t, h.l.runtimeDir, two)
	if err := h.l.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(h.rt.sessions); n != 1 {
		t.Fatalf("sessions = %d, want 1: the second order writes the same file", n)
	}
}

func TestCycleDispatchesBothOrdersWhenTheirFilesDiffer(t *testing.T) {
	h := newGateHarness(t, "none", "execute")
	h.l.config.Concurrency.MaxConcurrency = 2
	h.l.deps.Mise = &fakeMise{brief: mise.Brief{Backlog: []adapter.BacklogItem{
		{ID: "1", Title: "t", Status: "open"}, {ID: "2", Title: "t", Status: "open"},
	}}}
	two := OrdersFile{Orders: []Order{
		{ID: "1", Status: OrderStatusActive, Tier: "none", Stages: []Stage{writesStage("a.md")}},
		{ID: "2", Status: OrderStatusActive, Tier: "none", Stages: []Stage{writesStage("b.md")}},
	}}
	writeTestOrders(t, h.l.runtimeDir, two)
	if err := h.l.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(h.rt.sessions); n != 2 {
		t.Fatalf("sessions = %d, want 2 (control: no shared file)", n)
	}
}

func TestParkedAndAdoptedStagesHoldTheirSharedFiles(t *testing.T) {
	h := newGateHarness(t, "none", "execute")
	cand := []dispatchCandidate{{OrderID: "b", Stage: writesStage("brain/index.md")}}

	h.l.cooks.pendingReview["a"] = &pendingReviewCook{cookIdentity: cookIdentity{orderID: "a", stage: writesStage("brain/index.md")}}
	if got := h.l.withoutWriteClashes(cand); len(got) != 0 {
		t.Fatalf("parked order's file not held: kept %v", got)
	}
	delete(h.l.cooks.pendingReview, "a")

	active := writesStage("brain/index.md")
	active.Status = StageStatusActive
	writeTestOrders(t, h.l.runtimeDir, OrdersFile{Orders: []Order{{ID: "a", Status: OrderStatusActive, Stages: []Stage{active}}}})
	h.l.ordersLoaded = false
	h.l.cooks.adoptedTargets["a"] = "session"
	if got := h.l.withoutWriteClashes(cand); len(got) != 0 {
		t.Fatalf("adopted order's file not held: kept %v", got)
	}
}

func TestSharedWritesClashOnCaseAndDirectoryOverlap(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"brain/index.md", "Brain/Index.md", true},
		{"brain", "brain/index.md", true},
		{"brain/index.md", "brain", true},
		{"brain", "brainstorm/x.md", false},
		{"a.md", "b.md", false},
	} {
		if got := writePathsOverlap(c.a, c.b); got != c.want {
			t.Errorf("writePathsOverlap(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestParkedOrderHoldsFilesFromOrdersJSONAfterARestart(t *testing.T) {
	h := newGateHarness(t, "none", "execute")
	writeTestOrders(t, h.l.runtimeDir, OrdersFile{Orders: []Order{{ID: "a", Status: OrderStatusActive, Stages: []Stage{writesStage("brain/index.md")}}}})
	h.l.ordersLoaded = false
	// The reloaded cook has no Extra on its stage.
	h.l.cooks.pendingReview["a"] = &pendingReviewCook{cookIdentity: cookIdentity{orderID: "a", stageIndex: 0}}
	if got := h.l.withoutWriteClashes([]dispatchCandidate{{OrderID: "b", Stage: writesStage("brain/index.md")}}); len(got) != 0 {
		t.Fatalf("restarted parked order's file not held: kept %v", got)
	}
}

package loop

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/guangshinhaha/shinfactory/adapter"
	"github.com/guangshinhaha/shinfactory/internal/orderx"
	"github.com/guangshinhaha/shinfactory/mise"
)

// Trial 4, order 19: the scheduler rewrote an order mid-quality, which
// cancelled the stage and added another execute. An order with a stage in
// flight must come through untouched, and the rejection must be reported.
func TestConsumeOrdersNextRejectsAmendmentWhileStageInFlight(t *testing.T) {
	for _, inflight := range []orderx.StageStatus{orderx.StageStatusActive, orderx.StageStatusMerging} {
		t.Run(string(inflight), func(t *testing.T) {
			dir := t.TempDir()
			ordersPath := filepath.Join(dir, "orders.json")
			nextPath := filepath.Join(dir, "orders-next.json")

			existing := orderx.OrdersFile{Orders: []orderx.Order{{
				ID: "19", Title: "existing order", Status: orderx.OrderStatusActive,
				Stages: []orderx.Stage{
					{TaskKey: "execute", Provider: "codex", Model: "gpt-5.4", Status: orderx.StageStatusCompleted},
					{TaskKey: "quality", Provider: "claude", Model: "claude-opus-4-6", Status: inflight},
				},
			}}}
			if err := orderx.WriteOrdersAtomic(ordersPath, existing); err != nil {
				t.Fatal(err)
			}
			// The scheduler proposes another execute round ahead of quality.
			writeCompactOrders(t, nextPath, orderx.CompactOrdersFile{Orders: []orderx.CompactOrder{
				{ID: "19", Title: "amended order", Stages: []orderx.CompactStage{
					{Do: "execute", With: "codex", Model: "gpt-5.4", Prompt: "again"},
					{Do: "quality", With: "claude", Model: "claude-opus-4-6"},
				}},
			}})

			result := consumeOrdersNextAndPersist(t, nextPath, ordersPath)
			if len(result.AmendRejected) != 1 || result.AmendRejected[0] != "19" {
				t.Fatalf("AmendRejected = %v, want [19]", result.AmendRejected)
			}
			got, err := orderx.ReadOrders(ordersPath)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Orders[0], existing.Orders[0]) {
				t.Fatalf("order changed under its in-flight stage:\n got %+v\nwant %+v", got.Orders[0], existing.Orders[0])
			}
		})
	}
}

func TestConsumeOrdersNextUnchangedProposalIsNotReportedAsRejected(t *testing.T) {
	dir := t.TempDir()
	ordersPath := filepath.Join(dir, "orders.json")
	nextPath := filepath.Join(dir, "orders-next.json")
	existing := orderx.OrdersFile{Orders: []orderx.Order{{
		ID: "19", Status: orderx.OrderStatusActive,
		Stages: []orderx.Stage{{TaskKey: "quality", Skill: "quality", Provider: "claude", Model: "claude-opus-4-6", Status: orderx.StageStatusActive}},
	}}}
	if err := orderx.WriteOrdersAtomic(ordersPath, existing); err != nil {
		t.Fatal(err)
	}
	writeCompactOrders(t, nextPath, orderx.CompactOrdersFile{Orders: []orderx.CompactOrder{
		{ID: "19", Stages: []orderx.CompactStage{{Do: "quality", With: "claude", Model: "claude-opus-4-6"}}},
	}})
	if result := consumeOrdersNextAndPersist(t, nextPath, ordersPath); len(result.AmendRejected) != 0 {
		t.Fatalf("AmendRejected = %v, want none for an unchanged proposal", result.AmendRejected)
	}
}

// Trial 4, order 19, end to end: the scheduler proposes another execute round
// while quality runs. Quality must finish and the order must land, with no
// extra execute stage and no cancelled session.
func TestSchedulerAmendmentMidQualityLetsQualityFinishAndTheOrderLand(t *testing.T) {
	h := newGateHarness(t, "", "execute", "quality")
	h.l.deps.OrdersNextFile = filepath.Join(h.l.projectDir, ".noodle", "orders-next.json")
	h.l.deps.Mise = &fakeMise{brief: mise.Brief{Backlog: []adapter.BacklogItem{{ID: "42", Title: "t", Status: "open"}}}}

	h.next(t) // execute runs and completes
	if err := h.l.Cycle(context.Background()); err != nil {
		t.Fatalf("quality dispatch cycle: %v", err)
	}
	if len(h.rt.sessions) != 2 {
		t.Fatalf("sessions = %d, want quality dispatched as the second", len(h.rt.sessions))
	}
	quality := h.rt.sessions[1]

	writeCompactOrders(t, h.l.deps.OrdersNextFile, orderx.CompactOrdersFile{Orders: []orderx.CompactOrder{
		{ID: "42", Stages: []orderx.CompactStage{
			{Do: "execute", With: "claude", Model: "claude-opus-4-6", Prompt: "another round"},
			{Do: "quality", With: "claude", Model: "claude-opus-4-6", Prompt: "review again"},
		}},
	}})
	if err := h.l.Cycle(context.Background()); err != nil {
		t.Fatalf("amendment cycle: %v", err)
	}
	if quality.status != "running" {
		t.Fatalf("quality session status = %q after the amendment, want it left running", quality.status)
	}

	// The reviewer writes its findings file while quality runs.
	h.writeNow(t, 0, reviewFileName, "Covered: whole diff, high level, correctness and blast radius\nFindings: none\n")
	quality.complete("completed")
	if err := h.l.Cycle(context.Background()); err != nil {
		t.Fatalf("completion cycle: %v", err)
	}
	h.settleEvidence(t)
	executes := 0
	for _, call := range h.rt.calls {
		if call.Skill == "execute" {
			executes++
		}
	}
	if executes != 1 {
		t.Fatalf("execute sessions dispatched = %d, want 1: the amendment added an execute stage", executes)
	}
	h.shipped(t)
}

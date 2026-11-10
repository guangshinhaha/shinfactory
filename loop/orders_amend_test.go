package loop

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/guangshinhaha/shinfactory/internal/orderx"
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

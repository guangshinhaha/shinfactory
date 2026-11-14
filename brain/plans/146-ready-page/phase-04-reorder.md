Back to [[plans/146-ready-page/overview]]

# Phase 4 — Move up / down sends `reorder`

## Goal
Each planned row has move up/down controls that reorder the queue through the existing `reorder` action.

## Changes
- `ui/src/components/ReadyPage.tsx`: row controls; target index comes from `ReadyItem.OrdersIndex` of the neighbour row, never the list position.
- `ui/src/client/` helper that builds the `reorder` command (value is a string index).

## Data structures
`reorder{order_id, value: string(orders.json index)}`.

## Behaviour
- Disabled on the first/last planned row and on unplanned rows (tooltip: planned on the next scheduler pass).
- Disabled while the ack is pending; show the error ack message on failure ("order not found" style failure text).
- `controlReorder` (`loop/control_orders.go:241`) does NOT guard active orders: it moves to any index. The UI only targets a neighbour planned row's index, so it never crosses an active order; enforcement is UI-side unless a Go guard is added in this phase (decide in implementation; if added, test it).
- The index is read from a snapshot that can be stale: if `orders.json` changes between render and click, the index may point at a different order. Disable the controls while a snapshot update is pending and show the ack error; do not retry blindly.

## Routing
`builder`

## Verification
### Static
- `pnpm check`
### Runtime
- Vitest: clicking down on row 1 of 3 sends `reorder` with the neighbour's `OrdersIndex`; edges disabled; error ack shown. Test with an active order present so list position and orders index differ.
- Go: a control test for the chosen behaviour on reorder past an active order (today unguarded, so the old "keeps active in place" claim is false).

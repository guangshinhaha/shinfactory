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
- Active orders sit before queued ones in `orders.json`; a move never targets an index at or before an active order.

## Routing
`builder`

## Verification
### Static
- `pnpm check`
### Runtime
- Vitest: clicking down on row 1 of 3 sends `reorder` with the neighbour's `OrdersIndex`; edges disabled; error ack shown. Test with an active order present so list position and orders index differ.
- Go: extend `loop` control test to prove that reorder past an active order keeps active orders in place, if not already covered.

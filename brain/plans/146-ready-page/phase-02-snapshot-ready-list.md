Back to [[plans/146-ready-page/overview]]

# Phase 2 — Snapshot carries the Ready list

## Goal
`snapshot.ready` is the single ordered pick-up list, and `snapshot.inbox_count` is available. The UI needs no other source.

## Changes
- `loop/` (mise/brief build path) and `internal/snapshot/snapshot_builder.go`: persist the latest ready backlog and inbox count where the snapshot builder can read them, then build the list: queued orders in `orders.json` order, then ready issues with no order, in backlog order.
- `internal/snapshot/types.go`: add `Ready []ReadyItem`, `InboxCount`, and `Tier` on `Order`. Regenerate `ui/src/client/generated-types.ts` via tygo (`tygo.yaml`).
- Fixtures under `internal/snapshot/testdata`.

## Data structures
`ReadyItem{IssueID, Title, OrderID (empty if unplanned), OrdersIndex (-1 if unplanned), Tier, PlannedStages []string, Position}`. Use "planned" vs "unplanned" derived from `OrderID`, not a second flag.

## Routing
`builder`

## Verification
### Static
- `go test ./internal/snapshot/... ./loop/...`, `go vet ./...`, tygo output committed and `pnpm check` type step clean.
### Runtime
- Table tests: no orders and 2 ready issues gives 2 unplanned rows; 1 active + 2 queued orders gives rows for queued only, `OrdersIndex` counting the active order; an order whose issue is not ready is excluded; done orders excluded.

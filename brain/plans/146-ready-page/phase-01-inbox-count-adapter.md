Back to [[plans/146-ready-page/overview]]

# Phase 1 — Inbox count adapter

## Goal
The loop can read how many open issues have no state label, without the scheduler ever seeing them.

## Changes
- `defaults/adapters/github/backlog-inbox` (new script): counts open issues carrying none of the state labels (`ready`, `needs-shin`, shipped/done label). Same env vars and label defaults as `backlog-sync`.
- `adapter/runner.go`, `adapter/types.go`: add an optional `inbox` action that returns a count. Adapters without it report "unknown", not zero.
- Adapter docs (`docs/concepts/adapters.md`, `docs/concepts/backlog-states.md`): document the action.

## Data structures
`InboxCount{Count int, Known bool}`. Unknown renders as a dash in the UI.

## Routing
`builder`

## Verification
### Static
- `go test ./adapter/... ./defaults/...` and `go vet ./adapter/...`
### Runtime
- Fixture test (see `adapter/fixture_test.go`): 3 issues (ready, needs-shin, unlabelled) gives count 1. Missing script gives Known=false. gh failure surfaces as an error, not 0.

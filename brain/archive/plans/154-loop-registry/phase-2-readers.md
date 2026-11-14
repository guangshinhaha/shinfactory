# Phase 2: `ps` and `/api/loops` (done)

Done-when lines 2, 3 and 4.

- `cmd_ps.go`: table output, stale rows marked, `--json`; registered in `cmdmeta` and documented in `docs/reference/cli.md`.
- `server/server.go`: `GET /api/loops` returns `loopreg.List` output, same shape as `ps --json`.
- Tests: `cmd_ps_test.go`, `server/loops_test.go`.

Verify: `go test . ./server`.

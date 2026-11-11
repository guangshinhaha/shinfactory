# Phase 1: registry package and loop announce (done)

Done-when lines 1 and 4 (write/update/delete, stale pid, two loops).

- `internal/loopreg/loopreg.go`: `Registrar` (`Publish` atomic via `filex`, `SetPort`, `Remove`) and `List` (stale via `procx.IsPIDAlive`, torn files skipped, missing dir is empty), `DefaultDir`.
- Filename `<repo>.json` must not collide for two checkouts of one repo name: key includes a short hash of the project dir.
- `loop/stamp_status.go`: announce on start and on each loop-state change, same path that stamps status; `cmd_start.go` removes the file on clean exit.
- Tests: `internal/loopreg/loopreg_test.go`, `loop/announce_test.go`.

Verify: `go test ./internal/loopreg ./loop`.

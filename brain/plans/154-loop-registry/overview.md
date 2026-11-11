# Plan 154: Loop registry and `shinfactory ps` (#120 slice 1)

## Summary

Each running loop announces itself in `~/.local/state/shinfactory/loops/<repo>.json` so tools list every factory line on the machine without remembering ports. Out of scope: the UI switcher (#120 slice 2) and cross-repo control.

## Data shape

`loopreg.Entry`: repo, project_dir, port, pid, loop_state, mode, slots_busy, slots_max, needs_you, version, updated_at. `Stale` is computed by `List` from pid liveness, never written. One writer (the loop), many readers (`ps`, `/api/loops`), so a whole-file atomic replace needs no lock.

## Phases

- [x] [[plans/154-loop-registry/phase-1-registry-and-announce]] — `internal/loopreg` (write, list, delete, stale) and the loop announcing on start and each state change.
- [x] [[plans/154-loop-registry/phase-2-readers]] — `shinfactory ps [--json]` and `GET /api/loops`, both through `loopreg.List`.
- [ ] [[plans/154-loop-registry/phase-3-verify-and-close]] — run the full check, confirm each Done-when line, then archive this plan.

## Status

Phases 1 and 2 landed in c437d50 and e45e333 before this plan stage ran. Targeted tests pass (loopreg, server, loop, root `ps`). Remaining work is verification only.

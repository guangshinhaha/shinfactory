# 152 follow-up: close the remaining one-worktree-two-workers gaps

Status: planned. #152's own Done-when is met (commits 6bf42d2, 2b5fa13; `go test ./loop ./dispatcher` passes). This plan covers the two paths the brain note lists as not covered. Parent: #137.

## Gaps (verified by reading the code)
1. `cancelSupersededCook` (`loop/loop_cycle_pipeline.go:155`) logs a failed `ForceKill` and carries on: it untracks the cook, and with `resume` it re-queues the stage. On orca a failed worker-stop then yields a second worker in the same worktree. `controlStopKill` now returns the error instead. (The no-resume branch also cleans the worktree; that is intended for a superseded stage.)
2. `orcaSession.poll` lost-contact fence (`dispatcher/orca_session.go:106`) discards the `stopOrcaWorker` error and finishes `StatusFailed`. The loop then retries, possibly beside a live worker. Line 93 (ctx cancel) is shutdown: out of scope.

## Design choice: requeue vs retry on a failed stop
Recommend **no retry loop and no new state**; reuse what exists.
- Gap 1: if `ForceKill` fails, return the error before untracking or respawning. The cook stays active and the next cycle sees the amendment again, so the supersede retries naturally, like Stop does today. Reject: parking the order (the amendment is valid; the old worker is the problem).
- Gap 2: if the fence stop fails, do not `finish`. Reset `failures` and keep polling; the session stays live, so no retry spawns. If Orca stays unreachable the stop keeps failing and the order stays visibly active, which is honest. Reject: a `Failed` outcome with a "worker may be live" flag (new field, every consumer must honor it).

## Steps (each verifiable alone; tests first with `fakeorca.FailStop`)
1. Test: fence stop fails -> session not finished; stop succeeds on a later tick -> `Failed` "lost contact". Then change `poll`.
2. Test: `cancelSupersededCook` with a session whose `ForceKill` errors, `resume=true` -> cook still active, stage not re-pended, no second dispatch. Then return the error early.
3. Update brain note `codebase/stop-keeps-worktree-and-fences-orca-worker.md` (drop "Not covered").
4. `pnpm check`.

## Risks
- Gap 2 changes poll to hold a session open when Orca is down: confirm the loop's shutdown path (`loop/loop.go:625`) still force-kills it.
- Gap 1 error surfaces from the cycle pipeline: check it logs and retries rather than aborting the cycle (see #125 backlog-outage handling).

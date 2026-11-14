# Phase 5 — End-to-end proof

Back to [[plans/206-orca-default-for-new-repos/overview]]

## Goal
Prove the Done-when: a repo created from the template shows its cooks in `orca orchestration worker-list` with no manual config.

## Changes
- None to code. Add a short note to `brain/codebase/runtime-routing-owned-by-orders.md`: new-repo templates now set `default = "orca"`; fallback unchanged. Remove this plan from `brain/plans/` (archive) once merged.

## Data structures
None.

## Routing
`verifier`.

## Verification
- Runtime, orca present: scratch repo, `shinfactory start` init, add one ready backlog item, start the loop, then `orca orchestration worker-list` shows the cook. Save the output as the evidence.
- Runtime, orca absent: rerun with orca removed from PATH; `shinfactory status` shows the stage on `process` and the loop does not fail. `go test ./loop -run TestOrcaRuntimeAbsentFallsBackToProcess` also passes.
- Needs a human at the Orca app, so this phase is not async-executable; the executor reports the command output instead.
- `pnpm check` is green at the final head.

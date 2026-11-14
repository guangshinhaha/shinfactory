Back to [[plans/139-orca-live-steering/overview]]

# Phase 4 — Dashboard steer boxes and docs

## Goal

The steer boxes work for Orca workers and say what steering means there.

## Changes

- `ui/src/components/OrcaSteerNotice.tsx`: `isSteerable` returns true for Orca, so `AgentFeed` and `SchedulerFeed` show the normal steer box. Keep "Open in Orca" as a secondary link, not a replacement.
- Steer box hint for Orca: "Delivered as a message; the worker reads it, it is not interrupted."
- Update `AgentFeed.test.tsx`, `SchedulerFeed.test.tsx` and `e2e/ui/smoke.spec.ts` that assert the notice.
- `docs/concepts/operating-model.md`: steer works on Orca workers by message, Stop kills them.
- Brain: update [[codebase/scheduler-steer-alias-prefers-live-session]] and write `codebase/orca-steer-is-message-not-interrupt.md`; delete the #158 note if it states the refusal as current behaviour.

## Verification

`pnpm check` including the UI tests and e2e smoke.

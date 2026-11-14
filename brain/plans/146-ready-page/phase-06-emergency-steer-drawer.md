Back to [[plans/146-ready-page/overview]]

# Phase 6 — Emergency steer drawer

## Goal
A collapsed "Emergency: steer the scheduler" drawer on `/ready` replaces the scheduler feed's composer. On orca it shows the Open in Orca line until live steering lands (#139).

## Changes
- `ui/src/components/SteerDrawer.tsx` (new): collapsed by default; expanded shows the composer that sends `steer` with target `schedule` (extracted from `SchedulerFeed.tsx`), or `OrcaSteerNotice` when the scheduler session is not steerable (`isSteerable`).
- `ui/src/components/SchedulerFeed.tsx`: composer logic moves out (share the sender, do not copy it).

## Data structures
Reuses `useSendControl`, `isSteerable`, `dispatchIDForSession`. The steer alias behaviour is in [[codebase/scheduler-steer-alias-prefers-live-session]].

## Routing
`builder`

## Verification
### Static
- `pnpm check`
### Runtime
- Vitest: drawer collapsed by default; expanded non-orca sends `steer` `schedule` with the prompt; orca scheduler session shows Open in Orca and no composer; no scheduler session shows an idle message.
- Screenshot collapsed and expanded.

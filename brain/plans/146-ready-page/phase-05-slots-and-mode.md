Back to [[plans/146-ready-page/overview]]

# Phase 5 — Slots stepper and mode toggle

## Goal
Change slots (`set-max-concurrency`) and mode (`mode`) from the Ready page before walking away.

## Changes
- `ui/src/components/ReadyControls.tsx` (new): slots stepper (min 1, value from `snapshot.max_concurrency`), mode toggle over `auto` / `supervised` / `manual` (from `snapshot.mode`).
- `ui/src/components/ReadyPage.tsx`: mount the controls above the list; one line of copy per mode, matching `docs/concepts/modes.md`.

## Data structures
Controls are controlled by the snapshot value, so a failed ack leaves the old value; no local optimistic state beyond a pending flag.

## Behaviour
- Switching to `auto` asks for confirmation (merges happen without review); other modes do not.
- Stepper rejects values below 1; the loop already validates invalid values, surface its error.

## Routing
`builder`

## Verification
### Static
- `pnpm check`
### Runtime
- Vitest: plus sends `set-max-concurrency` with the next value, minus is disabled at 1, each mode sends `mode`, auto needs confirm, error ack reverts display.
- Manual/screenshot: run against a fixture loop and see the snapshot value change.

Back to [[plans/146-ready-page/overview]]

# Phase 3 — `/ready` route, nav entry, read-only list

## Goal
Opening `/ready` shows the pick-up list, the Inbox count and the "label an issue `ready`" copy.

## Changes
- `ui/src/routes/ready.tsx` (new) and `ui/src/components/ReadyPage.tsx` (new); `routeTree.gen.ts` regenerates.
- `ui/src/components/Sidebar.tsx`: add the Ready nav item with queued count, in the vocabulary order. Use backlog words only (Inbox, Ready).
- Inbox line: count, read-only, note that the factory ignores inbox issues until labelled `ready`. Unknown count shows a dash.

## Data structures
Reads `Snapshot.ready` and `Snapshot.inbox_count`; no UI-side derivation.

## Routing
`builder`, then `reviewer` for UI states.

## Verification
### Static
- `pnpm check`
### Runtime
- Vitest with seeded snapshots: empty (copy shown, no list), 2 planned + 1 unplanned, inbox count 0 / 7 / unknown. Planned rows show stages and tier; unplanned rows say "not planned yet".
- Screenshot of `/ready`.

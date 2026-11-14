Back to [[plans/146-ready-page/overview]]

# Phase 7 — Remove `/scheduler` and the New Order button

## Goal
Nothing links to the old scheduler feed or the stub button, and the code for both is gone.

## Changes
- `ui/src/components/Dashboard.tsx`: delete the New Order button (if #148 already removed Dashboard, nothing to do).
- Scheduler feed route/channel: delete the `/scheduler` route and `SchedulerFeed`; remove the `scheduler` channel from `__root.tsx`, `Sidebar.tsx`, `FeedKeyboardHandler.tsx`, `FeedPanel.tsx`, `tree-utils.ts` and `ContextPanel.tsx`, per what #143 left. Delete their tests with them.
- `docs/` and `brain/`: update mentions of "Talk to the scheduler" and `/scheduler`.

## Data structures
Remove `{type: "scheduler"}` from `ChannelId` in `ui/src/client/types.ts`; the compiler then lists every leftover use.

## Routing
`builder`, then `adversary` to hunt dead links and leftover references.

## Verification
### Static
- `pnpm check`, `pnpm build`, grep for `/scheduler`, `New Order`, `SchedulerFeed` returns nothing in `ui/src`.
### Runtime
- Vitest: sidebar has Ready and no Scheduler item; navigating to `/scheduler` is a not-found.
- Final screenshot walkthrough of `/ready` with seeded queue, drawer open and closed.

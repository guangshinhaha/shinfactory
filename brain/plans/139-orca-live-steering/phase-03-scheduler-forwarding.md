Back to [[plans/139-orca-live-steering/overview]]

# Phase 3 — forwardToScheduler on Orca

## Goal

Stage events reach a scheduler that runs on Orca, in order, without losing the `orders.json` recovery path.

## Changes

- `loop/cook_completion.go` `forwardToScheduler`: no code change to the steerable check; it now passes for Orca because of phase 2. Add a log line naming the runtime so a drop is explainable.
- Serialize sends per target: replace the per-message `go func` with a small per-session queue (one goroutine, FIFO), reusing the `sessionSteerMutex` idea. Today two quick events can race.
- `loop/cook_steer.go` `steer` for `target == schedule`: with Orca steerable, the `!Steerable()` reschedule branch is only for runtimes that stay non-steerable. Keep it; do not add a second path.
- Recovered and adopted Orca sessions: `runtime/process_recover.go` and `loop/reconcile.go` (`adoptedSession.Controller`) return a no-op controller. Check how Orca runs are recovered (#92 "recover every recorded run") and give a recovered Orca session the same controller, or the flag flips only for fresh spawns.

## Tests

- Scheduler cook on Orca: a `stage_yield` produces one `send` call with the `[stage_yield] order=...` text.
- Two events fired back to back arrive in order.
- Scheduler not active: message dropped, order stays (existing behaviour).
- Recovered Orca session is steerable.

## Verification

`pnpm check`.

# Failed schedule order is retried, stuck sessions are reported

- A failed `schedule` order stayed in `orders.json`. `hasScheduleOrder` kept returning true and dispatch skips non-active orders, so the loop idled with a ready issue until `control requeue schedule` (#205).
- `ensureScheduleIfNeeded` now calls `retryFailedScheduleOrder`: it requeues the failed schedule order (same path as `controlRequeue`), at most once per `scheduleRetryBackoff` (5 min) so a scheduler that dies at once cannot spin.
- `[monitor] stuck_after_minutes` (default 20, 0 = off): `noteStuckSessions` (run after each monitor pass) puts orders whose running session has `IdleSeconds` past it into `status.json` `stuck`, warns once per session, and `shinfactory status` prints `stuck=<order ids>`.

See also [[codebase/failed-target-auto-repair-for-requeued-orders]], [[codebase/schedule-bootstrap-hot-loop]]

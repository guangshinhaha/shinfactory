# Scheduler Active Order Amendments

- **An order with an active or merging stage is never amended** (#106). `consumeOrdersNext` skips the proposal, reports the ID in `mergeResult.AmendRejected`, and the loop logs the reason. Trial 4 order 19 was rewritten mid-quality three times, each cancelling quality and adding an execute. The proposal is dropped, not queued; nothing re-triggers the scheduler. Pending-only orders still amend as below.
- Fixture: `orders-next-amend-mid-quality`. Fixture active sessions may carry a `stage` so the loop doesn't treat them as superseded.

- `consumeOrdersNext` now treats duplicate `active` order IDs as amendable, not skip-only.
- Active-order amendment merge behavior:
  - preserve completed prefix from the existing order
  - if the current stage definition is unchanged, keep the running stage and replace only downstream stages
  - if the current stage definition changed, replace the current stage from scheduler input
- After promotion, the loop now compares each active cook against the promoted order's current stage:
  - if stage definitions no longer match (or the order disappeared), the cook is force-killed and worktree-cleaned
  - this prevents stale sessions from continuing after scheduler-amended pipelines

See also [[codebase/scheduler-steer-alias-prefers-live-session]], [[codebase/orders-lifecycle-defaults-on-promotion]], [[principles/make-operations-idempotent]], [[principles/serialize-shared-state-mutations]]

## Mise churn must not re-run the scheduler (#157)
- `ensureScheduleIfNeeded` injected a schedule order on every `miseChanged`. While a stage ran, tickets/events/resources changed mise.json constantly, so Haiku scheduler runs went back to back and each amendment was rejected (#106).
- `miseChangeNeedsSchedule` skips the injection when a non-schedule stage is in flight and the backlog fingerprint equals the one the last schedule run saw. A backlog change, or the stage finishing, schedules again.
- Test: `TestMiseNoiseDuringInFlightStageDoesNotInjectSchedule`.

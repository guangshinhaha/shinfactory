# Failed Schedule Order Is Retried

- A failed order is inert, and the `schedule` order is the only thing that starts new work, so a failed schedule order idled the whole loop (restart over a cancelled run: `orders=1`, ready issues, nothing scheduled).
- `retryFailedScheduleOrder` (called from `Cycle` before `prepareOrdersForCycle`) requeues it through `controlRequeue`. First retry is immediate; later ones back off 30s doubling to 10m. State is in memory (`scheduleRetries`, `scheduleRetryAt`) and clears once the schedule order is gone.
- It skips when `ctx` is cancelled: a retry during shutdown spawns a session the drain never completes (`TestCoordinatorStatusDrainResumeOnARunningLoop` caught this).
- `shinfactory status` prints `failed: <id> | <reason>` from the canonical snapshot's latest attempt error (`linestatus.Report.Failed`).

See also [[codebase/schedule-bootstrap-hot-loop]], [[codebase/failed-target-auto-repair-for-requeued-orders]]

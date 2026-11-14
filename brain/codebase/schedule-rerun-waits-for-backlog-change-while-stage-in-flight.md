# Schedule re-run waits for a backlog change while a stage is in flight

- Root cause (#157): `Mise.Build`'s `changed` covers history, tickets and active summary, not just the backlog. Each schedule completion changed history, so `ensureScheduleIfNeeded` injected another schedule run, and #106 rejected every amendment to the in-flight order.
- Fix: a mise-change schedule run is skipped while a non-schedule stage has a live cook and the backlog fingerprint equals the one the last such run saw (`scheduleBacklogKey`). It runs again on a backlog change or once no stage is in flight.
- Bootstrap (no non-schedule orders) is unaffected.

See also [[codebase/scheduler-active-order-amendments]], [[codebase/schedule-bootstrap-hot-loop]]

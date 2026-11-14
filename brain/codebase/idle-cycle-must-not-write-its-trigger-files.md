# Idle cycle must not write its own trigger files

- Root cause (#188): the runtime fsnotify watcher starts a cycle on any write to `orders.json`, `orders-next.json` or `control.ndjson`. Every cycle rewrote two of them with unchanged content: `flushState` replaced `orders.json` (atomic rename) and `processControlCommands` truncated an empty `control.ndjson`. Each cycle queued the next, so an idle loop never waited for its 1s tick and ran Mise.Build and the monitor over `.noodle/sessions` back to back (~150% CPU on a real project, 64% on an empty scratch project).
- Fix: `filex.WriteFileAtomicIfChanged` for the `orders.json` projection; skip the truncate when the control file is empty.
- Guard: `TestIdleLoopDoesNotTriggerItsOwnCycles` watches the runtime dir of an idle `Run` and fails on any trigger-file write.
- Rule: a file in `shouldTriggerRuntimeCycle` may only be written when its content changes. Adding a new trigger file means checking the idle cycle leaves it alone.
- Measured on a scratch project with a stub agent: 64.2% of a core before, 0.5% after (60s window).

See also [[codebase/schedule-bootstrap-hot-loop]]

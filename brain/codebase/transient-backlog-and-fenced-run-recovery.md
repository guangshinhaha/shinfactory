# Transient Backlog Errors Retry; A Fenced Orca Run Is Replaced

- `loop.buildBriefWithRetry` retries a backlog sync failure that looks like network or 5xx (`transientMarkers`) up to `backlogSyncAttempts` times with doubling backoff. Other errors (401, 404, missing script) fail at once. A persistent failure ends `start` with "backlog sync still failing after N attempts".
- `OrcaDispatcher.Dispatch` replaces the run once on `consumer_fenced` (host sleep): `replaceRun` creates and records a new run, then retries worker-start. A second fence surfaces.
- Fixtures: `loop/backlog_retry_test.go`, `fakeorca.FenceConsumer`.

See also [[codebase/backlog-sync-parse-errors-are-recoverable-warnings]], [[codebase/orca-runtime-usage-limit-hold]]

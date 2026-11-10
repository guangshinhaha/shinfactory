# Transient Backlog Errors Retry; A Fenced Orca Run Is Replaced

- `loop.buildBriefWithRetry` retries a backlog sync failure that looks like network or 5xx (`transientMarkers`) until `[backlog] retry_window_seconds` (default 300) of waiting is spent, with doubling backoff capped at 30s. DNS (`no such host`, `ENOTFOUND`) and `Client.Timeout exceeded` count as transient. Tests swap `Dependencies.BacklogRetrySleep` for virtual time. Other errors (401, 404, missing script) fail at once. A persistent failure ends `start` with "backlog sync still failing after N attempts over <window>".
- `OrcaDispatcher.Dispatch` replaces the run once on `consumer_fenced` (host sleep): `replaceRun` creates and records a new run, then retries worker-start. A second fence surfaces.
- Fixtures: `loop/backlog_retry_test.go`, `fakeorca.FenceConsumer`.

See also [[codebase/backlog-sync-parse-errors-are-recoverable-warnings]], [[codebase/orca-runtime-usage-limit-hold]]

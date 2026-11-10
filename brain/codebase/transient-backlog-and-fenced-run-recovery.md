# Transient Backlog Errors Retry; A Fenced Orca Run Is Replaced

- `loop.buildBriefWithRetry` retries a backlog sync failure that looks like network or 5xx (`transientMarkers`) across cycles, never sleeping inside one: it records `Loop.backlogRetry` (first failure, next attempt, doubling backoff capped at 30s) and returns a waiting cycle, so control commands (pause/stop) and status stamps keep running. The status shows a "backlog sync failing (attempt N); retrying in Xs" warning. The window `[backlog] retry_window_seconds` (default 300, max 86400; negative or over-cap values get a parse diagnostic) runs from the start of the first failing sync on wall-clock time, so slow sync calls count. DNS (`no such host`, `ENOTFOUND`) and `Client.Timeout exceeded` count as transient. The only test seam is `Dependencies.Now` (a fake clock). Other errors (401, 404, missing script) fail at once. A persistent failure ends `start` with "backlog sync still failing after N attempts over <window>".
- `OrcaDispatcher.Dispatch` replaces the run once on `consumer_fenced` (host sleep): `replaceRun` creates and records a new run, then retries worker-start. A second fence surfaces.
- Fixtures: `loop/backlog_retry_test.go`, `fakeorca.FenceConsumer`.

See also [[codebase/backlog-sync-parse-errors-are-recoverable-warnings]], [[codebase/orca-runtime-usage-limit-hold]]

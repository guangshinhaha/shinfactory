# Completion drain waits for a finished session's in-flight result

- `drainCompletions` polls 2ms for a running watcher. A session that already finished (`Done()` closed) has a result certain to arrive, but its watcher goroutine can run late on a loaded runner. With only the 2ms poll, `Cycle` missed the completion: `TestLocalLandingStillMergesLocally` and `TestLogCompletionMerge` failed together once on #67's CI (#66).
- `Loop.watchedDone` holds each watched session's `Done` channel from watcher start until its result is queued. If any is closed, the drain waits up to `finishedSessionWait` (2s) instead of 2ms.
- Test: `loop/cook_completion_wait_test.go`.

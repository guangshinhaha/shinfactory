# Orca Runtime And The Usage-Limit Hold

- `[runtime.orca]` in `.noodle.toml` plus the `orca` CLI on PATH enables the `orca` runtime (`Config.AvailableRuntimes`). Without the CLI nothing changes: `stageRuntime` maps `orca` to `process`.
- `OrcaDispatcher` runs `orca orchestration worker-start --spec-file ... --worktree path:<stage worktree> --agent <provider> --model <model>`, then polls `worker-list` for the worker's outcome. Orca workers get no factory env, so the spec carries `NOODLE_SESSION_ID` / `NOODLE_PROJECT_DIR` for the skills' `shinfactory event emit` calls. `stage_yield` / `stage_fail` still decide the result.
- A usage limit is detected in `worker-start` output (launch) or the last non-user transcript messages (mid-run; the user's spec can mention limits, so it is never scanned). Launch gives `dispatcher.UsageLimitError`; mid-run sets `SessionOutcome.UsageLimit`.
- The loop holds the runtime (`usageLimitHolds`, `runtime.orca.limit_retry_seconds`, default 300) and leaves the stage pending, never failed, with no fallback to `process` (same account, same failure). After the hold lapses the next launch is the probe: success clears the hold, another limit renews it. A mid-run death requeues the stage with its worktree kept.
- Fixtures use `internal/testutil/fakeorca`, a stand-in `orca` script; see `loop/orca_runtime_test.go`.
- `runtime.orca.max_concurrent` defaults to 0 (unlimited): a per-runtime cap makes `dispatchSession` fall back to `process` for overflow stages.
- Not done: `Recover` returns nothing, so an Orca worker still running when the loop restarts is orphaned; the hold does not parse the reset time from the message.

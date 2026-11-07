# Session Events Need The Project Dir

- Agents run in their worktree, and `shinfactory` resolves its runtime dir from flag, then `NOODLE_PROJECT_DIR`, then cwd.
- Without the env var, `shinfactory event emit --session` writes to `.worktrees/<name>/.noodle/`, which the loop never reads. `stage_message`, `stage_yield` and `stage_fail` are all lost, the stage completes, and a `quality` rejection does nothing.
- `ProcessDispatcher.sessionEnv` sets `NOODLE_PROJECT_DIR`. Unit tests that write events straight into the session log can't catch a regression here: only a real run through a worktree does. Check `.noodle/sessions/<id>/events.ndjson` for the event's own `type`.
- An agent also needs `shinfactory` on PATH to emit at all. The dispatcher doesn't add it.

See also [[codebase/worktree-gotchas]], [[codebase/session-failure-classification-crash-only]]

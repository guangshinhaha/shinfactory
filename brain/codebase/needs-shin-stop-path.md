# Stops That Need A Person Go To The Issue As `needs-shin`

- `loop/needs_person.go`: `stopForPerson` runs the backlog adapter's optional `stop` action (arg = order id, stdin = what is needed). The GitHub script (`defaults/adapters/github/backlog-stop`) comments once and adds `needs-shin`; `backlog-sync` skips issues carrying it, so removing the label is what lets the scheduler pick the issue up again. A backlog with no `stop` script is skipped (`isMissingAdapter`).
- Three triggers: a `stage_fail` starting `Unbuildable:` (no Done when), the second agent-reported `stage_fail` for one order, and a merge conflict in `handleMergeError`. PR-mode landing conflicts live in `loop/landing*.go` and are not wired yet.
- Failed rounds count in `.noodle/failed-rounds.json`, not on the order: a retry is a fresh order with the same ID. Cleared on stop and on the order's `done`.
- Idempotent: the script does nothing when the label is already up, so a rerun never comments twice.
- Mode: `resolveRunMode` (`loop/shinstack_mode.go`) reads `shinstack-mode:` from the project's CLAUDE.md and overrides `.noodle.toml` `mode`; undeclared or unknown values fall back to the config.
- Test trap: `exec.Command("gh")` resolves from the test process's PATH, not `cmd.Env`. A test that built its own env once ran the real gh; call the stub by absolute path.

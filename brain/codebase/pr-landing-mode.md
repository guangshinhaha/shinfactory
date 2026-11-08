# PR landing mode (`[landing] mode = "pr"`, the default)

- Replaces the per-stage local merge with one branch and worktree per order (`cookWorktreeName` returns `<order>-0-task` for every stage). Code: `loop/landing.go`.
- The first stage with commits pushes and opens a draft PR; later stages push to it. `PushAndEnsureDraft` is idempotent (it finds the PR by branch), so a crash mid-stage reruns safely.
- Ship = the order's last stage completing. Auto mode merges (`gh pr merge --squash`; never `--admin`, never `--auto`, because `--auto` returns success while only queueing the merge, so the order would be marked done unmerged). Other modes park the final stage before `stage.completed` is emitted, and `controlMerge` ships on approval (`approvePRShip`). Emitting `stage.completed` first drops the pending review.
- `cleanupCookWorktree` is a no-op in this mode: a failed stage keeps the shared worktree for the retry. Only a successful ship removes it.
- Tests use `fakeLander` (loop behaviour) and a stub `gh` on PATH with a local bare remote (`loop/landing_gh_test.go`).
- Known gap: after a PR merges, local main is stale, so new orders branch from old code. Tracked as a follow-up issue.
- `local` stays as an escape hatch for projects with no GitHub remote. Loop tests that exercise the local pipeline use `localLandingConfig()`.
- PR body: `Closes #N` only when the backlog adapter is the bundled GitHub one; otherwise `Refs #N`. Order rationale and plan are not sent to GitHub (PDPA).

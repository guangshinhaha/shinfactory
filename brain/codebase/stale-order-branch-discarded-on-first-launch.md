# Stale order branch is discarded on an order's first launch

- A closed PR leaves `<n>-0-task` (branch, worktree, remote branch) behind. A re-readied issue gives the new order the same id, so the same branch: it carried the old commits and their conflicts (#186).
- `prepareOrderBranch` (`loop/landing.go`) runs at stage 0, attempt 0, round 0, not a steer. If the branch or worktree exists and `Lander.BranchPRClosed` says every non-fork PR on it is closed or merged, it removes the branch, worktree and remote branch. `ensureWorktree` then cuts a fresh one off the fetched default branch.
- An open PR, or none, keeps the branch. GitHub is asked only when a branch or worktree already exists.
- `worktree.Create` still reuses an existing branch that has no worktree; the loop clears it first.
- Errors (gh, cleanup, remote delete) fail the dispatch, like a failed base fetch.

See [[codebase/pr-landing-mode]]

# Phase 3: verify and close

- Run `pnpm check` (full suite) in the worktree.
- Tick each Done-when line against a real run: start a loop, `shinfactory ps`, `curl /api/loops`, kill -9 the loop and confirm the row shows stale, stop cleanly and confirm the file is gone.
- Move this plan to `brain/archive/plans/` and update both indexes.

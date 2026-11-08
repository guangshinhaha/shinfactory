# Runtime Routing Owned By Orders

- Runtime selection belongs to order stages, not skill frontmatter.
- `Loop.stageRuntime` (order stages): the stage `runtime`, else `process`; only a configured default of `orca` replaces that fallback. `Loop.dispatchRuntime` (every dispatch): request runtime, else `config.Runtime.Default`, else `process`. In both, an `orca` that is not wired (no orca CLI) resolves to `process`.
- Config accepts runtime names `process`, `sprites`, `cursor` and `orca`; `defaultDependencies` registers `process`, optional `sprites`, and `orca` (see [[codebase/orca-runtime-usage-limit-hold]]). Cursor dispatch is not wired.
- Skill frontmatter and task registry (`taskreg.TaskType`) should not carry runtime fields.
- Keep non-functional backend tests optional during stub phases; compile-time interface assertions are enough until behavior exists.
- Merge detection followed the same pattern: moved from static skill metadata (`permissions.merge`) to runtime detection via `WorktreeManager.HasUnmergedCommits`.
- `worktreeHasChanges()` checks three paths: (1) persisted merge metadata (crash recovery via `Extra["merge_branch"]`), (2) sync result (remote branches via `spawn.json`), (3) local worktree via `git cherry`.
- Mode gating is orthogonal: mode controls merge approval, runtime detection controls whether there's anything to approve.

See also [[archive/plans/27-remote-dispatchers/overview]], [[principles/boundary-discipline]], [[principles/subtract-before-you-add]]

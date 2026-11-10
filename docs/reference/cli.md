# CLI Reference

All commands accept the global `--project-dir` flag. When omitted, shinfactory uses the current directory (or the `NOODLE_PROJECT_DIR` environment variable).

## Global flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--project-dir` | string | current directory | Project directory. Also settable via `NOODLE_PROJECT_DIR` |

---

## `shinfactory start`

Start the shinfactory loop. Spawns agent sessions and manages the full lifecycle.

Auto-starts a web server on port 3000 (configurable via `[server]`). Opens a browser unless `NOODLE_NO_BROWSER=1` is set.

```
shinfactory start [flags]
```

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--once` | bool | `false` | Run one scheduling cycle and exit |

---

## `shinfactory status`

Show compact runtime status. Prints active agent count, orders queue depth, and shinfactory loop state (running, paused, draining, or idle).

```
shinfactory status
```

---

## `shinfactory skills`

List resolved skills.

```
shinfactory skills
```

### `shinfactory skills list`

List all resolved skills.

```
shinfactory skills list
```

---

## `shinfactory schema`

Print generated schema docs for shinfactory runtime contracts. Takes an optional target argument.

```
shinfactory schema [target]
```

### `shinfactory schema list`

List available schema targets.

```
shinfactory schema list
```

---

## `shinfactory worktree`

Manage linked git worktrees. shinfactory uses worktrees to isolate concurrent agent sessions so they don't conflict on the working tree.

### `shinfactory worktree create`

Create a new linked worktree.

```
shinfactory worktree create <name> [flags]
```

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--from` | string | `HEAD` | Branch or commit to base the new worktree on |

### `shinfactory worktree exec`

Run a command inside a worktree. Sets the working directory to the worktree path before executing.

```
shinfactory worktree exec <name> <command...>
```

### `shinfactory worktree merge`

Merge a worktree branch into a target branch.

```
shinfactory worktree merge <name> [flags]
```

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--into` | string | integration branch | Target branch to merge into |

### `shinfactory worktree cleanup`

Remove a worktree without merging.

```
shinfactory worktree cleanup <name> [flags]
```

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--force` | bool | `false` | Remove even when unmerged commits exist |

### `shinfactory worktree list`

List all worktrees with merge status.

```
shinfactory worktree list
```

### `shinfactory worktree prune`

Remove merged and patch-equivalent worktrees.

```
shinfactory worktree prune
```

### `shinfactory worktree hook`

Run worktree session hook. Used internally by agent sessions.

```
shinfactory worktree hook
```

---

## `shinfactory event`

Manage shinfactory loop events.

### `shinfactory event emit`

Emit an external event into the shinfactory loop or a specific session.

```
shinfactory event emit <type> [flags]
```

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--payload` | string | — | Event payload as JSON |
| `--session` | string | — | Session ID. When set, writes to the session event log instead of the shinfactory loop event log |

---

## `shinfactory reset`

Clear all runtime state. Removes and recreates the runtime directory.

Refuses to run if shinfactory is currently running (checks the lock file).

```
shinfactory reset
```

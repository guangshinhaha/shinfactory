# Configuration Reference

Noodle reads `.noodle.toml` at project root. When no config file exists, `shinfactory start` uses sensible defaults.

## Minimal config

```toml
[routing.defaults]
provider = "claude"
model = "claude-opus-5-5"

[skills]
paths = [".agents/skills"]
```

## `mode`

Controls human oversight level: `auto` (full automation), `supervised` (human approves merges), `manual` (human triggers everything).

```toml
mode = "supervised"
```

## `[routing.defaults]`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `provider` | string | `"claude"` | Agent provider (`"claude"`, `"codex"`) |
| `model` | string | `"claude-sonnet-5-5"` | Model identifier |

## `[routing.roles]`

Maps a role to a provider and model. Omitted roles use the defaults below. The loop resolves a stage's role from its task key and fills the stage's provider and model.

| Role | Default model |
|------|---------------|
| `scheduler`, `debrief` | `claude-haiku-5-5` |
| `builder`, `explorer`, `reviewer`, `adversary` | `claude-sonnet-5-5` |
| `planner`, `verifier` | `claude-opus-5-5` |

```toml
[routing.roles]
builder = { provider = "claude", model = "claude-sonnet-5-5" }
```

A provider is used only when the config names it. `[routing] sensitive = true` keeps every stage on `routing.defaults.provider`.

## `[skills]`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `paths` | string[] | `[".agents/skills"]` | Directories to scan for SKILL.md files |

## `[concurrency]`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `max_concurrency` | int | `2` | Maximum concurrent agent sessions |

## `[agents.claude]`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `path` | string | `""` | Path to Claude CLI binary |
| `args` | string[] | `[]` | Extra CLI arguments for every invocation |

## `[agents.codex]`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `path` | string | `""` | Path to Codex CLI binary |
| `args` | string[] | `[]` | Extra CLI arguments for every invocation |

## `[runtime]`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `default` | string | `"process"` | Default runtime: `"process"`, `"sprites"`, or `"cursor"` |

### `[runtime.process]`

Local process runtime. Runs agent CLIs as child processes.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `max_concurrent` | int | `4` | Maximum concurrent process sessions |

### `[runtime.sprites]`

Cloud sandboxes via sprites.dev.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `token_env` | string | `"SPRITES_TOKEN"` | Env var for Sprites API token |
| `base_url` | string | `""` | Custom API base URL |
| `sprite_name` | string | `""` | Name prefix for spawned instances |
| `git_token_env` | string | `"GITHUB_TOKEN"` | Env var for git auth |
| `max_concurrent` | int | `50` | Maximum concurrent sprite sessions |

### `[runtime.cursor]`

Cloud runtime using Cursor background agents.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `api_key_env` | string | `"CURSOR_API_KEY"` | Env var for Cursor API key |
| `base_url` | string | `""` | Custom API base URL |
| `repository` | string | `""` | Repository identifier |
| `max_concurrent` | int | `10` | Maximum concurrent Cursor sessions |

## `[server]`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `port` | int | `3000` | Web UI server port |
| `enabled` | bool | auto | Start server (auto-starts in interactive terminals when omitted) |

## `[adapters.<name>]`

Adapters bridge external systems into the backlog. See https://poteto.github.io/noodle/concepts/adapters for the full guide.

| Field | Type | Description |
|-------|------|-------------|
| `skill` | string | Skill name this adapter extends |
| `scripts` | map | Named shell commands (`sync`, `add`, `done`, `edit`) |

```toml
[adapters.backlog]
skill = "backlog"

[adapters.backlog.scripts]
sync = "adapters/backlog-sync"
add = "adapters/backlog-add"
done = "adapters/backlog-done"
edit = "adapters/backlog-edit"
```

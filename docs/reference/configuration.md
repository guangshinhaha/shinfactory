# Configuration

shinfactory is configured through a `.noodle.toml` file at the root of your project. When no config file exists, shinfactory uses sensible defaults for all values.

## Minimal config

Most projects only need routing defaults and a skills path:

```toml
[routing.defaults]
provider = "claude"
model = "claude-sonnet-5-5"

[skills]
paths = [".agents/skills"]
```

## Full reference

### `mode`

Controls human oversight level.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `mode` | string | `"supervised"` | Autonomy mode: `"auto"`, `"supervised"`, or `"manual"` |

```toml
mode = "supervised"
```

When the project's `CLAUDE.md` declares `shinstack-mode: auto`, `supervised` or `manual`, the factory runs in that mode and the `mode` setting is only the fallback for a repo that declares none. `auto` merges once the gate passes; `supervised` stops at the PR for your yes.

---

### `[routing]`

Controls which Agent provider and model are used for skill execution.

#### `[routing.defaults]`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `provider` | string | `"claude"` | Agent provider name (e.g. `"claude"`, `"codex"`) |
| `model` | string | `"claude-sonnet-5-5"` | Model identifier passed to the provider |

```toml
[routing.defaults]
provider = "claude"
model = "claude-sonnet-5-5"
```

#### `[routing.roles]`

Maps a role (`scheduler`, `planner`, `builder`, `explorer`, `reviewer`, `adversary`, `verifier`, `debrief`) to a provider and model. Omitted roles use the defaults: `claude-haiku-5-5` for scheduler and debrief, `claude-sonnet-5-5` for builder, explorer, reviewer and adversary, `claude-opus-5-5` for planner and verifier. A provider is used only when a role or `routing.defaults` names it. `[routing] sensitive = true` keeps every stage on `routing.defaults.provider`. See [Model Routing](/cookbook/model-routing).

### `[skills]`

Configures where shinfactory discovers skill definitions.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `paths` | string[] | `[".agents/skills"]` | Directories to scan for SKILL.md files |

```toml
[skills]
paths = [".agents/skills"]
```

---

### `[concurrency]`

Controls parallel execution limits.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `max_concurrency` | int | `2` | Maximum number of concurrent agent sessions |

```toml
[concurrency]
max_concurrency = 2
```

::: warning Cost
Each agent consumes tokens independently. `max_concurrency = 4` means up to four concurrent API sessions. New repos start at 2; raise it with `set-max-concurrency` or `.noodle.toml` once you have seen the cost per session on your workload.
:::

---

### `[spend]`

Caps what one order may spend. Before each stage launch the loop checks the order against both caps. An order over either cap fails without launching, comments once on its issue with what it spent and why it stopped, and swaps `ready` for `needs-shin`. A person puts `ready` back to resume it.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `max_attempts` | int | `3` | Times one order may launch the same stage; `0` turns the cap off |
| `max_cost_usd` | float | `20` | Cost ceiling per order, summed from the cost its finished stages reported in their session logs; `0` turns the cap off |

```toml
[spend]
max_attempts = 3
max_cost_usd = 20
```

The cost cap does not apply where the runtime reports no cost: the Orca runtime reports none, so on Orca only `max_attempts` stops a runaway order. The stop comment says so when the order's recorded cost is zero.

The cost check runs between stages, so one stage can finish past the ceiling before the order stops. There is no cap across orders.

---

### `[backlog]`

Tunes how the loop treats a backlog sync that fails with a transient error: DNS failure, a client timeout, a connection reset or a 5xx. The loop retries with exponential backoff (1s, doubling, capped at 30s) until the window is spent, then stops with "backlog sync still failing". Other errors, such as bad credentials, stop at once.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `retry_window_seconds` | int | `300` | Total time, from the first failure, to keep retrying a transient backlog failure. Negative or over `86400` values are reset with a warning |

```toml
[backlog]
retry_window_seconds = 300
```

---

### `[agents]`

Configures paths and arguments for agent CLI binaries. Each sub-table names a provider.

#### `[agents.claude]`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `path` | string | `""` | Path to the Claude CLI binary or directory |
| `args` | string[] | `[]` | Additional CLI arguments passed on every invocation |

```toml
[agents.claude]
path = "~/.claude"
args = ["--dangerously-skip-permissions", "--verbose"]
```

#### `[agents.codex]`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `path` | string | `""` | Path to the Codex CLI binary or directory |
| `args` | string[] | `[]` | Additional CLI arguments passed on every invocation |

```toml
[agents.codex]
path = "~/.codex"
args = ["--full-auto"]
```

---

### `[runtime]`

Controls which runtime executes agent sessions and per-runtime settings.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `default` | string | `"process"` | Default runtime kind: `"process"` or `"sprites"` |

```toml
[runtime]
default = "process"
```

#### `[runtime.process]`

Local process runtime. Runs agent CLIs as child processes on the host machine.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `max_concurrent` | int | `4` | Maximum concurrent process sessions |

```toml
[runtime.process]
max_concurrent = 4
```

#### `[runtime.sprites]`

Cloud runtime using [Sprites](https://sprites.dev) sandboxed environments.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `token_env` | string | `"SPRITES_TOKEN"` | Environment variable name for the Sprites API token |
| `base_url` | string | `""` | Custom API base URL (leave empty for default) |
| `sprite_name` | string | `""` | Name prefix for spawned sprite instances |
| `git_token_env` | string | `"GITHUB_TOKEN"` | Environment variable name for the Git token used by sprites |
| `max_concurrent` | int | `50` | Maximum concurrent sprite sessions |

```toml
[runtime.sprites]
token_env = "SPRITES_TOKEN"
base_url = ""
sprite_name = "noodle-dev"
git_token_env = "GITHUB_TOKEN"
max_concurrent = 50
```

---

### `[landing]`

How finished work reaches the default branch.

```toml
[landing]
mode = "pr"      # "pr" or "local"; unset: "pr" with a GitHub origin, else "local"
base = "main"    # optional PR base; empty means the repository default
close_issues = false  # true: PR body says "Closes #<order id>"; only for a GitHub issues backlog
```

- Unset `mode` resolves per project: `pr` when `origin` is on github.com, `local` when it is not. An explicit value always wins.
- In `pr` mode each order's worktree branches from the freshly fetched remote default branch (or `base`), so an order never starts from a main that an earlier PR has outdated. A failed fetch fails the dispatch.
- `local`: for projects with no GitHub remote. Each stage's worktree is merged into the local default branch.
- `pr` (default): one branch and worktree per order, created at the first stage. After a stage leaves commits, the loop pushes the branch and opens a draft PR, so CI runs during review and verify. Later stages push to the same PR. When the order's last stage completes, the loop marks the PR ready and merges it (`gh pr merge --squash`, never `--admin` or `--auto`), so the repository's checks and ruleset decide. While checks are still running the order stays parked ("waiting for required checks") and the loop retries every 30 seconds, merging once they pass (a PR GitHub still reports no mergeability for after 10 minutes stops for a person); a merge GitHub refuses (a required check failing) fails the stage with that reason, or, after a wait, keeps the order parked with the refusal. The order is marked done only after the PR is merged. In `auto` mode this happens unasked; in `supervised` the draft PR stays up and the order parks until you approve the merge. Needs `git` and a signed-in `gh` on PATH.

### `[server]`

Controls the web UI server.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `port` | int | `3000` | Port for the web UI server |
| `enabled` | bool | `true` | Whether to start the server. When omitted, the server starts automatically |

```toml
[server]
port = 3000
enabled = true
```

---

### `[adapters.<name>]`

Adapters bridge external systems (issue trackers, project boards) into shinfactory's backlog. Each adapter is a named table mapping a skill to a set of shell scripts.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `skill` | string | — | Skill name this adapter extends |
| `scripts` | map | — | Named shell scripts the adapter invokes |

The default config includes a `backlog` adapter:

```toml
[adapters.backlog]
skill = "backlog"

[adapters.backlog.scripts]
sync = "my-adapters/backlog-sync"
done = "my-adapters/backlog-done"
```

Scripts are executed relative to the project root. Each script receives structured input on stdin and must produce structured output on stdout. See the [backlog skill docs](/concepts/skills) for the expected interface.

## Common configurations

### Local development with Claude

Minimal setup for a single developer using Claude as the Agent provider:

```toml
mode = "supervised"

[routing.defaults]
provider = "claude"
model = "claude-sonnet-5-5"

[skills]
paths = [".agents/skills"]

[agents.claude]
path = "~/.claude"

[concurrency]
max_concurrency = 2
```

### Cloud execution with Sprites

Scale out with cloud sandboxes while keeping a local fallback:

```toml
[routing.defaults]
provider = "claude"
model = "claude-sonnet-5-5"

[runtime]
default = "sprites"

[runtime.sprites]
sprite_name = "my-project"
max_concurrent = 20

[runtime.process]
max_concurrent = 2
```

### Defaults-only routing

Set a single project-wide default provider and model:

```toml
[routing.defaults]
provider = "claude"
model = "claude-sonnet-5-5"
```

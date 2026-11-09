# Adapters

Adapters bridge external systems into Noodle's backlog. If your team tracks work in GitHub Issues, Linear, Jira, or somewhere else, an adapter syncs those items so Noodle can schedule and execute them.

The default backlog is a markdown file (`brain/todos.md`). You don't need an adapter if that's all you use. Adapters are for when your tickets already live somewhere else and you don't want to duplicate them.

## How they work

An adapter is a set of commands configured in `.noodle.toml`. Each command handles one operation:

- **sync**: pulls items from the external source into Noodle's backlog format
- **done**: marks an item complete in the external system
- **add** _(planned)_: creates a new item in the external system
- **edit** _(planned)_: updates an existing item

```toml
[adapters.backlog]
skill = "backlog"

[adapters.backlog.scripts]
sync = "my-adapters/backlog-sync"
add = "my-adapters/backlog-add"
done = "my-adapters/backlog-done"
edit = "my-adapters/backlog-edit"
```

Commands are executed relative to the project root via `sh -c`. They can be anything that runs in a shell: a POSIX shell script, a Python script, a Node script, a compiled Go binary, a `curl` one-liner. As long as `sh -c <your-command>` can invoke it, it works.

## When the scheduler runs

The scheduler doesn't care where backlog items come from. It reads the backlog, and if an adapter is configured, the sync script runs first to pull in fresh items. From the scheduler's perspective, the backlog is the backlog regardless of source.

This means you can switch from `todos.md` to GitHub Issues (or vice versa) without changing your skills. The adapter handles the translation.

## Schema

Each adapter command has a specific contract for input and output.

### `sync`

No input. Prints newline-delimited JSON (NDJSON) to stdout, one backlog item per line.

```json
{
  "id": "1",
  "title": "Fix login bug",
  "status": "open",
  "tags": ["bug", "auth"],
  "priority": 1
}
{
  "id": "2",
  "title": "Update docs",
  "status": "done",
  "section": "Documentation"
}
{
  "id": "3",
  "title": "Refactor API",
  "estimate": "medium"
}
```

Only `id` and `title` are required. Everything else — any field, any type — is passed through to mise.json for the scheduler to see.

| Field    | Type   | Required | Notes                              |
| -------- | ------ | -------- | ---------------------------------- |
| `id`     | string | yes      | unique identifier                  |
| `title`  | string | yes      | item title                         |
| `status` | string | no       | used by Noodle to filter done items |
| `plan`   | string | no       | used by Noodle to read plan files  |

`status` and `plan` are the only optional fields Noodle interprets. All other fields (e.g. `tags`, `section`, `estimate`, `priority`, or anything your system tracks) are passed through as-is. The scheduler sees them in mise.json and can use them for context, but Noodle itself doesn't act on them.

### `done`

Noodle calls your `done` command when an order completes, passing the item ID as the first argument. Your command should mark that item complete in your external system. No stdin, no output expected.

```
my-adapters/backlog-done <id>
```

### `add` _(planned)_

Not yet called by the noodle loop. When implemented, your `add` command will receive JSON on stdin (`title`, plus optional `section`; the ready-made GitHub adapter also takes `body`) and should create the item in your external system, then print the new item's ID to stdout.

### `edit` _(planned)_

Not yet called by the noodle loop. When implemented, your `edit` command will receive the item ID as the first argument and JSON on stdin with the new values.

## Validation

Noodle validates each NDJSON line from the sync command individually. Invalid items are skipped — they don't block the rest of the backlog from processing.

**What gets validated:**
- Each line must be valid JSON
- `id` must be a non-empty string
- `title` must be a non-empty string

**What happens on failure:**
- Invalid items are skipped and a warning is generated for each one
- Valid items from the same sync still process normally
- If every item is invalid, the backlog is empty but no error is raised

**Where warnings surface:**
- **Web UI** — warnings appear in the dashboard warnings panel alongside config warnings
- **Backend logs** — logged at warn level during the build cycle
- **Scheduler prompt** — the scheduler sees an `ADAPTER WARNINGS` section and may create a fix task

**Example warnings:**
```
backlog sync line 3: invalid JSON: unexpected end of JSON input
backlog sync line 5: missing required field title
backlog sync line 12: missing required field id
```

## Writing an adapter

A ready-made GitHub Issues adapter lives in `defaults/adapters/github/`: every open issue labelled `ready` is a backlog item, labels become tags, a `Plan: brain/plans/NN-slug/overview.md` line in the body links a plan, and `done` labels the issue `shinfactory-done` instead of closing it. With landing mode `pr`, the order's PR body carries `Closes #N` when `[landing] close_issues = true`, so merging the PR closes the issue. Copy the five scripts into your project and point `[adapters.backlog.scripts]` at them; it needs `gh` signed in. Env: `NOODLE_GH_LABEL` renames the `ready` label, `NOODLE_GH_DONE_LABEL` renames the done label. `NOODLE_GH_STOP_LABEL` renames `needs-shin`.

When an order stops and needs a person, the loop runs the backlog adapter's optional `stop` action (the issue number is the argument, the message is on stdin). The GitHub script comments once with what is needed, then removes `ready` and adds `needs-shin` in one edit, so the scheduler leaves the issue alone until a person fixes it and adds `ready` back. The factory reads only `ready`. A stop is: a stage that reports `Unbuildable:` (no observable Done when), a second failed round on the same issue, or a merge conflict the loop cannot resolve. A backlog with no `stop` script is skipped.

### Handing an issue to the factory

The `ready` label is a person's permission to build. The factory never sees an open issue without it, and there is no setting that syncs every issue.

1. Write the issue with a Goal, an observable Done when, and an Out of scope. Exact headings aren't required, but the issue must state something you can test.
2. Create the label once per repo: `gh label create ready`. Until it exists, the backlog is empty.
3. Add it to the issue: `gh issue edit N --add-label ready`.

If the issue has no observable Done when, the scheduler doesn't build it. It comments the reason on the issue and removes `ready`. Fix the issue, then add the label again.

For another tracker, an agent can write the scripts from the schema above.

## Configuration reference

See [`[adapters.<name>]`](/reference/configuration#adapters-name) for the full config spec.

---
name: todo
description: >-
  Add, complete, view, or link plans to items in the project backlog (GitHub issues or
  brain/todos.md). Use when the user says "todo", "add a todo", "mark done", "what's on the
  backlog", "show todos", "list tasks", "check off", or wants to manage their task list.
---

# Todo

The one place that knows where the backlog lives. Other skills say "use the `todo` skill"; only this skill touches the backend.

**Pick the backend:** read `[adapters.backlog.scripts]` in `.noodle.toml`. If `sync` points at a `github/` adapter, the backlog is **GitHub issues**. Otherwise it is the **`brain/todos.md`** file.

## GitHub issues backend

Item IDs are issue numbers. Use the `gh` CLI.

- **View:** `gh issue list --state open`. Issues labelled `shinfactory-done` are finished on local main and waiting for a push; skip them.
- **Add:** `gh issue create --title "<title>" --body "<what, where, what done looks like>"`. Add `--label needs_plan` when it needs a plan before code. Report the new number.
- **Link a plan:** the issue body carries one line `Plan: brain/plans/NN-slug/overview.md`, where NN is the issue number. Add or replace it with `gh issue edit N --body-file <file>` (read the current body first with `gh issue view N --json body --jq .body`), and remove the `needs_plan` label.
- **Edit:** `gh issue edit N --title ...` or `--body-file ...`.
- **Mark done:** inside a factory order, don't close the issue; the loop labels it `shinfactory-done`, and the human's push with `Closes #N` closes it. Outside the factory, close with a reason: `gh issue close N --comment "<why>"`, adding `--reason "not planned"` for an item that is outdated or superseded.

## todos.md backend

Manage the backlog at `brain/todos.md`. Edit the file directly with Edit/Write tools.

### File Format

```markdown
# Todos

<!-- next-id: 26 -->

## Section Name

1. [x] Completed item description
2. [ ] Open item description [[plans/02-some-plan/overview]]
```

- Items are numbered markdown checkboxes: `N. [ ] description` or `N. [x] description`
- Sections are `## Heading` groups
- `<!-- next-id: N -->` tracks the next available ID

### Operations

#### Add an item

1. Read `<!-- next-id: N -->` to get the next ID
2. Append `N. [ ] description` to the target section
3. Increment the next-id marker: `<!-- next-id: N+1 -->`

#### Mark done

Change `[ ]` to `[x]` and update the description with a reason:

```
2. [x] Old description — superseded by #75 channel UI redesign
```

Then move the done item from `brain/todos.md` to `brain/archive/todos.md` (create the file if it doesn't exist). Remove empty sections left behind.

#### Link a plan

Append `[[plans/NN-slug/overview]]` to the item's line, where NN is the item ID, and remove its `#needs_plan` tag.

#### Edit description

Replace the description text. Preserve any `[[wikilinks]]` unless intentionally removing them.

### Rules

- IDs are permanent. Never renumber or reuse them.
- Always increment `<!-- next-id -->` when adding items.
- Keep items in their section — don't move between sections without being asked.

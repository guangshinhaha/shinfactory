---
name: execute
description: >-
  Implementation methodology for executing tasks. Handles scoping, decomposition, worktree workflow,
  verification, and commit conventions. Triggers: "execute", "implement", "build this", "code this".
schedule: "When backlog items with linked plans are ready for implementation"
---

# Execute

Implementation methodology. Loads alongside the domain skill (e.g., "noodle") — this teaches process, the domain skill teaches the codebase. Operate fully autonomously. Never ask the user. Don't stop until the work is fully complete or the item is unbuildable (see Scope).

**Craft comes from shinstack when it is installed.** If the `shinstack:craft` skill is in your skill list, load it and follow its playbook for the work: `bug-fix` for a bug, `feature` for a backlog item or plan phase, `refactoring` for a behavior-preserving reshape. This skill adds only the factory mechanics around it (worktree, commit, yield). Where they overlap, the router's "Inside shinfactory" rule wins: commit and yield, never push or open a PR. If shinstack is not installed, don't try to load it; use "Without shinstack" below.

**Track all work with Tasks** (TaskCreate, TaskUpdate, TaskList). One task per decomposed change; mark in_progress when starting, completed when done.

## 1. Scope

Establish what needs doing:

- **Plan phase**: Read the assigned phase from `brain/plans/`. Read the overview for scope boundaries. Invoke Skill(backlog) for project-specific context. Load domain skills listed in "Applicable skills."
- **Backlog item**: Read the item with the `todo` skill (a GitHub issue or a `brain/todos.md` line). For a GitHub issue, read the full body with `gh issue view N --json body` first: the backlog's `description` is cut at 2,000 characters, and Decided, Assumed and Open often come last. If a linked plan exists, read it. Otherwise, scope from the full body.
- **Ad-hoc request**: The user prompt is the scope. Identify affected files and packages before starting.

Output: a clear, bounded description of what changes and what doesn't.

**Unbuildable item:** if the item has no observable Done when, or something only a person can settle blocks the build, don't guess and don't change any code. Fail the stage, name what's missing, and stop:

```bash
shinfactory event emit --session $NOODLE_SESSION_ID stage_fail --payload '{"message": "Unbuildable: <what is missing and who can settle it>"}'
```

The message becomes the issue comment word for word, so write one or two plain sentences that state the decision a person must make. The loop fails the stage, discards the worktree, comments once on the issue with your message and swaps its `ready` label for `needs-shin` (a GitHub backlog), and the scheduler sees the reason like any other stage failure. This is the one case where you stop before the work is complete.

## 2. Decompose

Break scope into discrete changes. Each change:
- One function/type + its tests, OR one bug fix
- Independently compilable
- One conventional commit

Single-change scopes skip decomposition.

## 3. Implement

### Worktree First — Non-Negotiable

**Never edit files on main.** Multiple sessions run concurrently; editing main causes merge conflicts and lost work.

If CWD is already inside `.worktrees/`, use it. Otherwise: `shinfactory worktree create <descriptive-name>`

Use absolute paths or `shinfactory worktree exec <name> <cmd>`. **Never `cd` into a worktree** — if it gets removed while the shell is inside, the session dies permanently.

Commit inside the worktree. If the loop gave you this worktree, stop at commit and yield; the loop merges it. Only merge worktrees you created yourself (e.g. team-execution phases): `shinfactory worktree merge <name>`

Skip only when the user is interactively driving a single-agent session and explicitly chooses main.

### Delegation

- **Self-execute**: Single change, or tightly coupled changes (shared types, sequential dependencies).
- **Sub-agents**: 2+ independent changes touching different files. Front-load context: scope, relevant code, domain skill name.
- **Team execution**: 2+ parallelizable phases in a plan. See `references/team-execution.md`.
- **Codex**: Mechanical work not requiring judgment (renames, boilerplate, repetitive edits). Never for architectural decisions.

Sequential is fine when phases are tightly coupled. Before parallelizing, ask: "Does any phase's output become another phase's input?" Phases with shared type-level contracts must be sequential.

## 4. Verify

Run the full verification suite before committing. See `references/verification.md` for the complete checklist. Never commit failing code — fix and re-verify on failure.

## 5. Commit

`<type>(<scope>): <description>` with optional `Refs: #<issue-ID>` footer. Types: `feat`, `fix`, `refactor`, `test`, `docs`, `chore`. One commit per logical change.

## 6. Yield

After all changes are committed and verified, emit `stage_yield`:

```bash
shinfactory event emit --session $NOODLE_SESSION_ID stage_yield --payload '{"message": "Implemented: <brief summary>"}'
```

Without this, the stage only completes on clean process exit — interrupted work is lost to the pipeline.

## Scope Discipline

- Only change what's in scope. No defensive code, backwards-compat shims, or speculative features.
- Out-of-scope discoveries go in the quality review notes — don't change them.
- Wrong or incomplete plan/requirements: flag it in output, don't silently deviate.

## Principles

With shinstack installed, read each of these in full before applying:
- prove-it-works
- laziness-protocol
- guard-the-context-window
- boundary-discipline
- sequence-verifiable-units

## Without shinstack

Same job, built in:

- **Reproduce first.** For a bug, make it fail before you fix it. For a feature, write the check that fails without it.
- **Smallest change.** Meet the Done when and nothing else. No new layers, flags or options nobody asked for.
- **Slices.** Build in small steps that each end in a passing state; run the repo's checks after each, then commit.
- **Validate at the edge.** Check input where it enters; inside, trust the types.
- **Commit and yield.** Never push, open a PR or comment on issues from a factory stage; the loop owns those.
- **Prove it.** Run the real thing, not only the tests, and paste the output into your stage message. A self-report is not proof.
- **Language.** Go: wrap errors with context (`fmt.Errorf("doing X: %w", err)`), accept interfaces and return structs, table-driven tests, no goroutine without an owner that waits for it. TypeScript/React: no `any`, derive state instead of syncing it, effects only for external systems, test what a user sees.

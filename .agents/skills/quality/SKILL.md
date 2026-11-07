---
name: quality
description: Post-cook quality gate. Tests completed cook work against the issue's Done when, then reviews correctness, scope discipline, and principle compliance. Accepts with a stage_message event and rejects with a stage_fail event. Triggers after execute stages, on '/quality', 'review this', 'check the work', 'assess quality'.
schedule: "Follow-up stage after execute. Cross-provider review preferred — if codex executed, schedule quality on claude; if claude executed, schedule on codex."
---

# Quality

Review completed cook work and emit your verdict as an event. See `references/stage-message-schema.md` for the event schemas.

Operate fully autonomously. Never ask the user.

## Step 0 -- Load Principles

Load the `shinstack:craft` skill and read the principles its router lists for this change, in full. These govern the review. Do NOT skip this.

**Block only on what you can show** (shinstack `interrogate` rule): a failing run, a bug a user would hit, a failed gate, a missed Done when, or a doc line the change made wrong. Everything else is a non-blocking issue.

## Assessment Pipeline

Run in order. Stop early and reject on any high-severity finding.

### 1. Done When

You test the work; the cook doesn't get to mark its own. Skip this step only for a standalone order with no backlog item.

- Read the order's backlog item with the `todo` skill (the order ID is the item ID). For a GitHub issue, read the full body with `gh issue view N --json body`, plus its linked plan if there is one. Find what it says is observable when done. Exact headings aren't required.
- Test each criterion yourself against the real thing: run the command, call the API, open the page. A cook's claim, a passing unit test, or a line of docs doesn't count when the criterion describes behavior.
- Reject if any criterion is missed, or if the item states nothing observable. Quote the criterion and what you observed.
- If a criterion needs something this environment doesn't have, say so in the verdict as unverified. Don't call it met.

### 2. Scope Check

- Read the plan phase the cook was assigned (from the cook's initial prompt or mise context).
- Run `git diff --stat` and `git log --oneline` for the cook's commits.
- Flag files changed outside the plan phase's stated scope as scope violations.

### 3. Code Quality

- The repo's own gates must pass (lint, typecheck, tests), as named in its CLAUDE.md or package scripts. In shinfactory itself: `pnpm check`.
- Check error handling follows project conventions (failure-state messages, not expectation messages).

### 4. Principle Compliance

- For each changed file, check against loaded principles.
- Common violations: missing verification (prove-it-works), an added layer or flag the change didn't need (laziness-protocol), a symptom patched instead of the cause (fix-root-causes).

### 5. Test Evidence

- New behavior must have new tests.
- Tests must assert outcomes, not implementation details.
- Check coverage of edge cases mentioned in the plan phase.

### 6. Runtime Verification

- If the plan phase specifies runtime checks, verify they were performed.
- "It compiles" is not verification.

## Verdict

**Accept**: Emit a `stage_message` with `blocking: false`. The pipeline continues and the scheduler sees the green light.

```sh
shinfactory event emit --session $NOODLE_SESSION_ID stage_message --payload '{ "message": "All checks pass. Done when met: ran ./greet, it prints hello. Tests green, scope clean.", "blocking": false }'
```

**Reject**: Emit a `stage_fail`. The stage and order fail, later stages don't run, the backlog item stays open, and the scheduler reads findings and decides whether to retry or abort. In `auto` mode the cook's commits are already on local main when you run, and a rejection doesn't revert them, so name the commits in your message. Nothing pushes them; a person does that.

```sh
shinfactory event emit --session $NOODLE_SESSION_ID stage_fail --payload '{ "message": "Rejected: 2 high issues. [1] Done when missed: \"running ./greet prints hello\", but it printed nothing. [2] Scope violation: modified cook_merge.go outside plan phase scope." }'
```

Write the message as natural language the scheduler can act on. Include specific file paths, line context, and principle violations so the scheduler can brief the retry cook.

## Non-blocking Issues

Issues that don't warrant rejection (style nits, minor improvements, documentation gaps): file as backlog items with the `todo` skill, each with a Goal, an observable Done when and an Out of scope. On GitHub they wait until a person adds the `ready` label.

## Principles

From shinstack: prove-it-works, test-behavior-not-implementation, fix-root-causes.

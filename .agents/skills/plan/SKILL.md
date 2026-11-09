---
name: plan
description: >-
  Systematic planning for medium-to-large tasks. Gathers context, identifies domain skills,
  writes phased plans to brain/plans/. Does NOT implement. Use for new features, multi-file
  refactors, or architectural changes — not small fixes. Triggers: "plan this", "break this down".
schedule: "When backlog items are tagged #needs_plan and have no linked plan yet"
---

# Plan

Produce implementation plans grounded in project principles. Write plans to `brain/plans/`. **Do NOT implement anything — the plan is the deliverable.**

## Autonomous Session Mode

When this skill runs in a non-interactive Noodle execution session (for example `Cook`, `Oops`, or `Repair`):

- **Skip Step 2** (AskUserQuestion) — the scope is fully defined in the initial prompt.
- **Skip Step 6's pause** — write the plan, commit it, emit `stage_yield` (see Step 6), and end the session. Do not wait for human review.
- **Step 4** — use installed skills only; note any missing domain skill in the plan instead of installing one.

All other steps proceed normally.

**Use Tasks to track progress.** Create a task for each step (TaskCreate), mark each in_progress when starting and completed when done (TaskUpdate). Check TaskList after completing each step.

## Step 0 — Triage Complexity

Before running the full planning workflow, assess whether this task actually needs a plan:

**Trivially small (1-2 files, obvious approach):**
Tell the user this task doesn't need a plan and suggest implementing directly without the plan skill. **Stop here — do not implement.**

**Needs planning (proceed to Step 1):**
- The change spans 3+ files or introduces new architecture
- There are multiple valid approaches and the user should weigh in
- The task has unclear scope or cross-cutting concerns
- The user explicitly asks for a plan

## Step 1 — Load Principles

If the `shinstack:craft` skill is in your skill list, load it and follow its `multi-phase-plan` playbook for how to slice the work; the files still go in `brain/plans/` in the template below. Read the principles the router lists for this change, in full. These principles govern all plan decisions: cite them by name in the plan overview and phase files. For types or module boundaries, sketch them as shinstack `architect` would, skipping its human checkpoint in autonomous mode.

If shinstack is not installed, don't try to load it. Slice the work so each phase ends in a checkable state and fits the sizing rule in Step 5b. Govern decisions by these principles and cite them by name instead: **sequence-verifiable-units** (every phase ends in something you can run and check), **laziness-protocol** (the smallest design that meets the goal; no layer nobody needs), **prove-it-works** (each phase names the command that proves it).

**Do NOT skip this. Do NOT use memorized principle content — always read fresh.** The self-check in Step 5b will verify citations exist.

## Step 2 — Define Scope and Constraints

Use `AskUserQuestion` to resolve ambiguity before exploring the codebase:

- What is in scope vs explicitly out of scope?
- Are there constraints (dependencies, platform requirements, existing patterns to preserve)?
- What does "done" look like?

Frame questions with concrete options. If the request is already clear, confirm scope boundaries briefly and move on.

## Step 3 — Explore Context with Subagents

**Always** delegate exploration to subagents via the `Task` tool. Never do large-scale codebase exploration in the main context.

Spawn exploration agents (subagent_type: `Explore`) to:
- Read existing code in affected areas
- Identify patterns, conventions, and dependencies
- Map architecture relevant to the change
- Find tests, types, and related infrastructure

Run multiple agents in parallel when investigating independent areas. For large explorations, use a team.

## Step 4 — Gather Domain Skills

Match installed skills and discover missing ones. See `references/domain-skills.md` for the routing table and discovery workflow.

## Step 5 — Write the Plan

Create the plan using file tools. See `references/templates.md` for the full directory structure, overview/phase file formats, verification strategy, and CLI scaffolding steps.

### Phase sizing

- **1 function/type + tests** per phase, or **1 bug fix** — not "one file" or "one component" (too variable)
- **Max 2-3 files touched** per phase when possible
- **Prefer 8-10 small phases** over 3-4 large ones — small phases keep future options open
- If a phase lists >5 test cases or >3 functions, split it

### Redesign check

For changes touching existing code, apply redesign-from-first-principles:
> "If we were building this from scratch with this requirement, what would we build?"

Don't bolt changes onto existing designs — redesign holistically.

### Alternatives check

For architectural decisions, briefly sketch 2-3 approaches in the overview's Constraints section. State which was chosen and why. This prevents premature commitment and documents the design space explored. With shinstack installed, see its `exhaust-the-design-space` (situational).

### Update plans index and backlog

NN is the backlog item ID (the issue number on a GitHub backlog). Verify both after writing the plan:
- `brain/plans/index.md` has `- [[plans/NN-plan-name/overview]]`
- the backlog item links the plan: use the `todo` skill (Link a plan)

No backlog item yet: add one with the `todo` skill first, then link the plan. Do NOT edit `brain/index.md` — it is generated (`scripts/brain-index.sh`) and gitignored.

### Archiving completed plans

Move directory to `brain/archive/plans/`, update `brain/plans/index.md`, mark todo done via the `todo` skill.

## Step 5b — Self-Check

After writing all plan files, verify these three constraints before proceeding. Fix any violations before moving on.

**Principles cited:** The overview must cite at least 2 principles by name. With shinstack installed, they come from its router's principle list; without it, from the three named in Step 1. If it cites fewer, add the most relevant ones to the overview's design decisions.

**Phase sizing:** Review each phase. If any phase touches >3 files or lists >5 test cases, split it into smaller phases. Count the files listed under "Changes" — if the list exceeds 3, the phase is too big.

**No code in phases:** Phase files must not contain Go/TS/Python code blocks. Name types and functions but do not define them. A phase should read like a brief to a senior engineer, not a diff or implementation spec. If you find code blocks, replace them with prose describing the intended shape.

## Step 6 — Present and Yield

Summarize the plan: list the phases, scope boundaries, applicable skills, and verification approach. Ask the user to review the plan files in `brain/plans/`.

**Emit `stage_yield`** to signal the deliverable is complete:

```bash
shinfactory event emit --session $NOODLE_SESSION_ID stage_yield --payload '{"message": "Plan written to brain/plans/NN-slug-name/overview.md"}'
```

This tells the Noodle backend the stage's work is done, even if the agent process hasn't exited yet. Without this, the stage only completes on clean process exit.

**Stop here.** Do not begin implementation. The user decides when and how to execute the plan.

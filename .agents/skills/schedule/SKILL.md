---
name: schedule
description: Orders scheduler. Reads .noodle/mise.json, writes .noodle/orders-next.json. Schedules work orders based on backlog state, plan phases, session history, and task type schedules. Triggers when orders are empty, after backlog changes, when the loop re-evaluates, or when /schedule is invoked.
schedule: "When orders are empty, after backlog changes, or when session history suggests re-evaluation"
---

# Schedule

Read `.noodle/mise.json`, write `.noodle/orders-next.json`.
The loop atomically promotes `orders-next.json` into `orders.json` — never write `orders.json` directly.
Use `shinfactory schema mise` and `shinfactory schema orders` as the schema source of truth.

Operate fully autonomously. Never ask the user to choose or pause for confirmation.

## One Plan at a Time

This is the cardinal scheduling rule. Pick the highest-priority plan with remaining phases and schedule all of them. Do not spread work across multiple plans — finishing one plan end-to-end produces shippable results; advancing many plans one phase each produces nothing usable. If the current plan is blocked, idle (empty orders) rather than context-switching to a different plan. Exception: shared infra orders can run alongside a plan's phases.

## Orders Model

Output is `{orders: [...]}` where each order is a **pipeline of stages** executed sequentially. Group related work into stages within one order rather than separate orders.

### Stages

Each stage has a `do` field (task key, must match a registered task type) and runs one at a time within the order. The loop advances to the next stage when the current one completes.

A typical order pipeline: execute, then quality, then debrief — all as stages of one order.

### Tier and the review gate

Set `"tier"` on every order that builds a backlog item: `none` (docs or inert config), `low`, `medium`, or `high`. Take it from the item's `risk:` label if it has one; if the tier is unclear, take the higher one. A missing tier counts as `medium`. The loop refuses to ship an order whose stages don't fit its tier:

- `none`: any stages.
- `low`, `medium`: `execute`, then `quality` (after the last `execute`).
- `high`: `execute`, then `quality`, then `verify` (a fresh verifier that writes a verdict at the branch head).

Nothing merges before the order's last stage. Findings from `quality` or a `FAIL` from `verify` send the order back to `execute` on the same branch, at most 2 times, then the order stops and lands in `action_needed`. Don't add stages to answer a rejection yourself.

### Work type

Set `"work_type"` on every order that builds a backlog item: `bug` (something broken), `feature` (new behaviour) or `refactor` (restructuring with no behaviour change). Take it from the item's labels first (`bug`, `feature`/`enhancement`, `refactor`), then from its title and description. Leave it off if you can't tell; don't guess. The loop gives the `execute` stage the matching shinstack playbook when shinstack is installed. Before it accepts `execute` it runs the repo's checks (the `Before a PR:` list in the repo's CLAUDE.md) and, for a `bug`, requires a commit that touches only tests before the fix commit. A failure there stops the stage; don't add stages to answer it.

## Task Types

Read `task_types` from mise to discover every schedulable task type and its `schedule` hint. Any registered task type can be a stage within an order. Use `do` on each stage to bind it to a task type.

### Execute Tasks

Schedule execute tasks from the `backlog` array in mise. Use the backlog item ID (as a string) as the order `id`.

Backlog items always have `id` and `title`. Other fields are adapter-defined and may vary. The todos.md adapter provides: `status`, `section`, `tags`, `estimate`, and `plan`. The GitHub adapter provides `status`, `tags` (labels), `plan`, `url`, and `description` (the issue body, cut at 2,000 characters) — use the description as the task brief. Custom adapters may include any fields — treat unknown fields as useful context.

**No Done when, no build.** Before scheduling any backlog item, check that the item or its linked plan states something observable that a person or test can check when the work is done. Exact headings aren't required: "Running `greet` prints hello" passes under any wording. A goal with nothing to check ("make onboarding better") fails. If the `description` has no Done when and is 2,000 characters long, read the full issue with the `todo` skill before deciding, because it may have been cut off.

An item that fails gets no order. Listing it in `action_needed` isn't enough, because only the loop reads that. Send it back yourself in this session, naming what's missing, so the person sees why. Find the backlog in `.noodle.toml` under `[adapters.backlog]`:

- **GitHub Issues** (the `sync` script is the ready-made `defaults/adapters/github` one, or calls `gh`): run the backlog `stop` script (`stop` under `[adapters.backlog.scripts]`, normally `defaults/adapters/github/backlog-stop`) from the project root, with the issue number as the argument and what's missing on stdin: `printf '%s' "<what's missing>. Fix the issue, then add the ${NOODLE_GH_LABEL:-ready} label again." | <stop script> <id>`. It comments once ("shinfactory: stopped. ..."), removes `ready` and adds `needs-shin`, the same handoff the loop uses. Never remove `ready` with `gh issue edit` yourself: the issue would show no `needs-shin`. If the backlog has no `stop` script, or the project has no remote, don't send back.
- **todos.md** (no `scripts`): add `#not_ready` and the reason to the item's line, for example `3. [ ] Make it faster #not_ready (shinfactory: not built, no observable Done when)`.
- **Any other backlog**: follow the `todo` skill's Send back.

Skip any item tagged `not_ready`. Items with no plan and items with a plan both go through this check, and every order that builds a backlog item ends in a `quality` stage, which tests the work against the Done when.

**One order, one ready issue.** Every order's `id` is the ID of an item in `backlog`, which holds only issues a person marked ready. The loop drops any other order and logs its ID, so an invented ID (`chore-...`, `fix-...`, `infra-...`) builds nothing. Shared infrastructure, a cleanup you noticed, a follow-up from a quality note or a fix after a failed merge are all new work nobody marked ready: file each as an issue with the `todo` skill (Goal, observable Done when, Out of scope; never the `ready` label) and leave it out of `orders-next.json`. A person decides whether it gets built.

**Items with plans:** When a backlog item has a `plan` field (a relative path like `brain/plans/29-foo/overview.md`), read the plan overview and phase files to understand the work. Schedule one order with a stage per remaining unfinished phase (each unchecked `- [ ]` item). Populate `order.plan` with the plan path(s). Use `extra_prompt` on each stage to inject plan context: the phase brief and any cross-phase dependencies.

**Parallelizing phases:** Read the plan to identify dependencies between phases. Phases that depend on earlier phases' output (shared types, APIs, schemas) must be sequential stages. Phases that touch independent areas of the codebase (separate packages, unrelated features, docs vs code) can be parallel stages within the same order. When in doubt, sequential is safer — but don't serialize work that has no real dependency.

**Items without plans:** Assess complexity before scheduling. If the item is straightforward (single concern, clear scope, small change), schedule as a simple execute task using the backlog item's title and description as the prompt. If the item is complex (multi-file, cross-cutting, ambiguous scope, or you'd want to see an architecture sketch before coding), schedule a plan-first order: a `prompt`-only stage (no `do`) that invokes `/plan`, followed by an `adversarial-review` stage to challenge the plan. No quality or debrief stages — planning output is a design document, not code. **Do NOT use `"do": "execute"` for plan-first stages** — the execute skill tells the agent to implement, which conflicts with the plan skill's "stop after planning" instruction. The plan skill will write phased plans to `brain/plans/`; on the next scheduling cycle, the item will have a `plan` field and can be scheduled normally with the standard execute → quality → debrief pipeline.

**Nothing to schedule:** When no backlog items are actionable (all blocked, all in-progress, all done, etc.), still write `orders-next.json` with an empty orders array (`{"orders":[]}`). This signals to the loop that scheduling ran but found nothing — preventing hot-loop re-spawns.

### Follow-Up Stages

Each task type's `schedule` field describes when and how to schedule it — as a follow-up stage within an order. A hint that asks for a standalone order cannot run: the order would name no ready issue. Read these hints from `task_types` in mise and compose orders accordingly.

## Recent Events

The mise brief includes a `recent_events` array — lifecycle events emitted by the loop since the last schedule run. These are context for your scheduling decisions, not commands. See [references/events.md](references/events.md) for the full event type catalog (internal and external).

### Using Events for Scheduling

Events are context, not commands. Consider them alongside backlog state and session history when deciding what to schedule:

- After `stage.failed` or `order.failed` — consider whether the item should be retried with a different approach. A failure that needs separate work (a bug in the loop, a broken merge) becomes a new issue without the `ready` label, not an order. If the reason starts with `Unbuildable:`, don't retry and don't comment: the loop has already told a person and swapped its `ready` label for `needs-shin`. The same holds after two failed rounds on one item. To retry a `quality` rejection, write a fresh order with the same ID that ends in `quality`. Never use `add-stage` or `advance` to get past a failed `quality` stage: the order would complete and mark the item done with the rejection unanswered.
- After `order.completed` — consider follow-up work (debrief, related items that were blocked).
- After `merge.conflict` — the affected order may need manual attention; avoid re-scheduling it immediately.
- After external events like `ci.failed` — consider scheduling an investigation or fix order if it seems actionable.
- After `registry.rebuilt` — new task types may be available; check `task_types` for scheduling opportunities.

Don't react mechanically to every event. Use judgment: a single stage failure in a long pipeline is normal; three consecutive failures of the same order suggests a deeper problem.

## Scheduling Heuristics

- **Explicit rationale**: Every order must cite which principle or rule drove its placement.
- **Timebox failures**: If an item has failed 2+ times in `recent_history`, deschedule or split it.

## Model Routing

Do not choose models. Leave `with` and `model` off your stages: the loop fills them from `[routing.roles]` in `.noodle.toml`, using each stage's role (`routing.roles` in the mise brief shows the table). A provider the config does not name is replaced by the role's.

## Runtime Routing

Leave `runtime` off your stages. The loop picks it from `[runtime] default` in `.noodle.toml`; setting `process` by habit would override a configured `orca` default and hide cook stages from the factory's Orca run.

Set `runtime` only when this order needs a specific one, and then state why in the stage's `extra`: `"extra": {"runtime_reason": "<why this order needs it>"}`. A stage with `runtime` but no `runtime_reason` is ignored when the config default is `orca`. The `sprites` runtime is still WIP; do not use it.

## Output

Write valid JSON to `.noodle/orders-next.json` matching `shinfactory schema orders`.

See [references/examples.md](references/examples.md) for order JSON examples and `extra_prompt` field usage.

## Principles

- [[cost-aware-delegation]]
- [[foundational-thinking]]
- [[subtract-before-you-add]]
- [[never-block-on-the-human]]
- [[guard-the-context-window]]

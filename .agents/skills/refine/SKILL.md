---
name: refine
description: >-
  Sharpen backlog items (GitHub issues or brain/todos.md) into precise, actionable prompts via targeted questioning.
  Triggers: "refine", "refine todos", "clarify todos".
---

# Refine

Read the backlog with the `todo` skill and refine unclear items into actionable prompts through targeted questioning.

## Workflow

**Use Tasks to track progress.** Create a task for each step below (TaskCreate), mark each in_progress when starting and completed when done (TaskUpdate). Check TaskList after each step.

1. **Read** the open backlog items with the `todo` skill.
2. **Triage** each item: is it clear enough to act on without further context? A clear item states its *Goal*, a *Done when* you can test, and what's *Out of scope*. Exact headings aren't required. Skip items that already meet this bar.
3. **Ask** clarifying questions about unclear items using `AskUserQuestion`. Group related questions — aim for one round of questions, not a back-and-forth (to minimize back-and-forth and user fatigue). Good clarifying questions target:
   - **Goal** — why does it matter, and what should be true afterwards?
   - **Done when** — what can someone run or observe to tell it's done? "Running the command with the flag does X" is good. "README mentions the flag" is weak, because words alone can pass it.
   - **Out of scope** — what must the agent not touch or add?
4. **Rewrite** unclear items in place with the `todo` skill (Edit), incorporating the user's answers, stating the Goal, the Done when and the Out of scope. In a GitHub issue use those as `##` headings. In a `brain/todos.md` item keep it to one line (`Goal. Done when: ... Out of scope: ...`), because a `##` line there starts a new section. Each rewritten item should be a self-contained prompt that another agent could act on without additional context. Don't add the `ready` label: that stays the person's decision.
5. **Close** any items the user says are already done or no longer relevant, with the reason (`todo` skill, Mark done).
6. **Report** a summary of what changed.

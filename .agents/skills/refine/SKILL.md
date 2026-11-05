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
2. **Triage** each item: is it clear enough to act on without further context? A clear item specifies *what* is wrong, *where* it happens, and *what the fix should be*. Skip items that already meet this bar.
3. **Ask** clarifying questions about unclear items using `AskUserQuestion`. Group related questions — aim for one round of questions, not a back-and-forth (to minimize back-and-forth and user fatigue). Good clarifying questions target:
   - **What** — what exactly is the problem or desired behavior?
   - **Where** — which part of the app, which component, which flow?
   - **How** — how should it work instead? What does "fixed" look like?
4. **Rewrite** unclear items in place with the `todo` skill (Edit), incorporating the user's answers. Each rewritten item should be a self-contained prompt that another agent could act on without additional context.
5. **Close** any items the user says are already done or no longer relevant, with the reason (`todo` skill, Mark done).
6. **Report** a summary of what changed.

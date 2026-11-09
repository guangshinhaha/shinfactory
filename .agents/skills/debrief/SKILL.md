---
name: debrief
description: >-
  Debrief a finished factory order and record what this project taught us in the brain.
  Use when wrapping up a session, after mistakes or corrections, or when significant
  codebase knowledge was gained. Triggers: "debrief", "remember this".
schedule: "Follow-up stage after quality — capture learnings from the completed execute→quality cycle."
---

# Debrief

Review the session and persist what it taught about **this project**: to `brain/`, to this repo's skill files, or as structural enforcement.

Debrief is project-scoped. Shinstack's `reflect` (when installed) is the separate, human-approved review that changes how agents work across every project. Don't edit shinstack from here.

## Process

**Use Tasks to track progress** (TaskCreate per step, TaskUpdate to in_progress/completed).

1. **Read `brain/index.md`** (generated, gitignored; run `sh scripts/brain-index.sh` if it is missing) to understand what notes already exist
2. **Scan the conversation** for:
   - Mistakes made and corrections received
   - User preferences and workflow patterns
   - Codebase knowledge gained (architecture, gotchas, patterns)
   - Tool/library quirks discovered
   - Decisions made and their rationale
   - Friction in skill execution, orchestration, or delegation
   - Repeated manual steps that could be automated or encoded
3. **Skip** anything trivial or already captured in existing brain files
4. **Route each learning** to the right destination (see Routing below)
5. **Never edit `brain/index.md`.** It is generated from the note files, so parallel orders never conflict on it. Run `sh scripts/brain-index.sh` if you want to see it refreshed

## Routing

Not everything belongs in the brain. Route each learning to where it will have the most impact.

### Structural enforcement check

Before routing a learning to `brain/`, ask: can this be a lint rule, script, metadata flag, or runtime check? If yes, encode it structurally and skip the brain note. (This is shinstack's `encode-lessons-in-structure` principle; it applies with or without the plugin.)

### Lessons about how to work in general (not this project)

A lesson that would apply to any project (a principle, a playbook gap, a shinstack file that was wrong or ignored) does not go in `brain/`. If shinstack is installed (the `shinstack:craft` skill is in your skill list), append one line to `~/.claude/shinstack-friction.md` (`date | file | what went wrong`, no code or private content). Shinstack's `reflect` reads it and proposes the edit for the human to approve. If shinstack is not installed, don't create that file: list the lesson under "General lessons" in your summary for the human instead.

### Brain files (`brain/`)

Codebase knowledge, gotchas, project decisions — anything about this project that informs future sessions. This is the default destination. Use the `brain` skill for writing conventions.

- **New learnings from an order go in one new file: `brain/notes/<order-id>.md`**, committed in that order's own PR. Never edit another order's note, and never edit `brain/index.md`. This is what keeps two parallel debriefs from conflicting. Use the order id (the branch name with `/` replaced by `-`, e.g. `guangshinhaha-issue-71`).
- Fixing or deleting an existing `brain/codebase/` note is fine; add new knowledge as the order's note.
- One order's note may hold several short bullets; keep each bullet one topic.
- Group in directories with index files using `[[wikilinks]]`.
- No inlined content in index files.

### Skill improvements (`.agents/skills/<skill>/`)

If a learning is about how a specific skill works — its process, prompts, or edge cases — update the skill directly. Use the `skill-creator` skill for guidelines on effective skill content.

### Orchestration workflow improvements

If the session revealed systemic orchestration issues, route to:
- **General delegation heuristic** → the shinstack friction line above (or the summary when shinstack is absent), not the brain
- **Skill mechanics change** (monitoring loop, review step) → relevant skill file
- **New skill opportunity** (recurring workflow that could be encoded) → note in summary for the user

### Backlog items

Follow-up work that can't be done during the debrief — bugs, non-trivial rewrites, tooling gaps. Use the `todo` skill to file.

## Summary

```
## Debrief Summary
- Brain: [files created/updated, one-line each]
- Skills: [skill files modified, one-line each]
- Structural: [rules/scripts/checks added]
- Todos: [follow-up items filed]
- Shinstack friction: [lines appended, one each; omit when shinstack is not installed]
- General lessons: [only when shinstack is not installed: lessons for the human to carry upstream]
```

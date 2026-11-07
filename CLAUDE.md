# Noodle

Open-source AI coding framework. Skills as the only extension point, LLM-powered scheduling, kitchen brigade model. Built with Go.

## Brain

The `brain/` directory is an Obsidian vault — persistent memory across sessions.

- **Read first.** Read brain files relevant to your task before acting.
- **Write** after mistakes, corrections, or notable codebase learnings.
- **Structure:** One topic per file. Directories with `[[wikilink]]` indexes — no inlined content.
- **Maintain:** Delete outdated notes. Clean completed/abandoned plans from `brain/plans/`.
- **Principles live in shinstack, not the brain.** The brain holds project knowledge only. For how to work (principles, playbooks), load the `shinstack:craft` skill.
- **Ownership:** gantry owns machine checks, shinstack owns how to work, and shinfactory owns when work runs and who runs it. Each works without the others. A check with no judgment goes in gantry. A rule that would still apply to one session with no loop goes in shinstack, not in a skill here. See the [boundary decision](https://github.com/guangshinhaha/shinstack/blob/main/docs/decisions/shinstack-vs-shinfactory.md).

## Workflow

- **User ordering:** Follow bullet/numbered list order. Don't reorder silently.
- **Isolation:** Multiple sessions run concurrently — use `Skill(worktree)` and `Skill(commit)`.

## Building

- **Build:** `pnpm build`, not bare `go build`.
- **Check:** `pnpm check` (full suite).

## Coding

- **Error messages:** Describe failure state ("session not found"), not expectations ("session must exist").
- **Cross-platform** (macOS/Windows/Linux): No bash 4+ features. Prefer POSIX shell or Go.
- **No backward compatibility** by default. No `omitempty` shims, no legacy fallbacks, no dual-path support. Only add compat when explicitly requested.

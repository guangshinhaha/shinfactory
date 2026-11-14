---
id: 206
created: 2026-11-14
status: active
---

# 206 — New repos run cooks as Orca workers by default

## Context

Issue #206. The Orca runtime works (`runtime/orca_runtime.go`, `[runtime.orca]`) but nothing turns it on for a new repo. The first-run template (`startup/firstrun.go`) and INSTALL.md emit no `[runtime]` block, so `Runtime.Default` falls to `process` (`config/parse.go`, `defaultRuntimeName`). Cooks then run as plain processes and the user cannot see them as terminals in Orca. Done when a repo created from the template shows its cooks in `orca orchestration worker-list` with no manual config.

## What is already true (verified in this worktree)

- **Schedule skill rule:** `.agents/skills/schedule/SKILL.md` ("Runtime Routing") already says to leave `runtime` off stages so the loop follows `[runtime] default`, and `stageRuntime` ignores a stage runtime without `runtime_reason` when the default is `orca`. The "hardcode process" line the issue describes is gone from this repo. The examples (`examples/*/.agents/skills/schedule/SKILL.md`) carry no runtime line. Nothing to change here except a check that no copy still hardcodes `process`.
- **Fallback when the orca CLI is missing:** `OrcaConfig.available()` (LookPath) gates `AvailableRuntimes()`; `stageRuntime` and `dispatchRuntime` resolve an unwired `orca` to `process` ([[codebase/runtime-routing-owned-by-orders]]). `TestOrcaRuntimeAbsentFallsBackToProcess` in `loop/orca_runtime_test.go` covers it. No code change expected; the plan adds a proof, not a feature.
- **This repo's own `.noodle.toml`** already sets `default = "orca"` plus an empty `[runtime.orca]`. It is the reference shape.

## Scope

In:
- The `[runtime]` / `[runtime.orca]` blocks in the generated first-run `.noodle.toml` (`startup/firstrun.go`) and in both INSTALL.md templates (Claude and Codex).
- The same blocks in the generated `noodle` skill's config example (`generate/skill_noodle.go`) and the docs (`docs/concepts/runtimes.md`, `docs/reference/configuration.md`), including the restart note.
- A guard test that the shipped templates parse to `runtime.default = "orca"`, and a proof that a template config on a machine without the orca CLI still dispatches on `process`.
- The `new-repo` skill mirror (see the open decision).

Out:
- Migrating existing repos (the issue says so).
- Changing the Go default of `Runtime.Default` (see Alternatives).
- Any new runtime behaviour or startup warning.

## Constraints

- No backward compatibility by default (CLAUDE.md): no shim, no dual-path template.
- Cross-platform: nothing shell-specific in the template guard.
- A restart stops running process-runtime cooks; the docs must say so.
- `new-repo` lives at `~/.claude/skills/new-repo/SKILL.md`, outside this repo, and today only applies gantry; it never writes a `.noodle.toml`. Edits there are not part of a shinfactory PR.

## Alternatives

1. **Write the blocks into templates (chosen).** Explicit, visible in the file the user reads, matches the issue and this repo's own config. Fallback is already in code. Smallest change.
2. **Flip the Go default to `orca` when the CLI resolves.** Zero config, but it silently changes every existing repo on upgrade (the issue excludes migration), and `AvailableRuntimes` needs `[runtime.orca]` defined anyway. Rejected.
3. **Make first-run probe for the orca CLI and write `orca` only if found.** Avoids a dead default on machines without Orca, but the runtime already falls back, so the probe adds a code path for no behaviour. Rejected (laziness-protocol).

## Principles

- **laziness-protocol:** the fallback and the schedule rule exist; only templates and docs change.
- **sequence-verifiable-units:** each phase ends in a command that checks it.
- **prove-it-works:** the Done-when check is a real `orca orchestration worker-list` run on a scratch repo (phase 5), not "it compiles".
- **encode-lessons-in-structure:** a template test keeps the default from drifting back out.

## Open decision (for Shin)

The `new-repo` skill does not set up shinfactory at all. Options: (a) add a step that runs `shinfactory start` init and relies on the new template (recommended: one source of truth, no copy of the TOML), (b) copy the two TOML blocks into the skill, (c) leave it, since INSTALL.md is the shinfactory entry point. Phase 4 is written for (a); the executor should stop and ask if Shin prefers otherwise, because it edits a global skill.

## Applicable skills

`skill-creator` (phase 4 edits a skill), `commit`, `worktree`, `verify`, `quality`. Missing: none.

## Phases

1. [[plans/206-orca-default-for-new-repos/phase-01-first-run-template]]
2. [[plans/206-orca-default-for-new-repos/phase-02-install-md]]
3. [[plans/206-orca-default-for-new-repos/phase-03-docs-and-noodle-skill]]
4. [[plans/206-orca-default-for-new-repos/phase-04-new-repo-skill]]
5. [[plans/206-orca-default-for-new-repos/phase-05-end-to-end-proof]]

## Verification

`pnpm check` before the PR; `pnpm build` for the binary. Per-phase commands are in each phase file.

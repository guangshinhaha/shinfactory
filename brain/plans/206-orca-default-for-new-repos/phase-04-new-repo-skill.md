# Phase 4 — new-repo skill mirror

Back to [[plans/206-orca-default-for-new-repos/overview]]

## Goal
`/new-repo` leaves a repo whose cooks will run on Orca, without copying the TOML.

## Changes
- `~/.claude/skills/new-repo/SKILL.md` (global, outside this repo; use `skill-creator`): after gantry setup, add an optional step that runs `shinfactory start` init when the repo has no `.noodle.toml`, relying on phase 1's template, and a report line stating the runtime. Confirm the option choice with Shin first (see the overview's open decision); do not edit if (c) is chosen.
- No repo file changes, so this phase is not in the PR. Record the edit in the PR description.

## Data structures
None.

## Routing
`builder`, then `reviewer` because it edits a global skill.

## Verification
- Static: the skill frontmatter still parses; `description` still triggers on the same phrases.
- Runtime: run `/new-repo` against a throwaway empty repo and show the resulting `.noodle.toml` contains the two blocks.

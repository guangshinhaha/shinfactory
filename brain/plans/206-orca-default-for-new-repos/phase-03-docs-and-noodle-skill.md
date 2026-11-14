# Phase 3 — Docs and generated noodle skill

Back to [[plans/206-orca-default-for-new-repos/overview]]

## Goal
Reference docs and the generated `noodle` skill agree with the new default and carry the restart caveat.

## Changes
- `generate/skill_noodle.go`: add the two blocks to the "Most projects only need" config example. Regenerate `.agents/skills/noodle/` through `generate/cmd/gen-skill` (do not hand-edit the output); the `noodle` skill is a skill, so use `skill-creator`.
- `docs/concepts/runtimes.md` and `docs/reference/configuration.md`: state that new repos default to `orca`, that a missing CLI falls back to `process`, and that a restart stops running process-runtime cooks.

## Data structures
None.

## Routing
`builder`.

## Verification
- Static: `go test ./generate/...` (golden or freshness test), `pnpm check`.
- Runtime: `git diff` after regenerating shows only the intended lines; rerun the generator and confirm a clean tree.

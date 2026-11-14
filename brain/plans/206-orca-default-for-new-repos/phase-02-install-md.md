# Phase 2 — INSTALL.md

Back to [[plans/206-orca-default-for-new-repos/overview]]

## Goal
An agent following INSTALL.md produces a config that runs cooks on Orca.

## Changes
- `INSTALL.md` step 3: add the `[runtime]` and `[runtime.orca]` blocks to both the Claude and Codex templates, with one sentence: needs the `orca` CLI, falls back to `process` when it is missing, and a restart stops running process-runtime cooks.
- Check INSTALL.md step 5 (schedule skill guidance) so it does not tell agents to set `runtime`; add a line pointing to the "Runtime Routing" rule if absent.

## Data structures
None.

## Routing
`builder`.

## Verification
- Static: `pnpm check` passes (docs lint if any).
- Runtime: extract each TOML fence from INSTALL.md in a scratch test or script and load it with the config loader; both must give `Runtime.Default == "orca"` with no diagnostics. Prefer a small Go test that reads INSTALL.md so the template cannot drift (encode-lessons-in-structure).

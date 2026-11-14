# Phase 1 — First-run template

Back to [[plans/206-orca-default-for-new-repos/overview]]

## Goal
`shinfactory start` in a repo with no config writes a `.noodle.toml` that selects the Orca runtime.

## Changes
- `startup/firstrun.go`: append `[runtime] default = "orca"` and an empty `[runtime.orca]` to the generated config, matching this repo's `.noodle.toml`.
- `startup/firstrun_test.go`: assert the file written by init parses (via the config loader) to `Runtime.Default == "orca"` and that `orcaDefined` is true through `AvailableRuntimes()` when a fake orca binary is on PATH.

## Data structures
Existing `config.RuntimeConfig` and `OrcaConfig`; none added.

## Routing
`builder`.

## Verification
- Static: `go vet ./startup/... ./config/...`; `go test ./startup/...`.
- Runtime: run init into a temp dir and print the file; the test fails first (red) before the template edit.
- Edge: with no orca on PATH, `AvailableRuntimes()` is `["process"]` (the fallback), asserted in the same test.

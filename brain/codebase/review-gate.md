# Review gate (PR landing mode)

- **Shape:** `execute -> quality -> verify (tier high) -> ship`. Ship is the last stage completing (see [[codebase/pr-landing-mode]]), so in PR mode nothing merges earlier. Code: `loop/review_gate.go`, called from `handleCompletion` before `completeWithPR`.
- **Tier** lives on `orderx.Order.Tier` (scheduler wire field `tier`; empty = medium). `none` skips the gate, `low`/`medium` need a `quality` stage after the last `execute`, `high` also needs `verify` after `quality`. A bad shape fails the order at its first stage completion.
- **Files**, per fix round, under `.noodle/gate/<order>/round-N/`: `review.md` (needs a `Covered:` line and a `Findings:` line; listed findings under `Findings: none` still count) and `verify.json` (`head_sha`, `verdict`, `evidence`; `head_sha` must equal the worktree head, re-checked when a later final stage ships). Missing or empty files fail the stage: not run.
- **Rounds:** findings or FAIL reopen stages `lastExecute..current` as pending in canonical state and `orders.json`, `Order.Round++`, same worktree and branch. Round 2 with findings fails the order and writes `action_needed`.
- **Local mode is not gated.** It merges per stage by design (the no-remote fallback). The gate only guards PR mode.
- Tests: `loop/review_gate_test.go` (fakeLander, plus one run through the real `ghLander` with the stub gh). Landing-mechanics tests set `Tier = "none"` so they stay about landing.

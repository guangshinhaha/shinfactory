---
name: adversarial-review
description: >-
  Adversarial code review using cross-model approach. Spawns reviewers on the opposing model
  (Claude uses Codex, Codex uses Claude) to challenge work from distinct critical lenses. Work
  involving sensitive data stays on the same provider, with synthetic data.
  Produces a synthesized verdict with findings and lead judgment. Triggers: "adversarial review".
schedule: "After cook sessions that produce large diffs (200+ lines), implement plan phases, or complete a planning session"
---

# Adversarial Review

Spawn reviewers on the **opposite model** to challenge work. Reviewers attack from distinct
lenses grounded in principles (shinstack's when installed, otherwise the built-in three in Step 1). The deliverable is a synthesized verdict — do NOT make
changes.

**Hard constraint:** Sensitive data never goes to another provider. Run the data check in Step 3
before anything reaches a reviewer.

When the data check is clear, reviewers MUST run via the opposite model's CLI (`codex exec` or
`claude -p`). Do NOT use subagents, the Agent tool, or any internal delegation mechanism as
reviewers — those run on *your own* model, which defeats the purpose.

## Step 1 — Load Principles

If the `shinstack:craft` skill is in your skill list, load it and read the principles its router lists for this work, in full. These govern reviewer judgments. If shinstack is not installed, don't try to load it; use these as the lenses' frame, and tell each reviewer to apply them:
- **prove-it-works:** a claim counts only when it was run or shown.
- **laziness-protocol:** flag layers, flags and options nobody asked for.
- **fix-root-causes:** flag a fix that hides the symptom while the cause remains.
Apply shinstack `interrogate`'s rule to every finding, with or without the plugin: it
counts only if the reviewer can show it (a failing run, a concrete input, a cited line), not
assert it.

## Step 2 — Determine Scope and Intent

Identify what to review from context (recent diffs, referenced plans, user message).

Determine the **intent** — what the author is trying to achieve. This is critical: reviewers
challenge whether the work *achieves the intent well*, not whether the intent is correct.
State the intent explicitly before proceeding.

Assess change size:

| Size | Threshold | Reviewers |
|------|-----------|-----------|
| Small | < 50 lines, 1-2 files | 1 (Skeptic) |
| Medium | 50-200 lines, 3-5 files | 2 (Skeptic + Architect) |
| Large | 200+ lines or 5+ files | 3 (Skeptic + Architect + Minimalist) |

Read `references/reviewer-lenses.md` for lens definitions.

## Step 3 — Check the Data, Detect Model, and Spawn Reviewers

**Data check.** Before building any prompt, decide whether the work is sensitive. It is sensitive
if either of these holds:

- Anything a reviewer would receive (diff, fixtures, logs, plans) contains personal data about
  students, staff or citizens, real or possibly real.
- The repo's `CLAUDE.md` or docs say the project handles such data, even if this diff has none.

If you cannot tell, treat it as sensitive.

**If sensitive:**

1. If the review can't work without real data, don't run any reviewers. Report the review as not
   run, and why, then stop.
2. Replace real data with synthetic data in everything the prompt contains.
3. Run reviewers on your own provider's CLI (Claude uses `claude -p`, Codex uses `codex exec`).
   Never use the opposite model's CLI.

**If clear:** spawn reviewers on the opposite model, as below.

Create a temp directory for reviewer output:

```sh
REVIEW_DIR=$(mktemp -d /tmp/adversarial-review.XXXXXX)
```

Determine which model you are. Unless the data check sent you to your own provider, spawn
reviewers on the opposite:

**If you are Claude** — spawn Codex reviewers via `codex exec`:

```sh
codex exec --skip-git-repo-check -o "$REVIEW_DIR/skeptic.md" "prompt" 2>/dev/null
```

Use `--profile edit` only if the reviewer needs to run tests. Default to read-only.
Run with `run_in_background: true`, monitor via `TaskOutput` with `block: true, timeout: 600000`.

**If you are Codex** — spawn Claude reviewers via `claude` CLI:

```sh
claude -p "prompt" > "$REVIEW_DIR/skeptic.md" 2>/dev/null
```

Run with `run_in_background: true`.

Name each output file after the lens: `skeptic.md`, `architect.md`, `minimalist.md`.

Build each reviewer's prompt using the template in `references/reviewer-prompt.md`.

## Step 4 — Verify and Synthesize Verdict

Before reading reviewer output, log which CLI was used and confirm the output files exist:

```sh
echo "reviewer_cli=codex|claude data_check=clear|sensitive"
ls "$REVIEW_DIR"/*.md
```

If the data check was sensitive, state in the verdict that reviewers shared your provider, so the
cross-model check did not happen.

If any output file is missing or empty, note the failure in the verdict — do not silently skip
a reviewer.

Read each reviewer's output file from `$REVIEW_DIR/`. Deduplicate overlapping findings.
Produce a single verdict using the format in `references/verdict-format.md`.

## Step 5 — Render Judgment

After synthesizing the reviewers, apply your own judgment. Using the stated intent and the principles from Step 1
as your frame, state which findings you would accept and which you would reject —
and why. Reviewers are adversarial by design; not every finding warrants action. Call out
false positives, overreach, and findings that mistake style for substance.

Append the Lead Judgment section to the verdict (see `references/verdict-format.md`).

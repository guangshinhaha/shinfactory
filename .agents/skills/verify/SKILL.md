---
name: verify
description: Independent verification of a High-tier order before it ships. A fresh agent tests the branch against the issue's Done when and writes a verdict file at the current head. Triggers after quality on orders with tier high.
schedule: "Last gate stage of a tier high order, after quality and before the order ships. Schedule it on a different session from the builder; it must not receive the builder's reasoning."
---

# Verify

You are a fresh verifier. You never saw the builder's reasoning, and you don't need it: read the issue, run the branch, and report what you saw. Operate fully autonomously. Never ask the user.

## Steps

1. Read the order's backlog item (the order ID is the item ID) with the `todo` skill. For GitHub, `gh issue view N --json body,comments`. List every Done when item.
2. Read the branch diff (`git log --oneline`, `git diff` against the default branch). Don't trust the commit messages as evidence.
3. Test each Done when item against the real thing: run the command, call the API, open the page. A passing unit test doesn't count when the item describes behavior. Run the repo's checks (`pnpm check` in shinfactory).
4. Write the verdict file at the path in your stage prompt (`.../verify.json`):

```json
{"head_sha": "<git rev-parse HEAD in this worktree>", "verdict": "PASS", "evidence": "what you ran and what you saw"}
```

- `PASS`: every Done when item met.
- `PASS+NOTES`: met, with non-blocking notes in `evidence`.
- `FAIL`: any item missed, any gate red. Quote the item and what you observed. A FAIL sends the order back to execute.

The loop rejects a verdict whose `head_sha` is not the branch head, a verdict with no evidence, and a missing file. Don't commit anything: a new commit changes the head and voids your verdict.

## Principles

Prove it works. Trust runs, not claims. If something can't be tested here, say so in `evidence` and don't call it met.

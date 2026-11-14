# Verify reruns once when ship finds no verdict at the head

- Ship found `verdict-check` red because no verdict comment existed at the head (lost in an outage), and retried every 10 minutes without ever posting one (#186).
- `ghLander.MarkReadyAndMerge` marks a red-check wait `pendingError.noVerdict` when `verdict-check` is red, GitHub's head equals the worktree head, and no comment has a `head_sha` field naming it next to a `verdict` field. Comments are fetched only on that path.
- `rerunVerifyForMissingVerdict` (`loop/review_gate.go`) reopens verify and any later stage, clears `verdict-posted`, and writes `verify-rerun` (the head) in the round dir last. The rerun posts the verdict, then ships. A FAIL goes through `sendBack` ([[codebase/review-gate]]).
- A second miss at the same head, a PR a human already approved, or a failed rerun falls back to the red-check wait and stop.
- A red check with a verdict at the head is the stale-check case ([[codebase/pr-landing-mode]], #129): it waits.

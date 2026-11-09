# Per-Order Spending Cap Gates The Launch, Not The Run

- `loop/order_spend.go`: `.noodle/order-spend.json` keys by order ID (a retry is a fresh order with the same ID, like `failed-rounds.json`). Launches count per stage task key at dispatch; cost sums `session.TotalCost()` when a non-schedule stage finishes (`applyStageResult`).
- `spawnCook` checks `[spend]` (`max_attempts` 3, `max_cost_usd` 20) before creating the worktree. Over a cap: `stopOverCap` fails the order, then `stopForPerson` comments once and swaps `ready` for `needs-shin` (see [[codebase/needs-shin-stop-path]]). The ledger clears on stop and on the order's `done`.
- Limit: cost is only known when a stage ends, so one stage can pass the ceiling before the order stops. Adopted sessions report cost 0.
- Caps of zero or less are off in the loop; `config` normalizes them to the defaults.

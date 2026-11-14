# orders-next result file

- The loop deletes `orders-next.json` seconds after promoting it and only logged the outcome, so a schedule worker saw "file gone, no event" and reported `failed` (#214).
- `handlePromotionResult` / `handlePromotionError` now write `.noodle/orders-next.result.json` (`status` promoted|rejected, `reason`, `amend_rejected`, `unbuildable`, `at`) before the proposal is removed. The schedule skill tells the worker to read it.

See also [[codebase/scheduler-active-order-amendments]]

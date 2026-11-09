# Orders Must Name A Ready Issue

- `consumeOrdersNext` drops any proposed order whose id isn't in the cycle's `brief.Backlog` (ready issues only) except the `schedule` order. Dropped ids emit an `orders.unready` loop event and accumulate in `droppedOrderIDs` (not `lastPromotionError`, which a clean promotion resets); the next schedule prompt lists them once and clears them. If nothing but the `schedule` order survives it is an empty promotion (5 minute cooldown).
- Why: trial 1 (#3) saw the scheduler invent `chore-*` / `fix-*` orders from a quality side note and a failed merge. A skill rule alone didn't stop it.
- Consequence: standalone orders (e.g. `meditate`) can't run from the scheduler. Discovered work is filed as an unlabeled issue with the `todo` skill.
- Tests that propose orders must also put those ids in the fixture's `mise_result.backlog`.

See also [[codebase/orders-lifecycle-defaults-on-promotion]]

# Routing Resolves At Dispatch Only

- Queued stages in `orders.json` carry no resolved provider or model. `Config.ResolveStageRouting` runs at dispatch (`cook_spawn.go`, `schedule.go`), so an edit to `[routing.roles]` reaches orders already queued.
- The running `cookHandle.stage` is stamped with what it was dispatched on, so snapshots and events still show it.
- `scheduleOrder` and the bootstrap oops order leave provider and model empty for the same reason.
- `routing.sensitive` is enforced three ways: config `Validate` (roles), `NormalizeAndValidateOrders` (stage providers in orders), and the enqueue / add-stage / edit-item control commands via `Config.ValidateStageProvider`.
- `routing.defaults.model` defaults to the claude model only when the provider is claude; codex without a model is a fatal `routing_defaults_missing`.
- `config.UnroutedTasks` lists bundled task types that run on `routing.defaults` on purpose; `internal/taskreg/routing_registry_test.go` fails when a bundled task type is in neither it nor `RoleForTask`.
- `Loop.dispatchSession` sets `DispatchRequest.DenyProviders` from `Config.ForeignProviders` on a sensitive repo; the dispatcher turns it into `--disallowedTools=` (the `=` form, because the flag is variadic and swallows the prompt) and `NOODLE_SENSITIVE`. Codex stages get a `claude` PATH shim (`dispatcher/deny_shims.go`). Do not add `Bash(*codex exec*)`-style globs: they block `grep 'codex exec'`. Orca runtime and custom `runtimeDefault` templates are not enforced.

See also [[codebase/runtime-routing-owned-by-orders]], [[codebase/provider-routing-must-fail-loudly]]

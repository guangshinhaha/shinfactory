# Stage Message Schema

Emitted by agents via `shinfactory event emit --session $NOODLE_SESSION_ID stage_message --payload '<json>'`.

```json
{
  "message": "string — content for the scheduler, natural language",
  "blocking": "boolean — if true, scheduler must decide before stage advances. Omit or null = true"
}
```

## Blocking Behavior

- **`blocking: true` (or omitted)** — loop forwards message to scheduler, does NOT auto-advance. Scheduler must issue a control command (advance, add stage, etc.) before the pipeline continues.
- **`blocking: false`** — loop auto-advances AND forwards the message to the scheduler for information. The scheduler sees it but doesn't need to act.

## Examples

Quality accept:
```json
{ "message": "All checks pass. Tests green, scope clean.", "blocking": false }
```

## Failing a stage

To fail a stage, emit `stage_fail` instead. A blocking `stage_message` only parks the order for review; `stage_fail` fails the stage and the order, stops later stages, discards the stage's worktree, and puts the reason in `recent_events` for the scheduler.

Emitted by agents via `shinfactory event emit --session $NOODLE_SESSION_ID stage_fail --payload '<json>'`.

```json
{ "message": "string — why the stage failed, natural language" }
```

Quality reject:
```json
{ "message": "Rejected: 2 high issues. [1] Done when missed: \"running ./greet prints hello\", but it printed nothing. [2] Scope violation: modified cook_merge.go outside plan phase scope." }
```

Execute on an unbuildable item:
```json
{ "message": "Unbuildable: the issue says \"make onboarding better\" with nothing to check. It needs an observable Done when from a person." }
```

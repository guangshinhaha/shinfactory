---
schema_version: 1
expected_failure: false
bug: false
source_hash: 33a7b6a54db85d578abcd217f5390517746a23a6cc5f26b51066ec39df8b65fa
---

## Runtime Dump

```json
{
  "states": {
    "state-01": {
      "transition": "running",
      "normal_task_scheduled": true,
      "spawn_calls": 2,
      "normal_spawn_calls": 2,
      "created_worktrees": 2,
      "active_summary_total": 2,
      "first_spawn": {
        "name": "order-existing-0-execute",
        "skill": "execute",
        "provider": "claude",
        "model": "claude-opus-4-6"
      }
    },
    "state-02": {
      "transition": "running",
      "normal_task_scheduled": true,
      "spawn_calls": 2,
      "normal_spawn_calls": 2,
      "created_worktrees": 2,
      "active_summary_total": 2,
      "first_spawn": {
        "name": "order-existing-0-execute",
        "skill": "execute",
        "provider": "claude",
        "model": "claude-opus-4-6"
      }
    }
  }
}
```

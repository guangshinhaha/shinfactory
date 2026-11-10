---
schema_version: 1
expected_failure: false
bug: false
source_hash: 7f6e7a1040e18dccb644487540e5ecbb1faa3a062d76fb1c6adbdc02385c75a1
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

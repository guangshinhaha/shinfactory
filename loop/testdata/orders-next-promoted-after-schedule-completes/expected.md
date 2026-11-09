---
schema_version: 1
expected_failure: false
bug: false
source_hash: 8b2f59426a05f7884979bf6d1c180a71ef99b30c4d0bb68d955f708302499fd4
---

## Runtime Dump

```json
{
  "states": {
    "state-01": {
      "transition": "running",
      "normal_task_scheduled": true,
      "spawn_calls": 1,
      "normal_spawn_calls": 1,
      "created_worktrees": 1,
      "active_summary_total": 1,
      "first_spawn": {
        "name": "plan-42-0-execute",
        "skill": "execute",
        "provider": "claude",
        "model": "claude-opus-4-6"
      }
    }
  }
}
```

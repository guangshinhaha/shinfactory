---
schema_version: 1
expected_failure: false
bug: false
source_hash: c5802f993b442e8b7170a733b2395c453b133e1bdb6d6b212e2624764477609f
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
        "name": "7-0-execute",
        "skill": "execute",
        "provider": "claude",
        "model": "claude-opus-4-6"
      }
    }
  }
}
```

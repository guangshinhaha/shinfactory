# Backlog States

Every issue in a factory repo is in one of five states. The state is set by a label, and an issue carries at most one state label.

| State | How it shows | Meaning |
| --- | --- | --- |
| Inbox | no state label | New, not yet sorted. |
| `needs-shin` | label | An order stopped and needs a decision from Shin. |
| `icebox` | label | Parked. Not for now. |
| `ready` | label | Approved to build. The factory picks work from here. |
| Closed | issue closed | Done. Closed on merge. |

## Rules

- One state label per issue.
- The factory reads only `ready`.
- When an order stops, the factory swaps `ready` for `needs-shin`.
- The factory closes the issue on merge.
- The coordinator moves every other label.

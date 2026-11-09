# Operating Model

How the factory runs day to day: what drives the work, who decides, and where you watch it.

## The parts

- **One line per repo.** Each repo has one line of work. Its `ready` issues are the queue.
- **The coordinator chat is the control panel.** It tracks the lines, collects `needs-shin` issues, and asks Shin about them one at a time. After Shin decides, the coordinator may add `ready` to an issue.
- **Orca shows the agents.** The runtime is `orca`.
- **Mode follows `shinstack-mode`.**
- **`max_concurrency` is 2.**

## Rolling out toolchain changes

Toolchain repos move on one at a time, after a trial. Shinfactory runs on a pinned build.

## Related

- [Backlog states](/concepts/backlog-states): the labels that decide what the factory picks up.

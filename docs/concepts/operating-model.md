# Operating Model

How the factory runs day to day: what drives the work, who decides, and where you watch it.

## The parts

- **One line per repo.** Each repo has one line of work. Its `ready` issues are the queue.
- **The coordinator chat is the control panel.** It tracks the lines, collects `needs-shin` issues, and asks Shin about them one at a time. After Shin decides, the coordinator may add `ready` to an issue.
- **Orca shows the agents.** The runtime is `orca`.
- **Mode follows `shinstack-mode`.**
- **`max_concurrency` is 2.**

## Where you watch it

There are three places, each with a different job.

- **The dashboard shows the line.** It shows each repo's orders, their stages, each stage's model, status and cost, and the controls (pause, drain, stop, steer). It reads the factory's own state, the same state `shinfactory status --json` shows.
- **Orca shows each agent.** On the `orca` runtime, each stage card on the dashboard links to its Orca worker. That link is being built in [#113](https://github.com/guangshinhaha/shinfactory/issues/113).
- **The coordinator chat is the combined view** across repos. A combined dashboard is iceboxed as [#120](https://github.com/guangshinhaha/shinfactory/issues/120).

### One dashboard per repo

Each repo's loop serves its own dashboard, on its own port. The port is set in the repo's `.noodle.toml`:

```toml
[server]
port = 3000
```

The default is `3000`. If that port is busy, the loop tries the next ports, up to 9 above it, and uses the first free one. Give each repo its own port so two dashboards don't end up on the same one.

### Feedback goes through the coordinator

Shin gives feedback through the coordinator chat. The dashboard's "talk to the scheduler" box is for emergencies only.

## Rolling out toolchain changes

Toolchain repos move on one at a time, after a trial. Shinfactory runs on a pinned build.

## Related

- [Backlog states](/concepts/backlog-states): the labels that decide what the factory picks up.

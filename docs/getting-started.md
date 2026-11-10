# Getting Started

shinfactory orchestrates AI coding agents using skills. You write skills, shinfactory schedules and runs them. Read the [Introduction](/introduction) first if you haven't. This page gets you from zero to a running shinfactory loop.

## Install

Give this prompt to your coding agent ([Claude Code](https://docs.anthropic.com/en/docs/claude-code), [Codex CLI](https://github.com/openai/codex), etc.):

```md
Install shinfactory and set up this project. Follow the instructions at
https://raw.githubusercontent.com/guangshinhaha/shinfactory/main/INSTALL.md
```

The agent installs the binary, creates a config, writes schedule and execute skills tailored to your project, seeds a backlog, and gets the loop running.

::: details Manual install

shinfactory has no published release yet, so build it from source. You need **Git**, **Go**, **Node** and **pnpm**.

```sh
git clone https://github.com/guangshinhaha/shinfactory.git
cd shinfactory
pnpm install
pnpm build
```

`pnpm build` writes the binary to `bin/shinfactory`. Put it on PATH.

Verify: `shinfactory --version`

You also need **Git** and at least one agent CLI. Then follow the steps in [INSTALL.md](https://github.com/guangshinhaha/shinfactory/blob/main/INSTALL.md) manually.

:::

## Run `shinfactory start` and watch it work

Run:

```sh
shinfactory start
```

This launches the shinfactory loop and a local web UI so you can monitor what's happening. The shinfactory loop works in three phases:

1. **Schedule**: the scheduler reads the backlog and writes orders.
2. **Execute**: shinfactory spawns an agent in its own worktree. The agent runs the assigned skill and commits.
3. **Merge**: in `auto` mode, completed work merges back automatically. In `supervised` or `manual` mode, the worktree is left for your review.

This keeps going until the backlog is empty or you stop it.

## Review the output

After an agent finishes:

- **Commits** appear on the agent's branch. Each agent gets its own worktree, so concurrent work stays isolated.
- **Web UI** shows a live event feed, the order queue, and stage status for each session. In `supervised` or `manual` mode, the reviews page lets you approve or reject work before it merges.
- **Backlog updates**: completed items get marked done in the backlog.

Run `shinfactory status` to see the current shinfactory loop state from the terminal.

## Next steps

- [FAQ](/reference/faq): common questions about shinfactory
- [Skills](/concepts/skills): how to write and compose skills
- [Scheduling](/concepts/scheduling): how the scheduler decides what to do
- [Brain](/concepts/brain): optional persistent memory vault
- [Glossary](/reference/glossary): quick reference for shinfactory terminology
- [Configuration](/reference/configuration): all config options
- [Cookbook](/cookbook/): patterns and recipes to copy

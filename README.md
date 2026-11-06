> Shin's fork of [poteto/noodle](https://github.com/poteto/noodle) (MIT), being reshaped into a software factory driven by [shinstack](https://github.com/guangshinhaha/shinstack) skills.

# shinfactory

Skill-based agent orchestration. Built in Go.

## shinfactory and shinstack

shinfactory decides when work runs and who runs it. [shinstack](https://github.com/guangshinhaha/shinstack) says how to work. shinfactory depends on shinstack, never the reverse.

| Concern | Owner |
|---|---|
| Stage order, scheduling, retries, and which provider or model runs a stage | shinfactory |
| Merge and PRs inside a factory order | shinfactory |
| Principles, playbooks, craft skills, review standards | shinstack |
| Push, PR, merge and deploy outside the factory | shinstack |

To place a new rule, ask: would it still apply to one person running one agent session with no loop? If yes, it goes in shinstack. If it needs a schedule, a stage or an event, it goes here. Full reasoning: [the decision record](https://github.com/guangshinhaha/shinstack/blob/main/docs/decisions/shinstack-vs-shinfactory.md).

## Install

Give this to your agent:

```md
Install Noodle and set up this project. Follow the instructions at
https://raw.githubusercontent.com/poteto/noodle/main/INSTALL.md
```

Or follow the [getting started guide](https://noodle-run.github.io/noodle/getting-started) to set up manually.

## Docs

Full documentation lives at [noodle-run.github.io/noodle](https://noodle-run.github.io/noodle/).

## Contributing

```sh
pnpm build    # build
pnpm check    # lint + test + vet
```

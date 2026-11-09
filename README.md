> Shin's fork of [poteto/noodle](https://github.com/poteto/noodle) (MIT), being reshaped into a software factory.

# shinfactory

Skill-based agent orchestration. Built in Go.

## How signals, gantry, shinstack and shinfactory fit together

Each of the four works alone. When they are used together, they do not overlap.

| Tool | Answers | Owns | How it acts |
|---|---|---|---|
| [signals](https://github.com/guangshinhaha/signals) | What outside fact becomes a task, and did the fix hold? | Events from a live product, the cause that groups them, the task it opens, and the confirm after a release | An agent reads and writes an API |
| [gantry](https://github.com/guangshinhaha/gantry) | Did it pass? | Local hooks, CI checks, the secret scan and the required `gantry / ci-ok` check | Runs checks with no judgment, and can block a merge |
| [shinstack](https://github.com/guangshinhaha/shinstack) | How should the agent work, and may it act? | Principles, playbooks, review depth, PR format and approval modes | Advises the agent |
| [shinfactory](https://github.com/guangshinhaha/shinfactory) | What runs next, and who runs it? | Stage order, retries, provider and model, and merges inside a factory order | Runs the loop once a task exists |

To place a new rule, ask these in order and stop at the first yes:

1. Is it a fact from a live product (feedback, an exception, a release), or a check that the cause went quiet after a release? It goes in signals.
2. Can a machine check it with no judgment? It goes in gantry. A check that only one repo needs goes in that repo's own workflow.
3. Does it need a schedule, a stage, or an event inside a factory order that already has a task? It goes in shinfactory.
4. Would it still apply to one person running one agent session with no loop? It goes in shinstack.

Signals opens the task and stops. shinfactory decides when that task runs and which agent runs it. gantry decides whether the checks pass. shinstack tells the agent how to work, and its `approval-modes` or shinfactory's `mode` decides whether a merge may happen. A merge still needs both a green check and an approval.

Full reasoning: [the decision record](https://github.com/guangshinhaha/shinstack/blob/main/docs/decisions/shinstack-vs-shinfactory.md).

Today, the `execute`, `plan`, `quality` and `adversarial-review` skills still rely on shinstack. See [#17](https://github.com/guangshinhaha/shinfactory/issues/17).

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

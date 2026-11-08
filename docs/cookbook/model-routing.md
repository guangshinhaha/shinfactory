# Model Routing

Not every task needs the same model. Formatting a file is a waste of Opus; planning and verifying are not. Noodle picks each stage's model by **role**, from `.noodle.toml`.

## Roles

```toml
[routing.defaults]       # fallback for task keys with no role
provider = "claude"
model = "claude-sonnet-5-5"

[routing.roles]
scheduler = { provider = "claude", model = "claude-haiku-5-5" }
debrief   = { provider = "claude", model = "claude-haiku-5-5" }
builder   = { provider = "claude", model = "claude-sonnet-5-5" }
explorer  = { provider = "claude", model = "claude-sonnet-5-5" }
reviewer  = { provider = "claude", model = "claude-sonnet-5-5" }
adversary = { provider = "claude", model = "claude-sonnet-5-5" }
planner   = { provider = "claude", model = "claude-opus-5-5" }
verifier  = { provider = "claude", model = "claude-opus-5-5" }
```

Those are the defaults; list only the roles you want to change. The loop maps a stage's task key to its role: `schedule`→scheduler, `plan`→planner, `execute`/`oops`→builder, `explore`→explorer, `quality`→reviewer, `adversarial-review`→adversary, `verify`→verifier, `debrief`→debrief. Any other task key uses `[routing.defaults]`.

The loop fills `provider` and `model` on every stage from its role. A stage that sets its own `model` keeps it only if its provider is one the config names.

## Other providers

A provider is used only when the config names it. To send builds to Codex:

```toml
[routing.roles]
builder = { provider = "codex", model = "gpt-5.4" }
```

Without that line, a stage asking for `codex` runs on its role's Claude model.

## Sensitive repos

```toml
[routing]
sensitive = true
```

Every stage stays on `routing.defaults.provider`. A role that names another provider is a fatal config error, and the loop overrides any stage that asks for one.

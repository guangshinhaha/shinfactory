# Skills fall back without shinstack

Shipped skills (`execute`, `plan`, `quality`, `adversarial-review`, `debrief`, `meditate`) load `shinstack:craft` only when it is in the skill list. Otherwise they use an inline "Without shinstack" block, and friction lines go to the summary instead of `~/.claude/shinstack-friction.md`.

`skill/standalone_test.go` fails any shipped skill that names `shinstack:` with no "not installed" or "isn't" fallback wording. Proven with `claude -p --settings '{"enabledPlugins":{"shinstack@shinstack":false}}'` on a throwaway Go repo (issue #17).

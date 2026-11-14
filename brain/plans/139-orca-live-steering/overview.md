---
id: 139
created: 2026-11-14
status: planned
---

# Orca live steering through Orca messaging

Back to [[plans/index]]

## Context

Orca workers are followed by polling. `orcaSession.Controller()` returns `NoopController()` (`dispatcher/orca_session.go`) and `rtcap.OrcaCaps` has `CapSteerable: false`. #158 made the dashboard refuse a steer on such a worker. Two things follow:

- A dashboard steer on an Orca stage is refused (`refuseUnsteerable`, `loop/cook_steer.go`) and the UI shows `OrcaSteerNotice`.
- `forwardToScheduler` (`loop/cook_completion.go`) drops its message when the scheduler runs on Orca ("scheduler not steerable"), so the scheduler never hears stage events and the order sits in `orders.json` until the next recovery.

Goal: an `orcaController` that delivers a message to a live worker through Orca's own messaging, so both paths work on Orca.

## Verb decision: `send`, not `ask`

`orca orchestration --help` lists both.

- `ask` is worker to coordinator ("Ask the coordinator a question; Orca wakes you with the answer"). The factory is the coordinator that owns the run, so `ask` points the wrong way. It cannot address a worker.
- `send --to dispatch:<id>` is coordinator to worker. Its help says: "Use --to dispatch:<id> for attempt-specific coordinator guidance; Orca durably relays it to a connected worker server." That is a steer.
- `reply` only answers a worker's `question`/`ask` message by id. The factory has no such message to answer, so it does not apply.

So `SendMessage` runs `orca orchestration send --to dispatch:<dispatchID> --subject <short> --body <prompt> --json`. The exact `--type` and whether `--from` is needed are not documented. Phase 1 settles both by experiment.

## Open facts to prove before building (phase 1)

1. Does mail from `send` reach a worker that is **mid-turn**, or only when it next runs `check`? The worker preamble says to message the coordinator only to ask or finish and never says it polls its inbox. If delivery is passive, Orca messaging is not a steer and the plan stops here (report back instead of flipping the flag).
2. Does it reach an **idle** (parked) worker and wake it?
3. Which `--type` value arrives as readable guidance (`status`, `dispatch`, `handoff`)?
4. Is `--from` required when the sender is not a Run member?

## Design constraint: no interrupt verb

Orca offers no interrupt. `worker-stop` fences and kills. `AgentController` has `Interrupt` and callers treat it as live:

- `steerLive` returns early when `Interrupt` errors, so `Interrupt` must not return `ErrNotSteerable` once `Steerable()` is true, or steers silently die.
- `controlStop` calls `Interrupt` for any steerable session and keeps the session alive. For Orca a no-op `Interrupt` would turn Stop into a silent no-op.

Decision: `orcaController.Interrupt` is a documented no-op that returns nil, and the interface gains a capability so `controlStop` still kills Orca workers: add `CapInterruptible` to `rtcap` (false for Orca) and make `controlStop` pick the kill path when a session is steerable but not interruptible. Steer on Orca is then "queue guidance for the running turn", with no cut-off. The dashboard label must say so.

## Phases

1. [[plans/139-orca-live-steering/phase-01-delivery-spike]] — prove the four open facts against a real Orca worker.
2. [[plans/139-orca-live-steering/phase-02-controller]] — `orcaController`, `Steerable()` flip, `CapInterruptible`, stop path.
3. [[plans/139-orca-live-steering/phase-03-scheduler-forwarding]] — `forwardToScheduler` on Orca, recovered sessions.
4. [[plans/139-orca-live-steering/phase-04-dashboard]] — steer boxes, notice, docs.

Each phase is one commit that passes `pnpm check`. Phase 1 gates the rest.

## Out of scope

- Steering Cursor or Sprites workers.
- A real interrupt (needs an Orca feature).
- Reliable ordering and delivery receipts for forwarded events. `orders.json` stays the recovery source of truth.

## Risks

- Passive delivery (fact 1) invalidates the feature. Mitigation: phase 1 first.
- `forwardToScheduler` spawns one goroutine per message, so messages can arrive out of order. Orca mail is durable but the goroutines race. Fix in phase 3 by serializing sends per target.
- Steer text goes only to the local Orca CLI, not an external service, so no PDPA change. Keep it that way: no logging of the prompt body at info level.

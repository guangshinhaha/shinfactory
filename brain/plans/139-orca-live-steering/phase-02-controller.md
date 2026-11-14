Back to [[plans/139-orca-live-steering/overview]]

# Phase 2 — orcaController and the Steerable flip

## Goal

An Orca session accepts `SendMessage`, reports `Steerable() == true`, and Stop still kills it.

## Changes

- `dispatcher/orca_controller.go` (new): `orcaController{run orcaRunner, bin, dispatchID string}`.
  - `SendMessage`: `send --to dispatch:<id>` with the type from phase 1. Return the CLI error with stderr, in the failure-state style ("orca send failed: ...").
  - `Interrupt`: no-op, returns nil, comment explains Orca has no interrupt.
  - `Steerable`: true.
- `dispatcher/orca_session.go`: `Controller()` returns it. Refuse (`ErrNotSteerable`) once the session finished, so a late steer is not sent to a fenced dispatch.
- `internal/rtcap`: `OrcaCaps.CapSteerable = true`; add `CapInterruptible` (true for process and sprites, false for Orca and Cursor); `CanInterrupt` helper.
- `loop/control.go` `controlStop`: use the kill path when `!Steerable() || !CanInterrupt(runtime)`.
- `loop/cook_steer.go`: `refuseUnsteerable` now passes Orca through, so `steerCook` takes the live branch (`steerLive`).

## Tests

- Fake `orcaRunner` asserts the exact argv for `SendMessage` and the error text on failure.
- Update the #158 tests that assert Orca steer is refused: they now assert the message is sent and the worker is not killed.
- `controlStop` on an Orca cook still calls `ForceKill`.
- Finished session returns `ErrNotSteerable`.

## Verification

`pnpm check`.

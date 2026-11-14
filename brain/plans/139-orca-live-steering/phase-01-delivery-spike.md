Back to [[plans/139-orca-live-steering/overview]]

# Phase 1 — Prove Orca mail steers a live worker

## Goal

Answer the four open facts in the overview with real output, before any code lands.

## Changes

No production code. A throwaway script under the scratchpad that:

1. Starts a worker with `orca orchestration worker-start` on a trivial long task (a loop that sleeps and prints).
2. Sends `orca orchestration send --to dispatch:<id>` with each candidate `--type` while the worker is mid-turn, then again while it is idle.
3. Reads `orca orchestration worker-read` to see whether and where the message appears in the transcript.
4. Repeats once without `--from`.

Record results in `brain/codebase/orca-send-reaches-worker.md` (one fact per file).

## Decision gate

- Delivered mid-turn: continue to phase 2.
- Delivered only on idle or on `check`: stop. Report `stage_fail`-free yield with the finding, and ask the coordinator whether to add a poll instruction to the worker preamble instead.

## Verification

The note holds pasted `worker-read` output for each case.

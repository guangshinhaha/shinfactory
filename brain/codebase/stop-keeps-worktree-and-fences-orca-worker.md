# Stop Keeps The Worktree; A Failed Orca worker-stop Is Not Killed

- `controlStopKill` (`loop/control.go`) ForceKills a non-steerable session, then `parkStoppedCook` fails the stage with reason `stopped by user` and keeps the worktree and branch in every landing mode. `controlRequeue` resumes it. Before #152 it called `cleanupCookWorktree`, which force-removed the worktree outside PR mode.
- `orcaSession.Terminate` reports Killed only after `orca orchestration worker-stop` succeeds. On failure it returns the error and the session keeps polling, so the cook stays active and nothing spawns a second worker into the same worktree. The poll loop skips a settled worker only while `terminating` is set; a failed stop clears it.
- The 12-poll "lost contact" fence in `orcaSession.poll` also stays live when its `worker-stop` fails (resets the failure count, fences again after another 12 failed polls). `cancelSupersededCook` returns the `ForceKill` error before untracking, re-pending or cleaning the worktree; the error surfaces through `handlePromotionError`. Gap: promotion is the only caller, so a failed supersede is not retried by a later cycle. The cook stays tracked and ends through its own session.
- Ctx-cancel in `poll` (shutdown) still ignores a failed stop on purpose.
- Test fake: `fakeorca.FailStop` makes worker-stop exit non-zero; `fakeorca.FailList` makes worker-list fail.

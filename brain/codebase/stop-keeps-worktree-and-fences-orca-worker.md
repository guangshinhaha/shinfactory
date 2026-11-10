# Stop Keeps The Worktree; A Failed Orca worker-stop Is Not Killed

- `controlStopKill` (`loop/control.go`) ForceKills a non-steerable session, then `parkStoppedCook` fails the stage with reason `stopped by user` and keeps the worktree and branch in every landing mode. `controlRequeue` resumes it. Before #152 it called `cleanupCookWorktree`, which force-removed the worktree outside PR mode.
- `orcaSession.Terminate` reports Killed only after `orca orchestration worker-stop` succeeds. On failure it returns the error and the session keeps polling, so the cook stays active and nothing spawns a second worker into the same worktree. The poll loop skips a settled worker only while `terminating` is set; a failed stop clears it.
- Not covered: the 12-poll "lost contact" fence in `orcaSession.poll` and `cancelSupersededCook` still ignore a failed stop (the latter still cleans the worktree).
- Test fake: `fakeorca.FailStop` makes worker-stop exit non-zero.

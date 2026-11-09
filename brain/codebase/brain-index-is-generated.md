# Brain index is generated

- `brain/index.md` is gitignored and built by `scripts/brain-index.sh` from the files on disk. Nobody edits it.
- Debrief writes `brain/notes/<order-id>.md`, one new file per order, in that order's own PR. Parallel orders add different files, so they merge clean (trial 1 #3 conflicted on the old hand-edited index).
- Session start and the PostToolUse hook regenerate the index; a fresh worktree has none until then.
- Fixture: `brain_index_test.go` merges two orders' notes branches.

package loop

import (
	"encoding/json"
	"path"
	"strings"
)

// A stage names the shared files it writes in extra.writes, a list of paths
// relative to the repository root (for example ["brain/index.md"]). The
// scheduler never runs two stages that name the same file at the same time:
// the later one waits until the earlier one finishes. Different orders work in
// different worktrees, so without this two of them can both rewrite one file
// and conflict at ship.
const stageWritesKey = "writes"

// stageWrites returns the normalized shared paths a stage declares. A value
// that is not a list of strings is ignored.
func stageWrites(s Stage) []string {
	raw, ok := s.Extra[stageWritesKey]
	if !ok {
		return nil
	}
	var paths []string
	if err := json.Unmarshal(raw, &paths); err != nil {
		return nil
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		p = path.Clean(strings.ReplaceAll(strings.TrimSpace(p), "\\", "/"))
		p = strings.TrimPrefix(p, "./")
		if p != "" && p != "." {
			out = append(out, p)
		}
	}
	return out
}

// sharedWriteHolders maps each shared path to the order whose running stage
// holds it.
func (l *Loop) sharedWriteHolders() map[string]string {
	held := map[string]string{}
	for orderID, cook := range l.cooks.activeCooksByOrder {
		for _, p := range stageWrites(cook.stage) {
			held[p] = orderID
		}
	}
	return held
}

// withoutWriteClashes drops candidates whose stage writes a shared file that a
// running stage, or an earlier candidate in the same batch, already holds.
func (l *Loop) withoutWriteClashes(candidates []dispatchCandidate) []dispatchCandidate {
	held := l.sharedWriteHolders()
	kept := candidates[:0:0]
	for _, c := range candidates {
		writes := stageWrites(c.Stage)
		clash := ""
		for _, p := range writes {
			if holder, ok := held[p]; ok && holder != c.OrderID {
				clash = p
				break
			}
		}
		if clash != "" {
			l.logger.Info("stage waits for a shared file", "order", c.OrderID, "stage", c.StageIndex, "file", clash, "held_by", held[clash])
			continue
		}
		for _, p := range writes {
			held[p] = c.OrderID
		}
		kept = append(kept, c)
	}
	return kept
}

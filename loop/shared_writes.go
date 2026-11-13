package loop

import (
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"
)

// A stage names the shared files it writes in extra.writes, a list of paths
// relative to the repository root (for example ["brain/index.md"]). The
// scheduler never runs two stages that name the same file at the same time:
// the later one waits until the earlier one finishes. Different orders work in
// different worktrees, so without this two of them can both rewrite one file
// and conflict at ship.
const stageWritesKey = "writes"

// wholeRepoWrite is the normalized form of a bare "." in extra.writes: the
// stage may write anywhere, so it overlaps every other path.
const wholeRepoWrite = "."

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
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		p = path.Clean(strings.ReplaceAll(p, "\\", "/"))
		p = strings.TrimPrefix(strings.TrimPrefix(p, "./"), "/")
		switch {
		case p == "" || p == ".":
			// A bare "." names the whole repository.
			out = append(out, wholeRepoWrite)
		case p == ".." || strings.HasPrefix(p, "../"):
			// A path outside the repository can't clash with another order's file.
		default:
			out = append(out, p)
		}
	}
	return out
}

// writeHold is one shared path and the order whose stage holds it.
// parked is true when the holder waits in pending review.
type writeHold struct {
	path, orderID string
	parked        bool
}

// sharedWriteHolders lists the paths held by stages that are live: running,
// adopted from an earlier run, or parked in pending review. A parked order
// still owns its worktree and will ship or resume it, so its files stay held.
func (l *Loop) sharedWriteHolders() []writeHold {
	var held []writeHold
	add := func(orderID string, s Stage, parked bool) {
		for _, p := range stageWrites(s) {
			held = append(held, writeHold{p, orderID, parked})
		}
	}
	for orderID, cook := range l.cooks.activeCooksByOrder {
		add(orderID, cook.stage, false)
	}
	// A parked cook is rebuilt from canonical state after a restart without
	// its stage's Extra, so read the stage from orders.json, falling back to
	// the cook's own copy.
	for orderID, cook := range l.cooks.pendingReview {
		stage := cook.stage
		if order, ok := l.orderByID(orderID); ok && cook.stageIndex < len(order.Stages) {
			stage = order.Stages[cook.stageIndex]
		}
		add(orderID, stage, true)
	}
	// An adopted session runs the order's first active stage.
	for orderID := range l.cooks.adoptedTargets {
		if _, running := l.cooks.activeCooksByOrder[orderID]; running {
			continue
		}
		order, ok := l.orderByID(orderID)
		if !ok {
			continue
		}
		for _, st := range order.Stages {
			if st.Status == StageStatusActive {
				add(orderID, st, false)
				break
			}
		}
	}
	return held
}

// writePathsOverlap reports whether two normalized shared paths name the same
// file or one is a directory holding the other. Case is folded: holding back a
// stage on a case-sensitive file system costs a wait, while missing a clash on
// a case-insensitive one costs a conflict at ship.
func writePathsOverlap(a, b string) bool {
	if a == wholeRepoWrite || b == wholeRepoWrite {
		return true
	}
	a, b = strings.ToLower(a), strings.ToLower(b)
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// clashWith returns the held path that overlaps p and is held by another
// order, or "".
func clashWith(held []writeHold, p, orderID string) (writeHold, bool) {
	for _, h := range held {
		if h.orderID != orderID && writePathsOverlap(h.path, p) {
			return h, true
		}
	}
	return writeHold{}, false
}

// parkedHoldMarker is the middle of every parked-hold action_needed line, so
// clearEndedParkedHolds can tell them from other lines.
const parkedHoldMarker = ", which is parked in pending review: approve, reject or skip order "

func parkedHoldLine(waiter string, h writeHold) string {
	return fmt.Sprintf("order %s waits for %s, held by order %s%s%s", waiter, h.path, h.orderID, parkedHoldMarker, h.orderID)
}

// clearEndedParkedHolds removes the parked-hold lines whose hold is gone: the
// parked order left review, or the waiter no longer wants the file. live holds
// the lines still true this cycle.
func (l *Loop) clearEndedParkedHolds(live map[string]bool) {
	orders, err := l.currentOrders()
	if err != nil {
		l.logger.Warn("could not read action_needed to clear ended holds", "error", err)
		return
	}
	if !slices.ContainsFunc(orders.ActionNeeded, func(s string) bool { return strings.Contains(s, parkedHoldMarker) && !live[s] }) {
		return
	}
	err = l.mutateOrdersState(func(o *OrdersFile) (bool, error) {
		o.ActionNeeded = slices.DeleteFunc(o.ActionNeeded, func(s string) bool { return strings.Contains(s, parkedHoldMarker) && !live[s] })
		return true, nil
	})
	if err == nil {
		err = l.persistCanonicalCheckpoint()
	}
	if err != nil {
		l.logger.Warn("could not clear ended parked-hold lines", "error", err)
	}
}

// flagParkedHold tells a person that a parked order is holding up another
// stage. A parked order waits on a human and holds its files until then; the
// hold has no timeout, because releasing it early brings back the conflict at
// ship that the hold prevents. One line per pair, however many cycles it waits.
func (l *Loop) flagParkedHold(waiter string, h writeHold) {
	line := parkedHoldLine(waiter, h)
	orders, err := l.currentOrders()
	if err == nil && slices.Contains(orders.ActionNeeded, line) {
		return
	}
	if err := l.flagActionNeeded(line); err != nil {
		l.logger.Warn("could not flag a parked shared-file hold", "order", waiter, "held_by", h.orderID, "error", err)
	}
}

// withoutWriteClashes drops candidates whose stage writes a shared path that a
// live stage, or an earlier candidate in the same batch, already holds.
func (l *Loop) withoutWriteClashes(candidates []dispatchCandidate) []dispatchCandidate {
	held := l.sharedWriteHolders()
	kept := candidates[:0:0]
	live := map[string]bool{}
	defer func() { l.clearEndedParkedHolds(live) }()
	for _, c := range candidates {
		writes := stageWrites(c.Stage)
		var clash writeHold
		clashed := false
		for _, p := range writes {
			if clash, clashed = clashWith(held, p, c.OrderID); clashed {
				break
			}
		}
		if clashed {
			l.logger.Info("stage waits for a shared file", "order", c.OrderID, "stage", c.StageIndex, "file", clash.path, "held_by", clash.orderID)
			if clash.parked {
				live[parkedHoldLine(c.OrderID, clash)] = true
				l.flagParkedHold(c.OrderID, clash)
			}
			continue
		}
		for _, p := range writes {
			held = append(held, writeHold{p, c.OrderID, false})
		}
		kept = append(kept, c)
	}
	return kept
}

package loop

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/guangshinhaha/shinfactory/internal/filex"
	"github.com/guangshinhaha/shinfactory/internal/ingest"
)

// A conflict at ship is cleared once by rebasing the order's branch onto the
// current base and running review (and verify, for high tier) again on the new
// head. If the rebase conflicts, or the branch conflicts again after it, the
// order stops with the conflicting files named. Nothing retries it silently.
//
// The count is per order, not per base: a conflict long after the one rebase
// still stops the order, even if the base has moved on since. That keeps
// "once" simple and bounded; a person reshapes the issue and puts it back.

// rebaseTimeout bounds the fetch, rebase and push of one rebase.
const rebaseTimeout = 3 * time.Minute

// rebasedFile counts rebases per order. A retry is a fresh order with the same
// ID, so the count lives beside orders.json, not in it.
func (l *Loop) rebasedFile() string {
	return filepath.Join(l.runtimeDir, "rebased.json")
}

var rebasedMu sync.Mutex

func (l *Loop) readRebased() map[string]int {
	counts := map[string]int{}
	data, err := os.ReadFile(l.rebasedFile())
	if err != nil {
		return counts
	}
	if err := json.Unmarshal(data, &counts); err != nil {
		l.logger.Warn("rebased.json unreadable; counting from zero", "error", err)
		return map[string]int{}
	}
	return counts
}

func (l *Loop) writeRebased(counts map[string]int) error {
	data, err := json.Marshal(counts)
	if err == nil {
		err = filex.WriteFileAtomic(l.rebasedFile(), data)
	}
	return err
}

func (l *Loop) rebaseCount(orderID string) int {
	rebasedMu.Lock()
	defer rebasedMu.Unlock()
	return l.readRebased()[orderID]
}

// bumpRebased records a rebase. It must succeed: an unrecorded rebase would
// let the next conflict rebase again, and "once" would mean nothing.
func (l *Loop) bumpRebased(orderID string) error {
	rebasedMu.Lock()
	defer rebasedMu.Unlock()
	counts := l.readRebased()
	counts[orderID]++
	return l.writeRebased(counts)
}

// unbumpRebased takes back a bumpRebased for a rebase that did not run.
func (l *Loop) unbumpRebased(orderID string) {
	rebasedMu.Lock()
	defer rebasedMu.Unlock()
	counts := l.readRebased()
	if counts[orderID] <= 1 {
		delete(counts, orderID)
	} else {
		counts[orderID]--
	}
	if err := l.writeRebased(counts); err != nil {
		l.logger.Warn("could not take back the rebase count", "order", orderID, "error", err)
	}
}

func (l *Loop) clearRebased(orderID string) {
	rebasedMu.Lock()
	defer rebasedMu.Unlock()
	counts := l.readRebased()
	if _, ok := counts[orderID]; !ok {
		return
	}
	delete(counts, orderID)
	if err := l.writeRebased(counts); err != nil {
		l.logger.Warn("could not clear rebase count", "order", orderID, "error", err)
	}
}

// reviewStageToReopen is the first review stage after the order's last execute
// stage, up to the stage that tried to ship, or -1 when the tier needs no
// review.
func reviewStageToReopen(order Order, shipStage int) int {
	if needReview, _ := gateNeeds(order.Tier); !needReview {
		return -1
	}
	lastBuild := -1
	for i := 0; i <= shipStage && i < len(order.Stages); i++ {
		if gateStageKey(order.Stages[i]) == gateBuildKey {
			lastBuild = i
		}
	}
	for i := lastBuild + 1; i <= shipStage && i < len(order.Stages); i++ {
		if gateStageKey(order.Stages[i]) == gateReviewKey {
			return i
		}
	}
	return -1
}

// onShipConflict handles a PR that conflicts with the base at ship. parked is
// true when the cook's final stage sits in pending review.
//
// stopped reports that the order was dealt with: stopped for a person, or sent
// back to review on the rebased head. When it is false, err is the result of
// shipping the rebased PR again (nil means it merged).
func (l *Loop) onShipConflict(ctx context.Context, cook *cookHandle, pr PullRequest, parked bool) (stopped bool, err error) {
	order, ok := l.orderByID(cook.orderID)
	if !ok {
		return true, l.failStage(ctx, cook, "merge conflict at ship, but the order is missing from orders.json, so its tier is unknown")
	}
	base := l.config.Landing.Base
	if l.rebaseCount(cook.orderID) >= 1 {
		return true, l.stopConflicted(ctx, cook, l.probeConflicts(ctx, cook, base), "it still conflicts after the one rebase")
	}
	if err := l.bumpRebased(cook.orderID); err != nil {
		return true, l.failStage(ctx, cook, "could not record the rebase, so it will not run: "+err.Error())
	}
	rctx, cancel := context.WithTimeout(ctx, rebaseTimeout)
	defer cancel()
	if err := l.lander().RebaseOntoBase(rctx, cook.worktreePath, base); err != nil {
		var ce *RebaseConflictError
		if errors.As(err, &ce) {
			return true, l.stopConflicted(ctx, cook, ce.Files, "the rebase onto the base branch conflicts")
		}
		if isWaitableError(err) {
			l.unbumpRebased(cook.orderID) // it never ran: the retry gets its one rebase
		}
		return true, l.failStageOrWaitNetwork(ctx, cook, "rebase onto the base branch", err)
	}
	l.logger.Info("order rebased onto the base branch", "order", cook.orderID, "pr", pr.URL)

	idx := reviewStageToReopen(order, cook.stageIndex)
	if idx < 0 {
		// No review to repeat: ship the rebased head.
		err := l.shipPR(ctx, cook, pr)
		if errors.Is(err, ErrPRConflict) {
			return true, l.stopConflicted(ctx, cook, l.probeConflicts(ctx, cook, base), "it still conflicts after the one rebase")
		}
		return false, err
	}
	// reopenStages checkpoints the canonical state, so the parked entry must
	// be gone first. The reducer drops it.
	if parked {
		err = l.emitEventChecked(ingest.EventStageReviewApproved, map[string]any{
			"order_id":    cook.orderID,
			"stage_index": cook.stageIndex,
			"reason":      "branch rebased onto the base branch; review runs again",
		})
	}
	if err == nil {
		err = l.reopenStages(cook.orderID, idx, cook.stageIndex, order.Round)
	}
	if err != nil {
		if parked {
			// The branch now holds rebased commits nobody reviewed, so the
			// order must not park again: an approve would merge them. A
			// person decides.
			return true, errors.Join(err, l.stopUnreviewedRebase(ctx, cook, err))
		}
		return true, err
	}
	if parked {
		if err := l.syncPendingReviewProjection(); err != nil {
			return true, err
		}
	}
	l.forwardToScheduler(cook, "rebased", "branch rebased onto the base branch after a merge conflict; review runs again on the new head", nil)
	return true, nil
}

// probeConflicts names the files the branch conflicts in, or nil when git
// cannot say.
func (l *Loop) probeConflicts(ctx context.Context, cook *cookHandle, base string) []string {
	ctx, cancel := context.WithTimeout(ctx, rebaseTimeout)
	defer cancel()
	files, err := l.lander().ConflictingFiles(ctx, cook.worktreePath, base)
	if err != nil {
		l.logger.Warn("could not list conflicting files", "order", cook.orderID, "error", err)
	}
	return files
}

// stopUnreviewedRebase ends a parked order whose rebased head could not be sent
// back to review: the stage fails and the issue goes to a person.
func (l *Loop) stopUnreviewedRebase(ctx context.Context, cook *cookHandle, cause error) error {
	msg := "the branch was rebased onto the base branch but review could not run again on the new head: " + cause.Error()
	l.logger.Warn("rebased head not reviewed, stopping order", "order", cook.orderID, "error", cause)
	// The approve step dropped the parked entry from canonical state; drop it
	// from the in-memory view too, even if later writes fail.
	if err := l.loadPendingReview(); err != nil {
		cause = errors.Join(cause, err)
	}
	flagErr := l.flagActionNeeded(cook.orderID + ": " + msg)
	l.stopForPerson(ctx, cook.orderID,
		"The branch was rebased after a merge conflict, but the factory could not send it back to review, so it was not merged. Check the branch, then put ready back. Cause:",
		msg)
	return errors.Join(flagErr, l.failStage(ctx, cook, msg))
}

// stopConflicted ends an order whose conflict the factory cannot clear: the
// stage fails, action_needed names the files, and the issue goes to a person.
func (l *Loop) stopConflicted(ctx context.Context, cook *cookHandle, files []string, why string) error {
	list := "files unknown"
	if len(files) > 0 {
		list = strings.Join(files, ", ")
	}
	msg := "merge conflict, needs a person: " + why + ". Conflicting files: " + list
	l.logger.Warn("merge conflict, stopping order", "order", cook.orderID, "files", files, "why", why)
	_ = l.events.Emit(LoopEventMergeConflict, MergeConflictPayload{
		OrderID:      cook.orderID,
		StageIndex:   cook.stageIndex,
		WorktreeName: cook.worktreeName,
		Files:        files,
	})
	// A failed flag write must not skip the stop (failStage clears the count): the order
	// would stay claimed with nobody told. Report both errors.
	flagErr := l.flagActionNeeded(cook.orderID + ": " + msg)
	l.stopForPerson(ctx, cook.orderID,
		"A merge conflict the factory cannot resolve. Resolve it by hand or reshape the issue, then put ready back. Conflict:",
		msg)
	return errors.Join(flagErr, l.failStage(ctx, cook, msg))
}

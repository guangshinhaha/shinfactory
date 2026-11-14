package loop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// reviewRetryAttempts is how many times a review stage may launch when it
// writes no findings file: the original run and one retry.
const reviewRetryAttempts = 2

// fallbackRetryAttempts bounds retries when both [spend] caps are off, so a
// stage that never passes cannot loop forever.
const fallbackRetryAttempts = 3

// rejectStage handles a stage the loop or its agent rejected: evidence that
// did not hold up, or a stage_fail. A build stage has work to fix, so while it
// has attempts left it goes back to pending with the reason in its next
// prompt, and the launch counts against max_attempts. Anything else (a
// quality or verify verdict has nothing to fix itself), no attempts left, or a
// failure no retry can fix (Unbuildable) fails the order and a person hears.
// A quality or verify rejection in PR mode sends the order back to execute with
// its findings (once, see sendBack) before a person hears.
func (l *Loop) rejectStage(ctx context.Context, cook *cookHandle, reason string) error {
	switch gateStageKey(cook.stage) {
	case gateBuildKey:
	case gateReviewKey, gateVerifyKey:
		if order, ok := l.orderByID(cook.orderID); ok && l.config.Landing.PR() &&
			!strings.HasPrefix(reason, unbuildablePrefix) && !isScheduleOrder(order) {
			return l.sendBack(cook, order, reason)
		}
		return l.failStage(ctx, cook, reason)
	default:
		return l.failStage(ctx, cook, reason)
	}
	if l.rejectedTwiceAlike(cook.orderID, reason) {
		return l.failStage(ctx, cook, "Rejected twice for the same reason, so another attempt would fail the same way: "+reason)
	}
	return l.rejectStageUpTo(ctx, cook, reason, 0)
}

func (l *Loop) lastRejectionFile(orderID string) string {
	return filepath.Join(l.runtimeDir, "last-rejection", orderID+".txt")
}

// rejectedTwiceAlike records reason as the order's latest build rejection and
// reports whether the one before it was word for word the same. The retry note
// cannot serve: the next launch consumes it.
func (l *Loop) rejectedTwiceAlike(orderID, reason string) bool {
	path := l.lastRejectionFile(orderID)
	prev, _ := os.ReadFile(path)
	if string(prev) == reason {
		return true
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
		_ = os.WriteFile(path, []byte(reason), 0o644)
	}
	return false
}

// rejectStageUpTo retries any stage kind up to limit launches (0 = the spend
// cap). The review gate uses it: a review that wrote no file can simply run again.
func (l *Loop) rejectStageUpTo(ctx context.Context, cook *cookHandle, reason string, limit int) error {
	if !strings.HasPrefix(reason, unbuildablePrefix) && l.canRetryStage(cook, limit) {
		return l.retryStage(cook, reason)
	}
	return l.failStage(ctx, cook, reason)
}

func (l *Loop) canRetryStage(cook *cookHandle, limit int) bool {
	order, ok := l.orderByID(cook.orderID)
	if !ok {
		return false
	}
	key := spendTaskKey(cook.stage)
	if l.overSpendCap(cook.orderID, key, order.Round) != "" {
		return false
	}
	if limit <= 0 {
		limit = l.config.Spend.MaxAttempts
	}
	if limit <= 0 {
		limit = fallbackRetryAttempts
	}
	spend := l.orderSpendOf(cook.orderID)
	return order.Round > spend.Round || spend.Attempts[key] < limit
}

// retryStage puts the rejected stage back to pending on the same worktree and
// branch. The launch it just used is already counted by recordStageLaunch.
func (l *Loop) retryStage(cook *cookHandle, reason string) error {
	if err := l.writeRetryNote(cook.orderID, cook.stageIndex, retryPrompt(reason)); err != nil {
		return err
	}
	if err := l.resetStageToPending(cook.orderID, cook.stageIndex, reason); err != nil {
		return err
	}
	l.logger.Warn("stage rejected; sending it back with the reason",
		"order", cook.orderID, "stage", cook.stageIndex, "reason", reason)
	l.forwardToScheduler(cook, "stage_retry", reason, nil)
	return nil
}

// retryPrompt is what the next attempt reads: the exact reason, and for a
// rejection the loop knows how to fix, the way to fix it.
func retryPrompt(reason string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Your previous attempt at this stage was rejected by the factory loop:\n\n%s\n\nThe work is still on this branch. Fix what the rejection names, then finish the stage again.", reason)
	if strings.Contains(reason, "no test-first commit") {
		b.WriteString("\n\nTo fix a test-first rejection: `git reset --soft` back to where the branch left main, commit only the failing test(s) as a test-only commit, then commit the fix on top of it.")
	}
	return b.String()
}

func (l *Loop) retryNoteFile(orderID string) string {
	return filepath.Join(l.runtimeDir, "retry-notes", orderID+".md")
}

func (l *Loop) writeRetryNote(orderID string, stageIndex int, text string) error {
	path := l.retryNoteFile(orderID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("record retry reason: %w", err)
	}
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%d\n%s", stageIndex, text)), 0o644); err != nil {
		return fmt.Errorf("record retry reason: %w", err)
	}
	return nil
}

// retryNote returns the rejection reason waiting for this stage's next
// launch, or "".
func (l *Loop) retryNote(orderID string, stageIndex int) string {
	data, err := os.ReadFile(l.retryNoteFile(orderID))
	if err != nil {
		return ""
	}
	head, body, ok := strings.Cut(string(data), "\n")
	if !ok || head != fmt.Sprint(stageIndex) {
		return ""
	}
	return strings.TrimSpace(body)
}

func (l *Loop) clearRetryNote(orderID string) {
	_ = os.Remove(l.retryNoteFile(orderID))
}

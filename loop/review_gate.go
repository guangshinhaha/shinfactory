package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/guangshinhaha/shinfactory/internal/orderx"
	"github.com/guangshinhaha/shinfactory/internal/state"
	"github.com/guangshinhaha/shinfactory/internal/stringx"
)

// The review gate keeps unreviewed work off the default branch. In landing
// mode "pr" nothing merges until the order's last stage completes, so the gate
// only has to decide what that last stage may ship:
//
//	execute -> quality (review) -> verify (high tier only) -> ship
//
// Review and verify each leave a file the loop reads. Findings or a FAIL send
// the order back to execute on the same branch, at most maxFixRounds times.
const (
	gateBuildKey  = "execute"
	gateReviewKey = "quality"
	gateVerifyKey = "verify"

	// maxFixRounds is how many times findings may send an order back to
	// execute. A review that still finds problems after that stops the order.
	maxFixRounds = 2

	reviewFileName = "review.md"
	verifyFileName = "verify.json"

	// reviewedHeadFile records the head a clean review saw.
	reviewedHeadFile = "reviewed-head"
)

// Verify verdicts.
const (
	VerdictPass      = "PASS"
	VerdictPassNotes = "PASS+NOTES"
	VerdictFail      = "FAIL"
)

// headSHA reads the checked-out commit of a worktree. Tests with fake
// worktrees swap it.
var headSHA = gitHeadSHA

func gitHeadSHA(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("read worktree head: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func gateStageKey(s Stage) string {
	return stringx.Normalize(nonEmpty(s.TaskKey, s.Skill))
}

// gateNeeds reports which gate stages a tier requires before ship.
func gateNeeds(tier string) (review, verify bool) {
	switch tier {
	case orderx.TierNone:
		return false, false
	case orderx.TierHigh:
		return true, true
	default:
		return true, false
	}
}

func hasBuildStage(order Order) bool {
	for _, s := range order.Stages {
		if gateStageKey(s) == gateBuildKey {
			return true
		}
	}
	return false
}

// checkGateShape fails an order whose stages could ship work its tier has not
// had reviewed. Orders with no execute stage are not build orders and pass.
func checkGateShape(order Order) error {
	lastBuild, review, verify := -1, -1, -1
	for i, s := range order.Stages {
		switch gateStageKey(s) {
		case gateBuildKey:
			lastBuild = i
		case gateReviewKey:
			review = i
		case gateVerifyKey:
			verify = i
		}
	}
	if lastBuild < 0 {
		return nil
	}
	needReview, needVerify := gateNeeds(order.Tier)
	tier := nonEmpty(order.Tier, orderx.TierMedium)
	if needReview && review < lastBuild {
		return fmt.Errorf("order cannot ship: tier %s needs a %s stage after the last %s stage", tier, gateReviewKey, gateBuildKey)
	}
	if needVerify && verify < review {
		return fmt.Errorf("order cannot ship: tier %s needs a %s stage after the %s stage", tier, gateVerifyKey, gateReviewKey)
	}
	return nil
}

// gateDir is the order's directory for one fix round. It is named like the
// order's branch, which is unique per order and never contains a path
// separator.
func (l *Loop) gateDir(orderID string, round int) string {
	return filepath.Join(l.runtimeDir, "gate", cookBaseName(orderID, 0, ""), fmt.Sprintf("round-%d", round))
}

// reviewReport is what a review file says.
type reviewReport struct {
	Covered string
	Clean   bool
}

// parseReview reads a findings file. It needs one "Covered:" line saying what
// the review looked at and one "Findings:" line, whose value is "none" (the
// value may sit on the next line). Anything else after the Findings line, bar
// the Covered line, counts as a finding, so a clean verdict never hides one.
// An empty file, or one with no coverage statement, is not a review.
func parseReview(data []byte) (reviewReport, error) {
	var rep reviewReport
	findings := -1
	var after []string
	var value string
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		bare := strings.ToLower(strings.Trim(line, "*_`# "))
		switch {
		case strings.HasPrefix(bare, "covered:"):
			rep.Covered = strings.TrimSpace(strings.Trim(line[strings.Index(strings.ToLower(line), "covered:")+len("covered:"):], "*_` "))
		case strings.HasPrefix(bare, "findings:"):
			if findings >= 0 {
				return rep, errors.New("review file has more than one \"Findings:\" line")
			}
			findings = 0
			value = strings.TrimSpace(bare[len("findings:"):])
		case findings >= 0:
			after = append(after, bare)
		}
	}
	if rep.Covered == "" {
		return rep, errors.New("review file does not say what it covered (needs a \"Covered:\" line)")
	}
	if findings < 0 {
		return rep, errors.New("review file has no \"Findings:\" line")
	}
	if value == "" && len(after) > 0 {
		value, after = after[0], after[1:]
	}
	value = strings.Trim(value, "*_`()[]. ")
	rep.Clean = value == "none" && len(after) == 0
	return rep, nil
}

// verifyVerdict is the verify stage's verdict file.
type verifyVerdict struct {
	HeadSHA  string `json:"head_sha"`
	Verdict  string `json:"verdict"`
	Evidence string `json:"evidence"`

	raw []byte // the file as the verifier wrote it
}

func parseVerdict(data []byte) (verifyVerdict, error) {
	var v verifyVerdict
	if err := json.Unmarshal(data, &v); err != nil {
		return v, fmt.Errorf("verdict file is not valid JSON: %w", err)
	}
	switch v.Verdict {
	case VerdictPass, VerdictPassNotes, VerdictFail:
	default:
		return v, fmt.Errorf("verdict %q is not PASS, PASS+NOTES or FAIL", v.Verdict)
	}
	if strings.TrimSpace(v.Evidence) == "" {
		return v, errors.New("verdict file has no evidence")
	}
	if strings.TrimSpace(v.HeadSHA) == "" {
		return v, errors.New("verdict file has no head_sha")
	}
	return v, nil
}

// readVerdict loads the verdict for the order's current round and checks it
// was written at the worktree's current head.
func (l *Loop) readVerdict(order Order, cook *cookHandle) (verifyVerdict, error) {
	path := filepath.Join(l.gateDir(order.ID, order.Round), verifyFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return verifyVerdict{}, fmt.Errorf("verify wrote no verdict file at %s", path)
	}
	v, err := parseVerdict(data)
	if err != nil {
		return v, err
	}
	v.raw = data
	head, err := headSHA(cook.worktreePath)
	if err != nil {
		return v, err
	}
	if v.HeadSHA != head {
		return v, fmt.Errorf("verdict is for %s but the branch head is %s", v.HeadSHA, head)
	}
	return v, nil
}

// verdictPostedFile records the verdict file already posted for a round, so a
// crash and rerun does not comment twice.
const verdictPostedFile = "verdict-posted"

// verdictPostAttempts tries the comment this many times before failing the stage.
const verdictPostAttempts = 3

var verdictPostBackoff = 2 * time.Second

// postVerdict carries the verifier's file to the PR as a comment, for gantry's
// verdict-check to read. The loop never writes a verdict: the comment holds
// the verifier's bytes unchanged.
func (l *Loop) postVerdict(ctx context.Context, order Order, cook *cookHandle, v verifyVerdict) error {
	marker := filepath.Join(l.gateDir(order.ID, order.Round), verdictPostedFile)
	if done, err := os.ReadFile(marker); err == nil && string(done) == string(v.raw) {
		return nil
	}
	raw := strings.TrimSpace(string(v.raw))
	fence := "```"
	for strings.Contains(raw, fence) {
		fence += "`" // evidence may quote code; the fence must outlast any backtick run in it
	}
	body := "Verify verdict at head " + v.HeadSHA + ", copied from the verify stage's file:\n\n" + fence + "json\n" + raw + "\n" + fence + "\n"
	// Push first (a no-op when nothing is new): the PR must exist and sit at
	// the head the verifier judged. Targeting the PR by URL keeps an older
	// closed PR on the same branch name out of it.
	pushCtx, cancelPush := context.WithTimeout(ctx, shipTimeout)
	pr, err := l.lander().PushAndEnsureDraft(pushCtx, cook.worktreePath, l.prSpec(order, cook))
	cancelPush()
	if err != nil {
		return fmt.Errorf("push before posting the verify verdict: %w", err)
	}
	for attempt := 1; ; attempt++ {
		postCtx, cancelPost := context.WithTimeout(ctx, shipTimeout)
		err = l.lander().PostVerdict(postCtx, cook.worktreePath, pr, body)
		cancelPost()
		if err == nil || attempt == verdictPostAttempts || ctx.Err() != nil {
			break
		}
		// A transient gh error must not kill a verified order; shutdown still ends the wait.
		select {
		case <-ctx.Done():
		case <-time.After(verdictPostBackoff):
		}
	}
	if err != nil {
		return fmt.Errorf("post verify verdict on the PR: %w", err)
	}
	if err := os.WriteFile(marker, v.raw, 0o644); err != nil {
		return fmt.Errorf("record posted verdict: %w", err)
	}
	return nil
}

// reviewGate runs when a stage completes in landing mode "pr", before the
// stage pushes or ships. It returns true when it has handled the completion
// itself (failed the stage or sent the order back to execute).
func (l *Loop) reviewGate(ctx context.Context, cook *cookHandle) (bool, error) {
	order, ok := l.orderByID(cook.orderID)
	if !ok || isScheduleOrder(order) {
		return false, nil
	}
	if order.Tier == "" && !hasBuildStage(order) {
		return false, nil // a standalone order (meditate, debrief) that never declared a tier
	}
	if err := checkGateShape(order); err != nil {
		return true, l.failStage(nil, cook, err.Error())
	}
	needReview, needVerify := gateNeeds(order.Tier)
	switch gateStageKey(cook.stage) {
	case gateReviewKey:
		if !needReview {
			return false, nil
		}
		path := filepath.Join(l.gateDir(order.ID, order.Round), reviewFileName)
		data, err := os.ReadFile(path)
		if err != nil {
			return true, l.failStage(nil, cook, "review wrote no findings file at "+path+"; an empty review counts as not run")
		}
		rep, err := parseReview(data)
		if err != nil {
			return true, l.failStage(nil, cook, err.Error()+" ("+path+")")
		}
		if !rep.Clean {
			return true, l.sendBack(cook, order, "review found issues, listed in "+path)
		}
		// Remember the head the review saw, so a later stage can't ship more.
		head, err := headSHA(cook.worktreePath)
		if err != nil {
			return true, l.failStage(nil, cook, err.Error())
		}
		if err := os.WriteFile(filepath.Join(l.gateDir(order.ID, order.Round), reviewedHeadFile), []byte(head), 0o644); err != nil {
			return true, l.failStage(nil, cook, "record reviewed head: "+err.Error())
		}
	case gateVerifyKey:
		if !needVerify {
			return false, nil
		}
		v, err := l.readVerdict(order, cook)
		if err != nil {
			return true, l.failStage(nil, cook, err.Error())
		}
		// Before any merge, and for a FAIL too, so the PR shows why it is blocked.
		if err := l.postVerdict(ctx, order, cook, v); err != nil {
			return true, l.failStage(nil, cook, err.Error())
		}
		if v.Verdict == VerdictFail {
			return true, l.sendBack(cook, order, "verify returned FAIL: "+v.Evidence)
		}
	default:
		// A later stage must not ship commits the review or verdict never saw.
		if !l.isFinalStage(cook) {
			break
		}
		if needVerify {
			if v, err := l.readVerdict(order, cook); err != nil {
				return true, l.failStage(nil, cook, err.Error())
			} else if v.Verdict == VerdictFail {
				return true, l.failStage(nil, cook, "verify returned FAIL: "+v.Evidence)
			}
		} else if needReview && gateStageKey(cook.stage) != gateBuildKey {
			if err := l.checkReviewedHead(order, cook); err != nil {
				return true, l.failStage(nil, cook, err.Error())
			}
		}
	}
	return false, nil
}

// sendBack returns the order to its last execute stage on the same branch, or
// stops it once maxFixRounds fix rounds are used up.
func (l *Loop) sendBack(cook *cookHandle, order Order, reason string) error {
	if order.Round >= maxFixRounds {
		msg := fmt.Sprintf("stopped after %d fix rounds, needs a person: %s", maxFixRounds, reason)
		if err := l.flagActionNeeded(cook.orderID + ": " + msg); err != nil {
			return err
		}
		return l.failStage(nil, cook, msg)
	}
	buildIdx := -1
	for i := 0; i < cook.stageIndex && i < len(order.Stages); i++ {
		if gateStageKey(order.Stages[i]) == gateBuildKey {
			buildIdx = i
		}
	}
	if buildIdx < 0 {
		return l.failStage(nil, cook, "no execute stage to send the order back to: "+reason)
	}
	if err := l.reopenStages(cook.orderID, buildIdx, cook.stageIndex, order.Round+1); err != nil {
		return err
	}
	l.logger.Info("order sent back to execute",
		"order", cook.orderID, "from_stage", cook.stageIndex, "to_stage", buildIdx, "round", order.Round+1, "reason", reason)
	l.forwardToScheduler(cook, "sent_back", reason, nil)
	return nil
}

// reopenStages sets stages from..to back to pending in the canonical state and
// orders.json and records the new fix round. The worktree and branch stay.
func (l *Loop) reopenStages(orderID string, from, to, round int) error {
	if err := l.ensureCanonicalOrderFromOrders(orderID); err != nil {
		return err
	}
	now := timeNowUTC(l.deps.Now)
	// A failure after the first mutation must not leave the stages reopened
	// in one store and not the other: snapshot both and roll back.
	prevNode, hadNode := l.canonical.Orders[orderID]
	if hadNode {
		prevNode = state.State{Orders: map[string]state.OrderNode{orderID: prevNode}}.Clone().Orders[orderID]
	}
	var prevStatuses []orderx.StageStatus
	prevRound := 0
	ordersTouched := false
	rollback := func(cause error) error {
		if hadNode {
			l.canonical.Orders[orderID] = prevNode
		}
		if !ordersTouched {
			return cause
		}
		if rerr := l.mutateOrdersState(func(orders *OrdersFile) (bool, error) {
			for oi := range orders.Orders {
				order := &orders.Orders[oi]
				if order.ID != orderID {
					continue
				}
				for i, st := range prevStatuses {
					order.Stages[from+i].Status = st
				}
				order.Round = prevRound
				return true, nil
			}
			return false, nil
		}); rerr != nil {
			return errors.Join(cause, fmt.Errorf("restore stages after failed reopen: %w", rerr))
		}
		return cause
	}
	if node, ok := l.canonical.Orders[orderID]; ok {
		for i := from; i <= to && i < len(node.Stages); i++ {
			stage := node.Stages[i]
			stage.Status = state.StagePending
			if n := len(stage.Attempts); n > 0 {
				last := &stage.Attempts[n-1]
				if last.Status == state.AttemptLaunching || last.Status == state.AttemptRunning {
					last.Status = state.AttemptCancelled
					last.CompletedAt = now
					last.Error = "sent back to execute"
				}
			}
			node.Stages[i] = stage
		}
		node.Round = round
		node.Status = state.OrderPending
		node.UpdatedAt = now
		l.canonical.Orders[orderID] = node
	}
	if err := l.mutateOrdersState(func(orders *OrdersFile) (bool, error) {
		for oi := range orders.Orders {
			order := &orders.Orders[oi]
			if order.ID != orderID {
				continue
			}
			prevStatuses = prevStatuses[:0]
			for i := from; i <= to && i < len(order.Stages); i++ {
				prevStatuses = append(prevStatuses, order.Stages[i].Status)
				order.Stages[i].Status = StageStatusPending
			}
			prevRound = order.Round
			order.Round = round
			ordersTouched = true
			return true, nil
		}
		return false, fmt.Errorf("order %q not found", orderID)
	}); err != nil {
		return rollback(err)
	}
	if err := l.persistCanonicalCheckpoint(); err != nil {
		return rollback(err)
	}
	return nil
}

// flagActionNeeded adds a line to the orders file's action_needed list.
func (l *Loop) flagActionNeeded(msg string) error {
	if err := l.mutateOrdersState(func(orders *OrdersFile) (bool, error) {
		orders.ActionNeeded = append(orders.ActionNeeded, msg)
		return true, nil
	}); err != nil {
		return err
	}
	return l.persistCanonicalCheckpoint()
}

// gateNote tells a gate stage where its file goes. It is "" for stages the
// gate does not touch.
func (l *Loop) gateNote(order Order, stage Stage) string {
	if !l.config.Landing.PR() {
		return ""
	}
	dir := l.gateDir(order.ID, order.Round)
	tier := nonEmpty(order.Tier, orderx.TierMedium)
	switch gateStageKey(stage) {
	case gateBuildKey:
		if order.Round == 0 {
			return ""
		}
		prev := l.gateDir(order.ID, order.Round-1)
		return fmt.Sprintf("Fix round %d of %d. Review or verify sent this order back. Read %s and %s (whichever exists), fix every finding on this branch with new commits, and do not open a new PR.",
			order.Round, maxFixRounds, filepath.Join(prev, reviewFileName), filepath.Join(prev, verifyFileName))
	case gateReviewKey:
		return fmt.Sprintf("Review gate, tier %s. Review at the depth your skill sets for this tier. Write %s with a line \"Covered: <what you read, the level, the kinds of problem you checked>\" and a line \"Findings: none\" or \"Findings:\" followed by a list. An empty file or one with no Covered line counts as not run and fails the stage. Findings send the order back to execute (round %d of %d used).",
			tier, filepath.Join(dir, reviewFileName), order.Round, maxFixRounds)
	case gateVerifyKey:
		return fmt.Sprintf("Verify gate, tier %s. You are a fresh verifier: you have not seen the builder's reasoning, so test the branch against the issue's Done when yourself. Write %s as {\"head_sha\": <git rev-parse HEAD of this worktree>, \"verdict\": \"PASS\"|\"PASS+NOTES\"|\"FAIL\", \"evidence\": <what you ran and saw>}. A verdict for any other head is rejected.",
			tier, filepath.Join(dir, verifyFileName))
	}
	return ""
}

// clearGateFile removes a stale gate file before its stage runs, so a file
// left by an earlier run of the same order can't pass for this stage's work.
func (l *Loop) clearGateFile(order Order, stage Stage) {
	name := ""
	switch gateStageKey(stage) {
	case gateReviewKey:
		name = reviewFileName
	case gateVerifyKey:
		name = verifyFileName
	default:
		return
	}
	if err := os.Remove(filepath.Join(l.gateDir(order.ID, order.Round), name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		l.logger.Warn("stale gate file not removed", "order", order.ID, "file", name, "error", err)
	}
}

// checkReviewedHead fails a ship whose head is not the one the clean review saw.
func (l *Loop) checkReviewedHead(order Order, cook *cookHandle) error {
	path := filepath.Join(l.gateDir(order.ID, order.Round), reviewedHeadFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("no record of a clean review at %s", path)
	}
	head, err := headSHA(cook.worktreePath)
	if err != nil {
		return err
	}
	if seen := strings.TrimSpace(string(data)); seen != head {
		return fmt.Errorf("review saw %s but the branch head is %s", seen, head)
	}
	return nil
}

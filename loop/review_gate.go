package loop

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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

func (l *Loop) gateDir(orderID string, round int) string {
	safe := strings.NewReplacer("/", "_", "\\", "_").Replace(strings.TrimSpace(orderID))
	return filepath.Join(l.runtimeDir, "gate", safe, fmt.Sprintf("round-%d", round))
}

// reviewReport is what a review file says.
type reviewReport struct {
	Covered string
	Clean   bool
}

// parseReview reads a findings file. It needs a Covered line saying what the
// review looked at and a Findings line: "Findings: none", or "Findings:"
// followed by the list. An empty file, or one with no coverage statement,
// is not a review.
func parseReview(data []byte) (reviewReport, error) {
	var rep reviewReport
	var sawFindings, none, listed bool
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		lower := strings.ToLower(line)
		switch {
		case line == "":
		case strings.HasPrefix(lower, "covered:"):
			rep.Covered = strings.TrimSpace(line[len("covered:"):])
		case strings.HasPrefix(lower, "findings:"):
			sawFindings = true
			rest := strings.ToLower(strings.TrimSpace(line[len("findings:"):]))
			rest = strings.TrimRight(rest, ". ")
			none = rest == "none"
			listed = rest != "" && !none
		case sawFindings && (strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") || (line[0] >= '0' && line[0] <= '9')):
			listed = true
		}
	}
	if rep.Covered == "" {
		return rep, errors.New("review file does not say what it covered (needs a \"Covered:\" line)")
	}
	if !sawFindings {
		return rep, errors.New("review file has no \"Findings:\" line")
	}
	rep.Clean = none && !listed
	return rep, nil
}

// verifyVerdict is the verify stage's verdict file.
type verifyVerdict struct {
	HeadSHA  string `json:"head_sha"`
	Verdict  string `json:"verdict"`
	Evidence string `json:"evidence"`
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
	head, err := headSHA(cook.worktreePath)
	if err != nil {
		return v, err
	}
	if v.HeadSHA != head {
		return v, fmt.Errorf("verdict is for %s but the branch head is %s", v.HeadSHA, head)
	}
	return v, nil
}

// reviewGate runs when a stage completes in landing mode "pr", before the
// stage pushes or ships. It returns true when it has handled the completion
// itself (failed the stage or sent the order back to execute).
func (l *Loop) reviewGate(cook *cookHandle) (bool, error) {
	order, ok := l.orderByID(cook.orderID)
	if !ok || isScheduleOrder(order) {
		return false, nil
	}
	if err := checkGateShape(order); err != nil {
		return true, l.failStage(nil, cook, err.Error())
	}
	_, needVerify := gateNeeds(order.Tier)
	switch gateStageKey(cook.stage) {
	case gateReviewKey:
		if needReview, _ := gateNeeds(order.Tier); !needReview {
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
	case gateVerifyKey:
		if !needVerify {
			return false, nil
		}
		v, err := l.readVerdict(order, cook)
		if err != nil {
			return true, l.failStage(nil, cook, err.Error())
		}
		if v.Verdict == VerdictFail {
			return true, l.sendBack(cook, order, "verify returned FAIL: "+v.Evidence)
		}
	default:
		// A later stage must not ship commits the verdict never saw.
		if needVerify && l.isFinalStage(cook) {
			if v, err := l.readVerdict(order, cook); err != nil {
				return true, l.failStage(nil, cook, err.Error())
			} else if v.Verdict == VerdictFail {
				return true, l.failStage(nil, cook, "verify returned FAIL: "+v.Evidence)
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
			for i := from; i <= to && i < len(order.Stages); i++ {
				order.Stages[i].Status = StageStatusPending
			}
			order.Round = round
			return true, nil
		}
		return false, fmt.Errorf("order %q not found", orderID)
	}); err != nil {
		return err
	}
	return l.persistCanonicalCheckpoint()
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

package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/guangshinhaha/shinfactory/internal/ingest"
	"github.com/guangshinhaha/shinfactory/internal/mode"
	"github.com/guangshinhaha/shinfactory/worktree"
)

// PullRequest identifies the order's PR on the remote.
type PullRequest struct {
	URL     string
	Branch  string
	IsDraft bool
}

// PRSpec is what a new draft PR is opened with.
type PRSpec struct {
	Title string
	Body  string
	Base  string // empty means the repository default branch
	// Branch is the order's own branch. The lander acts only on this branch.
	Branch string
}

// Lander moves an order's branch to the remote and through its PR. Every
// method is safe to repeat: the loop may crash and run the same stage again.
type Lander interface {
	// PushAndEnsureDraft pushes the worktree's branch and returns its open PR,
	// opening a draft PR first when none exists.
	PushAndEnsureDraft(ctx context.Context, dir string, spec PRSpec) (PullRequest, error)
	// MarkReadyAndMerge marks the PR ready for review and merges it once the
	// repository's required checks allow. It never bypasses them. It returns
	// ErrChecksPending, without failing, while checks are still running or a
	// merge queue has not merged the PR yet; the caller waits and calls again.
	// It acts only on the PR whose head is pr.Branch.
	MarkReadyAndMerge(ctx context.Context, dir string, pr PullRequest) error
}

// ErrChecksPending means the PR cannot merge yet because checks are still
// running or the merge is queued. It is not a failure: call again later.
var ErrChecksPending = errors.New("required checks pending")

// realGHEnv lets a test run the real gh and git. A test binary refuses to
// otherwise, so no test can act on a real PR by accident.
const realGHEnv = "SHINFACTORY_ALLOW_REAL_GH"

// defaultLander builds the Lander a loop uses when none is injected. Test
// binaries replace it with a fake.
var defaultLander = func() Lander { return ghLander{} }

// ghLander implements Lander with the git and gh CLIs run inside the order's
// worktree, so gh resolves the repository from the worktree's remote.
type ghLander struct{}

func (ghLander) run(ctx context.Context, dir, bin string, args ...string) (string, error) {
	if testing.Testing() && os.Getenv(realGHEnv) == "" {
		return "", fmt.Errorf("refusing to run %s in a test binary: set %s=1 only in a test that stubs it", bin, realGHEnv)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		return text, fmt.Errorf("%s %s: %w: %s", bin, strings.Join(args, " "), err, text)
	}
	return text, nil
}

func (g ghLander) PushAndEnsureDraft(ctx context.Context, dir string, spec PRSpec) (PullRequest, error) {
	branch, err := g.run(ctx, dir, "git", "branch", "--show-current")
	if err != nil {
		return PullRequest{}, err
	}
	if branch == "" {
		return PullRequest{}, fmt.Errorf("worktree %s is on a detached HEAD, so there is no branch to push", dir)
	}
	// Never act on whatever branch happens to be checked out: only the branch
	// the order created. A human-named branch may carry a PR of its own.
	if spec.Branch == "" || branch != spec.Branch {
		return PullRequest{}, fmt.Errorf("worktree %s is on branch %q, not the order branch %q; refusing to push or open a PR", dir, branch, spec.Branch)
	}
	if _, err := g.run(ctx, dir, "git", "push", "--set-upstream", "origin", branch); err != nil {
		return PullRequest{}, err
	}
	if pr, ok, err := g.find(ctx, dir, branch, spec.Base); err != nil {
		return PullRequest{}, err
	} else if ok {
		return pr, nil
	}
	// The PR may already be merged (by a human, or a merge queue, while the
	// order waited on checks). Opening another would be a stray PR.
	if pr, ok, err := g.findMerged(ctx, dir, branch); err != nil {
		return PullRequest{}, err
	} else if ok {
		return pr, nil
	}
	args := []string{"pr", "create", "--draft", "--head", branch, "--title", spec.Title, "--body", spec.Body}
	if spec.Base != "" {
		args = append(args, "--base", spec.Base)
	}
	if _, err := g.run(ctx, dir, "gh", args...); err != nil {
		return PullRequest{}, err
	}
	pr, ok, err := g.find(ctx, dir, branch, spec.Base)
	if err != nil {
		return PullRequest{}, err
	}
	if !ok {
		return PullRequest{}, fmt.Errorf("gh pr create succeeded but no open PR exists for branch %s", branch)
	}
	return pr, nil
}

// findMerged returns the branch's merged PR when its head is the worktree's
// HEAD, so a reused branch name with new commits is not mistaken for it.
func (g ghLander) findMerged(ctx context.Context, dir, branch string) (PullRequest, bool, error) {
	head, err := g.run(ctx, dir, "git", "rev-parse", "HEAD")
	if err != nil {
		return PullRequest{}, false, err
	}
	out, err := g.run(ctx, dir, "gh", "pr", "list", "--head", branch, "--state", "merged", "--json", "url,headRefOid,isCrossRepository", "--limit", "1")
	if err != nil {
		return PullRequest{}, false, err
	}
	var prs []struct {
		URL               string `json:"url"`
		HeadRefOid        string `json:"headRefOid"`
		IsCrossRepository bool   `json:"isCrossRepository"`
	}
	if err := json.Unmarshal([]byte(out), &prs); err != nil {
		return PullRequest{}, false, fmt.Errorf("parse gh pr list output %q: %w", out, err)
	}
	if len(prs) == 0 || prs[0].IsCrossRepository || prs[0].HeadRefOid != head {
		return PullRequest{}, false, nil
	}
	return PullRequest{URL: prs[0].URL, Branch: branch}, true, nil
}

func (g ghLander) find(ctx context.Context, dir, branch, base string) (PullRequest, bool, error) {
	out, err := g.run(ctx, dir, "gh", "pr", "list", "--head", branch, "--state", "open", "--json", "url,isDraft,baseRefName,isCrossRepository", "--limit", "1")
	if err != nil {
		return PullRequest{}, false, err
	}
	var prs []struct {
		URL               string `json:"url"`
		IsDraft           bool   `json:"isDraft"`
		BaseRefName       string `json:"baseRefName"`
		IsCrossRepository bool   `json:"isCrossRepository"`
	}
	if err := json.Unmarshal([]byte(out), &prs); err != nil {
		return PullRequest{}, false, fmt.Errorf("parse gh pr list output %q: %w", out, err)
	}
	if len(prs) == 0 {
		return PullRequest{}, false, nil
	}
	if prs[0].IsCrossRepository {
		return PullRequest{}, false, fmt.Errorf("open PR %s for branch %s comes from a fork, not this order; refusing to reuse it", prs[0].URL, branch)
	}
	if base != "" && prs[0].BaseRefName != base {
		return PullRequest{}, false, fmt.Errorf("open PR %s for branch %s targets %s, not %s; close it or fix landing.base", prs[0].URL, branch, prs[0].BaseRefName, base)
	}
	return PullRequest{URL: prs[0].URL, Branch: branch, IsDraft: prs[0].IsDraft}, true, nil
}

// prState is the slice of `gh pr view` the ship step decides on.
type prState struct {
	HeadRefName       string `json:"headRefName"`
	State             string `json:"state"`
	IsDraft           bool   `json:"isDraft"`
	IsCrossRepository bool   `json:"isCrossRepository"`
	MergeStateStatus  string `json:"mergeStateStatus"`
	StatusCheckRollup []struct {
		Status string `json:"status"` // CheckRun: QUEUED, IN_PROGRESS, COMPLETED
		State  string `json:"state"`  // StatusContext: PENDING, EXPECTED, SUCCESS, ...
	} `json:"statusCheckRollup"`
}

func (p prState) checksPending() bool {
	if p.MergeStateStatus == "UNKNOWN" {
		return true
	}
	for _, c := range p.StatusCheckRollup {
		if c.Status != "" && c.Status != "COMPLETED" {
			return true
		}
		if c.State == "PENDING" || c.State == "EXPECTED" {
			return true
		}
	}
	return false
}

func (g ghLander) view(ctx context.Context, dir string, pr PullRequest) (prState, error) {
	out, err := g.run(ctx, dir, "gh", "pr", "view", pr.URL, "--json", "headRefName,state,isDraft,isCrossRepository,mergeStateStatus,statusCheckRollup")
	if err != nil {
		return prState{}, err
	}
	var st prState
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		return prState{}, fmt.Errorf("parse gh pr view output %q: %w", out, err)
	}
	return st, nil
}

func (g ghLander) MarkReadyAndMerge(ctx context.Context, dir string, pr PullRequest) error {
	st, err := g.view(ctx, dir, pr)
	if err != nil {
		return err
	}
	// Confirm this is the order's PR before touching it.
	if pr.Branch == "" || st.HeadRefName != pr.Branch || st.IsCrossRepository {
		return fmt.Errorf("PR %s has head %q, not the order branch %q; refusing to ready or merge it", pr.URL, st.HeadRefName, pr.Branch)
	}
	switch st.State {
	case "MERGED":
		return nil
	case "OPEN":
	default:
		return fmt.Errorf("PR %s is %s, so it cannot be merged", pr.URL, strings.ToLower(st.State))
	}
	if st.IsDraft {
		if _, err := g.run(ctx, dir, "gh", "pr", "ready", pr.URL); err != nil {
			return err
		}
		if st, err = g.view(ctx, dir, pr); err != nil {
			return err
		}
	}
	if st.checksPending() {
		return fmt.Errorf("%w on %s", ErrChecksPending, pr.URL)
	}
	// A plain merge: GitHub refuses it while a required check is failing, and
	// the order is then parked with that reason. --auto would return success
	// while only queueing the merge. There is deliberately no --admin.
	if _, err := g.run(ctx, dir, "gh", "pr", "merge", pr.URL, "--squash"); err != nil {
		return err
	}
	// A merge queue can accept the merge and return before it lands.
	if st, err = g.view(ctx, dir, pr); err != nil {
		return err
	}
	if st.State != "MERGED" {
		return fmt.Errorf("%w: merge of %s is queued, not merged", ErrChecksPending, pr.URL)
	}
	return nil
}

// prSpec is the order's PR spec. Its branch is the order's own worktree
// branch, which the lander checks before it acts.
func (l *Loop) prSpec(order Order, cook *cookHandle) PRSpec {
	spec := prSpecFor(order, l.config.Landing.Base, l.config.Landing.CloseIssues)
	spec.Branch = cook.worktreeName
	return spec
}

func prSpecFor(order Order, base string, closeIssue bool) PRSpec {
	title := strings.TrimSpace(order.Title)
	if title == "" {
		title = "order " + order.ID
	}
	// The body deliberately omits the order's rationale and plan: they are
	// model-written and may carry data that must not leave for GitHub.
	var b strings.Builder
	if isIssueNumber(order.ID) {
		// Only a GitHub backlog numbers orders by issue; any other backlog's
		// numeric IDs must not close an unrelated issue.
		keyword := "Refs"
		if closeIssue {
			keyword = "Closes"
		}
		b.WriteString(keyword + " #" + order.ID + "\n\n")
	}
	b.WriteString("Opened by the factory loop as a draft. It is marked ready and merged when the order's last stage completes.\n")
	return PRSpec{Title: title, Body: b.String(), Base: base}
}

func isIssueNumber(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// cookWorktreeName names the worktree a stage runs in. In local mode every
// stage gets its own. In PR mode the whole order shares one, so the branch
// created at the first stage carries every later stage's commits.
func (l *Loop) cookWorktreeName(orderID string, stageIndex int, taskKey string) string {
	if l.config.Landing.PR() {
		return cookBaseName(orderID, 0, "")
	}
	return cookBaseName(orderID, stageIndex, taskKey)
}

func (l *Loop) lander() Lander {
	if l.deps.Lander != nil {
		return l.deps.Lander
	}
	return defaultLander()
}

func (l *Loop) orderByID(id string) (Order, bool) {
	orders, err := l.currentOrders()
	if err != nil {
		return Order{}, false
	}
	for _, o := range orders.Orders {
		if o.ID == id {
			return o, true
		}
	}
	return Order{}, false
}

// isFinalStage reports whether the cook's stage is the last one of its order.
func (l *Loop) isFinalStage(cook *cookHandle) bool {
	order, ok := l.orderByID(cook.orderID)
	if !ok {
		return false
	}
	return cook.stageIndex == len(order.Stages)-1
}

// completeWithPR finishes a stage in landing mode "pr". A stage that leaves
// commits pushes them to the order's branch and opens the draft PR if it does
// not exist yet. The last stage then hands the PR to the ship step.
func (l *Loop) completeWithPR(ctx context.Context, cook *cookHandle, hasCommits bool, msg *string) error {
	order, _ := l.orderByID(cook.orderID)
	var pr PullRequest
	pushed := false
	if hasCommits {
		var err error
		pr, err = l.lander().PushAndEnsureDraft(ctx, cook.worktreePath, l.prSpec(order, cook))
		if err != nil {
			return l.failStage(ctx, cook, "push and open draft PR: "+err.Error())
		}
		pushed = true
		l.logger.Info("order branch pushed", "order", cook.orderID, "stage", cook.stageIndex, "pr", pr.URL, "draft", pr.IsDraft)
	}
	if pushed && l.isFinalStage(cook) {
		gate := mode.ModeGate{}
		if !gate.CanAutoMerge(l.canonical.Mode) {
			// Merging is a shared write: only auto mode does it unasked.
			// The draft PR stays up for the human to ready and merge here.
			return l.parkPendingReview(cook, gate.BlockedReason(l.canonical.Mode, mode.ActionAutoMerge)+" ("+pr.URL+")")
		}
		if err := l.shipPR(ctx, cook, pr); err != nil {
			if errors.Is(err, ErrChecksPending) {
				// Gantry's checks run on the final push. Wait for them: the
				// order stays parked and each cycle retries the merge.
				l.lastChecksPoll = l.deps.Now()
				return l.parkPendingReview(cook, awaitingChecksReason(pr.URL))
			}
			return l.failStage(ctx, cook, "ship PR: "+err.Error())
		}
	}
	if err := l.emitEventChecked(ingest.EventStageCompleted, map[string]any{
		"order_id":      cook.orderID,
		"stage_index":   cook.stageIndex,
		"worktree_name": cook.worktreeName,
		"mergeable":     false,
		"pr_url":        pr.URL,
	}); err != nil {
		return err
	}
	return l.advanceAndPersist(ctx, cook, msg)
}

// shipPR marks the PR ready and merges it through the repository's checks,
// then removes the order's worktree.
func (l *Loop) shipPR(ctx context.Context, cook *cookHandle, pr PullRequest) error {
	if err := l.lander().MarkReadyAndMerge(ctx, cook.worktreePath, pr); err != nil {
		return err
	}
	l.logger.Info("order PR shipped", "order", cook.orderID, "pr", pr.URL)
	_ = l.deps.Worktree.Cleanup(cook.worktreeName, worktree.CleanupOpts{Force: true})
	return nil
}

// awaitingChecksPrefix marks a parked final stage whose PR waits for checks.
// The loop merges it by itself once they pass; no human approval is needed.
const awaitingChecksPrefix = "waiting for required checks"

// checksPollInterval is how often a waiting PR is re-checked.
const checksPollInterval = 30 * time.Second

func awaitingChecksReason(url string) string {
	return awaitingChecksPrefix + " (" + url + ")"
}

// retryAwaitingChecks merges parked PRs whose checks have finished. A PR whose
// merge is refused stays parked with the refusal as its reason, for a human.
func (l *Loop) retryAwaitingChecks(ctx context.Context) error {
	if !l.config.Landing.PR() || !(mode.ModeGate{}).CanAutoMerge(l.canonical.Mode) {
		return nil
	}
	now := l.deps.Now()
	if now.Sub(l.lastChecksPoll) < checksPollInterval {
		return nil
	}
	var waiting []*pendingReviewCook
	for _, pending := range l.cooks.pendingReview {
		if strings.HasPrefix(pending.reason, awaitingChecksPrefix) {
			waiting = append(waiting, pending)
		}
	}
	if len(waiting) == 0 {
		return nil
	}
	l.lastChecksPoll = now
	for _, pending := range waiting {
		cook := &cookHandle{
			cookIdentity: pending.cookIdentity,
			orderStatus:  OrderStatusActive,
			worktreeName: pending.worktreeName,
			worktreePath: pending.worktreePath,
			session:      &adoptedSession{id: pending.sessionID, status: "completed"},
		}
		err := l.shipParkedPR(cook, "required checks passed")
		switch {
		case err == nil, errors.Is(err, ErrChecksPending):
		default:
			l.logger.Warn("parked PR could not merge", "order", cook.orderID, "error", err)
			pending.reason = "merge refused: " + err.Error()
			if perr := l.parkPendingReview(cook, pending.reason); perr != nil {
				return perr
			}
		}
	}
	return nil
}

// approvePRShip is controlMerge in landing mode "pr": the human approved the
// parked final stage, so ready and merge its PR, then finish the order.
func (l *Loop) approvePRShip(cook *cookHandle) error {
	return l.shipParkedPR(cook, "approved by user")
}

// shipParkedPR ships the parked final stage's PR and finishes the order.
// reason is recorded on the review approval.
func (l *Loop) shipParkedPR(cook *cookHandle, reason string) error {
	ctx := context.Background()
	if !l.isFinalStage(cook) {
		// A parked non-final stage (e.g. a blocking stage message): approving
		// it moves the order on; only the last stage ships the PR.
		if err := l.emitEventChecked(ingest.EventStageReviewApproved, map[string]any{
			"order_id":    cook.orderID,
			"stage_index": cook.stageIndex,
			"reason":      reason,
		}); err != nil {
			return err
		}
		if err := l.emitEventChecked(ingest.EventStageCompleted, map[string]any{
			"order_id":      cook.orderID,
			"stage_index":   cook.stageIndex,
			"worktree_name": cook.worktreeName,
			"mergeable":     false,
		}); err != nil {
			return err
		}
		if err := l.advanceAndPersist(ctx, cook); err != nil {
			return err
		}
		return l.syncPendingReviewProjection()
	}
	order, _ := l.orderByID(cook.orderID)
	pr, err := l.lander().PushAndEnsureDraft(ctx, cook.worktreePath, l.prSpec(order, cook))
	if err != nil {
		return fmt.Errorf("find order PR: %w", err)
	}
	if err := l.shipPR(ctx, cook, pr); err != nil {
		return fmt.Errorf("ship PR: %w", err)
	}
	if err := l.emitEventChecked(ingest.EventStageReviewApproved, map[string]any{
		"order_id":    cook.orderID,
		"stage_index": cook.stageIndex,
		"reason":      reason,
	}); err != nil {
		return err
	}
	if err := l.emitEventChecked(ingest.EventStageCompleted, map[string]any{
		"order_id":      cook.orderID,
		"stage_index":   cook.stageIndex,
		"worktree_name": cook.worktreeName,
		"mergeable":     false,
		"pr_url":        pr.URL,
	}); err != nil {
		return err
	}
	if err := l.advanceAndPersist(ctx, cook); err != nil {
		return err
	}
	return l.syncPendingReviewProjection()
}

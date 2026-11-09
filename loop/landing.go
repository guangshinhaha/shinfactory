package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

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
	// repository's required checks allow. It never bypasses them.
	MarkReadyAndMerge(ctx context.Context, dir string, pr PullRequest) error
}

// ghLander implements Lander with the git and gh CLIs run inside the order's
// worktree, so gh resolves the repository from the worktree's remote.
type ghLander struct{}

func (ghLander) run(ctx context.Context, dir, bin string, args ...string) (string, error) {
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
	if _, err := g.run(ctx, dir, "git", "push", "--set-upstream", "origin", branch); err != nil {
		return PullRequest{}, err
	}
	if pr, ok, err := g.find(ctx, dir, branch, spec.Base); err != nil {
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

func (g ghLander) MarkReadyAndMerge(ctx context.Context, dir string, pr PullRequest) error {
	if pr.IsDraft {
		if _, err := g.run(ctx, dir, "gh", "pr", "ready", pr.URL); err != nil {
			return err
		}
	}
	// A plain merge: GitHub refuses it while a required check is pending or
	// failing, and the stage then fails with that reason. --auto would return
	// success while only queueing the merge, and the order would be marked
	// done unmerged. There is deliberately no --admin.
	_, err := g.run(ctx, dir, "gh", "pr", "merge", pr.URL, "--squash")
	return err
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
	return ghLander{}
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
		pr, err = l.lander().PushAndEnsureDraft(ctx, cook.worktreePath, prSpecFor(order, l.config.Landing.Base, l.backlogIsGitHub()))
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

// approvePRShip is controlMerge in landing mode "pr": the human approved the
// parked final stage, so ready and merge its PR, then finish the order.
func (l *Loop) approvePRShip(cook *cookHandle) error {
	ctx := context.Background()
	if !l.isFinalStage(cook) {
		// A parked non-final stage (e.g. a blocking stage message): approving
		// it moves the order on; only the last stage ships the PR.
		if err := l.emitEventChecked(ingest.EventStageReviewApproved, map[string]any{
			"order_id":    cook.orderID,
			"stage_index": cook.stageIndex,
			"reason":      "approved by user",
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
	pr, err := l.lander().PushAndEnsureDraft(ctx, cook.worktreePath, prSpecFor(order, l.config.Landing.Base, l.backlogIsGitHub()))
	if err != nil {
		return fmt.Errorf("find order PR: %w", err)
	}
	if err := l.emitEventChecked(ingest.EventStageReviewApproved, map[string]any{
		"order_id":    cook.orderID,
		"stage_index": cook.stageIndex,
		"reason":      "approved by user",
	}); err != nil {
		return err
	}
	if err := l.shipPR(ctx, cook, pr); err != nil {
		return fmt.Errorf("ship PR: %w", err)
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

// backlogIsGitHub reports whether the backlog adapter is the bundled GitHub
// issues one, so that an order ID is an issue number in this repository.
func (l *Loop) backlogIsGitHub() bool {
	for _, script := range l.config.Adapters["backlog"].Scripts {
		if strings.Contains(script, "adapters/github/") {
			return true
		}
	}
	return false
}

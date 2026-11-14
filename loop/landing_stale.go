package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/guangshinhaha/shinfactory/worktree"
)

// Stale order branches (#186) and verdict lookups for ship's red verdict-check.

func (g ghLander) BranchPRClosed(ctx context.Context, dir, branch string) ([]string, error) {
	out, err := g.run(ctx, dir, "gh", "pr", "list", "--head", branch, "--state", "all", "--json", "state,headRefOid,isCrossRepository", "--limit", "30")
	if err != nil {
		return nil, err
	}
	var prs []struct {
		State             string `json:"state"`
		HeadRefOid        string `json:"headRefOid"`
		IsCrossRepository bool   `json:"isCrossRepository"`
	}
	if err := json.Unmarshal([]byte(out), &prs); err != nil {
		return nil, fmt.Errorf("parse gh pr list output %q: %w", out, err)
	}
	var heads []string
	for _, pr := range prs {
		if pr.IsCrossRepository {
			continue // a fork's branch of the same name is not this order's
		}
		if pr.State == "OPEN" {
			return nil, nil
		}
		heads = append(heads, pr.HeadRefOid)
	}
	return heads, nil
}

// branchHoldsNewWork reports whether the local branch has a tip that is not
// inside the history of any closed PR head: commits the closed order never had.
// A retry of a rebuilt order's first stage lands here before its own PR exists,
// and must not wipe that order's work. A branch ref that is missing, or a git
// error, reads as no new work only for a missing ref; an error keeps the branch.
func branchHoldsNewWork(projectDir, branch string, closedHeads []string) bool {
	tip, err := exec.Command("git", "-C", projectDir, "rev-parse", "--verify", "-q", "refs/heads/"+branch).Output()
	if err != nil {
		return false // no local branch: nothing to lose
	}
	for _, head := range closedHeads {
		cmd := exec.Command("git", "-C", projectDir, "merge-base", "--is-ancestor", strings.TrimSpace(string(tip)), head)
		runErr := cmd.Run()
		if runErr == nil {
			return false // the tip is the closed order's own history
		}
		var ee *exec.ExitError
		if !errors.As(runErr, &ee) || ee.ExitCode() != 1 {
			return true // cannot tell (head not fetched?): keep the branch
		}
	}
	return true
}

// verdictCheckRed reports a finished, failing verdict-check.
func (p prState) verdictCheckRed() bool {
	for _, c := range p.StatusCheckRollup {
		if c.failing() && strings.Contains(c.label(), "verdict-check") {
			return true
		}
	}
	return false
}

// hasVerdictFor reports whether a comment body carries a verdict JSON for head:
// a head_sha field naming it next to a verdict field. A remark that merely
// quotes the sha does not count.
func hasVerdictFor(head string, bodies []string) bool {
	re := regexp.MustCompile(`"head_sha"\s*:\s*"` + regexp.QuoteMeta(head) + `"`)
	for _, b := range bodies {
		if re.MatchString(b) && strings.Contains(b, `"verdict"`) {
			return true
		}
	}
	return false
}

// commentBodies lists the PR's comment bodies. It is read only when a
// verdict-check is red, not on every view.
func (g ghLander) commentBodies(ctx context.Context, dir string, pr PullRequest) ([]string, error) {
	out, err := g.run(ctx, dir, "gh", "pr", "view", pr.URL, "--json", "comments")
	if err != nil {
		return nil, err
	}
	var v struct {
		Comments []struct {
			Body string `json:"body"`
		} `json:"comments"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return nil, fmt.Errorf("parse gh pr view output %q: %w", out, err)
	}
	bodies := make([]string, len(v.Comments))
	for i, c := range v.Comments {
		bodies[i] = c.Body
	}
	return bodies, nil
}

// prepareOrderBranch clears an earlier order's leftovers before a new order
// starts on the branch name. When the issue went back to ready after its PR was
// closed (or merged), the new order has the same id, so the same branch and
// worktree. Reusing them would carry the old commits, and often their
// conflicts, into the new order (#186). The branch, the worktree and the
// remote branch go; ensureWorktree then cuts a fresh one off the remote's
// default branch. A branch with an open PR, or none, is the order's own and
// stays, and so is one whose tip holds commits the closed PR never had. GitHub is asked only when a branch or worktree already exists.
func (l *Loop) prepareOrderBranch(ctx context.Context, name string) error {
	if !l.config.Landing.PR() {
		return nil
	}
	_, statErr := os.Stat(l.worktreePath(name))
	if statErr != nil && !branchExists(l.projectDir, name) {
		return nil
	}
	closedHeads, err := l.lander().BranchPRClosed(ctx, l.projectDir, name)
	if err != nil {
		return fmt.Errorf("check for an earlier PR on branch %s: %w", name, err)
	}
	if len(closedHeads) == 0 {
		return nil
	}
	if branchHoldsNewWork(l.projectDir, name, closedHeads) {
		return nil // a retry of the rebuilt order: its own commits stay
	}
	l.logger.Warn("discarding the branch of an earlier order whose PR is closed", "branch", name)
	if err := l.deps.Worktree.Cleanup(name, worktree.CleanupOpts{Force: true}); err != nil {
		return fmt.Errorf("discard stale branch %s: %w", name, err)
	}
	// A closed PR leaves its branch on the remote, and a push of the new
	// branch would be refused as a non-fast-forward.
	if err := deleteRemoteBranch(ctx, l.projectDir, name); err != nil {
		return fmt.Errorf("delete stale remote branch %s: %w", name, err)
	}
	return nil
}

// deleteRemoteBranch removes branch from origin when it is there.
func deleteRemoteBranch(ctx context.Context, projectDir, branch string) error {
	ctx, cancel := context.WithTimeout(ctx, shipTimeout)
	defer cancel()
	if exec.CommandContext(ctx, "git", "-C", projectDir, "remote", "get-url", "origin").Run() != nil {
		return nil // no origin, so no remote branch to clear
	}
	out, err := exec.CommandContext(ctx, "git", "-C", projectDir, "ls-remote", "--heads", "origin", branch).Output()
	if err != nil {
		return fmt.Errorf("list remote branches: %w", err)
	}
	if strings.TrimSpace(string(out)) == "" {
		return nil
	}
	if msg, err := exec.CommandContext(ctx, "git", "-C", projectDir, "push", "origin", "--delete", branch).CombinedOutput(); err != nil {
		return fmt.Errorf("git push origin --delete %s: %w: %s", branch, err, strings.TrimSpace(string(msg)))
	}
	return nil
}

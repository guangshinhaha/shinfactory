package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/guangshinhaha/shinfactory/worktree"
)

// Stale order branches (#186) and verdict lookups for ship's red verdict-check.

func (g ghLander) BranchPRClosed(ctx context.Context, dir, branch string) (bool, error) {
	out, err := g.run(ctx, dir, "gh", "pr", "list", "--head", branch, "--state", "all", "--json", "state,isCrossRepository", "--limit", "30")
	if err != nil {
		return false, err
	}
	var prs []struct {
		State             string `json:"state"`
		IsCrossRepository bool   `json:"isCrossRepository"`
	}
	if err := json.Unmarshal([]byte(out), &prs); err != nil {
		return false, fmt.Errorf("parse gh pr list output %q: %w", out, err)
	}
	closed := false
	for _, pr := range prs {
		if pr.IsCrossRepository {
			continue // a fork's branch of the same name is not this order's
		}
		if pr.State == "OPEN" {
			return false, nil
		}
		closed = true
	}
	return closed, nil
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
// stays. GitHub is asked only when a branch or worktree already exists.
func (l *Loop) prepareOrderBranch(ctx context.Context, name string) error {
	if !l.config.Landing.PR() {
		return nil
	}
	_, statErr := os.Stat(l.worktreePath(name))
	if statErr != nil && !branchExists(l.projectDir, name) {
		return nil
	}
	closed, err := l.lander().BranchPRClosed(ctx, l.projectDir, name)
	if err != nil {
		return fmt.Errorf("check for an earlier PR on branch %s: %w", name, err)
	}
	if !closed {
		return nil
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

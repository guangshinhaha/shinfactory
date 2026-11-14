package loop

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guangshinhaha/shinfactory/config"
	"github.com/guangshinhaha/shinfactory/worktree"
)

// #186: an order's PR is closed for conflicts and its issue goes back to
// ready. The new order reuses the id, so it gets the same branch name. It must
// start from the remote's current default branch, not from the old commits.
func TestRebuiltOrderStartsFromCurrentMainNotTheClosedPRsBranch(t *testing.T) {
	fetchBase = fetchLandingBase
	t.Cleanup(func() { fetchBase = func(string, string) (string, error) { return "origin/main", nil } })

	remote := t.TempDir()
	gitIn(t, remote, "init", "--bare", "-b", "main")
	project := t.TempDir()
	gitIn(t, project, "init", "-b", "main")
	gitIn(t, project, "remote", "add", "origin", remote)
	writeFile(t, filepath.Join(project, "shared.txt"), "base\n")
	gitIn(t, project, "add", ".")
	gitIn(t, project, "commit", "-m", "base")
	gitIn(t, project, "push", "origin", "main")

	wt := &worktree.App{Root: project}
	cfg := config.DefaultConfig()
	cfg.Landing.Mode = config.LandingModePR
	ln := &fakeLander{}
	l := New(project, "noodle", cfg, Dependencies{Worktree: wt, Lander: ln})
	l.config.Landing.Mode = config.LandingModePR

	// The first order commits and pushes its branch, then its PR is closed.
	const branch = "131-0-task"
	if _, err := l.ensureWorktree(branch); err != nil {
		t.Fatalf("first order worktree: %v", err)
	}
	old := l.worktreePath(branch)
	writeFile(t, filepath.Join(old, "old.txt"), "old order\n")
	gitIn(t, old, "add", ".")
	gitIn(t, old, "commit", "-m", "old order work")
	gitIn(t, old, "push", "origin", branch)
	oldHead := gitIn(t, old, "rev-parse", "HEAD")
	ln.closedPR = true
	ln.closedHeads = []string{oldHead}

	// Main moves on while the issue waits.
	gh := t.TempDir()
	gitIn(t, gh, "clone", remote, ".")
	writeFile(t, filepath.Join(gh, "shared.txt"), "base\nanother order\n")
	gitIn(t, gh, "commit", "-am", "another order merged")
	gitIn(t, gh, "push", "origin", "main")
	mainHead := gitIn(t, gh, "rev-parse", "HEAD")

	// The re-readied issue becomes a new order with the same branch name.
	if err := l.prepareOrderBranch(context.Background(), branch); err != nil {
		t.Fatalf("prepareOrderBranch: %v", err)
	}
	if _, err := l.ensureWorktree(branch); err != nil {
		t.Fatalf("rebuilt order worktree: %v", err)
	}
	fresh := l.worktreePath(branch)
	if got := gitIn(t, fresh, "rev-parse", "HEAD"); got != mainHead {
		t.Fatalf("new branch head = %s, want current main %s (old order head was %s)", got, mainHead, oldHead)
	}
	if out := gitIn(t, fresh, "log", "--format=%H", "origin/main..HEAD"); out != "" {
		t.Fatalf("new branch carries commits main lacks: %s", out)
	}
	if gitIn(t, project, "ls-remote", "origin", "refs/heads/"+branch) != "" {
		t.Fatal("the closed PR's remote branch is still there, so the first push would be rejected")
	}
}

// An open PR, or no PR yet, means the branch is still the order's own.
func TestPrepareOrderBranchKeepsABranchWhosePRIsNotClosed(t *testing.T) {
	project := t.TempDir()
	gitIn(t, project, "init", "-b", "main")
	writeFile(t, filepath.Join(project, "a.txt"), "a\n")
	gitIn(t, project, "add", ".")
	gitIn(t, project, "commit", "-m", "base")
	wt := &worktree.App{Root: project}
	cfg := config.DefaultConfig()
	cfg.Landing.Mode = config.LandingModePR
	ln := &fakeLander{}
	l := New(project, "noodle", cfg, Dependencies{Worktree: wt, Lander: ln})
	l.config.Landing.Mode = config.LandingModePR

	const branch = "7-0-task"
	if err := wt.Create(branch); err != nil {
		t.Fatal(err)
	}
	dir := l.worktreePath(branch)
	writeFile(t, filepath.Join(dir, "wip.txt"), "wip\n")
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-m", "wip")
	head := gitIn(t, dir, "rev-parse", "HEAD")

	if err := l.prepareOrderBranch(context.Background(), branch); err != nil {
		t.Fatal(err)
	}
	if got := gitIn(t, dir, "rev-parse", "HEAD"); got != head || !strings.Contains(gitIn(t, project, "branch", "--list", branch), branch) {
		t.Fatalf("branch with no closed PR was reset: head %s, want %s", got, head)
	}
	// A brand-new order has neither branch nor worktree: no gh call at all.
	ln.closedPRChecks = 0
	if err := l.prepareOrderBranch(context.Background(), "8-0-task"); err != nil {
		t.Fatal(err)
	}
	if ln.closedPRChecks != 0 {
		t.Fatalf("asked GitHub about a branch that does not exist (%d calls)", ln.closedPRChecks)
	}
}

// The first launch of an order runs the cleanup; the dispatch path calls it.
func TestFirstLaunchDiscardsAClosedPRsBranch(t *testing.T) {
	h := newLandingHarness(t, "auto", 1)
	name := h.l.cookWorktreeName("42", 0, "execute")
	if err := os.MkdirAll(h.l.worktreePath(name), 0o755); err != nil {
		t.Fatal(err)
	}
	h.ln.closedPR = true
	if err := h.l.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(h.wt.cleaned) == 0 || h.wt.cleaned[0] != name {
		t.Fatalf("cleaned = %v, want the stale %s discarded before the first stage launched", h.wt.cleaned, name)
	}
}

func TestHasVerdictForNeedsAVerdictJSONNamingTheHead(t *testing.T) {
	cases := []struct {
		name   string
		bodies []string
		want   bool
	}{
		{"no comments", nil, false},
		{"verdict for an older head", []string{`{"head_sha": "aaa", "verdict": "PASS"}`}, false},
		{"verdict at head", []string{"Verify verdict at head bbb:\n```json\n{\"head_sha\": \"bbb\", \"verdict\": \"PASS\"}\n```"}, true},
		{"remark quoting the sha and the word verdict", []string{`my verdict on bbb: looks fine`}, false},
	}
	for _, tc := range cases {
		if got := hasVerdictFor("bbb", tc.bodies); got != tc.want {
			t.Errorf("%s: hasVerdictFor = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A retry of the rebuilt order's first stage runs before its own PR exists, so
// the old closed PR still counts. The new order's commits must survive it.
func TestPrepareOrderBranchKeepsNewWorkOnRetryWhileOldPRIsClosed(t *testing.T) {
	fetchBase = func(string, string) (string, error) { return "main", nil }
	t.Cleanup(func() { fetchBase = func(string, string) (string, error) { return "origin/main", nil } })
	project := t.TempDir()
	gitIn(t, project, "init", "-b", "main")
	writeFile(t, filepath.Join(project, "a.txt"), "a\n")
	gitIn(t, project, "add", ".")
	gitIn(t, project, "commit", "-m", "base")
	wt := &worktree.App{Root: project}
	cfg := config.DefaultConfig()
	cfg.Landing.Mode = config.LandingModePR
	ln := &fakeLander{}
	l := New(project, "noodle", cfg, Dependencies{Worktree: wt, Lander: ln})
	l.config.Landing.Mode = config.LandingModePR

	const branch = "131-0-task"
	if err := wt.Create(branch); err != nil {
		t.Fatal(err)
	}
	dir := l.worktreePath(branch)
	writeFile(t, filepath.Join(dir, "old.txt"), "old\n")
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-m", "old order work")
	ln.closedPR = true
	ln.closedHeads = []string{gitIn(t, dir, "rev-parse", "HEAD")}

	// The rebuilt order starts fresh, commits, and its stage is retried.
	if err := l.prepareOrderBranch(context.Background(), branch); err != nil {
		t.Fatal(err)
	}
	if _, err := l.ensureWorktree(branch); err != nil {
		t.Fatal(err)
	}
	dir = l.worktreePath(branch)
	writeFile(t, filepath.Join(dir, "new.txt"), "new\n")
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-m", "new order work")
	newHead := gitIn(t, dir, "rev-parse", "HEAD")

	if err := l.prepareOrderBranch(context.Background(), branch); err != nil {
		t.Fatal(err)
	}
	if got := gitIn(t, dir, "rev-parse", "HEAD"); got != newHead {
		t.Fatalf("retry discarded the rebuilt order's work: head %s, want %s", got, newHead)
	}
}

// The closed PR's head was pushed from another clone, so this clone never saw
// it. The check fetches it, finds the stale tip inside it, and rebuilds from
// current main (#186).
func TestPrepareOrderBranchFetchesAClosedHeadMissingFromTheClone(t *testing.T) {
	fetchBase = fetchLandingBase
	t.Cleanup(func() { fetchBase = func(string, string) (string, error) { return "origin/main", nil } })

	remote := t.TempDir()
	gitIn(t, remote, "init", "--bare", "-b", "main")
	project := t.TempDir()
	gitIn(t, project, "init", "-b", "main")
	gitIn(t, project, "remote", "add", "origin", remote)
	writeFile(t, filepath.Join(project, "a.txt"), "a\n")
	gitIn(t, project, "add", ".")
	gitIn(t, project, "commit", "-m", "base")
	gitIn(t, project, "push", "origin", "main")
	wt := &worktree.App{Root: project}
	cfg := config.DefaultConfig()
	cfg.Landing.Mode = config.LandingModePR
	ln := &fakeLander{}
	l := New(project, "noodle", cfg, Dependencies{Worktree: wt, Lander: ln})
	l.config.Landing.Mode = config.LandingModePR

	const branch = "131-0-task"
	if _, err := l.ensureWorktree(branch); err != nil {
		t.Fatal(err)
	}
	dir := l.worktreePath(branch)
	writeFile(t, filepath.Join(dir, "old.txt"), "old\n")
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-m", "old order work")
	gitIn(t, dir, "push", "origin", branch)

	// Someone else pushes one more commit to the PR branch before it closes.
	other := t.TempDir()
	gitIn(t, other, "clone", "-b", branch, remote, ".")
	writeFile(t, filepath.Join(other, "more.txt"), "more\n")
	gitIn(t, other, "add", ".")
	gitIn(t, other, "commit", "-m", "pushed from elsewhere")
	gitIn(t, other, "push", "origin", branch)
	elsewhere := gitIn(t, other, "rev-parse", "HEAD")
	if exec.Command("git", "-C", project, "cat-file", "-e", elsewhere+"^{commit}").Run() == nil {
		t.Fatal("setup: the clone already has the closed head")
	}
	ln.closedPR = true
	ln.closedHeads = []string{"1111111111111111111111111111111111111111", elsewhere}

	if err := l.prepareOrderBranch(context.Background(), branch); err != nil {
		t.Fatal(err)
	}
	if _, err := l.ensureWorktree(branch); err != nil {
		t.Fatal(err)
	}
	mainHead := gitIn(t, project, "rev-parse", "origin/main")
	if got := gitIn(t, l.worktreePath(branch), "rev-parse", "HEAD"); got != mainHead {
		t.Fatalf("rebuilt branch head = %s, want current main %s", got, mainHead)
	}
}

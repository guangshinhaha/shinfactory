package loop

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/guangshinhaha/shinfactory/internal/orderx"
)

// An execute stage follows the shinstack playbook for its order's work type,
// and the loop checks the evidence that playbook asks for before it accepts
// the stage as done. The factory owns when this happens and what a failure
// does; shinstack owns the playbook's content.

// playbookFiles maps a work type to its shinstack playbook.
var playbookFiles = map[string]string{
	orderx.WorkTypeBug:      "bug-fix.md",
	orderx.WorkTypeFeature:  "feature.md",
	orderx.WorkTypeRefactor: "refactoring.md",
}

// checkTimeout bounds one repo check run before a stage is accepted.
const checkTimeout = 20 * time.Minute

// maxCheckOutput is how much of a failing check's output goes in the failure
// reason; the tail is where the failure is.
const maxCheckOutput = 2000

// shinstackRoots lists directories that may hold shinstack's playbooks/, best
// first: SHINSTACK_DIR, then the newest installed plugin version. Tests swap it.
var shinstackRoots = defaultShinstackRoots

func defaultShinstackRoots() []string {
	var roots []string
	if dir := strings.TrimSpace(os.Getenv("SHINSTACK_DIR")); dir != "" {
		roots = append(roots, dir)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return roots
	}
	base := filepath.Join(home, ".claude", "plugins", "cache", "shinstack", "shinstack")
	entries, err := os.ReadDir(base)
	if err != nil {
		return roots
	}
	var versions []string
	for _, e := range entries {
		if e.IsDir() {
			versions = append(versions, e.Name())
		}
	}
	sort.Slice(versions, func(i, j int) bool { return versionLess(versions[j], versions[i]) })
	for _, v := range versions {
		roots = append(roots, filepath.Join(base, v))
	}
	return roots
}

// versionLess orders dotted numeric versions ("0.9.0" < "0.10.0").
func versionLess(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var x, y int
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		if x != y {
			return x < y
		}
	}
	return false
}

// playbookFor returns the shinstack playbook text for a work type. It is ""
// when the order has no work type or shinstack is not installed, so the stage
// runs on its own skill text.
func playbookFor(workType string) string {
	name, ok := playbookFiles[workType]
	if !ok {
		return ""
	}
	for _, root := range shinstackRoots() {
		data, err := os.ReadFile(filepath.Join(root, "playbooks", name))
		if err != nil {
			continue
		}
		if text := strings.TrimSpace(string(data)); text != "" {
			return fmt.Sprintf("Follow this shinstack playbook (%s) for this %s. The loop checks its evidence before it accepts your stage.\n\n%s", name, workType, text)
		}
	}
	return ""
}

// playbookNote is the prompt section for an execute stage.
func playbookNote(order Order, stage Stage) string {
	if gateStageKey(stage) != gateBuildKey {
		return ""
	}
	return playbookFor(order.WorkType)
}

var (
	repoChecksHeader = regexp.MustCompile(`(?i)^\s*before a pr\s*:\s*$`)
	repoCheckBullet  = regexp.MustCompile("^\\s*[-*]\\s+`([^`]+)`")
)

// repoChecks reads the commands a repo lists under "Before a PR:" in its
// CLAUDE.md, the block shinstack init writes. No file or no list means the
// repo declares no checks. Blank lines between the header and the bullets are
// fine; the first other line ends the list.
func repoChecks(dir string) []string {
	data, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		return nil
	}
	var cmds []string
	in := false
	for _, line := range strings.Split(string(stripCodeFences(data)), "\n") {
		switch {
		case repoChecksHeader.MatchString(line):
			in = true
		case in && strings.TrimSpace(line) == "" && len(cmds) == 0:
		case in:
			m := repoCheckBullet.FindStringSubmatch(line)
			if m == nil {
				return cmds
			}
			cmds = append(cmds, strings.TrimSpace(m[1]))
		}
	}
	return cmds
}

// runRepoCheck runs one check command in dir. Tests swap it.
var runRepoCheck = func(ctx context.Context, dir, command string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
	}
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		err = fmt.Errorf("timed out after %s", checkTimeout)
	}
	return out.String(), err
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}

// checkRepoChecks runs the checks declared in declaredIn's CLAUDE.md inside the
// worktree dir and reports the first that fails. The declaration comes from the
// main checkout, so the cook being checked can't edit its own checks away.
func checkRepoChecks(ctx context.Context, declaredIn, dir string) error {
	for _, command := range repoChecks(declaredIn) {
		out, err := runRepoCheck(ctx, dir, command)
		if err != nil {
			return fmt.Errorf("repo check `%s` failed (%v): %s", command, err, tail(out, maxCheckOutput))
		}
	}
	return nil
}

// commitFiles is one commit on the order's branch and the paths it touches.
type commitFiles struct {
	SHA   string
	Files []string
}

// branchCommits lists the commits only this worktree's branch has, oldest
// first. Tests swap it.
var branchCommits = gitBranchCommits

func gitBranchCommits(dir string) ([]commitFiles, error) {
	git := func(args ...string) (string, error) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
		return string(out), err
	}
	branch, err := git("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("read worktree branch: %w", err)
	}
	branch = strings.TrimSpace(branch)
	// Commits no other branch has: this branch's own work, pushed or not.
	// --exclude patterns are relative to the --branches/--remotes namespace.
	revs, err := git("rev-list", "--reverse", "HEAD", "--not",
		"--exclude="+branch, "--branches",
		"--exclude=origin/"+branch, "--remotes")
	if err != nil {
		return nil, fmt.Errorf("list branch commits: %w", err)
	}
	var commits []commitFiles
	for _, sha := range strings.Fields(revs) {
		files, err := git("diff-tree", "--root", "--no-commit-id", "--name-only", "-r", "-z", sha)
		if err != nil {
			return nil, fmt.Errorf("list files of %s: %w", sha, err)
		}
		commits = append(commits, commitFiles{SHA: sha, Files: strings.FieldsFunc(files, func(r rune) bool { return r == 0 })})
	}
	return commits, nil
}

// isTestPath reports whether a path is test code or test data.
func isTestPath(p string) bool {
	p = filepath.ToSlash(strings.ToLower(p))
	base := p[strings.LastIndex(p, "/")+1:]
	switch {
	case strings.HasSuffix(base, "_test.go"), strings.HasSuffix(base, "_test.py"),
		strings.HasPrefix(base, "test_"), strings.Contains(base, ".test."),
		strings.Contains(base, ".spec."):
		return true
	}
	for _, seg := range strings.Split(p[:max(strings.LastIndex(p, "/"), 0)], "/") {
		switch seg {
		case "test", "tests", "__tests__", "testdata", "fixtures", "e2e":
			return true
		}
	}
	return false
}

// checkTestFirst requires a commit that touches only tests before the first
// commit that touches code, and at least one fix commit after it. Commits that
// touch only Markdown (notes, docs) are neither: they can sit anywhere.
func checkTestFirst(commits []commitFiles) error {
	sawTestOnly := false
	for _, c := range commits {
		testOnly, docsOnly := len(c.Files) > 0, len(c.Files) > 0
		for _, f := range c.Files {
			if !isTestPath(f) {
				testOnly = false
			}
			if !strings.HasSuffix(strings.ToLower(f), ".md") {
				docsOnly = false
			}
		}
		switch {
		case docsOnly && !testOnly:
		case testOnly:
			sawTestOnly = true
		case len(c.Files) == 0:
		case !sawTestOnly:
			return fmt.Errorf("bug fix has no test-first commit: commit %s changes non-test files before any commit that touches only tests", short(c.SHA))
		default:
			return nil
		}
	}
	if !sawTestOnly {
		return errors.New("bug fix has no test-first commit: the branch has no commit that touches only tests")
	}
	return errors.New("bug fix has no fix commit after its test-only commit")
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// checkExecuteEvidence runs when an execute stage reports done. It returns the
// reason the stage must not be accepted, or nil.
func (l *Loop) checkExecuteEvidence(ctx context.Context, order Order, cook *cookHandle) error {
	if gateStageKey(cook.stage) != gateBuildKey || cook.worktreePath == "" {
		return nil
	}
	if err := checkRepoChecks(ctx, l.projectDir, cook.worktreePath); err != nil {
		return err
	}
	if order.WorkType == orderx.WorkTypeBug {
		commits, err := branchCommits(cook.worktreePath)
		if err != nil {
			return err
		}
		return checkTestFirst(commits)
	}
	return nil
}

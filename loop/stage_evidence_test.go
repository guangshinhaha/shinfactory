package loop

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/guangshinhaha/shinfactory/internal/orderx"
)

// evidenceHarness is a PR-mode order of the given work type whose execute
// stage runs in a real directory, so the repo's CLAUDE.md can name its checks.
func evidenceHarness(t *testing.T, workType, claudeMD string, keys ...string) *gateHarness {
	t.Helper()
	if len(keys) == 0 {
		keys = []string{"execute"}
	}
	h := newGateHarness(t, "none", keys...)
	if err := h.l.mutateOrdersState(func(o *OrdersFile) (bool, error) {
		o.Orders[0].WorkType = workType
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	dir := h.l.worktreePath(h.l.cookWorktreeName("42", 0, "execute"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if claudeMD != "" {
		// The repo's checks come from the main checkout, not the cook's worktree.
		if err := os.WriteFile(filepath.Join(h.l.projectDir, "CLAUDE.md"), []byte(claudeMD), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	oldCommits := branchCommits
	t.Cleanup(func() { branchCommits = oldCommits })
	return h
}

func withPlaybooks(t *testing.T, files map[string]string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "playbooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, "playbooks", name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := shinstackRoots
	shinstackRoots = func() []string { return []string{root} }
	t.Cleanup(func() { shinstackRoots = old })
}

// shipped asserts the single-stage order was accepted and merged.
func (h *gateHarness) shipped(t *testing.T) {
	t.Helper()
	if len(h.ln.merged) != 1 {
		t.Fatalf("merges = %d, want 1: the stage should have been accepted", len(h.ln.merged))
	}
}

const checksMD = "# X\n## shinstack\nBefore a PR:\n- `pnpm check`\n\nDecisions: none\n"

func TestExecutePromptCarriesThePlaybookForItsWorkType(t *testing.T) {
	withPlaybooks(t, map[string]string{
		"bug-fix.md":     "BUG PLAYBOOK: reproduce first",
		"feature.md":     "FEATURE PLAYBOOK: slices",
		"refactoring.md": "REFACTOR PLAYBOOK: keep behaviour",
	})
	for workType, want := range map[string]string{
		orderx.WorkTypeBug:      "BUG PLAYBOOK",
		orderx.WorkTypeFeature:  "FEATURE PLAYBOOK",
		orderx.WorkTypeRefactor: "REFACTOR PLAYBOOK",
	} {
		t.Run(workType, func(t *testing.T) {
			h := evidenceHarness(t, workType, "")
			branchCommits = func(string) ([]commitFiles, error) {
				return []commitFiles{{SHA: "a1", Files: []string{"x_test.go"}}, {SHA: "b2", Files: []string{"x.go"}}}, nil
			}
			if p := h.next(t); !strings.Contains(p, want) {
				t.Fatalf("execute prompt lacks the %s playbook: %s", workType, p)
			}
		})
	}
}

func TestPlaybookOnlyGoesToExecuteWhenShinstackAndWorkTypeExist(t *testing.T) {
	withPlaybooks(t, map[string]string{"feature.md": "FEATURE PLAYBOOK"})

	h := evidenceHarness(t, "feature", "", "execute", "quality")
	h.next(t)
	h.review(t, 0, "none")
	if qp := h.next(t); strings.Contains(qp, "FEATURE PLAYBOOK") {
		t.Fatalf("quality stage got the playbook: %s", qp)
	}

	if p := evidenceHarness(t, "", "").next(t); strings.Contains(p, "PLAYBOOK") {
		t.Fatalf("order with no work type got a playbook: %s", p)
	}

	old := shinstackRoots
	shinstackRoots = func() []string { return nil } // shinstack not installed
	t.Cleanup(func() { shinstackRoots = old })
	if p := evidenceHarness(t, "feature", "").next(t); strings.Contains(p, "PLAYBOOK") || !strings.Contains(p, "Use Skill(execute)") {
		t.Fatalf("without shinstack the stage should run on its own skill text: %s", p)
	}
}

func TestFailingRepoChecksRejectTheExecuteStage(t *testing.T) {
	h := evidenceHarness(t, "feature", checksMD)
	var ran []string
	old := runRepoCheck
	runRepoCheck = func(_ context.Context, dir, cmd string) (string, error) {
		ran = append(ran, cmd)
		return "FAIL: TestThing", errors.New("exit status 1")
	}
	t.Cleanup(func() { runRepoCheck = old })

	h.next(t)
	if len(ran) != 1 || ran[0] != "pnpm check" {
		t.Fatalf("checks run = %v, want [pnpm check]", ran)
	}
	h.nextUntilFailed(t) // sent back with the reason until attempts run out
	h.failedWith(t, "failing checks")
	if len(h.ln.pushes) != 0 {
		t.Fatal("a stage with failing checks was pushed")
	}
}

func TestPassingRepoChecksLetTheStageThrough(t *testing.T) {
	h := evidenceHarness(t, "feature", checksMD)
	old := runRepoCheck
	runRepoCheck = func(context.Context, string, string) (string, error) { return "ok", nil }
	t.Cleanup(func() { runRepoCheck = old })

	h.next(t)
	h.shipped(t)
}

func TestRealFailingCheckCommandRejectsTheStage(t *testing.T) {
	h := evidenceHarness(t, "feature", "Before a PR:\n- `exit 3`\n")
	h.nextUntilFailed(t)
	h.failedWith(t, "a check command that exits non-zero")
}

func TestBugWithoutTestFirstCommitIsRejected(t *testing.T) {
	h := evidenceHarness(t, "bug", "")
	branchCommits = func(string) ([]commitFiles, error) {
		return []commitFiles{{SHA: "aaaa1111", Files: []string{"loop/x.go", "loop/x_test.go"}}}, nil
	}
	h.nextUntilFailed(t)
	h.failedWith(t, "bug without a test-first commit")
}

func TestBugWithTestFirstCommitShips(t *testing.T) {
	h := evidenceHarness(t, "bug", "")
	branchCommits = func(string) ([]commitFiles, error) {
		return []commitFiles{
			{SHA: "aaaa1111", Files: []string{"loop/x_test.go"}},
			{SHA: "bbbb2222", Files: []string{"loop/x.go"}},
		}, nil
	}
	h.next(t)
	h.shipped(t)
}

func TestFeatureNeedsNoTestFirstCommit(t *testing.T) {
	h := evidenceHarness(t, "feature", "")
	branchCommits = func(string) ([]commitFiles, error) {
		t.Fatal("feature order asked for the test-first check")
		return nil, nil
	}
	h.next(t)
	h.shipped(t)
}

func TestRepoChecksParse(t *testing.T) {
	cases := map[string]struct {
		md   string
		want []string
	}{
		"one":                         {"Before a PR:\n- `pnpm check`\n", []string{"pnpm check"}},
		"blank line after the header": {"Before a PR:\n\n- `a`\n", []string{"a"}},
		"several":                     {"x\nBefore a PR:\n- `a`\n* `b c`\n\nlater\n- `d`\n", []string{"a", "b c"}},
		"none":                        {"# nothing\n", nil},
		"in a fence":                  {"```\nBefore a PR:\n- `a`\n```\n", nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte(tc.md), 0o644); err != nil {
				t.Fatal(err)
			}
			got := repoChecks(dir)
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Fatalf("repoChecks = %v, want %v", got, tc.want)
			}
		})
	}
	if got := repoChecks(t.TempDir()); got != nil {
		t.Fatalf("repoChecks without CLAUDE.md = %v, want none", got)
	}
}

func TestCheckTestFirst(t *testing.T) {
	c := func(sha string, files ...string) commitFiles { return commitFiles{SHA: sha, Files: files} }
	cases := []struct {
		name    string
		commits []commitFiles
		ok      bool
	}{
		{"tests then fix", []commitFiles{c("a", "x_test.go"), c("b", "x.go")}, true},
		{"fix commit carrying its test is not test-only", []commitFiles{c("a", "x.go", "x_test.go")}, false},
		{"fix then test", []commitFiles{c("a", "x.go"), c("b", "x_test.go")}, false},
		{"only tests", []commitFiles{c("a", "x_test.go")}, false},
		{"no commits", nil, false},
		{"tests, docs, fix", []commitFiles{c("a", "web/app.spec.ts"), c("b", "README.md"), c("c", "x.go")}, true},
		{"notes commit first is neutral", []commitFiles{c("a", "brain/note.md"), c("b", "x_test.go"), c("c", "x.go")}, true},
		{"docs-only branch has no fix", []commitFiles{c("a", "x_test.go"), c("b", "README.md")}, false},
		{"tests plus docs first, then fix", []commitFiles{c("a", "x_test.go", "README.md"), c("b", "x.go")}, true},
		{"testdata counts as test", []commitFiles{c("a", "pkg/testdata/in.json"), c("b", "x.go")}, true},
		{"shell test script", []commitFiles{c("a", "scripts/supply-chain/test.sh"), c("b", "x.go")}, true},
		{"shell test script by suffix", []commitFiles{c("a", "scripts/check_test.sh"), c("b", "x.go")}, true},
		{"bats test", []commitFiles{c("a", "scripts/x.bats"), c("b", "x.go")}, true},
		{"non-test shell script is code", []commitFiles{c("a", "scripts/build.sh"), c("b", "x.go")}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkTestFirst(tc.commits, nil); (err == nil) != tc.ok {
				t.Fatalf("checkTestFirst err = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

func TestBranchCommitsListsOnlyTheBranchsOwnCommitsOldestFirst(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name string) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-b", "main")
	write("base.go")
	git("add", "-A")
	git("commit", "-m", "base")
	git("checkout", "-b", "order-42")
	write("x_test.go")
	git("add", "-A")
	git("commit", "-m", "test")
	write("x.go")
	git("add", "-A")
	git("commit", "-m", "fix")

	commits, err := gitBranchCommits(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 2 || commits[0].Files[0] != "x_test.go" || commits[1].Files[0] != "x.go" {
		t.Fatalf("commits = %+v, want the test commit then the fix commit", commits)
	}
	if err := checkTestFirst(commits, nil); err != nil {
		t.Fatalf("real branch rejected: %v", err)
	}
}

func TestVersionLessOrdersNumerically(t *testing.T) {
	if !versionLess("0.9.0", "0.10.0") || versionLess("0.22.1", "0.22.0") || versionLess("1.0", "1.0.0") {
		t.Fatal("versionLess misorders dotted versions")
	}
}

func TestWorkTypeRoundTripsAndRejectsUnknown(t *testing.T) {
	file, err := orderx.ParseCompactOrders([]byte(`{"orders":[{"id":"1","work_type":"bug","stages":[{"do":"execute"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	exp, err := orderx.ExpandCompactOrders(file)
	if err != nil || exp.Orders[0].WorkType != "bug" {
		t.Fatalf("expanded work type = %q, err %v", exp.Orders[0].WorkType, err)
	}
	if _, err := orderx.ParseCompactOrders([]byte(`{"orders":[{"id":"1","work_type":"chore","stages":[{"do":"execute"}]}]}`)); err == nil {
		t.Fatal("unknown work_type accepted")
	}
}

func TestAmendmentCanCorrectTheWorkType(t *testing.T) {
	existing := Order{ID: "1", Status: OrderStatusActive, WorkType: "feature", Stages: []Stage{{TaskKey: "execute", Status: StageStatusActive}}}
	incoming := Order{ID: "1", Status: OrderStatusActive, WorkType: "bug", Stages: []Stage{{TaskKey: "execute", Status: StageStatusPending}, {TaskKey: "quality", Status: StageStatusPending}}}
	if merged, _ := mergeAmendedActiveOrder(existing, incoming); merged.WorkType != "bug" {
		t.Fatalf("merged work type = %q, want the corrected bug", merged.WorkType)
	}
}

func TestChecksAreReadFromTheMainCheckoutNotTheWorktree(t *testing.T) {
	h := evidenceHarness(t, "feature", "")
	wt := h.l.worktreePath(h.l.cookWorktreeName("42", 0, "execute"))
	if err := os.WriteFile(filepath.Join(wt, "CLAUDE.md"), []byte("Before a PR:\n- `exit 9`\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.next(t)
	h.shipped(t) // the cook's own CLAUDE.md declares nothing the loop trusts
}

func TestWorkTypeSurvivesAnAmendment(t *testing.T) {
	existing := Order{ID: "1", Status: OrderStatusActive, WorkType: "bug", Stages: []Stage{{TaskKey: "execute", Status: StageStatusActive}}}
	incoming := Order{ID: "1", Status: OrderStatusActive, Stages: []Stage{{TaskKey: "execute", Status: StageStatusPending}, {TaskKey: "quality", Status: StageStatusPending}}}
	merged, changed := mergeAmendedActiveOrder(existing, incoming)
	if !changed || merged.WorkType != "bug" {
		t.Fatalf("merged work type = %q (changed %v), want bug kept", merged.WorkType, changed)
	}
}

// A slow check on one order's execute stage must not hold up the loop: control
// commands are still processed while it runs, and the stage is accepted when
// the check finishes.
func TestSlowCheckDoesNotBlockControlCommands(t *testing.T) {
	h := evidenceHarness(t, "feature", checksMD)
	started, release := make(chan struct{}), make(chan struct{})
	old := runRepoCheck
	runRepoCheck = func(context.Context, string, string) (string, error) {
		close(started)
		<-release
		return "ok", nil
	}
	t.Cleanup(func() { runRepoCheck = old })

	if err := h.l.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.rt.sessions[h.n].complete("completed")
	h.n++

	done := make(chan error, 1)
	go func() { done <- h.l.Cycle(context.Background()) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the check never started")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the loop cycle is stuck behind the running check")
	}

	cmd, _ := json.Marshal(ControlCommand{ID: "c1", Action: "pause"})
	if err := os.WriteFile(filepath.Join(h.l.runtimeDir, "control.ndjson"), append(cmd, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.l.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.l.cmds.processedIDs["c1"]; !ok {
		t.Fatal("control command not processed while a check was running")
	}
	if len(h.ln.merged) != 0 {
		t.Fatal("stage accepted before its check finished")
	}

	close(release)
	h.l.cmds.processedIDs = map[string]struct{}{}
	h.settleEvidence(t)
	h.shipped(t)
}

// A second order's completion lands while the first order's check is blocked.
func TestSlowCheckDoesNotDelayAnotherOrdersCompletion(t *testing.T) {
	h := evidenceHarness(t, "feature", checksMD)
	if err := h.l.mutateOrdersState(func(o *OrdersFile) (bool, error) {
		o.Orders = append(o.Orders, Order{ID: "43", Title: "Other", Status: OrderStatusActive,
			Stages: []Stage{{TaskKey: "quality", Skill: "quality", Provider: "claude", Model: "claude-opus-4-6", Status: StageStatusPending}}})
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	old := runRepoCheck
	runRepoCheck = func(context.Context, string, string) (string, error) {
		close(started)
		<-release
		return "ok", nil
	}
	t.Cleanup(func() { runRepoCheck = old })
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()

	if err := h.l.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(h.rt.sessions) != 2 {
		t.Fatalf("sessions = %d, want both orders dispatched", len(h.rt.sessions))
	}
	var executeIdx, qualityIdx int
	for i, c := range h.rt.calls {
		if c.Skill == "execute" {
			executeIdx = i
		} else {
			qualityIdx = i
		}
	}
	h.rt.sessions[executeIdx].complete("completed")
	h.rt.sessions[qualityIdx].complete("completed")
	cycleDone := make(chan error, 1)
	go func() { cycleDone <- h.l.Cycle(context.Background()) }()
	<-started
	select {
	case err := <-cycleDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the loop cycle is stuck behind the running check")
	}
	if o, ok := h.l.orderByID("43"); ok {
		for _, s := range o.Stages {
			if s.Status != StageStatusCompleted {
				t.Fatalf("order 43 stage %s = %s, want completed while order 42's check blocks", s.TaskKey, s.Status)
			}
		}
	}
	if !h.l.evidence.running("42") {
		t.Fatal("order 42's check should still be running")
	}
	close(release)
	h.settleEvidence(t)
}

func TestTailNeverSplitsARune(t *testing.T) {
	s := strings.Repeat("é", 10) // two bytes each
	for n := 1; n <= len(s); n++ {
		got := tail(s, n)
		if !utf8.ValidString(got) {
			t.Fatalf("tail(%d) = %q, not valid UTF-8", n, got)
		}
	}
}

func TestCheckTestFirstDeclaredTestPaths(t *testing.T) {
	commits := []commitFiles{{SHA: "a", Files: []string{"ci/verify-go-version.sh"}}, {SHA: "b", Files: []string{"x.go"}}}
	if err := checkTestFirst(commits, nil); err == nil {
		t.Fatal("undeclared script counted as a test")
	}
	if err := checkTestFirst(commits, []string{"ci/verify-*.sh"}); err != nil {
		t.Fatalf("declared test path rejected: %v", err)
	}
	nested := []commitFiles{{SHA: "a", Files: []string{"scripts/a/b/test.sh"}}, {SHA: "b", Files: []string{"x.go"}}}
	if err := checkTestFirst(nested, []string{"scripts/**/test*.sh"}); err != nil {
		t.Fatalf("nested declared test path rejected: %v", err)
	}
}

package loop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guangshinhaha/shinfactory/config"
	loopruntime "github.com/guangshinhaha/shinfactory/runtime"
)

// ghCalls reads the stub gh's call log; empty when gh was never run.
func ghCalls(logPath string) string {
	b, _ := os.ReadFile(logPath)
	return string(b)
}

func newOrderRepo(t *testing.T, branch string) (work string) {
	t.Helper()
	remote := t.TempDir()
	gitIn(t, remote, "init", "--bare", "-b", "main")
	work = t.TempDir()
	gitIn(t, work, "init", "-b", branch)
	gitIn(t, work, "remote", "add", "origin", remote)
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, work, "add", ".")
	gitIn(t, work, "commit", "-m", "work")
	return work
}

// The regression: loop tests with default config reached the real gh from the
// real shinfactory worktree and merged its open PR. A test binary must never
// run gh unless a test opts in on purpose.
func TestGHLanderRefusesToRunInsideATestBinaryWithoutOptIn(t *testing.T) {
	logPath := stubGH(t)
	work := newOrderRepo(t, "42-0-task")

	if _, err := (ghLander{}).PushAndEnsureDraft(context.Background(), work, PRSpec{Title: "T", Branch: "42-0-task"}); err == nil {
		t.Fatal("ghLander ran inside a test binary with no opt-in")
	}
	err := (ghLander{}).MarkReadyAndMerge(context.Background(), work, PullRequest{URL: "https://github.test/o/r/pull/7", Branch: "42-0-task", IsDraft: true})
	if err == nil {
		t.Fatal("ghLander merged inside a test binary with no opt-in")
	}
	if calls := ghCalls(logPath); calls != "" {
		t.Fatalf("gh was run from a test:\n%s", calls)
	}
}

func TestLoopWithoutALanderGetsTheFakeUnderTest(t *testing.T) {
	l := New(t.TempDir(), "noodle", config.DefaultConfig(), Dependencies{
		Runtimes: map[string]loopruntime.Runtime{"process": newMockRuntime()},
		Now:      time.Now,
	})
	if _, real := l.lander().(ghLander); real {
		t.Fatal("a loop built with default config and no Lander would call the real gh")
	}
}

// The branch the lander acts on is the order's own, never the branch a human
// happens to have checked out.
func TestGHLanderOnlyActsOnTheOrdersBranch(t *testing.T) {
	t.Setenv("SHINFACTORY_ALLOW_REAL_GH", "1")
	logPath := stubGH(t)
	work := newOrderRepo(t, "guangshinhaha/issue-6-fixes")

	_, err := (ghLander{}).PushAndEnsureDraft(context.Background(), work, PRSpec{Title: "T", Branch: "42-0-task"})
	if err == nil || !strings.Contains(err.Error(), "guangshinhaha/issue-6-fixes") {
		t.Fatalf("err = %v, want a refusal naming the human-named branch", err)
	}
	if _, err := (ghLander{}).PushAndEnsureDraft(context.Background(), work, PRSpec{Title: "T"}); err == nil {
		t.Fatal("a spec with no order branch must be refused")
	}
	if calls := ghCalls(logPath); calls != "" {
		t.Fatalf("gh acted on a branch that is not the order's:\n%s", calls)
	}
	if out := gitIn(t, work, "ls-remote", "origin"); out != "" {
		t.Fatalf("pushed a human-named branch:\n%s", out)
	}
}

// MarkReadyAndMerge confirms the PR it was handed really is the order's PR.
func TestGHLanderMergeRefusesAPRThatIsNotTheOrders(t *testing.T) {
	t.Setenv("SHINFACTORY_ALLOW_REAL_GH", "1")
	bin := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "calls.log")
	script := "#!/bin/sh\necho \"$*\" >> \"" + logPath + "\"\n" +
		"case \"$1 $2\" in\n" +
		"\"pr view\") echo '{\"headRefName\":\"guangshinhaha/issue-6-fixes\",\"state\":\"OPEN\",\"isCrossRepository\":false}' ;;\n" +
		"*) exit 0 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	err := (ghLander{}).MarkReadyAndMerge(context.Background(), t.TempDir(), PullRequest{URL: "https://github.test/o/r/pull/41", Branch: "42-0-task", IsDraft: true})
	if err == nil {
		t.Fatal("merged a PR whose head is not the order's branch")
	}
	calls := ghCalls(logPath)
	if strings.Contains(calls, "pr ready") || strings.Contains(calls, "pr merge") {
		t.Fatalf("acted on a foreign PR:\n%s", calls)
	}
}

package loop

import (
	"context"
	"errors"
	"os"
	"os/exec"
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
	logPath := installStubGH(t)
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

// A parked PR that was merged outside the loop (a human, or a merge queue)
// must be recognised as merged. Opening a fresh PR for it is how stray PRs
// like #43 appear.
func TestGHLanderDoesNotOpenAnotherPRForABranchAlreadyMerged(t *testing.T) {
	t.Setenv("SHINFACTORY_ALLOW_REAL_GH", "1")
	work := newOrderRepo(t, "42-0-task")
	head := gitIn(t, work, "rev-parse", "HEAD")
	bin := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "calls.log")
	script := "#!/bin/sh\necho \"$*\" >> \"" + logPath + "\"\n" +
		"case \"$*\" in\n" +
		"*\"--state merged\"*) echo '[{\"url\":\"https://github.test/o/r/pull/9\",\"isDraft\":false,\"baseRefName\":\"main\",\"isCrossRepository\":false,\"headRefOid\":\"" + head + "\"}]' ;;\n" +
		"\"pr list\"*) echo '[]' ;;\n" +
		"\"pr view\"*) echo '{\"headRefName\":\"42-0-task\",\"headRefOid\":\"" + head + "\",\"state\":\"MERGED\",\"isDraft\":false,\"isCrossRepository\":false,\"mergeStateStatus\":\"UNKNOWN\",\"statusCheckRollup\":[]}' ;;\n" +
		"*) echo \"unexpected: $*\" >&2; exit 1 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	pr, err := (ghLander{}).PushAndEnsureDraft(context.Background(), work, PRSpec{Title: "T", Branch: "42-0-task"})
	if err != nil {
		t.Fatalf("PushAndEnsureDraft: %v", err)
	}
	if strings.Contains(ghCalls(logPath), "pr create") {
		t.Fatalf("opened a new PR for a merged branch:\n%s", ghCalls(logPath))
	}
	if err := (ghLander{}).MarkReadyAndMerge(context.Background(), work, pr); err != nil {
		t.Fatalf("a merged PR must count as shipped: %v", err)
	}
}

// No checks reported yet and the PR BLOCKED is how a fresh push looks; it is
// waiting, not refused.
func TestGHLanderTreatsABlockedPRWithNoChecksYetAsPending(t *testing.T) {
	t.Setenv("SHINFACTORY_ALLOW_REAL_GH", "1")
	bin := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "calls.log")
	script := "#!/bin/sh\necho \"$*\" >> \"" + logPath + "\"\n" +
		"case \"$1 $2\" in\n" +
		"\"pr view\") echo '{\"headRefName\":\"42-0-task\",\"state\":\"OPEN\",\"isDraft\":false,\"isCrossRepository\":false,\"mergeStateStatus\":\"BLOCKED\",\"statusCheckRollup\":[]}' ;;\n" +
		"*) echo refused >&2; exit 1 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	err := (ghLander{}).MarkReadyAndMerge(context.Background(), newOrderRepo(t, "42-0-task"), PullRequest{URL: "https://github.test/o/r/pull/7", Branch: "42-0-task"})
	if !errors.Is(err, ErrChecksPending) {
		t.Fatalf("err = %v, want ErrChecksPending", err)
	}
	if !strings.Contains(err.Error(), "required review") {
		t.Fatalf("err = %v, want it to say what the PR waits on", err)
	}
	if strings.Contains(ghCalls(logPath), "pr merge") {
		t.Fatal("tried to merge a PR that had no checks yet")
	}
}

// A hung gh must not stall Cycle: every ship attempt has a deadline.
func TestShipAttemptHasADeadline(t *testing.T) {
	h := newLandingHarness(t, "auto", 1)
	h.runStage(t, 1)
	if !h.ln.deadline {
		t.Fatal("MarkReadyAndMerge ran without a deadline")
	}
}

// GitHub can still show the previous head right after a push. Merging then
// would ship without the final stage's commits.
func TestGHLanderWaitsWhenThePRHeadIsBehindTheWorktree(t *testing.T) {
	t.Setenv("SHINFACTORY_ALLOW_REAL_GH", "1")
	work := newOrderRepo(t, "42-0-task")
	bin := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "calls.log")
	script := "#!/bin/sh\necho \"$*\" >> \"" + logPath + "\"\n" +
		"case \"$1 $2\" in\n" +
		"\"pr view\") echo '{\"headRefName\":\"42-0-task\",\"headRefOid\":\"0000000000000000000000000000000000000000\",\"state\":\"OPEN\",\"isDraft\":false,\"isCrossRepository\":false,\"mergeStateStatus\":\"CLEAN\",\"statusCheckRollup\":[{\"status\":\"COMPLETED\"}]}' ;;\n" +
		"*) exit 0 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	err := (ghLander{}).MarkReadyAndMerge(context.Background(), work, PullRequest{URL: "https://github.test/o/r/pull/7", Branch: "42-0-task"})
	if !errors.Is(err, ErrChecksPending) {
		t.Fatalf("err = %v, want ErrChecksPending while GitHub shows an older head", err)
	}
	if strings.Contains(ghCalls(logPath), "pr merge") {
		t.Fatal("merged a PR whose head is not the worktree's HEAD")
	}
}

// gitHubStub writes a gh that answers `pr view` with viewJSON (a format with
// one %s for the worktree head) and `pr list` with listJSON, logging calls.
func gitHubStub(t *testing.T, head, viewJSON, listJSON string) (logPath string) {
	t.Helper()
	t.Setenv("SHINFACTORY_ALLOW_REAL_GH", "1")
	bin := t.TempDir()
	logPath = filepath.Join(t.TempDir(), "calls.log")
	script := "#!/bin/sh\necho \"$*\" >> \"" + logPath + "\"\n" +
		"case \"$1 $2\" in\n" +
		"\"pr view\") echo '" + strings.ReplaceAll(viewJSON, "HEAD", head) + "' ;;\n" +
		"\"pr list\") echo '" + strings.ReplaceAll(listJSON, "HEAD", head) + "' ;;\n" +
		"\"pr merge\") exit 0 ;;\n" +
		"*) echo \"unexpected: $*\" >&2; exit 1 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

// A human merging an older head must not mark the order done: the final
// commits would never have shipped.
func TestGHLanderRefusesAMergedPRWhoseHeadIsNotTheWorktrees(t *testing.T) {
	work := newOrderRepo(t, "42-0-task")
	gitHubStub(t, "", `{"headRefName":"42-0-task","headRefOid":"0000000000000000000000000000000000000000","state":"MERGED","isDraft":false,"isCrossRepository":false,"mergeStateStatus":"UNKNOWN","statusCheckRollup":[]}`, `[]`)
	err := (ghLander{}).MarkReadyAndMerge(context.Background(), work, PullRequest{URL: "https://github.test/o/r/pull/7", Branch: "42-0-task"})
	if err == nil || errors.Is(err, ErrChecksPending) {
		t.Fatalf("err = %v, want a hard refusal for a merged older head", err)
	}
}

// A merge queue merges and deletes the branch. Pushing again would put it back.
func TestGHLanderDoesNotPushABranchWhoseMergedPRHasThisHead(t *testing.T) {
	work := newOrderRepo(t, "42-0-task")
	head := gitIn(t, work, "rev-parse", "HEAD")
	gitHubStub(t, head, `{}`, `[{"url":"https://github.test/o/r/pull/9","headRefOid":"HEAD","isCrossRepository":false}]`)
	pr, err := (ghLander{}).PushAndEnsureDraft(context.Background(), work, PRSpec{Title: "T", Branch: "42-0-task"})
	if err != nil || pr.URL != "https://github.test/o/r/pull/9" {
		t.Fatalf("pr=%+v err=%v, want the merged PR", pr, err)
	}
	remote := gitIn(t, work, "remote", "get-url", "origin")
	if out, err := exec.Command("git", "-C", remote, "rev-parse", "--verify", "42-0-task").CombinedOutput(); err == nil {
		t.Fatalf("branch pushed back to origin: %s", out)
	}
}

// The merge ran; a failed status read afterwards must retry, not fail the stage.
func TestGHLanderTreatsAFailedReadAfterMergeAsPending(t *testing.T) {
	work := newOrderRepo(t, "42-0-task")
	head := gitIn(t, work, "rev-parse", "HEAD")
	bin := t.TempDir()
	count := filepath.Join(t.TempDir(), "views")
	script := "#!/bin/sh\ncase \"$1 $2\" in\n" +
		"\"pr view\") echo x >> \"" + count + "\"; if [ \"$(wc -l < \"" + count + "\")\" -ge 2 ]; then echo boom >&2; exit 1; fi; " +
		"echo '{\"headRefName\":\"42-0-task\",\"headRefOid\":\"" + head + "\",\"state\":\"OPEN\",\"isDraft\":false,\"isCrossRepository\":false,\"mergeStateStatus\":\"CLEAN\",\"statusCheckRollup\":[{\"status\":\"COMPLETED\"}]}' ;;\n" +
		"\"pr merge\") exit 0 ;;\n*) exit 1 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHINFACTORY_ALLOW_REAL_GH", "1")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	err := (ghLander{}).MarkReadyAndMerge(context.Background(), work, PullRequest{URL: "https://github.test/o/r/pull/7", Branch: "42-0-task"})
	if !errors.Is(err, ErrChecksPending) {
		t.Fatalf("err = %v, want ErrChecksPending after a merge whose status read failed", err)
	}
}

// Right after a push GitHub can report mergeable UNKNOWN. Merging then would
// surface gh's generic conflict error instead of ErrPRConflict, so wait.
func TestGHLanderWaitsWhileMergeabilityIsUnknown(t *testing.T) {
	work := newOrderRepo(t, "42-0-task")
	head := gitIn(t, work, "rev-parse", "HEAD")
	logPath := gitHubStub(t, head, `{"headRefName":"42-0-task","headRefOid":"HEAD","state":"OPEN","isDraft":false,"isCrossRepository":false,"mergeStateStatus":"CLEAN","mergeable":"UNKNOWN","statusCheckRollup":[{"status":"COMPLETED"}]}`, `[]`)
	err := (ghLander{}).MarkReadyAndMerge(context.Background(), work, PullRequest{URL: "https://github.test/o/r/pull/7", Branch: "42-0-task"})
	if !errors.Is(err, ErrChecksPending) {
		t.Fatalf("err = %v, want ErrChecksPending", err)
	}
	if strings.Contains(ghCalls(logPath), "pr merge") {
		t.Fatal("tried to merge while mergeability was unknown")
	}
}

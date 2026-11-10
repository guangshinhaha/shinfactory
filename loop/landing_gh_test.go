package loop

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.test", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.test")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// stubGH puts a gh script first on PATH. It keeps its state in files and logs
// every call, so the test can read what the lander asked GitHub to do.
func stubGH(t *testing.T) (logPath string) {
	t.Helper()
	t.Setenv("SHINFACTORY_ALLOW_REAL_GH", "1")
	return installStubGH(t)
}

// installStubGH installs the stub without the opt-in, to prove the guard.
func installStubGH(t *testing.T) (logPath string) {
	t.Helper()
	bin := t.TempDir()
	state := t.TempDir()
	logPath = filepath.Join(state, "calls.log")
	script := `#!/bin/sh
echo "$*" >> "` + logPath + `"
case "$1 $2" in
"pr list")
  if [ -f "` + state + `/pr" ]; then
    if [ -f "` + state + `/ready" ]; then d=false; else d=true; fi
    echo "[{\"url\":\"https://github.test/o/r/pull/7\",\"isDraft\":$d}]"
  else echo "[]"; fi ;;
"pr view")
  if [ -f "` + state + `/ready" ]; then d=false; else d=true; fi
  if [ -f "` + state + `/merged" ]; then st=MERGED; else st=OPEN; fi
  if [ -f "` + state + `/pending" ]; then r='[{"status":"IN_PROGRESS"}]'; else r='[{"status":"COMPLETED"}]'; fi
  mg=MERGEABLE; ms=CLEAN
  if [ -n "$STUB_MERGEABLE_BY_GIT" ]; then
    # A repository that requires branches to be up to date: a branch that does
    # not contain origin/main reports as conflicting until it is rebased.
    git fetch -q origin main 2>/dev/null
    if ! git merge-base --is-ancestor origin/main HEAD; then mg=CONFLICTING; ms=DIRTY; fi
  fi
  echo "{\"headRefName\":\"${STUB_BRANCH:-order-42}\",\"headRefOid\":\"$(git rev-parse HEAD)\",\"state\":\"$st\",\"isDraft\":$d,\"isCrossRepository\":false,\"mergeStateStatus\":\"$ms\",\"mergeable\":\"$mg\",\"statusCheckRollup\":$r}" ;;
"pr create") touch "` + state + `/pr"; echo https://github.test/o/r/pull/7 ;;
"pr ready") touch "` + state + `/ready" ;;
"pr merge") touch "` + state + `/merged" ;;
"pr comment")
  # gh pr comment <branch> --body <text>: keep each body, separated by a marker line.
  printf '%s\n---\n' "$5" >> "` + state + `/comments.log" ;;
*) echo "unexpected gh call: $*" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func TestGHLanderPushesBranchOpensDraftOnceAndMergesWithoutAdmin(t *testing.T) {
	logPath := stubGH(t)
	remote := t.TempDir()
	gitIn(t, remote, "init", "--bare", "-b", "main")
	work := t.TempDir()
	gitIn(t, work, "init", "-b", "order-42")
	gitIn(t, work, "remote", "add", "origin", remote)
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, work, "add", ".")
	gitIn(t, work, "commit", "-m", "execute")

	ctx := context.Background()
	l := ghLander{}
	pr, err := l.PushAndEnsureDraft(ctx, work, PRSpec{Title: "T", Body: "B", Branch: "order-42"})
	if err != nil {
		t.Fatalf("first push: %v", err)
	}
	if !pr.IsDraft || pr.URL != "https://github.test/o/r/pull/7" {
		t.Fatalf("pr = %+v, want a draft", pr)
	}
	if got := gitIn(t, remote, "rev-parse", "order-42"); got != gitIn(t, work, "rev-parse", "HEAD") {
		t.Fatalf("remote branch %s is not at the worktree HEAD", got)
	}

	// A later stage commits more; the same PR gets the commits and no second PR opens.
	if err := os.WriteFile(filepath.Join(work, "b.txt"), []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, work, "add", ".")
	gitIn(t, work, "commit", "-m", "review fixes")
	if _, err := l.PushAndEnsureDraft(ctx, work, PRSpec{Title: "T", Body: "B", Branch: "order-42"}); err != nil {
		t.Fatalf("second push: %v", err)
	}
	if got := gitIn(t, remote, "rev-parse", "order-42"); got != gitIn(t, work, "rev-parse", "HEAD") {
		t.Fatal("second stage's commit did not reach the remote branch")
	}

	if err := l.MarkReadyAndMerge(ctx, work, pr); err != nil {
		t.Fatalf("merge: %v", err)
	}

	logBytes, _ := os.ReadFile(logPath)
	log := string(logBytes)
	if n := strings.Count(log, "pr create"); n != 1 {
		t.Fatalf("pr create called %d times, want 1:\n%s", n, log)
	}
	if !strings.Contains(log, "pr create --draft") {
		t.Fatalf("PR not opened as draft:\n%s", log)
	}
	if strings.Index(log, "pr ready") < 0 || strings.Index(log, "pr ready") > strings.Index(log, "pr merge") {
		t.Fatalf("PR must be marked ready before it is merged:\n%s", log)
	}
	if strings.Contains(log, "--auto") {
		t.Fatalf("merge must not queue with --auto, or the order is done before it merges:\n%s", log)
	}
	if strings.Contains(log, "--admin") {
		t.Fatalf("merge must never use --admin:\n%s", log)
	}
}

func TestGHLanderRefusesToReuseAPRWithTheWrongBaseOrFromAFork(t *testing.T) {
	remote := t.TempDir()
	gitIn(t, remote, "init", "--bare", "-b", "main")
	work := t.TempDir()
	gitIn(t, work, "init", "-b", "order-42")
	gitIn(t, work, "remote", "add", "origin", remote)
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, work, "add", ".")
	gitIn(t, work, "commit", "-m", "execute")

	for name, listing := range map[string]string{
		"wrong base": `[{"url":"u","isDraft":false,"baseRefName":"release/1.0","isCrossRepository":false}]`,
		"fork":       `[{"url":"u","isDraft":false,"baseRefName":"main","isCrossRepository":true}]`,
	} {
		bin := t.TempDir()
		script := "#!/bin/sh\ncase \"$1 $2\" in\n\"pr list\") echo '" + listing + "' ;;\n*) echo \"unexpected: $*\" >&2; exit 1 ;;\nesac\n"
		if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		if _, err := (ghLander{}).PushAndEnsureDraft(context.Background(), work, PRSpec{Title: "T", Base: "main", Branch: "order-42"}); err == nil {
			t.Errorf("%s: reused a PR that is not this order's", name)
		}
	}
}

func TestPRBodyOmitsOrderRationaleAndPlan(t *testing.T) {
	spec := prSpecFor(Order{ID: "7", Title: "t", Rationale: "Student 12345 failed login", Plan: []string{"call Tan"}}, "", false)
	if strings.Contains(spec.Body, "12345") || strings.Contains(spec.Body, "Tan") {
		t.Fatalf("PR body leaks order text: %q", spec.Body)
	}
	if strings.Contains(strings.ToLower(spec.Body), "closes") || !strings.Contains(spec.Body, "Refs #7") {
		t.Fatalf("PR body = %q, want Refs #7 and no closing keyword for a non-GitHub backlog", spec.Body)
	}
	if !strings.Contains(prSpecFor(Order{ID: "7"}, "", true).Body, "Closes #7") {
		t.Fatal("a GitHub backlog order should close its issue")
	}
}

func TestGHLanderWaitsWhileChecksArePendingAndNeverMergesEarly(t *testing.T) {
	logPath := stubGH(t)
	work := newOrderRepo(t, "order-42")
	ctx := context.Background()
	pr, err := (ghLander{}).PushAndEnsureDraft(ctx, work, PRSpec{Title: "T", Branch: "order-42"})
	if err != nil {
		t.Fatal(err)
	}
	pending := filepath.Join(filepath.Dir(logPath), "pending")
	if err := os.WriteFile(pending, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	err = (ghLander{}).MarkReadyAndMerge(ctx, work, pr)
	if !errors.Is(err, ErrChecksPending) {
		t.Fatalf("err = %v, want ErrChecksPending while a check is running", err)
	}
	if strings.Contains(ghCalls(logPath), "pr merge") {
		t.Fatalf("merged with a check still running:\n%s", ghCalls(logPath))
	}

	if err := os.Remove(pending); err != nil {
		t.Fatal(err)
	}
	if err := (ghLander{}).MarkReadyAndMerge(ctx, work, pr); err != nil {
		t.Fatalf("merge after checks passed: %v", err)
	}
	if !strings.Contains(ghCalls(logPath), "pr merge") {
		t.Fatal("never merged after checks passed")
	}
	// A repeat on an already merged PR is a no-op, not an error.
	if err := (ghLander{}).MarkReadyAndMerge(ctx, work, pr); err != nil {
		t.Fatalf("repeat on merged PR: %v", err)
	}
}

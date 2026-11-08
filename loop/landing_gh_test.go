package loop

import (
	"context"
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
"pr create") touch "` + state + `/pr"; echo https://github.test/o/r/pull/7 ;;
"pr ready") touch "` + state + `/ready" ;;
"pr merge") touch "` + state + `/merged" ;;
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
	pr, err := l.PushAndEnsureDraft(ctx, work, PRSpec{Title: "T", Body: "B"})
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
	if _, err := l.PushAndEnsureDraft(ctx, work, PRSpec{Title: "T", Body: "B"}); err != nil {
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
	if strings.Contains(log, "--admin") {
		t.Fatalf("merge must never use --admin:\n%s", log)
	}
}

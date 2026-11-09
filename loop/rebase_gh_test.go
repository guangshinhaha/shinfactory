package loop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// conflictFixture is a bare remote whose main holds shared.md, plus order-42's
// worktree on a branch off it. Landing another order moves main underneath it.
type conflictFixture struct {
	remote, work string
}

func newConflictFixture(t *testing.T) conflictFixture {
	t.Helper()
	remote := t.TempDir()
	gitIn(t, remote, "init", "--bare", "-b", "main")
	seed := t.TempDir()
	gitIn(t, seed, "init", "-b", "main")
	gitIn(t, seed, "remote", "add", "origin", remote)
	writeIn(t, seed, "shared.md", "top\n\n\n\n\n\n\n\n\n\nbottom\n")
	gitIn(t, seed, "add", ".")
	gitIn(t, seed, "commit", "-m", "base")
	gitIn(t, seed, "push", "origin", "main")

	work := t.TempDir()
	gitIn(t, work, "clone", remote, ".")
	gitIn(t, work, "checkout", "-b", "order-42")
	t.Setenv("STUB_MERGEABLE_BY_GIT", "1")
	// A rebase commits as the configured user; CI has none.
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.test")
	return conflictFixture{remote: remote, work: work}
}

func writeIn(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// landOther lands another order on main with shared.md rewritten by edit.
func (f conflictFixture) landOther(t *testing.T, edit func(string) string) {
	t.Helper()
	other := t.TempDir()
	gitIn(t, other, "clone", f.remote, ".")
	data, err := os.ReadFile(filepath.Join(other, "shared.md"))
	if err != nil {
		t.Fatal(err)
	}
	writeIn(t, other, "shared.md", edit(string(data)))
	gitIn(t, other, "commit", "-am", "other order")
	gitIn(t, other, "push", "origin", "main")
}

func TestSecondOrderEditingTheSameFileRebasesAndLands(t *testing.T) {
	logPath := stubGH(t)
	f := newConflictFixture(t)
	writeIn(t, f.work, "shared.md", "top\n\n\n\n\n\n\n\n\n\nbottom edited by order 42\n")
	gitIn(t, f.work, "commit", "-am", "order 42")
	ctx := context.Background()
	l := ghLander{}
	pr, err := l.PushAndEnsureDraft(ctx, f.work, PRSpec{Title: "T", Branch: "order-42"})
	if err != nil {
		t.Fatal(err)
	}

	f.landOther(t, func(s string) string { return strings.Replace(s, "top", "top edited by the other order", 1) })

	err = l.MarkReadyAndMerge(ctx, f.work, pr)
	if !errors.Is(err, ErrPRConflict) {
		t.Fatalf("err = %v, want ErrPRConflict once main moved under the branch", err)
	}
	if strings.Contains(ghCalls(logPath), "pr merge") {
		t.Fatal("merged a conflicting PR")
	}

	if err := l.RebaseOntoBase(ctx, f.work, "main"); err != nil {
		t.Fatalf("rebase: %v", err)
	}
	if err := l.MarkReadyAndMerge(ctx, f.work, pr); err != nil {
		t.Fatalf("merge after rebase: %v", err)
	}
	if got := gitIn(t, f.remote, "rev-parse", "order-42"); got != gitIn(t, f.work, "rev-parse", "HEAD") {
		t.Fatal("rebased branch was not pushed")
	}
	merged := gitIn(t, f.work, "show", "HEAD:shared.md")
	if !strings.Contains(merged, "edited by the other order") || !strings.Contains(merged, "edited by order 42") {
		t.Fatalf("rebased file lost an edit:\n%s", merged)
	}
}

func TestUnresolvableConflictAbortsTheRebaseAndNamesTheFile(t *testing.T) {
	stubGH(t)
	f := newConflictFixture(t)
	writeIn(t, f.work, "shared.md", "top changed by order 42\n\n\n\n\n\n\n\n\n\nbottom\n")
	gitIn(t, f.work, "commit", "-am", "order 42")
	ctx := context.Background()
	l := ghLander{}
	if _, err := l.PushAndEnsureDraft(ctx, f.work, PRSpec{Title: "T", Branch: "order-42"}); err != nil {
		t.Fatal(err)
	}
	before := gitIn(t, f.work, "rev-parse", "HEAD")

	f.landOther(t, func(s string) string { return strings.Replace(s, "top", "top changed by the other order", 1) })

	var ce *RebaseConflictError
	if err := l.RebaseOntoBase(ctx, f.work, "main"); !errors.As(err, &ce) {
		t.Fatalf("err = %v, want a RebaseConflictError", err)
	} else if len(ce.Files) != 1 || ce.Files[0] != "shared.md" {
		t.Fatalf("conflict files = %v, want [shared.md]", ce.Files)
	}
	if after := gitIn(t, f.work, "rev-parse", "HEAD"); after != before {
		t.Fatal("a failed rebase moved the branch")
	}
	if status := gitIn(t, f.work, "status", "--porcelain"); status != "" {
		t.Fatalf("worktree left dirty after the abort:\n%s", status)
	}
	if got := gitIn(t, f.remote, "rev-parse", "order-42"); got != before {
		t.Fatal("a failed rebase pushed to the remote")
	}

	files, err := l.ConflictingFiles(ctx, f.work, "main")
	if err != nil || len(files) != 1 || files[0] != "shared.md" {
		t.Fatalf("ConflictingFiles = %v, %v, want [shared.md]", files, err)
	}
}

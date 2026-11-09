package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// Two orders debrief in parallel worktrees; each commits only its own note
// file. Both merge into main with no conflict and the generated index lists both.
func TestParallelDebriefsMergeWithoutConflict(t *testing.T) {
	script, err := filepath.Abs("scripts/brain-index.sh")
	if err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	runGit(t, repo, "init", "-q", "-b", "main")
	if err := os.MkdirAll(filepath.Join(repo, "brain", "codebase"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(repo, "brain", "codebase", "seed.md"), []byte("# Seed\n"), 0o644)
	os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("brain/index.md\n"), 0o644)
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-qm", "seed")

	orders := []string{"order-a", "order-b"}
	trees := map[string]string{}
	for _, o := range orders {
		wt := filepath.Join(t.TempDir(), o)
		runGit(t, repo, "worktree", "add", "-q", "-b", o, wt)
		trees[o] = wt
	}
	var wg sync.WaitGroup
	for _, o := range orders {
		wg.Add(1)
		go func(o string) {
			defer wg.Done()
			wt := trees[o]
			os.MkdirAll(filepath.Join(wt, "brain", "notes"), 0o755)
			os.WriteFile(filepath.Join(wt, "brain", "notes", o+".md"), []byte("# "+o+"\n- learning\n"), 0o644)
			if out, err := exec.Command("sh", script, filepath.Join(wt, "brain")).CombinedOutput(); err != nil {
				t.Errorf("%s: index: %v\n%s", o, err, out)
				return
			}
			runGit(t, wt, "add", "-A")
			runGit(t, wt, "commit", "-qm", "debrief "+o)
		}(o)
	}
	wg.Wait()

	for _, o := range orders {
		if st := runGit(t, trees[o], "ls-files", "brain/index.md"); strings.TrimSpace(st) != "" {
			t.Fatalf("%s committed brain/index.md", o)
		}
		runGit(t, repo, "merge", "-q", "--no-edit", o)
	}
	if out, err := exec.Command("sh", script, filepath.Join(repo, "brain")).CombinedOutput(); err != nil {
		t.Fatalf("index: %v\n%s", err, out)
	}
	idx, _ := os.ReadFile(filepath.Join(repo, "brain", "index.md"))
	for _, want := range []string{"[[notes/order-a]]", "[[notes/order-b]]", "[[codebase/seed]]"} {
		if !strings.Contains(string(idx), want) {
			t.Errorf("index missing %s:\n%s", want, idx)
		}
	}
}

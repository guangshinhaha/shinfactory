package loop

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/guangshinhaha/shinfactory/config"
	"github.com/guangshinhaha/shinfactory/worktree"
)

func TestLandingModeDefaultsFromTheProjectRemote(t *testing.T) {
	githubRepo := t.TempDir()
	gitIn(t, githubRepo, "init", "-b", "main")
	gitIn(t, githubRepo, "remote", "add", "origin", "git@github.com:o/r.git")
	httpsRepo := t.TempDir()
	gitIn(t, httpsRepo, "init", "-b", "main")
	gitIn(t, httpsRepo, "remote", "add", "origin", "https://github.com/o/r.git")
	noRemote := t.TempDir()
	gitIn(t, noRemote, "init", "-b", "main")
	lookalike := t.TempDir()
	gitIn(t, lookalike, "init", "-b", "main")
	gitIn(t, lookalike, "remote", "add", "origin", "git@gitlab.com:org/github.com-mirror.git")
	otherHost := t.TempDir()
	gitIn(t, otherHost, "init", "-b", "main")
	gitIn(t, otherHost, "remote", "add", "origin", "https://gitlab.example/o/r.git")

	cases := []struct {
		name, dir, explicit, want string
	}{
		{"github ssh remote defaults to pr", githubRepo, "", config.LandingModePR},
		{"github https remote defaults to pr", httpsRepo, "", config.LandingModePR},
		{"no remote falls back to local", noRemote, "", config.LandingModeLocal},
		{"non-github remote falls back to local", otherHost, "", config.LandingModeLocal},
		{"github.com only in the path is not github", lookalike, "", config.LandingModeLocal},
		{"explicit local wins over a github remote", githubRepo, config.LandingModeLocal, config.LandingModeLocal},
		{"explicit pr wins over no remote", noRemote, config.LandingModePR, config.LandingModePR},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.Landing.Mode = tc.explicit
			l := New(tc.dir, "noodle", cfg, Dependencies{})
			if got := l.config.Landing.Mode; got != tc.want {
				t.Fatalf("landing mode = %q, want %q", got, tc.want)
			}
		})
	}
}

// Order 1's PR merges on the remote, so local main is stale. Order 2's
// worktree must still branch from what the remote now holds, with no conflict.
func TestSecondOrderBranchesFromTheCommitTheFirstOrdersPRMerged(t *testing.T) {
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
	stale := gitIn(t, project, "rev-parse", "main")

	// Order 1 edits shared.txt on its own branch; its PR squash-merges on the
	// remote only (a second clone stands in for GitHub). Local main stays put.
	gh := t.TempDir()
	gitIn(t, gh, "clone", remote, ".")
	writeFile(t, filepath.Join(gh, "shared.txt"), "base\norder 1\n")
	gitIn(t, gh, "commit", "-am", "order 1 merged")
	gitIn(t, gh, "push", "origin", "main")
	merged := gitIn(t, gh, "rev-parse", "HEAD")
	if gitIn(t, project, "rev-parse", "main") != stale {
		t.Fatal("fixture broken: local main moved")
	}

	l := New(project, "noodle", config.DefaultConfig(), Dependencies{Worktree: &optsWorktree{}})
	// This fixture's remote is a local path, so set pr as a GitHub project resolves to.
	l.config.Landing.Mode = config.LandingModePR

	from, err := fetchBase(project, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := gitIn(t, project, "rev-parse", from); got != merged {
		t.Fatalf("base %s resolves to %s, want the merged commit %s (stale main is %s)", from, got, merged, stale)
	}

	// Order 2 edits the same file from that base and fast-forwards onto it.
	wtDir := filepath.Join(project, ".worktrees", "order-2")
	gitIn(t, project, "worktree", "add", "-b", "order-2", wtDir, from)
	writeFile(t, filepath.Join(wtDir, "shared.txt"), "base\norder 1\norder 2\n")
	gitIn(t, wtDir, "commit", "-am", "order 2")
	gitIn(t, wtDir, "push", "origin", "order-2")
	gitIn(t, gh, "fetch", "origin")
	gitIn(t, gh, "merge", "--ff-only", "origin/order-2") // would fail on any conflict or divergence
	if got := gitIn(t, gh, "show", "HEAD:shared.txt"); got != "base\norder 1\norder 2" {
		t.Fatalf("merged shared.txt = %q", got)
	}

	// And the loop itself passes that base to Create.
	l.deps.Worktree = &optsWorktree{}
	if _, err := l.ensureWorktree("order-3"); err != nil {
		t.Fatalf("ensureWorktree: %v", err)
	}
	ow := l.deps.Worktree.(*optsWorktree)
	if len(ow.from) != 1 || ow.from[0] != "refs/remotes/origin/main" {
		t.Fatalf("Create From = %v, want [refs/remotes/origin/main]", ow.from)
	}
}

func TestLocalModeCreatesWorktreesFromHEAD(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Landing.Mode = config.LandingModeLocal
	ow := &optsWorktree{}
	l := New(t.TempDir(), "noodle", cfg, Dependencies{Worktree: ow})
	if _, err := l.ensureWorktree("order-1"); err != nil {
		t.Fatal(err)
	}
	if len(ow.from) != 1 || ow.from[0] != "" {
		t.Fatalf("local mode Create From = %v, want one call with no From", ow.from)
	}
}

func TestPRModeFailsLoudlyWhenTheBaseCannotBeFetched(t *testing.T) {
	fetchBase = fetchLandingBase
	t.Cleanup(func() { fetchBase = func(string, string) (string, error) { return "origin/main", nil } })
	project := t.TempDir()
	gitIn(t, project, "init", "-b", "main")
	cfg := config.DefaultConfig()
	cfg.Landing.Mode = config.LandingModePR
	ow := &optsWorktree{}
	l := New(project, "noodle", cfg, Dependencies{Worktree: ow})
	if _, err := l.ensureWorktree("order-1"); err == nil {
		t.Fatal("ensureWorktree succeeded with no origin to fetch")
	}
	if len(ow.from) != 0 {
		t.Fatal("a worktree was created from a base that was never fetched")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// optsWorktree records the From each Create was asked for ("" when none).
type optsWorktree struct {
	fakeWorktree
	from []string
}

func (o *optsWorktree) Create(name string, opts ...worktree.CreateOpts) error {
	from := ""
	if len(opts) > 0 {
		from = opts[0].From
	}
	o.from = append(o.from, from)
	return nil
}

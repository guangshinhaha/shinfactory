package loop

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/guangshinhaha/shinfactory/config"
)

// resolveLandingMode returns an explicit mode unchanged. An unset mode becomes
// "pr" when the project's origin is on GitHub and "local" when it has no
// GitHub remote.
func resolveLandingMode(projectDir, mode string) string {
	if mode != "" {
		return mode
	}
	if hasGitHubRemote(projectDir) {
		return config.LandingModePR
	}
	return config.LandingModeLocal
}

// hasGitHubRemote reports whether the project's origin URL points at GitHub.
func hasGitHubRemote(projectDir string) bool {
	out, err := exec.Command("git", "-C", projectDir, "config", "--get", "remote.origin.url").Output()
	if err != nil {
		return false
	}
	return githubRemoteURL.MatchString(strings.TrimSpace(string(out)))
}

// githubRemoteURL matches github.com as the URL's host: after "@" or "//",
// before ":" or "/".
var githubRemoteURL = regexp.MustCompile(`(?i)(@|//)(www\.)?github\.com[:/]`)

// fetchMu serializes the fetch itself: concurrent dispatches update the same
// ref. The default-branch lookup does not write, so it runs outside the lock.
var fetchMu sync.Mutex

// fetchTimeout bounds all of one dispatch's git network calls together, so a
// hung remote stalls the loop for this long once, not once per call.
var fetchTimeout = time.Minute

// gitRemote runs git against the project's origin without ever prompting for
// credentials, so a missing login fails instead of hanging the loop.
func gitRemote(ctx context.Context, projectDir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", projectDir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// fetchBase is fetchLandingBase; test binaries swap it so loop tests with fake
// worktrees need no remote.
var fetchBase = fetchLandingBase

// fetchLandingBase fetches the remote default branch (or base, when set) and
// returns the remote-tracking ref a new order worktree branches from. A failed
// fetch is an error: branching from a stale local main is what this prevents.
func fetchLandingBase(projectDir, base string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
	defer cancel()
	if base == "" {
		out, err := gitRemote(ctx, projectDir, "ls-remote", "--symref", "origin", "HEAD")
		if err != nil {
			return "", fmt.Errorf("resolve origin default branch: %w: %s", err, strings.TrimSpace(out))
		}
		for _, line := range strings.Split(out, "\n") {
			if rest, ok := strings.CutPrefix(line, "ref: refs/heads/"); ok {
				base, _, _ = strings.Cut(rest, "\t")
				break
			}
		}
		if base == "" {
			return "", fmt.Errorf("origin default branch not found in ls-remote output %q", strings.TrimSpace(out))
		}
	}
	ref := "refs/remotes/origin/" + base
	fetchMu.Lock()
	defer fetchMu.Unlock()
	if out, err := gitRemote(ctx, projectDir, "fetch", "origin", "+refs/heads/"+base+":"+ref); err != nil {
		return "", fmt.Errorf("fetch origin %s: %w: %s", base, err, strings.TrimSpace(out))
	}
	return ref, nil
}

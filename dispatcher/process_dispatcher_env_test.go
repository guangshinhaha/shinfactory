package dispatcher

import (
	"os/exec"
	"strings"
	"testing"
)

func TestSessionEnvPointsAgentAtProjectFromWorktree(t *testing.T) {
	t.Setenv("NOODLE_PROJECT_DIR", "/stale/from/parent")
	d := NewProcessDispatcher(ProcessDispatcherConfig{ProjectDir: "/project", RuntimeDir: "/project/.noodle"})

	cmd := exec.Command("sh", "-c", `printf '%s|%s' "$NOODLE_PROJECT_DIR" "$NOODLE_SESSION_ID"`)
	cmd.Dir = t.TempDir()
	cmd.Env = d.sessionEnv(DispatchRequest{WorktreePath: cmd.Dir}, "s1")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "/project|s1" {
		t.Fatalf("agent env = %q, want /project|s1", got)
	}
}

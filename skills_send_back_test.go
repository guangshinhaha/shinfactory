package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A send-back of a ready issue must go through the backlog `stop` adapter
// (comment, remove ready, add needs-shin). Removing `ready` by hand leaves the
// issue with no handoff label (issue #73).
func TestSendBackSkillsUseBacklogStop(t *testing.T) {
	for _, rel := range []string{".agents/skills/schedule/SKILL.md", ".agents/skills/todo/SKILL.md"} {
		raw, err := os.ReadFile(filepath.FromSlash(rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		text := string(raw)
		if !strings.Contains(text, "backlog-stop") {
			t.Errorf("%s: send-back does not name the backlog-stop adapter", rel)
		}
		if strings.Contains(text, "--remove-label") {
			t.Errorf("%s: send-back removes ready by hand instead of calling backlog-stop", rel)
		}
		if strings.Contains(text, "not built.") {
			t.Errorf("%s: send-back wording differs from backlog-stop (\"shinfactory: stopped.\")", rel)
		}
	}
}

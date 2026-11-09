package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every shipped skill that names a shinstack skill must also say what to do
// when shinstack is not installed, so a stage never fails on a missing skill.
func TestShippedSkillsHaveShinstackFallback(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", ".agents", "skills", "*", "SKILL.md"))
	if err != nil || len(files) == 0 {
		t.Fatalf("shipped skills not found: %v", err)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		body := string(raw)
		if !strings.Contains(body, "shinstack:") {
			continue
		}
		if !strings.Contains(body, "not installed") && !strings.Contains(body, "isn't") {
			t.Errorf("%s names a shinstack skill but has no fallback for when shinstack is not installed", f)
		}
	}
}

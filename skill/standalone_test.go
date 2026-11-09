package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every shipped SKILL.md that loads a shinstack skill must carry a labeled
// "Without shinstack" fallback, so a stage never fails on a missing skill.
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
		if !strings.Contains(body, "shinstack is not installed") && !strings.Contains(body, "Without shinstack") {
			t.Errorf("%s loads a shinstack skill but has no \"Without shinstack\" fallback", f)
		}
	}
}

// Skill text must not assume shinstack is present.
func TestShippedSkillsDoNotRequireShinstack(t *testing.T) {
	var files []string
	for _, pat := range []string{"*/SKILL.md", "*/references/*.md"} {
		m, _ := filepath.Glob(filepath.Join("..", ".agents", "skills", pat))
		files = append(files, m...)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "Per shinstack ") {
			t.Errorf("%s states a shinstack dependency as a given; qualify it with \"when installed\"", f)
		}
	}
}

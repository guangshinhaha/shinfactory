package skill

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Floors for the shipped tree. A glob or walk that silently matches fewer
// files than this has stopped guarding anything.
const (
	minSkillFiles = 17 // SKILL.md files
	minDocFiles   = 31 // every .md under .agents/skills
)

var (
	// A load of a shinstack skill: `shinstack:craft`, /shinstack:blast-radius.
	shinstackLoad = regexp.MustCompile(`\bshinstack:[a-z]`)
	// A shinstack dependency stated as a given.
	shinstackGiven = regexp.MustCompile(`(?i)\b(per|see|from) shinstack\b`)
	// Words that make a sentence conditional on shinstack being present.
	shinstackQualifier = regexp.MustCompile(`(?i)installed|skill list|with or without`)
	// A labeled fallback somewhere in the file.
	shinstackFallback = regexp.MustCompile(`shinstack is not installed|Without shinstack`)
	sentenceBreak     = regexp.MustCompile(`[.!?]\s+|\n\s*\n`)
)

// shinstackViolations returns the problems in one skill document: every
// sentence that loads shinstack or states it as a given must itself say
// "when installed" (or equivalent), and a SKILL.md that loads shinstack must
// carry a labeled fallback (references fall under their skill's).
func shinstackViolations(body string, needsFallback bool) []string {
	var out []string
	loads := false
	for _, s := range sentenceBreak.Split(body, -1) {
		isLoad := shinstackLoad.MatchString(s)
		isGiven := shinstackGiven.MatchString(s)
		if !isLoad && !isGiven {
			continue
		}
		loads = loads || isLoad
		if !shinstackQualifier.MatchString(s) {
			out = append(out, "unqualified shinstack reference: "+strings.TrimSpace(s))
		}
	}
	if needsFallback && loads && !shinstackFallback.MatchString(body) {
		out = append(out, `loads a shinstack skill but has no "Without shinstack" fallback`)
	}
	return out
}

func shippedSkillDocs(t *testing.T) (docs []string, skills int) {
	t.Helper()
	root := filepath.Join("..", ".agents", "skills")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(p) != ".md" {
			return nil
		}
		docs = append(docs, p)
		if d.Name() == "SKILL.md" {
			skills++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("shipped skills not readable: %v", err)
	}
	return docs, skills
}

// Skill text must not assume shinstack is present: each load or dependency
// is qualified in its own sentence, and files that load it keep a fallback.
func TestShippedSkillsDoNotRequireShinstack(t *testing.T) {
	docs, skills := shippedSkillDocs(t)
	if skills < minSkillFiles || len(docs) < minDocFiles {
		t.Fatalf("matched %d skills and %d docs, want at least %d and %d", skills, len(docs), minSkillFiles, minDocFiles)
	}
	for _, f := range docs {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range shinstackViolations(string(raw), filepath.Base(f) == "SKILL.md") {
			t.Errorf("%s: %s", f, v)
		}
	}
}

func TestShinstackViolations(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
	}{
		{"conditional load with fallback", "If the `shinstack:craft` skill is in your skill list, load it.\n\n## Without shinstack\nx", 0},
		{"unconditional load, fallback elsewhere", "Load `shinstack:craft` first. Then work.\n\n## Without shinstack\nx", 1},
		{"conditional load, no fallback", "When shinstack is installed, load `shinstack:craft`.", 1},
		{"Per shinstack", "Per shinstack the rule is X.", 1},
		{"See shinstack's", "See shinstack's interrogate rule.", 1},
		{"From shinstack:", "From shinstack: prove-it-works.", 1},
		{"From shinstack when installed", "From shinstack, when installed: prove-it-works.", 0},
		{"qualifier on a wrapped line", "Load `shinstack:craft` first\nwhen it is installed.\n\nWithout shinstack: skip.", 0},
		{"no mention", "Plain text.", 0},
	}
	for _, c := range cases {
		if got := shinstackViolations(c.body, true); len(got) != c.want {
			t.Errorf("%s: got %d violations %v, want %d", c.name, len(got), got, c.want)
		}
	}
}

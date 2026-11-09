package loop

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/guangshinhaha/shinfactory/internal/state"
)

// shinstackModeLine matches the repo's declaration in CLAUDE.md, e.g.
// "shinstack-mode: auto".
var shinstackModeLine = regexp.MustCompile(`(?mi)^\s*shinstack-mode\s*:\s*([a-z]+)\s*$`)

// resolveRunMode returns the repo's declared shinstack-mode when CLAUDE.md
// names one the factory understands, and the configured mode otherwise.
func resolveRunMode(projectDir, configured string) string {
	data, err := os.ReadFile(filepath.Join(projectDir, "CLAUDE.md"))
	if err != nil {
		return configured
	}
	match := shinstackModeLine.FindSubmatch(stripCodeFences(data))
	if match == nil {
		return configured
	}
	declared := strings.ToLower(string(match[1]))
	switch state.RunMode(declared) {
	case state.RunModeAuto, state.RunModeSupervised, state.RunModeManual:
		return declared
	}
	return configured
}

// stripCodeFences drops fenced code blocks, so an example of the declaration in
// the docs is not mistaken for the repo's own.
func stripCodeFences(data []byte) []byte {
	var kept []string
	fenced := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			continue
		}
		if !fenced {
			kept = append(kept, line)
		}
	}
	return []byte(strings.Join(kept, "\n"))
}

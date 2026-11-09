package dispatcher

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// writeDenyShims writes a stub for each denied provider CLI that exits non-zero
// with a message, and returns the directory to put first on PATH. The agent
// binary itself is resolved before this PATH applies, so the session's own
// provider is unaffected. Returns "" when there is nothing to deny.
func writeDenyShims(runtimeDir string, providers []string) (string, error) {
	var names []string
	for _, p := range providers {
		if p = strings.TrimSpace(p); p != "" {
			names = append(names, p)
		}
	}
	if len(names) == 0 {
		return "", nil
	}
	dir := filepath.Join(runtimeDir, "deny-bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	for _, name := range names {
		msg := name + " is blocked: routing.sensitive keeps this repo on one provider"
		files := map[string]string{
			name:          "#!/bin/sh\necho '" + msg + "' >&2\nexit 126\n",
			name + ".cmd": "@echo off\r\necho " + msg + " 1>&2\r\nexit /b 126\r\n",
		}
		for file, body := range files {
			if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o755); err != nil {
				return "", fmt.Errorf("write shim %s: %w", file, err)
			}
		}
	}
	return dir, nil
}

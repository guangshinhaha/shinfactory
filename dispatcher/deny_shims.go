package dispatcher

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/guangshinhaha/shinfactory/internal/shellx"
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
		msg := denyShimMessage(name)
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

func denyShimMessage(name string) string {
	return name + " is blocked: routing.sensitive keeps this repo on one provider"
}

// denyShimScript is a POSIX snippet that writes the same stubs on a remote
// machine and puts them first on PATH. Provider names come from config
// validation, so quoting them is enough. Empty when nothing is denied.
func denyShimScript(providers []string) string {
	var b strings.Builder
	for _, p := range providers {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if b.Len() == 0 {
			b.WriteString("mkdir -p /tmp/deny-bin && PATH=/tmp/deny-bin:$PATH && export PATH")
		}
		file := shellx.Quote("/tmp/deny-bin/" + p)
		fmt.Fprintf(&b, " && printf '#!/bin/sh\\necho %%s >&2\\nexit 126\\n' %s > %s && chmod +x %s",
			shellx.Quote(denyShimMessage(p)), file, file)
	}
	return b.String()
}

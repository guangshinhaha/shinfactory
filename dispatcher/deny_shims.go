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
			name:          shimBody(name),
			name + ".cmd": "@echo off\r\necho " + msg + " 1>&2\r\nexit /b 126\r\n",
		}
		for file, body := range files {
			if err := writeFileAtomic(filepath.Join(dir, file), []byte(body)); err != nil {
				return "", fmt.Errorf("write shim %s: %w", file, err)
			}
		}
	}
	return dir, nil
}

// shimBody is the POSIX stub for a denied CLI, shared by the local and sprite shims.
func shimBody(name string) string {
	return "#!/bin/sh\necho " + shellx.Quote(denyShimMessage(name)) + " >&2\nexit 126\n"
}

// writeFileAtomic renames a finished temp file into place, so a concurrent
// session never execs a truncated stub.
func writeFileAtomic(path string, data []byte) error {
	if have, err := os.ReadFile(path); err == nil && string(have) == string(data) {
		return nil // unchanged: skip the rename, which fails on Windows while a session runs the stub
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".shim-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
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
		fmt.Fprintf(&b, " && printf %%s %s > %s && chmod +x %s", shellx.Quote(shimBody(p)), file, file)
	}
	return b.String()
}

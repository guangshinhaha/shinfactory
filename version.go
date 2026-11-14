package main

import (
	_ "embed"
	"path/filepath"
	"runtime/debug"
	"strings"
)

const fallbackVersion = "dev"

//go:embed VERSION
var canonicalVersion string

func currentVersion() string {
	if version := normalizeVersion(canonicalVersion); version != "" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return fallbackVersion
	}
	if version := normalizeVersion(info.Main.Version); version != "" {
		return version
	}
	return fallbackVersion
}

func normalizeVersion(version string) string {
	version = strings.TrimSpace(version)
	switch version {
	case "", "devel", "(devel)", "unknown":
		return ""
	default:
		return version
	}
}

// buildRevision is stamped at link time by pnpm build (-X main.buildRevision=...).
// Go's own VCS stamp is missing in some worktree builds, so the link-time
// value wins and the VCS stamp is the fallback.
var buildRevision string

// buildSHA is the short git revision this binary was built from, or "dev"
// when the build carries neither stamp.
func buildSHA() string {
	if rev := strings.TrimSpace(buildRevision); rev != "" {
		return shortRevision(rev)
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return fallbackVersion
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" && setting.Value != "" {
			return shortRevision(setting.Value)
		}
	}
	return fallbackVersion
}

// repoName is the name the dashboard shows for the project at dir.
func repoName(dir string) string {
	return filepath.Base(filepath.Clean(dir))
}

func shortRevision(rev string) string {
	if len(rev) > 7 {
		return rev[:7]
	}
	return rev
}

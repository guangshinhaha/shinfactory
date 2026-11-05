package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGH puts a stub `gh` first on PATH. It appends its arguments to a log file and,
// for `issue list --jq <filter>`, evaluates the filter over canned issues with jq so the
// adapter's real filter is exercised.
func fakeGH(t *testing.T, issuesJSON string) (env []string, logPath string) {
	t.Helper()
	dir := t.TempDir()
	logPath = filepath.Join(dir, "gh.log")
	issuesPath := filepath.Join(dir, "issues.json")
	if err := os.WriteFile(issuesPath, []byte(issuesJSON), 0o644); err != nil {
		t.Fatalf("write issues: %v", err)
	}
	stub := `#!/bin/sh
printf '%s\n' "$*" >> "` + logPath + `"
if [ "$1 $2" = "issue list" ]; then
  while [ $# -gt 0 ]; do
    if [ "$1" = "--jq" ]; then jq -c "$2" "` + issuesPath + `"; exit $?; fi
    shift
  done
fi
if [ "$1 $2" = "issue create" ]; then echo "https://github.com/o/r/issues/42"; fi
`
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(stub), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH")), logPath
}

func runGitHubAdapter(t *testing.T, env []string, name, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.Command(filepath.Join("defaults", "adapters", "github", name), args...)
	cmd.Env = env
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", name, err, out)
	}
	return string(out)
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read gh log: %v", err)
	}
	return string(data)
}

func TestGitHubBacklogSyncMapsIssues(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed; the filter runs inside gh in real use")
	}
	issues := `[
  {"number": 7, "title": "Add login", "url": "https://github.com/o/r/issues/7",
   "labels": [{"name": "needs_plan"}], "body": "Context here.\nPlan: brain/plans/07-login/overview.md\n"},
  {"number": 8, "title": "Done already", "url": "https://github.com/o/r/issues/8",
   "labels": [{"name": "shinfactory-done"}], "body": null}
]`
	env, logPath := fakeGH(t, issues)

	out := runGitHubAdapter(t, env, "backlog-sync", "")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("line count = %d, output:\n%s", len(lines), out)
	}

	var first, second map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("parse line 1: %v", err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("parse line 2: %v", err)
	}
	if first["id"] != "7" || first["title"] != "Add login" || first["status"] != "open" {
		t.Fatalf("unexpected first item: %#v", first)
	}
	if first["plan"] != "brain/plans/07-login/overview.md" {
		t.Fatalf("plan = %v", first["plan"])
	}
	if tags, _ := first["tags"].([]any); len(tags) != 1 || tags[0] != "needs_plan" {
		t.Fatalf("tags = %#v", first["tags"])
	}
	if second["status"] != "done" {
		t.Fatalf("done-labelled issue status = %v, want done", second["status"])
	}
	if _, ok := second["plan"]; ok {
		t.Fatalf("issue without a Plan line has plan %v", second["plan"])
	}
	if !strings.Contains(readLog(t, logPath), "issue list --state open") {
		t.Fatalf("sync did not list open issues: %s", readLog(t, logPath))
	}
}

func TestGitHubBacklogSyncLabelFilter(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed")
	}
	env, logPath := fakeGH(t, `[]`)
	env = append(env, "NOODLE_GH_LABEL=factory")
	runGitHubAdapter(t, env, "backlog-sync", "")
	if !strings.Contains(readLog(t, logPath), "--label factory") {
		t.Fatalf("label filter not passed: %s", readLog(t, logPath))
	}
}

func TestGitHubBacklogDoneLabelsWithoutClosing(t *testing.T) {
	env, logPath := fakeGH(t, `[]`)
	runGitHubAdapter(t, env, "backlog-done", "", "12")
	log := readLog(t, logPath)
	if !strings.Contains(log, "issue edit 12 --add-label shinfactory-done") {
		t.Fatalf("done did not label the issue: %s", log)
	}
	if strings.Contains(log, "issue close") {
		t.Fatalf("done closed the issue; it should wait for the push: %s", log)
	}
}

func TestGitHubBacklogAddPrintsIssueNumber(t *testing.T) {
	env, logPath := fakeGH(t, `[]`)
	out := runGitHubAdapter(t, env, "backlog-add", `{"title":"Fix flaky test","section":"Bugs"}`)
	if strings.TrimSpace(out) != "42" {
		t.Fatalf("add printed %q, want 42", out)
	}
	if !strings.Contains(readLog(t, logPath), "--label Bugs") {
		t.Fatalf("section not mapped to a label: %s", readLog(t, logPath))
	}
}

func TestGitHubBacklogEditRenames(t *testing.T) {
	env, logPath := fakeGH(t, `[]`)
	runGitHubAdapter(t, env, "backlog-edit", `{"title":"Sharper title"}`, "3")
	if !strings.Contains(readLog(t, logPath), "issue edit 3 --title Sharper title") {
		t.Fatalf("edit did not rename: %s", readLog(t, logPath))
	}
}

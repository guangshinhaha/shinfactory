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
// for `issue list --label <label> --jq <filter>`, evaluates the filter with jq over the
// canned issues carrying that label, so the adapter's real label and filter are exercised.
// `api ... --input -` calls save stdin to api-input.json beside the log and print issue number 42.
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
  select_label='.'
  while [ $# -gt 0 ]; do
    if [ "$1" = "--label" ]; then select_label="map(select(any(.labels[]; .name == \"$2\")))"; fi
    if [ "$1" = "--jq" ]; then jq -c "$select_label | $2" "` + issuesPath + `"; exit $?; fi
    shift
  done
fi
case "$1 $*" in api*--input*) cat > "` + filepath.Join(dir, "api-input.json") + `"; echo 42 ;; esac
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
   "labels": [{"name": "ready"}, {"name": "needs_plan"}], "body": "Context here.\nPlan: brain/plans/07-login/overview.md\n"},
  {"number": 8, "title": "Done already", "url": "https://github.com/o/r/issues/8",
   "labels": [{"name": "ready"}, {"name": "shinfactory-done"}], "body": null}
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
	if tags, _ := first["tags"].([]any); len(tags) != 2 || tags[1] != "needs_plan" {
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

func syncedIDs(t *testing.T, out string) []string {
	t.Helper()
	ids := []string{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		var item struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			t.Fatalf("parse %q: %v", line, err)
		}
		ids = append(ids, item.ID)
	}
	return ids
}

const labelledIssues = `[
  {"number": 1, "title": "No label", "url": "u", "labels": [], "body": "x"},
  {"number": 2, "title": "Other label", "url": "u", "labels": [{"name": "bug"}], "body": "x"},
  {"number": 3, "title": "Ready", "url": "u", "labels": [{"name": "ready"}], "body": "x"},
  {"number": 4, "title": "Factory", "url": "u", "labels": [{"name": "factory"}], "body": "x"}
]`

func TestGitHubBacklogSyncSkipsIssuesWithoutReadyLabel(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed")
	}
	env, _ := fakeGH(t, labelledIssues)
	ids := syncedIDs(t, runGitHubAdapter(t, env, "backlog-sync", ""))
	if len(ids) != 1 || ids[0] != "3" {
		t.Fatalf("synced issues = %v, want only the ready one [3]", ids)
	}
}

func TestGitHubBacklogSyncReadyLabelIsConfigurable(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed")
	}
	env, _ := fakeGH(t, labelledIssues)
	env = append(env, "NOODLE_GH_LABEL=factory")
	ids := syncedIDs(t, runGitHubAdapter(t, env, "backlog-sync", ""))
	if len(ids) != 1 || ids[0] != "4" {
		t.Fatalf("synced issues = %v, want only the factory one [4]", ids)
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
	if !strings.Contains(readLog(t, logPath), "api repos/{owner}/{repo}/issues/42/labels --method PUT -f labels[]=Bugs") {
		t.Fatalf("section not mapped to a label: %s", readLog(t, logPath))
	}
}

func TestGitHubBacklogAddSendsBodyIntact(t *testing.T) {
	env, logPath := fakeGH(t, `[]`)
	payload := `{"title":"Fix \"flaky\" test","body":"## Goal\nStop the flake.\n\n## Done when\nRunning \"go test\" passes 20 times."}`
	if out := runGitHubAdapter(t, env, "backlog-add", payload); strings.TrimSpace(out) != "42" {
		t.Fatalf("add printed %q, want 42", out)
	}
	sent, err := os.ReadFile(filepath.Join(filepath.Dir(logPath), "api-input.json"))
	if err != nil {
		t.Fatalf("read api input: %v", err)
	}
	var decoded struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := json.Unmarshal(sent, &decoded); err != nil {
		t.Fatalf("api input is not JSON: %v\n%s", err, sent)
	}
	if decoded.Title != `Fix "flaky" test` {
		t.Fatalf("title = %q", decoded.Title)
	}
	if want := "## Goal\nStop the flake.\n\n## Done when\nRunning \"go test\" passes 20 times."; decoded.Body != want {
		t.Fatalf("body = %q, want %q", decoded.Body, want)
	}
	if strings.Contains(readLog(t, logPath), "ready") {
		t.Fatalf("add touched the ready label, which only a person may set: %s", readLog(t, logPath))
	}
}

func TestGitHubBacklogAddRefusesBadPayloads(t *testing.T) {
	for _, tc := range []struct{ name, payload, want string }{
		{"missing title", `{"body":"no title"}`, "title required"},
		{"labels field", `{"title":"x","labels":["ready"]}`, "labels field not accepted"},
		{"ready section", `{"title":"x","section":"ready"}`, "section ready not accepted"},
		{"section with escapes", `{"title":"x","section":"say \"hi\" now"}`, "section has quotes or escapes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, logPath := fakeGH(t, `[]`)
			cmd := exec.Command(filepath.Join("defaults", "adapters", "github", "backlog-add"))
			cmd.Env = env
			cmd.Stdin = strings.NewReader(tc.payload)
			out, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(out), tc.want) {
				t.Fatalf("err = %v, output = %s, want %q", err, out, tc.want)
			}
			if _, statErr := os.Stat(logPath); !os.IsNotExist(statErr) {
				t.Fatalf("gh was called: %s", readLog(t, logPath))
			}
		})
	}
}

func TestGitHubBacklogAddAllowsTheWordLabelsInABody(t *testing.T) {
	env, _ := fakeGH(t, `[]`)
	out := runGitHubAdapter(t, env, "backlog-add", `{"title":"x","body":"the \"labels\" field"}`)
	if strings.TrimSpace(out) != "42" {
		t.Fatalf("add printed %q, want 42", out)
	}
}

func TestGitHubBacklogAddAllowsASectionNamedLabels(t *testing.T) {
	env, logPath := fakeGH(t, `[]`)
	runGitHubAdapter(t, env, "backlog-add", `{"title":"x","section":"labels"}`)
	if !strings.Contains(readLog(t, logPath), "labels[]=labels") {
		t.Fatalf("section labels not applied: %s", readLog(t, logPath))
	}
}

// A labels key spelled with a JSON escape gets past the text check, so add must clear
// whatever labels the API applied.
func TestGitHubBacklogAddClearsLabelsTheAPIApplied(t *testing.T) {
	env, logPath := fakeGH(t, `[]`)
	runGitHubAdapter(t, env, "backlog-add", `{"title":"x","lab\u0065ls":["ready"]}`)
	if !strings.Contains(readLog(t, logPath), "api repos/{owner}/{repo}/issues/42/labels --method DELETE") {
		t.Fatalf("add left the API's labels in place: %s", readLog(t, logPath))
	}
}

func TestGitHubBacklogEditRenames(t *testing.T) {
	env, logPath := fakeGH(t, `[]`)
	runGitHubAdapter(t, env, "backlog-edit", `{"title":"Sharper title"}`, "3")
	if !strings.Contains(readLog(t, logPath), "issue edit 3 --title Sharper title") {
		t.Fatalf("edit did not rename: %s", readLog(t, logPath))
	}
}

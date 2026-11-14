package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/guangshinhaha/shinfactory/config"
)

func TestRunStatusNoActiveCooks(t *testing.T) {
	projectDir := t.TempDir()
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("chdir project dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalDir)
	})

	output := captureStdout(t, func() {
		if err := runStatus(&App{}); err != nil {
			t.Fatalf("runStatus: %v", err)
		}
	})

	if !strings.Contains(output, "no active cooks") {
		t.Fatalf("expected no-active output, got: %q", output)
	}
	if !strings.Contains(output, "orders=0") {
		t.Fatalf("expected orders depth output, got: %q", output)
	}
}

func TestRunStatusReadsSessionsAndOrders(t *testing.T) {
	projectDir := t.TempDir()
	runtimeDir := filepath.Join(projectDir, ".noodle")

	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatalf("mkdir runtime: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(runtimeDir, "status.json"),
		[]byte(`{"active":["order-1"],"loop_state":"draining","max_concurrency":4}`),
		0o644,
	); err != nil {
		t.Fatalf("write status: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(runtimeDir, "orders.json"),
		[]byte(`{"orders":[{"id":"1","stages":[{"status":"pending","provider":"c","model":"m"}],"status":"active"},{"id":"2","stages":[{"status":"pending","provider":"c","model":"m"}],"status":"active"},{"id":"3","stages":[{"status":"pending","provider":"c","model":"m"}],"status":"active"}]}`),
		0o644,
	); err != nil {
		t.Fatalf("write orders: %v", err)
	}

	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("chdir project dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalDir)
	})

	output := captureStdout(t, func() {
		if err := runStatus(&App{}); err != nil {
			t.Fatalf("runStatus: %v", err)
		}
	})

	if !strings.Contains(output, "active cooks=1") {
		t.Fatalf("expected active cook count, got: %q", output)
	}
	if !strings.Contains(output, "orders=3") {
		t.Fatalf("expected orders depth, got: %q", output)
	}
	if !strings.Contains(output, "loop=draining") {
		t.Fatalf("expected loop state, got: %q", output)
	}
}

func TestRunStatusJSONPrintsTheLine(t *testing.T) {
	projectDir := t.TempDir()
	runtimeDir := filepath.Join(projectDir, ".noodle")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "status.json"),
		[]byte(`{"loop_state":"paused","mode":"supervised","max_concurrency":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	originalDir, _ := os.Getwd()
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalDir) })

	var out bytes.Buffer
	if err := runStatusJSON(&App{}, &out); err != nil {
		t.Fatalf("runStatusJSON: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if got["mode"] != "supervised" || got["concurrency"] != float64(2) || got["loop_state"] != "paused" {
		t.Fatalf("header = %v", got)
	}
	for _, key := range []string{"running", "queued", "parked", "needs_shin"} {
		if list, ok := got[key].([]any); !ok || len(list) != 0 {
			t.Fatalf("%s = %v, want []", key, got[key])
		}
	}
}

// An open issue labelled needs-shin is waiting on Shin however it got the
// label: the scheduler's refusal runs the stop script, not the loop's record.
func TestRunStatusJSONListsNeedsShinIssues(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed; the filter runs inside gh in real use")
	}
	env, _ := fakeGH(t, `[
  {"number": 16, "title": "Refused by the scheduler", "url": "https://github.com/o/r/issues/16", "labels": [{"name": "needs-shin"}]},
  {"number": 17, "title": "Ready", "url": "https://github.com/o/r/issues/17", "labels": [{"name": "ready"}]}
]`)
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == "PATH" {
			t.Setenv("PATH", v)
		}
	}
	projectDir := t.TempDir()
	script, err := filepath.Abs(filepath.Join("defaults", "adapters", "github", "backlog-stopped"))
	if err != nil {
		t.Fatal(err)
	}
	app := &App{projectDir: projectDir, Config: config.Config{Adapters: map[string]config.AdapterConfig{
		"backlog": {Scripts: map[string]string{"stopped": script}},
	}}}

	var out bytes.Buffer
	if err := runStatusJSON(app, &out); err != nil {
		t.Fatalf("runStatusJSON: %v", err)
	}
	var got struct {
		NeedsShin []struct {
			Order string `json:"order"`
			Title string `json:"title"`
		} `json:"needs_shin"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if len(got.NeedsShin) != 1 || got.NeedsShin[0].Order != "16" || got.NeedsShin[0].Title != "Refused by the scheduler" {
		t.Fatalf("needs_shin = %+v, want only issue 16", got.NeedsShin)
	}
}

// With no `stopped` script the loop's own record is all there is, and it stands.
func TestRunStatusJSONKeepsRecordedStopsWithoutStoppedScript(t *testing.T) {
	projectDir := t.TempDir()
	runtimeDir := filepath.Join(projectDir, ".noodle")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "needs-shin.json"),
		[]byte(`[{"order":"7","reason":"two rounds failed"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runStatusJSON(&App{projectDir: projectDir}, &out); err != nil {
		t.Fatalf("runStatusJSON: %v", err)
	}
	if !strings.Contains(out.String(), `"order": "7"`) {
		t.Fatalf("recorded stop dropped:\n%s", out.String())
	}
}

func TestRunStatusReportsDeadLoopAsStopped(t *testing.T) {
	projectDir := t.TempDir()
	runtimeDir := filepath.Join(projectDir, ".noodle")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "status.json"),
		[]byte(`{"active":["order-1"],"loop_state":"running"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	dead := exec.Command(os.Args[0], "-test.run=^$")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "noodle.lock"),
		[]byte(strconv.Itoa(dead.ProcessState.Pid())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	originalDir, _ := os.Getwd()
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalDir) })

	output := captureStdout(t, func() {
		if err := runStatus(&App{}); err != nil {
			t.Fatalf("runStatus: %v", err)
		}
	})
	if !strings.Contains(output, "loop=stopped") {
		t.Fatalf("output = %q, want loop=stopped", output)
	}
}

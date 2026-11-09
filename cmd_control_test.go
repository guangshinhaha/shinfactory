package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guangshinhaha/shinfactory/loop"
)

func chdirTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	return dir
}

func TestRunControlRejectsBadInputBeforeQueueing(t *testing.T) {
	dir := chdirTemp(t)
	var out bytes.Buffer
	if err := runControl(&App{}, &out, "explode", nil, controlFlags{}); err == nil || !strings.Contains(err.Error(), `unknown control "explode"`) {
		t.Fatalf("unknown control err = %v", err)
	}
	if err := runControl(&App{}, &out, "requeue", nil, controlFlags{}); err == nil || !strings.Contains(err.Error(), "usage: requeue <order>") {
		t.Fatalf("missing arg err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".noodle", "control.ndjson")); err == nil {
		t.Fatal("a rejected control reached the queue")
	}
}

func TestRunControlQueuesTheCommandAndPrintsTheAck(t *testing.T) {
	dir := chdirTemp(t)
	runtimeDir := filepath.Join(dir, ".noodle")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Stand in for the loop: ack whatever lands in the queue.
	go func() {
		for i := 0; i < 100; i++ {
			data, _ := os.ReadFile(filepath.Join(runtimeDir, "control.ndjson"))
			if line := strings.TrimSpace(string(data)); line != "" {
				var cmd loop.ControlCommand
				_ = json.Unmarshal([]byte(line), &cmd)
				ack, _ := json.Marshal(loop.ControlAck{ID: cmd.ID, Action: cmd.Action, Status: "ok", Message: "value=" + cmd.Value, At: time.Now()})
				_ = os.WriteFile(filepath.Join(runtimeDir, "control-ack.ndjson"), append(ack, '\n'), 0o644)
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()

	var out bytes.Buffer
	if err := runControl(&App{}, &out, "set-max-concurrency", []string{"3"}, controlFlags{timeout: 5 * time.Second}); err != nil {
		t.Fatalf("runControl: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "set-max-concurrency: ok (value=3)" {
		t.Fatalf("output = %q", got)
	}
}

func TestRunControlSaysSoWhenNoLoopAnswers(t *testing.T) {
	chdirTemp(t)
	err := runControl(&App{}, &bytes.Buffer{}, "pause", nil, controlFlags{timeout: 300 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "pause was queued but the loop did not answer") {
		t.Fatalf("err = %v", err)
	}
}

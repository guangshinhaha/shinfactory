package loop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAppendControlCommandQueuesOneLinePerCommand(t *testing.T) {
	dir := t.TempDir()
	for _, action := range []string{"pause", "resume"} {
		if err := AppendControlCommand(dir, ControlCommand{ID: "c-" + action, Action: action}); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "control.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"pause"`) || !strings.Contains(lines[1], `"resume"`) {
		t.Fatalf("queue = %q", data)
	}
}

func TestWaitControlAckFindsTheAckForOneCommand(t *testing.T) {
	dir := t.TempDir()
	acks := []ControlAck{{ID: "other", Action: "pause", Status: "ok"}, {ID: "mine", Action: "drain", Status: "error", Message: "boom"}}
	go func() {
		time.Sleep(150 * time.Millisecond)
		_ = appendAcks(filepath.Join(dir, "control-ack.ndjson"), acks)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ack, err := WaitControlAck(ctx, dir, "mine")
	if err != nil || ack.Status != "error" || ack.Message != "boom" {
		t.Fatalf("ack = %+v, err = %v", ack, err)
	}
}

func TestWaitControlAckTimesOutWithoutALoop(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := WaitControlAck(ctx, t.TempDir(), "x"); err == nil || !strings.Contains(err.Error(), "no ack for command x") {
		t.Fatalf("err = %v", err)
	}
}

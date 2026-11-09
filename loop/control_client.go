package loop

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// AppendControlCommand queues cmd for the loop that owns runtimeDir. The web UI
// and the shinfactory CLI both send controls this way. It takes the lock the loop
// holds while it reads and truncates the queue, so a command can't land in
// between and be lost.
func AppendControlCommand(runtimeDir string, cmd ControlCommand) error {
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		return fmt.Errorf("create control directory: %w", err)
	}
	lock, err := os.OpenFile(filepath.Join(runtimeDir, "control.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open control lock: %w", err)
	}
	defer lock.Close()
	if err := acquireFileLock(lock.Fd()); err != nil {
		return fmt.Errorf("lock control file: %w", err)
	}
	defer func() { _ = releaseFileLock(lock.Fd()) }()

	file, err := os.OpenFile(filepath.Join(runtimeDir, "control.ndjson"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open control file: %w", err)
	}
	defer file.Close()

	line, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("encode control command: %w", err)
	}
	if _, err := file.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("append control command: %w", err)
	}
	return nil
}

// WaitControlAck returns the loop's ack for command id, polling until ctx ends.
// No ack means no loop is reading the queue, or it is busy.
func WaitControlAck(ctx context.Context, runtimeDir, id string) (ControlAck, error) {
	path := filepath.Join(runtimeDir, "control-ack.ndjson")
	for {
		if ack, ok := findAck(path, id); ok {
			return ack, nil
		}
		select {
		case <-ctx.Done():
			return ControlAck{}, fmt.Errorf("no ack for command %s: %w", id, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func findAck(path, id string) (ControlAck, bool) {
	file, err := os.Open(path)
	if err != nil {
		return ControlAck{}, false
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var ack ControlAck
		if json.Unmarshal(scanner.Bytes(), &ack) == nil && ack.ID == id {
			return ack, true
		}
	}
	return ControlAck{}, false
}

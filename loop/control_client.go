package loop

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
// No ack means no loop is reading the queue, or it is busy. Each poll reads only
// the bytes appended since the last one.
func WaitControlAck(ctx context.Context, runtimeDir, id string) (ControlAck, error) {
	path := filepath.Join(runtimeDir, "control-ack.ndjson")
	var offset int64
	for {
		ack, next, ok := findAckFrom(path, id, offset)
		if ok {
			return ack, nil
		}
		offset = next
		select {
		case <-ctx.Done():
			return ControlAck{}, fmt.Errorf("no ack for command %s: %w", id, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// findAckFrom scans complete lines of path from offset for id's ack. It returns
// the offset to resume from: the end of the last complete line read, so a line
// the loop is still writing is read whole next time. A file shorter than offset
// was replaced, so the scan restarts at 0.
func findAckFrom(path, id string, offset int64) (ControlAck, int64, bool) {
	file, err := os.Open(path)
	if err != nil {
		return ControlAck{}, offset, false
	}
	defer file.Close()
	if info, err := file.Stat(); err != nil || info.Size() < offset {
		offset = 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return ControlAck{}, offset, false
	}
	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return ControlAck{}, offset, false // partial or no line left
		}
		offset += int64(len(line))
		var ack ControlAck
		if json.Unmarshal(line, &ack) == nil && ack.ID == id {
			return ack, offset, true
		}
	}
}

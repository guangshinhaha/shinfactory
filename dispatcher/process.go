package dispatcher

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// pipeDrainGrace is how long a reader may sit blocked on an empty pipe after
// the child exits before the read end is closed.
const pipeDrainGrace = 10 * time.Second

// drainReader is a pipe read end that closes itself at EOF and, once the
// child has exited, when a Read has been blocked on an empty pipe for the
// grace period. A reader that is busy elsewhere is never cut off, so a slow
// reader keeps the tail of the output; the idle close releases the fds when a
// grandchild inherited the pipe and outlived the child.
type drainReader struct {
	f *os.File

	mu           sync.Mutex
	blockedSince time.Time // zero while no Read is in flight
	closed       chan struct{}
	closeOnce    sync.Once
}

func newDrainReader(f *os.File) *drainReader {
	return &drainReader{f: f, closed: make(chan struct{})}
}

func (r *drainReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	r.blockedSince = time.Now()
	r.mu.Unlock()
	n, err := r.f.Read(p)
	r.mu.Lock()
	r.blockedSince = time.Time{}
	r.mu.Unlock()
	if err == io.EOF {
		_ = r.Close()
	}
	return n, err
}

func (r *drainReader) Close() error {
	var err error
	r.closeOnce.Do(func() {
		close(r.closed)
		err = r.f.Close()
	})
	return err
}

// closeWhenIdle closes the pipe once a Read has been blocked for grace after
// exitedAt. It returns when the pipe is closed.
func (r *drainReader) closeWhenIdle(exitedAt time.Time, grace time.Duration) {
	tick := grace / 4
	if tick < time.Millisecond {
		tick = time.Millisecond
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-r.closed:
			return
		case <-t.C:
		}
		r.mu.Lock()
		since := r.blockedSince
		r.mu.Unlock()
		if since.IsZero() {
			continue
		}
		if since.Before(exitedAt) {
			since = exitedAt
		}
		if time.Since(since) >= grace {
			_ = r.Close()
			return
		}
	}
}

// ProcessHandle wraps an os/exec.Cmd with lifecycle primitives for child
// process management. It replaces what the tmux dispatcher provided: process isolation,
// liveness checking, and graceful shutdown.
type ProcessHandle struct {
	cmd *exec.Cmd

	stdin  io.WriteCloser
	stdout *drainReader
	stderr *drainReader

	done       chan struct{}
	drainGrace time.Duration
	doneOnce   sync.Once

	mu       sync.Mutex
	exitCode int
	exited   bool
}

// processMetadata is written to process.json for crash recovery.
type processMetadata struct {
	PID       int       `json:"pid"`
	SessionID string    `json:"session_id"`
	StartedAt time.Time `json:"started_at"`
}

// StartProcess spawns a child process with process group isolation and
// returns a handle for lifecycle management.
func StartProcess(cmd *exec.Cmd) (*ProcessHandle, error) {
	return startProcess(cmd, pipeDrainGrace)
}

func startProcess(cmd *exec.Cmd, drainGrace time.Duration) (*ProcessHandle, error) {
	configureChildProcess(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdin pipe: %w", err)
	}
	// Plain os pipes, not cmd.StdoutPipe: Wait closes those as soon as the
	// child exits, which can discard output the reader has not read yet.
	stdout, stdoutW, err := os.Pipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}
	stderr, stderrW, err := os.Pipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stdoutW.Close()
		return nil, fmt.Errorf("create stderr pipe: %w", err)
	}
	cmd.Stdout, cmd.Stderr = stdoutW, stderrW

	err = cmd.Start()
	// The child holds its own copies of the write ends.
	_ = stdoutW.Close()
	_ = stderrW.Close()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, ProcessStartError{Cause: err}
	}

	h := &ProcessHandle{
		cmd:        cmd,
		stdin:      stdin,
		stdout:     newDrainReader(stdout),
		stderr:     newDrainReader(stderr),
		done:       make(chan struct{}),
		drainGrace: drainGrace,
	}

	go h.wait()
	return h, nil
}

// Stdin returns the write end of the child's stdin pipe.
func (h *ProcessHandle) Stdin() io.WriteCloser { return h.stdin }

// Stdout returns the read end of the child's stdout pipe.
func (h *ProcessHandle) Stdout() io.ReadCloser { return h.stdout }

// Stderr returns the read end of the child's stderr pipe.
func (h *ProcessHandle) Stderr() io.ReadCloser { return h.stderr }

// Done returns a channel that is closed when the process exits.
func (h *ProcessHandle) Done() <-chan struct{} { return h.done }

// PID returns the process ID, or 0 if the process hasn't started.
func (h *ProcessHandle) PID() int {
	if h.cmd.Process == nil {
		return 0
	}
	return h.cmd.Process.Pid
}

// ExitCode returns the exit code and whether the process has exited.
func (h *ProcessHandle) ExitCode() (int, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.exitCode, h.exited
}

// Terminate sends SIGTERM to the process group and returns immediately.
func (h *ProcessHandle) Terminate() error {
	return terminateProcess(h.cmd)
}

// ForceKill sends SIGKILL to the process group and returns immediately.
func (h *ProcessHandle) ForceKill() error {
	return forceKillProcess(h.cmd)
}

func (h *ProcessHandle) wait() {
	err := h.cmd.Wait()
	h.mu.Lock()
	h.exited = true
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			h.exitCode = exitErr.ExitCode()
		} else {
			h.exitCode = -1
		}
	}
	h.mu.Unlock()
	h.doneOnce.Do(func() { close(h.done) })
	// The read ends close at EOF. If a grandchild inherited a pipe and outlived
	// the child, EOF never comes, so a reader left blocked on the empty pipe
	// for the grace period is cut loose.
	exitedAt := time.Now()
	go h.stdout.closeWhenIdle(exitedAt, h.drainGrace)
	go h.stderr.closeWhenIdle(exitedAt, h.drainGrace)
}

// Close closes both read ends. Use it when the handle is dropped without its
// output being read to EOF.
func (h *ProcessHandle) Close() {
	_ = h.stdout.Close()
	_ = h.stderr.Close()
}

// WriteProcessMetadata writes process.json to the session directory.
func WriteProcessMetadata(sessionDir, sessionID string, pid int, startedAt time.Time) error {
	sessionDir = strings.TrimSpace(sessionDir)
	if sessionDir == "" {
		return fmt.Errorf("session directory not set")
	}
	payload, err := json.Marshal(processMetadata{
		PID:       pid,
		SessionID: sessionID,
		StartedAt: startedAt.UTC(),
	})
	if err != nil {
		return fmt.Errorf("encode process metadata: %w", err)
	}
	path := filepath.Join(sessionDir, "process.json")
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		return fmt.Errorf("write process metadata: %w", err)
	}
	return nil
}

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

// pipeDrainGrace is how long after exit the read ends stay open.
const pipeDrainGrace = 2 * time.Second

// ProcessHandle wraps an os/exec.Cmd with lifecycle primitives for child
// process management. It replaces what the tmux dispatcher provided: process isolation,
// liveness checking, and graceful shutdown.
type ProcessHandle struct {
	cmd *exec.Cmd

	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser

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
		stdout:     stdout,
		stderr:     stderr,
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
	// Readers get a grace period to drain what the child wrote. After that,
	// closing the read ends releases the fds and unblocks a reader stuck on a
	// grandchild that inherited the pipe and outlived the child.
	time.AfterFunc(h.drainGrace, func() {
		_ = h.stdout.Close()
		_ = h.stderr.Close()
	})
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

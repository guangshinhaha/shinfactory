package dispatcher

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestProcessHandleSpawnAndDone(t *testing.T) {
	cmd := exec.Command("echo", "hello")
	h, err := StartProcess(cmd)
	if err != nil {
		t.Fatalf("StartProcess: %v", err)
	}

	buf := make([]byte, 64)
	n, _ := h.Stdout().Read(buf)
	got := string(buf[:n])
	if got != "hello\n" {
		t.Fatalf("stdout = %q, want %q", got, "hello\n")
	}

	select {
	case <-h.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("process did not exit")
	}

	code, exited := h.ExitCode()
	if !exited {
		t.Fatal("expected exited = true")
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
}

func TestProcessHandleForceKill(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	h, err := StartProcess(cmd)
	if err != nil {
		t.Fatalf("StartProcess: %v", err)
	}

	if err := h.ForceKill(); err != nil {
		t.Fatalf("ForceKill: %v", err)
	}

	select {
	case <-h.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("process did not exit after kill")
	}

	_, exited := h.ExitCode()
	if !exited {
		t.Fatal("expected exited = true after kill")
	}
}

func TestProcessHandlePID(t *testing.T) {
	cmd := exec.Command("echo", "hello")
	h, err := StartProcess(cmd)
	if err != nil {
		t.Fatalf("StartProcess: %v", err)
	}
	pid := h.PID()
	if pid <= 0 {
		t.Fatalf("PID = %d, want > 0", pid)
	}
	<-h.Done()
}

func TestProcessHandleNonZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test requires sh")
	}
	cmd := exec.Command("sh", "-c", "exit 42")
	h, err := StartProcess(cmd)
	if err != nil {
		t.Fatalf("StartProcess: %v", err)
	}
	<-h.Done()

	code, exited := h.ExitCode()
	if !exited {
		t.Fatal("expected exited = true")
	}
	if code != 42 {
		t.Fatalf("exit code = %d, want 42", code)
	}
}

func TestWriteProcessMetadata(t *testing.T) {
	dir := t.TempDir()
	ts := time.Date(2026, 2, 27, 12, 0, 0, 0, time.UTC)

	if err := WriteProcessMetadata(dir, "session-1", 12345, ts); err != nil {
		t.Fatalf("WriteProcessMetadata: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "process.json"))
	if err != nil {
		t.Fatalf("read process.json: %v", err)
	}
	want := `{"pid":12345,"session_id":"session-1","started_at":"2026-02-27T12:00:00Z"}`
	if string(data) != want {
		t.Fatalf("process.json =\n  %s\nwant\n  %s", data, want)
	}
}

func TestProcessHandleReadEndsCloseAfterGraceWhenGrandchildHoldsPipe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	// The background sleep inherits stdout and outlives the shell.
	h, err := startProcess(exec.Command("sh", "-c", "echo hi; sleep 30 &"), 100*time.Millisecond)
	if err != nil {
		t.Fatalf("StartProcess: %v", err)
	}
	t.Cleanup(func() { _ = h.ForceKill() })

	read := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(h.Stdout())
		read <- string(b)
	}()
	select {
	case got := <-read:
		if got != "hi\n" {
			t.Fatalf("stdout = %q, want %q", got, "hi\n")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stdout reader still blocked after the child exited")
	}
}

// A reader that is slow to start still gets everything the child wrote, even
// when the child has exited and the drain grace has long passed.
func TestProcessHandleDeliversFullOutputToSlowReaderAfterExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	h, err := startProcess(exec.Command("sh", "-c", "echo head; echo tail"), 20*time.Millisecond)
	if err != nil {
		t.Fatalf("StartProcess: %v", err)
	}
	select {
	case <-h.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("child did not exit")
	}
	time.Sleep(300 * time.Millisecond) // many grace periods

	got, err := io.ReadAll(h.Stdout())
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if string(got) != "head\ntail\n" {
		t.Fatalf("stdout = %q, want %q", got, "head\ntail\n")
	}
}

// Package fakeorca provides a stand-in `orca` CLI for tests, so nothing
// touches a real Orca. It speaks the slice of `orca orchestration` the orca
// runtime uses: worker-start, worker-list, worker-read and worker-stop.
package fakeorca

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// Fake is a fake orca CLI backed by a state directory.
type Fake struct {
	// Bin is the path of the fake CLI script.
	Bin string
	// Dir holds the fake's state: calls.log, a w-<dispatch> file per worker
	// holding its outcome, spec-<n>.md copies, and the optional limit file.
	Dir string
}

const script = `#!/bin/sh
D="$FAKE_ORCA_DIR"
echo "$*" >> "$D/calls.log"
cmd="$2"
case "$cmd" in
worker-start)
  if [ -f "$D/limit" ]; then
    echo "Claude usage limit reached. Your limit will reset at 3pm." >&2
    exit 1
  fi
  n=$(( $(cat "$D/n" 2>/dev/null || echo 0) + 1 ))
  echo "$n" > "$D/n"
  prev=""
  for a in "$@"; do
    [ "$prev" = "--spec-file" ] && cp "$a" "$D/spec-$n.md"
    prev="$a"
  done
  echo "in_progress" > "$D/w-ctx_fake$n"
  echo "{\"ok\":true,\"result\":{\"dispatchId\":\"ctx_fake$n\",\"state\":\"ready\"}}"
  ;;
worker-list)
  printf '{"ok":true,"result":{"workers":['
  sep=""
  for f in "$D"/w-ctx_fake*; do
    [ -e "$f" ] || continue
    id=$(basename "$f" | sed 's/^w-//')
    outcome=$(cat "$f")
    state=ready
    [ "$outcome" != "in_progress" ] && state=stopped
    printf '%s{"dispatchId":"%s","state":"%s","outcome":"%s"}' "$sep" "$id" "$state" "$outcome"
    sep=","
  done
  printf ']}}\n'
  ;;
worker-read)
  id=""
  prev=""
  for a in "$@"; do
    [ "$prev" = "--dispatch" ] && id="$a"
    prev="$a"
  done
  if [ -f "$D/transcript-$id" ]; then
    text=$(cat "$D/transcript-$id")
  else
    text="done"
  fi
  printf '{"ok":true,"result":{"dispatchId":"%s","transcript":{"messages":[{"role":"user","blocks":[{"type":"text","text":"spec mentions usage limit and rate limit"}]},{"role":"assistant","blocks":[{"type":"text","text":"%s"}]}]}}}\n' "$id" "$text"
  ;;
worker-stop)
  id=""
  prev=""
  for a in "$@"; do
    [ "$prev" = "--dispatch" ] && id="$a"
    prev="$a"
  done
  [ -f "$D/w-$id" ] && echo "failed" > "$D/w-$id"
  echo '{"ok":true,"result":{}}'
  ;;
*)
  echo "fakeorca: unsupported command $cmd" >&2
  exit 2
  ;;
esac
`

// New writes a fake orca into a temp dir and points the process at its state
// directory. It skips the test on Windows, where the fake (a POSIX script) can't run.
func New(t *testing.T) *Fake {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake orca is a POSIX shell script")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "orca")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake orca: %v", err)
	}
	state := filepath.Join(dir, "state")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatalf("make fake orca state dir: %v", err)
	}
	t.Setenv("FAKE_ORCA_DIR", state)
	return &Fake{Bin: bin, Dir: state}
}

// SetLimit makes worker-start fail with a usage-limit error until cleared.
func (f *Fake) SetLimit(t *testing.T, on bool) {
	t.Helper()
	path := filepath.Join(f.Dir, "limit")
	if on {
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatalf("set limit: %v", err)
		}
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		t.Fatalf("clear limit: %v", err)
	}
}

// Settle ends a worker with the given outcome ("succeeded", "failed").
func (f *Fake) Settle(t *testing.T, dispatchID, outcome string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.Dir, "w-"+dispatchID), []byte(outcome), 0o644); err != nil {
		t.Fatalf("settle %s: %v", dispatchID, err)
	}
}

// SetTranscript sets the last assistant message worker-read returns for a worker.
func (f *Fake) SetTranscript(t *testing.T, dispatchID, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.Dir, "transcript-"+dispatchID), []byte(text), 0o644); err != nil {
		t.Fatalf("set transcript: %v", err)
	}
}

// Calls returns every orca invocation so far, one argument string per call.
func (f *Fake) Calls() []string {
	data, err := os.ReadFile(filepath.Join(f.Dir, "calls.log"))
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// Starts returns the worker-start invocations.
func (f *Fake) Starts() []string {
	var starts []string
	for _, call := range f.Calls() {
		if strings.HasPrefix(call, "orchestration worker-start ") {
			starts = append(starts, call)
		}
	}
	return starts
}

// Spec returns the spec the n-th (1-based) worker-start was given.
func (f *Fake) Spec(t *testing.T, n int) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.Dir, "spec-"+strconv.Itoa(n)+".md"))
	if err != nil {
		t.Fatalf("read spec %d: %v", n, err)
	}
	return string(data)
}

// LastDispatch returns the id of the most recently launched worker. A start
// refused for a limit launches nothing, so it does not count.
func (f *Fake) LastDispatch(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.Dir, "n"))
	if err != nil {
		t.Fatalf("no worker launched yet: %v", err)
	}
	return "ctx_fake" + strings.TrimSpace(string(data))
}

// Package fakeorca provides a stand-in `orca` CLI for tests, so nothing
// touches a real Orca. It speaks the slice of `orca orchestration` the orca
// runtime uses: run-create, run-show, worker-start, worker-list, worker-show, worker-read and worker-stop.
package fakeorca

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// Fake is a fake orca CLI backed by a state directory. It models runs: every
// worker belongs to the run it was started with, and worker-list shows only
// the run it is asked for.
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
# row prints a worker the way real Orca reports it: a successful worker is
# state "succeeded" with no outcome, a failed one is "stopped"/"failed".
row() {
  case "$2" in
    succeeded) printf '{"dispatchId":"%s","state":"succeeded"}' "$1" ;;
    in_progress) printf '{"dispatchId":"%s","state":"ready","outcome":"in_progress"}' "$1" ;;
    *) printf '{"dispatchId":"%s","state":"stopped","outcome":"%s"}' "$1" "$2" ;;
  esac
}
case "$cmd" in
worker-start)
  if [ -f "$D/unknown" ]; then
    left=$(cat "$D/unknown")
    if [ "$left" -gt 0 ]; then
      echo $(( left - 1 )) > "$D/unknown"
      echo '{"ok":false,"error":{"code":"selector_not_found","message":"No worktree matches selector"}}' >&2
      exit 1
    fi
  fi
  if [ -f "$D/fenced" ]; then
    left=$(cat "$D/fenced")
    if [ "$left" -gt 0 ]; then
      echo $(( left - 1 )) > "$D/fenced"
      echo '{"ok":false,"error":{"code":"consumer_fenced","message":"Consumer was fenced by a newer run owner"}}' >&2
      exit 1
    fi
  fi
  if [ -f "$D/limit" ]; then
    cat "$D/limit" >&2
    exit 1
  fi
  n=$(( $(cat "$D/n" 2>/dev/null || echo 0) + 1 ))
  echo "$n" > "$D/n"
  prev=""
  run=""
  for a in "$@"; do
    [ "$prev" = "--spec-file" ] && cp "$a" "$D/spec-$n.md"
    [ "$prev" = "--run" ] && run="$a"
    prev="$a"
  done
  echo "in_progress" > "$D/w-ctx_fake$n"
  echo "$run" > "$D/run-ctx_fake$n"
  echo "{\"ok\":true,\"result\":{\"dispatchId\":\"ctx_fake$n\",\"state\":\"ready\"}}"
  ;;
run-create)
  n=$(( $(cat "$D/runs" 2>/dev/null || echo 0) + 1 ))
  echo "$n" > "$D/runs"
  echo "run_fake$n" >> "$D/runs.list"
  echo "{\"ok\":true,\"result\":{\"run\":{\"id\":\"run_fake$n\"}}}"
  ;;
run-show)
  id=""
  prev=""
  for a in "$@"; do
    [ "$prev" = "--id" ] && id="$a"
    prev="$a"
  done
  grep -qx "$id" "$D/runs.list" 2>/dev/null || { echo "no such run" >&2; exit 1; }
  echo "{\"ok\":true,\"result\":{\"run\":{\"id\":\"$id\"}}}"
  ;;
worker-list)
  want=""
  prev=""
  for a in "$@"; do
    [ "$prev" = "--run" ] && want="$a"
    prev="$a"
  done
  printf '{"ok":true,"result":{"workers":['
  sep=""
  for f in "$D"/w-ctx_fake*; do
    [ -e "$f" ] || continue
    id=$(basename "$f" | sed 's/^w-//')
    [ -n "$want" ] && [ "$(cat "$D/run-$id")" != "$want" ] && continue
    printf '%s%s' "$sep" "$(row "$id" "$(cat "$f")")"
    sep=","
  done
  printf ']}}\n'
  ;;
worker-show)
  id=""
  prev=""
  for a in "$@"; do
    [ "$prev" = "--dispatch" ] && id="$a"
    prev="$a"
  done
  [ -f "$D/w-$id" ] || { echo "no such dispatch" >&2; exit 1; }
  printf '{"ok":true,"result":%s}\n' "$(row "$id" "$(cat "$D/w-$id")")"
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
  if [ -f "$D/stopfail" ]; then
    echo '{"ok":false,"error":{"code":"internal","message":"worker-stop failed"}}' >&2
    exit 1
  fi
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
	f.SetLimitMessage(t, on, "Claude usage limit reached.")
}

// SetLimitMessage is SetLimit with the text the refusal prints, so a fixture
// can include a reset time.
func (f *Fake) SetLimitMessage(t *testing.T, on bool, message string) {
	t.Helper()
	path := filepath.Join(f.Dir, "limit")
	if on {
		if err := os.WriteFile(path, []byte(message+"\n"), 0o644); err != nil {
			t.Fatalf("set limit: %v", err)
		}
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		t.Fatalf("clear limit: %v", err)
	}
}

// FailStop makes worker-stop exit non-zero and leave the worker running, until
// cleared.
func (f *Fake) FailStop(t *testing.T, on bool) {
	t.Helper()
	path := filepath.Join(f.Dir, "stopfail")
	if on {
		if err := os.WriteFile(path, []byte("1\n"), 0o644); err != nil {
			t.Fatalf("fail stop: %v", err)
		}
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		t.Fatalf("clear fail stop: %v", err)
	}
}

// RefuseUnknownWorktree makes the next n worker-starts fail with
// selector_not_found, as real Orca does for a worktree it has not noticed yet.
func (f *Fake) RefuseUnknownWorktree(t *testing.T, n int) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.Dir, "unknown"), []byte(strconv.Itoa(n)), 0o644); err != nil {
		t.Fatalf("refuse unknown worktree: %v", err)
	}
}

// FenceConsumer makes the next n worker-starts fail with consumer_fenced, as
// real Orca does for a run whose consumer lost ownership (a host sleep).
func (f *Fake) FenceConsumer(t *testing.T, n int) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.Dir, "fenced"), []byte(strconv.Itoa(n)), 0o644); err != nil {
		t.Fatalf("fence consumer: %v", err)
	}
}

// RunOf is the run a worker was started in.
func (f *Fake) RunOf(t *testing.T, dispatchID string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.Dir, "run-"+dispatchID))
	if err != nil {
		t.Fatalf("read run of %s: %v", dispatchID, err)
	}
	return strings.TrimSpace(string(data))
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

// Runs returns the ids of the runs the fake has created.
func (f *Fake) Runs() []string {
	data, err := os.ReadFile(filepath.Join(f.Dir, "runs.list"))
	if err != nil {
		return nil
	}
	return strings.Fields(string(data))
}

// Worker returns a worker's state as the fake stores it: "in_progress",
// "succeeded", or the failed outcome it was settled or stopped with.
func (f *Fake) Worker(t *testing.T, dispatchID string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.Dir, "w-"+dispatchID))
	if err != nil {
		t.Fatalf("no worker %s: %v", dispatchID, err)
	}
	return strings.TrimSpace(string(data))
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

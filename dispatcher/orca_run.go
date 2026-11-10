package dispatcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/guangshinhaha/shinfactory/internal/filex"
)

// orcaRunFile remembers the factory's Orca run across restarts, so a restarted
// loop can find the workers its previous process left behind.
const orcaRunFile = "orca-run.json"

// orcaRecoverMaxPages bounds how many pages of workers recovery reads.
const orcaRecoverMaxPages = 50

// ensureRun returns the Orca run the factory launches its workers in. Stages
// never land in the coordinator's run: the run is either the configured one,
// the one a previous loop recorded (if Orca still has it), or a new one.
func (d *OrcaDispatcher) ensureRun(ctx context.Context) (string, error) {
	d.runMu.Lock()
	defer d.runMu.Unlock()
	if d.runID != "" {
		return d.runID, nil
	}
	callCtx, cancel := context.WithTimeout(ctx, orcaStartTimeout)
	defer cancel()

	if id := d.recordedRun(); id != "" {
		if _, err := d.run(callCtx, d.bin, "orchestration", "run-show", "--id", id, "--json"); err == nil {
			d.runID = id
			return id, nil
		}
	}
	return d.createRun(callCtx)
}

// replaceRun abandons a run Orca fenced (the host slept and another owner took
// the run) and creates a new one. It does nothing if another caller already
// replaced fenced.
func (d *OrcaDispatcher) replaceRun(ctx context.Context, fenced string) (string, error) {
	d.runMu.Lock()
	defer d.runMu.Unlock()
	if d.runID != "" && d.runID != fenced {
		return d.runID, nil
	}
	callCtx, cancel := context.WithTimeout(ctx, orcaStartTimeout)
	defer cancel()
	return d.createRun(callCtx)
}

// createRun makes a new Orca run and records it. Callers hold runMu.
func (d *OrcaDispatcher) createRun(callCtx context.Context) (string, error) {
	// run-create binds the calling terminal to the new run. Every call the
	// factory makes names its run, so the binding only matters to a loop
	// started from a coordinator's terminal; set runtime.orca.run_id there.
	out, err := d.run(callCtx, d.bin, "orchestration", "run-create",
		"--objective", "shinfactory stages for "+filepath.Base(d.projectDir), "--json")
	if err != nil {
		return "", fmt.Errorf("orca run-create failed: %w", err)
	}
	id, err := parseOrcaRunID(out)
	if err != nil {
		return "", fmt.Errorf("orca run-create reply unreadable: %w", err)
	}
	payload, _ := json.Marshal(map[string]string{"run_id": id})
	if err := filex.WriteFileAtomic(filepath.Join(d.runtimeDir, orcaRunFile), payload); err != nil {
		return "", fmt.Errorf("record orca run: %w", err)
	}
	d.runID = id
	return id, nil
}

// recordedRun is the run id a previous loop wrote down, or "".
func (d *OrcaDispatcher) recordedRun() string {
	data, err := os.ReadFile(filepath.Join(d.runtimeDir, orcaRunFile))
	if err != nil {
		return ""
	}
	var rec struct {
		RunID string `json:"run_id"`
	}
	if json.Unmarshal(data, &rec) != nil {
		return ""
	}
	return strings.TrimSpace(rec.RunID)
}

// parseOrcaRunID finds the run id in a run-create --json reply without
// pinning the receipt's nesting.
func parseOrcaRunID(out []byte) (string, error) {
	var reply any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &reply); err != nil {
		return "", err
	}
	if obj, ok := reply.(map[string]any); ok {
		if okVal, present := obj["ok"].(bool); present && !okVal {
			return "", fmt.Errorf("orca reported failure: %s", strings.TrimSpace(string(out)))
		}
	}
	if id := findJSONString(reply, "runId"); id != "" {
		return id, nil
	}
	if id := findJSONString(reply, "id"); strings.HasPrefix(id, "run_") {
		return id, nil
	}
	return "", fmt.Errorf("no run id in reply")
}

// RecoverOrphans stops Orca workers a previous loop process launched and never
// saw finish, and returns their dispatch ids. The restarted loop has no session
// for them, so it cannot collect their results: stopping them leaves each stage
// pending, and it relaunches in its kept worktree instead of two workers sharing
// one. Only workers this factory launched (those with a session record) are
// touched, so a configured run shared with other work stays safe. Each session
// record names the run its worker launched in, so every run is scanned, even
// after run_id changed or orca-run.json was lost; one run failing does not skip
// the others.
func (d *OrcaDispatcher) RecoverOrphans(ctx context.Context) ([]string, error) {
	var stopped []string
	var errs []error
	byRun := d.launchedDispatchIDs()
	runIDs := make([]string, 0, len(byRun))
	for runID := range byRun {
		runIDs = append(runIDs, runID)
	}
	sort.Strings(runIDs)
	for _, runID := range runIDs {
		got, err := d.recoverRun(ctx, runID, byRun[runID])
		stopped = append(stopped, got...)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return stopped, errors.Join(errs...)
}

// recoverRun stops the unsettled workers of ours that run lists.
func (d *OrcaDispatcher) recoverRun(ctx context.Context, runID string, ours map[string]bool) ([]string, error) {
	callCtx, cancel := context.WithTimeout(ctx, orcaStartTimeout)
	defer cancel()
	var stopped []string
	var firstErr error
	cursor := ""
	for page := 0; page < orcaRecoverMaxPages; page++ {
		args := []string{"orchestration", "worker-list", "--run", runID, "--json", "--limit", "100"}
		if cursor != "" {
			args = append(args, "--cursor", cursor)
		}
		out, err := d.run(callCtx, d.bin, args...)
		if err != nil {
			return stopped, fmt.Errorf("list orca workers of run %s for recovery: %w", runID, err)
		}
		var reply struct {
			Result struct {
				Workers    []orcaWorker `json:"workers"`
				NextCursor string       `json:"nextCursor"`
			} `json:"result"`
		}
		if err := json.Unmarshal(out, &reply); err != nil {
			return stopped, fmt.Errorf("worker-list reply of run %s unreadable: %w", runID, err)
		}
		for _, w := range reply.Result.Workers {
			if !ours[w.DispatchID] || w.settled() {
				continue
			}
			if err := stopOrcaWorker(d.run, d.bin, w.DispatchID); err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("stop orphaned orca worker %s: %w", w.DispatchID, err)
				}
				continue
			}
			stopped = append(stopped, w.DispatchID)
		}
		if reply.Result.NextCursor == "" {
			break
		}
		cursor = reply.Result.NextCursor
	}
	return stopped, firstErr
}

// launchedDispatchIDs reads, per Orca run, the dispatch id of every session
// this factory started there.
func (d *OrcaDispatcher) launchedDispatchIDs() map[string]map[string]bool {
	byRun := map[string]map[string]bool{}
	paths, _ := filepath.Glob(filepath.Join(d.runtimeDir, "sessions", "*", "orca.json"))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var rec struct {
			DispatchID string `json:"dispatch_id"`
			RunID      string `json:"run_id"`
		}
		if json.Unmarshal(data, &rec) != nil || rec.DispatchID == "" {
			continue
		}
		if rec.RunID == "" {
			// A record without a run id belongs to the run this factory records,
			// else the configured one.
			if rec.RunID = nonEmptyOr(d.recordedRun(), d.runID); rec.RunID == "" {
				continue
			}
		}
		if byRun[rec.RunID] == nil {
			byRun[rec.RunID] = map[string]bool{}
		}
		byRun[rec.RunID][rec.DispatchID] = true
	}
	return byRun
}

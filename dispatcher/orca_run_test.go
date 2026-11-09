package dispatcher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Recovery follows worker-list pages, so an orphan past the first 100 rows of
// a long-lived run is still stopped.
func TestRecoverOrphansReadsEveryPage(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"d5", "d120"} {
		sd := filepath.Join(dir, "sessions", "s-"+id)
		if err := os.MkdirAll(sd, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sd, "orca.json"), []byte(`{"dispatch_id":"`+id+`","run_id":"run_x"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var stops []string
	d := NewOrcaDispatcher(OrcaDispatcherConfig{ProjectDir: dir, RuntimeDir: dir, RunID: "run_x"})
	d.run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "worker-stop"):
			stops = append(stops, args[len(args)-2])
			return []byte(`{"ok":true,"result":{}}`), nil
		case strings.Contains(joined, "--cursor c2"):
			return []byte(`{"result":{"workers":[{"dispatchId":"d120","state":"ready","outcome":"in_progress"}]}}`), nil
		default:
			return []byte(`{"result":{"workers":[{"dispatchId":"d5","state":"ready","outcome":"in_progress"},{"dispatchId":"other","state":"ready","outcome":"in_progress"}],"nextCursor":"c2"}}`), nil
		}
	}
	got, err := d.RecoverOrphans(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "d5,d120" || strings.Join(stops, ",") != "d5,d120" {
		t.Fatalf("stopped = %v (stop calls %v), want d5 and d120 only", got, stops)
	}
}

// Recovery scans every run a session recorded, not just the current one, and a
// run that fails to list does not hide the others.
func TestRecoverOrphansScansEveryRecordedRun(t *testing.T) {
	dir := t.TempDir()
	for id, run := range map[string]string{"d1": "run_old", "d2": "run_new", "d3": "run_gone"} {
		sd := filepath.Join(dir, "sessions", "s-"+id)
		if err := os.MkdirAll(sd, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sd, "orca.json"), []byte(`{"dispatch_id":"`+id+`","run_id":"`+run+`"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var stops []string
	d := NewOrcaDispatcher(OrcaDispatcherConfig{ProjectDir: dir, RuntimeDir: dir, RunID: "run_new"})
	d.run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "worker-stop"):
			stops = append(stops, args[len(args)-2])
			return []byte(`{"ok":true,"result":{}}`), nil
		case strings.Contains(joined, "--run run_old"):
			return []byte(`{"result":{"workers":[{"dispatchId":"d1","state":"ready","outcome":"in_progress"}]}}`), nil
		case strings.Contains(joined, "--run run_new"):
			return []byte(`{"result":{"workers":[{"dispatchId":"d2","state":"ready","outcome":"in_progress"}]}}`), nil
		}
		return nil, errors.New("run not found")
	}
	got, err := d.RecoverOrphans(context.Background())
	if err == nil || !strings.Contains(err.Error(), "run_gone") {
		t.Fatalf("err = %v, want the unreadable run named", err)
	}
	if strings.Join(got, ",") != "d2,d1" {
		t.Fatalf("stopped = %v, want d1 and d2 from both readable runs", got)
	}
}

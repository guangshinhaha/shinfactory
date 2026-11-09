package dispatcher

import (
	"context"
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
		if err := os.WriteFile(filepath.Join(sd, "orca.json"), []byte(`{"dispatch_id":"`+id+`"}`), 0o644); err != nil {
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

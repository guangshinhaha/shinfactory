package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guangshinhaha/shinfactory/internal/reducer"
	"github.com/guangshinhaha/shinfactory/internal/state"
)

func orcaOpenServer(t *testing.T, run orcaRunner) *Server {
	t.Helper()
	dir := t.TempDir()
	st := state.State{Mode: state.RunModeAuto, Orders: map[string]state.OrderNode{
		"22": {OrderID: "22", Sequence: 1, Status: state.OrderActive, Stages: []state.StageNode{
			{TaskKey: "execute", Status: state.StageRunning, Attempts: []state.AttemptNode{{SessionID: "sess-x"}}},
		}},
	}}
	if err := reducer.WriteSnapshotAtomic(filepath.Join(dir, "state.snapshot.json"), reducer.BuildSnapshot(st, nil, time.Now())); err != nil {
		t.Fatal(err)
	}
	sd := filepath.Join(dir, "sessions", "sess-x")
	if err := os.MkdirAll(sd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sd, "orca.json"), []byte(`{"dispatch_id":"ctx_8f03"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(Options{RuntimeDir: dir})
	s.runOrca = run
	return s
}

func postOpen(s *Server, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/orca/open", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	s.handleOrcaOpen(rec, req)
	return rec
}

func TestOrcaOpenSwitchesToWorkerTerminal(t *testing.T) {
	var calls []string
	s := orcaOpenServer(t, func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		if args[0] == "orchestration" {
			return []byte(`{"ok":true,"result":{"terminal":"term_1"}}`), nil
		}
		return []byte(`{"ok":true}`), nil
	})
	rec := postOpen(s, `{"dispatch_id":"ctx_8f03"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	want := []string{
		"orchestration worker-show --dispatch ctx_8f03 --json",
		"terminal switch --terminal term_1 --json",
	}
	if strings.Join(calls, "|") != strings.Join(want, "|") {
		t.Fatalf("orca calls = %q, want %q", calls, want)
	}
}

func TestOrcaOpenRejectsUnknownDispatch(t *testing.T) {
	s := orcaOpenServer(t, func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("orca ran for a dispatch the factory never recorded")
		return nil, nil
	})
	if rec := postOpen(s, `{"dispatch_id":"ctx_other"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if rec := postOpen(s, `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestOrcaOpenReportsWorkerWithoutTerminal(t *testing.T) {
	s := orcaOpenServer(t, func(context.Context, string, ...string) ([]byte, error) {
		return []byte(`{"ok":true,"result":{"terminal":""}}`), nil
	})
	if rec := postOpen(s, `{"dispatch_id":"ctx_8f03"}`); rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestOrcaOpenRequiresJSONContentType(t *testing.T) {
	s := orcaOpenServer(t, func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("orca ran for a request that was not JSON")
		return nil, nil
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/orca/open", strings.NewReader(`{"dispatch_id":"ctx_8f03"}`))
	req.Header.Set("Content-Type", "text/plain")
	s.handleOrcaOpen(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", rec.Code)
	}
}

func TestOrcaOpenHidesCLIErrorDetail(t *testing.T) {
	s := orcaOpenServer(t, func(context.Context, string, ...string) ([]byte, error) {
		return nil, fmt.Errorf("orca: /Users/shin/secret/path exploded")
	})
	rec := postOpen(s, `{"dispatch_id":"ctx_8f03"}`)
	if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), "/Users/shin") {
		t.Fatalf("status = %d body = %q", rec.Code, rec.Body)
	}
}

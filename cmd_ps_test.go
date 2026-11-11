package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guangshinhaha/shinfactory/internal/loopreg"
)

func TestRunPsListsLoopsAndMarksStale(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	r := loopreg.NewRegistrar(dir, "/work/alpha", func() time.Time { return now.Add(-90 * time.Second) })
	if err := r.Publish(loopreg.Entry{ProjectDir: "/work/alpha", LoopState: "running", Mode: "auto", SlotsBusy: 1, SlotsMax: 2, NeedsYou: 3, Version: "1.0.0"}); err != nil {
		t.Fatal(err)
	}
	// pid 0 is never alive.
	stale := `{"repo":"beta","project_dir":"/work/beta","pid":0,"port":3001,"loop_state":"running"}`
	if err := os.WriteFile(filepath.Join(dir, "beta.json"), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}

	var table bytes.Buffer
	if err := runPs(&table, dir, false, now); err != nil {
		t.Fatal(err)
	}
	out := table.String()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("table has %d lines, want header + 2:\n%s", len(lines), out)
	}
	if !strings.Contains(lines[1], "alpha") || !strings.Contains(lines[1], "1/2") || !strings.Contains(lines[1], "1m30s ago") || strings.Contains(lines[1], "stale") {
		t.Fatalf("live row = %q", lines[1])
	}
	if !strings.Contains(lines[2], "beta") || !strings.Contains(lines[2], "stale") {
		t.Fatalf("stale row = %q", lines[2])
	}

	var js bytes.Buffer
	if err := runPs(&js, dir, true, now); err != nil {
		t.Fatal(err)
	}
	var got []loopreg.Entry
	if err := json.Unmarshal(js.Bytes(), &got); err != nil {
		t.Fatalf("--json output is not JSON: %v\n%s", err, js.String())
	}
	if len(got) != 2 || got[0].Stale || !got[1].Stale || got[0].NeedsYou != 3 {
		t.Fatalf("json = %+v", got)
	}
}

func TestRunPsEmptyRegistry(t *testing.T) {
	var out bytes.Buffer
	if err := runPs(&out, filepath.Join(t.TempDir(), "none"), false, time.Now()); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "no loops registered" {
		t.Fatalf("out = %q", out.String())
	}
	out.Reset()
	if err := runPs(&out, filepath.Join(t.TempDir(), "none"), true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "[]" {
		t.Fatalf("json out = %q, want []", out.String())
	}
}

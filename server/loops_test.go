package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/guangshinhaha/shinfactory/internal/loopreg"
)

func getLoops(t *testing.T, s *Server) []loopreg.Entry {
	t.Helper()
	w := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/loops", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var got []loopreg.Entry
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, w.Body.String())
	}
	return got
}

func TestGetLoopsReturnsTheRegistry(t *testing.T) {
	loopsDir := t.TempDir()
	now := func() time.Time { return time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC) }
	for _, p := range []string{"/work/b-repo", "/work/a-repo"} {
		if err := loopreg.NewRegistrar(loopsDir, p, now).Publish(loopreg.Entry{ProjectDir: p, LoopState: "running", SlotsMax: 2}); err != nil {
			t.Fatal(err)
		}
	}
	s := New(Options{RuntimeDir: t.TempDir(), LoopsDir: loopsDir})

	got := getLoops(t, s)
	want, _ := loopreg.List(loopsDir)
	if len(got) != 2 || got[0].Repo != "a-repo" || got[1].Repo != "b-repo" {
		t.Fatalf("loops = %+v", got)
	}
	if got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("API differs from ps list:\n got %+v\nwant %+v", got, want)
	}
}

func TestGetLoopsEmptyWithoutRegistry(t *testing.T) {
	s := New(Options{RuntimeDir: t.TempDir()})
	w := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/loops", nil))
	if w.Body.String() != "[]\n" {
		t.Fatalf("body = %q, want []", w.Body.String())
	}
}

package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/guangshinhaha/shinfactory/loop"
)

func TestConfigReportsRepoPortAndBuild(t *testing.T) {
	for _, repo := range []string{"gantry", "shinfactory"} {
		s := New(Options{
			RuntimeDir:        t.TempDir(),
			Addr:              "127.0.0.1:0",
			LoopStateProvider: &staticProvider{state: loop.LoopState{Status: "running"}},
			Repo:              repo,
			Build:             "abc1234",
		})
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3456/api/config", nil)
		rec := httptest.NewRecorder()
		s.handleConfig(rec, req)
		var got map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode config: %v", err)
		}
		if got["repo"] != repo || got["port"] != "3456" || got["build_sha"] != "abc1234" {
			t.Fatalf("config identity = %v, want repo %q port 3456 build abc1234", got, repo)
		}
	}
}

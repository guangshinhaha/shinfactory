package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/guangshinhaha/shinfactory/internal/linestatus"
)

// orcaRunner runs the orca CLI. Injected for testing.
type orcaRunner func(ctx context.Context, bin string, args ...string) ([]byte, error)

func runOrcaCLI(ctx context.Context, bin string, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, bin, args...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("orca %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

const orcaOpenTimeout = 15 * time.Second

// handleOrcaOpen brings an orca-runtime stage's worker to the front in Orca.
// Only a dispatch id the factory itself recorded is accepted, so the endpoint
// cannot be pointed at an arbitrary worker.
func (s *Server) handleOrcaOpen(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
	var req struct {
		DispatchID string `json:"dispatch_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.DispatchID) == "" {
		http.Error(w, "dispatch_id missing", http.StatusBadRequest)
		return
	}
	known, err := s.knownDispatch(req.DispatchID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !known {
		http.Error(w, "dispatch not found: "+req.DispatchID, http.StatusNotFound)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), orcaOpenTimeout)
	defer cancel()
	bin := strings.TrimSpace(s.config.Runtime.Orca.Bin)
	if bin == "" {
		bin = "orca"
	}
	out, err := s.runOrca(ctx, bin, "orchestration", "worker-show", "--dispatch", req.DispatchID, "--json")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	var shown struct {
		Result struct {
			Terminal string `json:"terminal"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &shown); err != nil {
		http.Error(w, "orca worker-show returned unreadable output", http.StatusBadGateway)
		return
	}
	if shown.Result.Terminal == "" {
		http.Error(w, "worker has no terminal to open (released or never started)", http.StatusConflict)
		return
	}
	if _, err := s.runOrca(ctx, bin, "terminal", "switch", "--terminal", shown.Result.Terminal, "--json"); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "dispatch_id": req.DispatchID})
}

func (s *Server) knownDispatch(id string) (bool, error) {
	line, err := linestatus.Read(s.runtimeDir)
	if err != nil {
		return false, fmt.Errorf("read line state: %w", err)
	}
	for _, group := range [][]linestatus.Order{line.Running, line.Queued, line.Parked} {
		for _, o := range group {
			for _, st := range o.Stages {
				if st.DispatchID == id {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

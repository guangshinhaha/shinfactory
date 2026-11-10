package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"mime"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/guangshinhaha/shinfactory/internal/linestatus"
)

// orcaRunner runs the orca CLI. Injected for testing.
type orcaRunner func(ctx context.Context, bin string, args ...string) ([]byte, error)

func runOrcaCLI(ctx context.Context, bin string, args ...string) ([]byte, error) {
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("orca %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

const orcaOpenTimeout = 15 * time.Second

// handleOrcaOpen brings an orca-runtime stage's worker to the front in Orca.
// Only a dispatch id the factory itself recorded is accepted, so the endpoint
// cannot be pointed at an arbitrary worker.
func (s *Server) handleOrcaOpen(w http.ResponseWriter, r *http.Request) {
	// Only a JSON content type: a cross-site form post cannot send one without a
	// CORS preflight, which this server never answers.
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
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
		orcaFailed(w, err)
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
		orcaFailed(w, err)
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

// orcaFailed logs the CLI's own error and answers with a fixed message, so
// local paths and worker details in the output never reach the browser.
func orcaFailed(w http.ResponseWriter, err error) {
	log.Printf("open in orca: %v", err)
	http.Error(w, "orca could not open the worker; see the shinfactory log", http.StatusBadGateway)
}

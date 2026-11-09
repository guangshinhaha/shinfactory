// Package linestatus reads one repo's factory state from its runtime
// directory: what the loop is doing, what is queued, and what waits for a person.
package linestatus

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/guangshinhaha/shinfactory/internal/jsonx"
	"github.com/guangshinhaha/shinfactory/internal/reducer"
	"github.com/guangshinhaha/shinfactory/internal/state"
	"github.com/guangshinhaha/shinfactory/internal/statusfile"
)

// Report is the state of one line, as `shinfactory status --json` prints it.
type Report struct {
	Mode        string      `json:"mode"`
	Concurrency int         `json:"concurrency"`
	LoopState   string      `json:"loop_state"`
	Running     []Order     `json:"running"`
	Queued      []Order     `json:"queued"`
	Parked      []Order     `json:"parked"`
	NeedsShin   []NeedsShin `json:"needs_shin"`
}

// Order is one order and its stages.
type Order struct {
	ID     string  `json:"id"`
	Title  string  `json:"title,omitempty"`
	Tier   string  `json:"tier,omitempty"`
	Reason string  `json:"reason,omitempty"` // why a parked order waits
	Stages []Stage `json:"stages"`
}

// Stage is one stage of an order. SessionID and DispatchID are the latest
// attempt's; DispatchID is set only for stages the factory started on Orca.
type Stage struct {
	TaskKey    string `json:"task_key"`
	Status     string `json:"status"`
	Provider   string `json:"provider,omitempty"`
	Model      string `json:"model,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	DispatchID string `json:"dispatch_id,omitempty"`
}

// NeedsShin is an order the loop stopped because only a person can settle it.
type NeedsShin struct {
	Order  string    `json:"order"`
	Reason string    `json:"reason,omitempty"`
	Title  string    `json:"title,omitempty"`
	URL    string    `json:"url,omitempty"`
	At     time.Time `json:"at,omitempty"`
}

// MergeNeedsShin adds the backlog's stopped items to the report. An order the
// loop already recorded keeps its entry, which carries the reason; a stopped
// issue the loop never recorded (a scheduler refusal, a hand-applied label)
// is added after them.
func (r *Report) MergeNeedsShin(stopped []NeedsShin) {
	have := map[string]bool{}
	for _, it := range r.NeedsShin {
		have[it.Order] = true
	}
	for _, it := range stopped {
		if !have[it.Order] {
			have[it.Order] = true
			r.NeedsShin = append(r.NeedsShin, it)
		}
	}
}

const needsShinFile = "needs-shin.json"

// RecordNeedsShin notes that order was handed to a person. One entry per order;
// a later stop replaces the earlier one.
func RecordNeedsShin(runtimeDir string, item NeedsShin) error {
	items, err := readNeedsShin(runtimeDir)
	if err != nil {
		return err
	}
	kept := items[:0]
	for _, it := range items {
		if it.Order != item.Order {
			kept = append(kept, it)
		}
	}
	return jsonx.WriteJSON(filepath.Join(runtimeDir, needsShinFile), append(kept, item))
}

func readNeedsShin(runtimeDir string) ([]NeedsShin, error) {
	return jsonx.ReadJSON[[]NeedsShin](filepath.Join(runtimeDir, needsShinFile))
}

// Read builds the report from runtimeDir. A directory the loop never wrote to
// reads as an idle line, not an error.
func Read(runtimeDir string) (Report, error) {
	status, err := statusfile.Read(filepath.Join(runtimeDir, "status.json"))
	if err != nil {
		return Report{}, err
	}
	canonical, err := readCanonical(filepath.Join(runtimeDir, "state.snapshot.json"))
	if err != nil {
		return Report{}, err
	}
	stopped, err := readNeedsShin(runtimeDir)
	if err != nil {
		return Report{}, fmt.Errorf("read %s: %w", needsShinFile, err)
	}

	rep := Report{
		Mode:        status.Mode,
		Concurrency: status.MaxConcurrency,
		LoopState:   strings.TrimSpace(status.LoopState),
		Running:     []Order{},
		Queued:      []Order{},
		Parked:      []Order{},
		NeedsShin:   []NeedsShin{},
	}
	if rep.Mode == "" {
		rep.Mode = string(canonical.Mode)
	}
	if rep.LoopState == "" {
		rep.LoopState = "running"
	}

	nodes := make([]state.OrderNode, 0, len(canonical.Orders))
	for _, node := range canonical.Orders {
		if !node.Status.IsTerminal() {
			nodes = append(nodes, node)
		}
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Sequence < nodes[j].Sequence })

	live := map[string]bool{}
	for _, node := range nodes {
		live[node.OrderID] = true
		order := toOrder(runtimeDir, node)
		switch {
		case hasParked(canonical, node):
			order.Reason = canonical.PendingReviews[node.OrderID].Reason
			rep.Parked = append(rep.Parked, order)
		case isRunning(node):
			rep.Running = append(rep.Running, order)
		default:
			rep.Queued = append(rep.Queued, order)
		}
	}
	// A person who put the order back has retried it; the old stop no longer applies.
	for _, it := range stopped {
		if !live[it.Order] {
			rep.NeedsShin = append(rep.NeedsShin, it)
		}
	}
	return rep, nil
}

// readCanonical reads the loop's durable snapshot, which wraps the state with
// its effect ledger. A missing file is a loop that never ran.
func readCanonical(path string) (state.State, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) || (err == nil && len(bytes.TrimSpace(data)) == 0) {
		return state.State{}, nil
	}
	if err != nil {
		return state.State{}, fmt.Errorf("read state snapshot: %w", err)
	}
	snap, err := reducer.ReadSnapshot(path)
	if err != nil {
		return state.State{}, err
	}
	return snap.State, nil
}

func hasParked(s state.State, node state.OrderNode) bool {
	_, ok := s.PendingReviews[node.OrderID]
	return ok
}

func isRunning(node state.OrderNode) bool {
	for _, st := range node.Stages {
		if st.Status.IsBusy() {
			return true
		}
	}
	return false
}

func toOrder(runtimeDir string, node state.OrderNode) Order {
	order := Order{ID: node.OrderID, Title: node.Title, Tier: node.Tier, Stages: make([]Stage, 0, len(node.Stages))}
	for _, st := range node.Stages {
		out := Stage{TaskKey: st.TaskKey, Status: string(st.Status), Provider: st.Provider, Model: st.Model}
		if n := len(st.Attempts); n > 0 {
			out.SessionID = st.Attempts[n-1].SessionID
			out.DispatchID = dispatchID(runtimeDir, out.SessionID)
			// Routing resolves at dispatch, so the snapshot rarely names the model;
			// the session's spawn record holds what actually ran.
			if provider, model := spawnRouting(runtimeDir, out.SessionID); model != "" {
				out.Provider, out.Model = provider, model
			}
		}
		order.Stages = append(order.Stages, out)
	}
	return order
}

// dispatchID reads the Orca dispatch id the dispatcher recorded for a session.
// A session that did not run on Orca has none.
func dispatchID(runtimeDir, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	rec, err := jsonx.ReadJSON[struct {
		DispatchID string `json:"dispatch_id"`
	}](filepath.Join(runtimeDir, "sessions", sessionID, "orca.json"))
	if err != nil {
		return ""
	}
	return rec.DispatchID
}

// spawnRouting reads the provider and model a session was dispatched with.
// A session with no spawn record has none.
func spawnRouting(runtimeDir, sessionID string) (provider, model string) {
	rec, err := jsonx.ReadJSON[struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}](filepath.Join(runtimeDir, "sessions", sessionID, "spawn.json"))
	if err != nil {
		return "", ""
	}
	return rec.Provider, rec.Model
}

package dispatcher

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/guangshinhaha/shinfactory/event"
	"github.com/guangshinhaha/shinfactory/parse"
)

const (
	// orcaPollFailureLimit is how many polls in a row may fail before the
	// session gives up on reaching Orca.
	orcaPollFailureLimit = 12
	// orcaTranscriptTail is how many trailing transcript messages are scanned
	// for a usage-limit message.
	orcaTranscriptTail = 2
)

type orcaSessionConfig struct {
	id            string
	dispatchID    string
	runID         string
	bin           string
	run           orcaRunner
	pollInterval  time.Duration
	stampedPath   string
	canonicalPath string
	eventWriter   *event.EventWriter
	prompt        string
	sink          SessionEventSink
}

// orcaSession follows one Orca worker by polling `orca orchestration`. The
// stage's own result still arrives through the session's event log
// (stage_yield / stage_fail); this session only reports when the worker ended
// and how.
type orcaSession struct {
	sessionBase
	dispatchID   string
	runID        string
	bin          string
	run          orcaRunner
	pollInterval time.Duration
}

func newOrcaSession(cfg orcaSessionConfig) *orcaSession {
	return &orcaSession{
		sessionBase: newSessionBase(sessionBaseConfig{
			id:            cfg.id,
			eventWriter:   cfg.eventWriter,
			stampedPath:   cfg.stampedPath,
			canonicalPath: cfg.canonicalPath,
			prompt:        cfg.prompt,
			sink:          cfg.sink,
		}),
		dispatchID:   cfg.dispatchID,
		runID:        cfg.runID,
		bin:          cfg.bin,
		run:          cfg.run,
		pollInterval: cfg.pollInterval,
	}
}

func (s *orcaSession) start(ctx context.Context) {
	s.writeInitialHeartbeat()
	s.publish(SessionEvent{Type: "init", Message: "orca worker " + s.dispatchID, Timestamp: nowUTC()})
	s.appendCanonical(parse.EventInit, "orca worker "+s.dispatchID)
	go s.closeEventsWhenDone()
	go s.poll(ctx)
}

func (s *orcaSession) poll(ctx context.Context) {
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()
	failures := 0
	for {
		select {
		case <-ctx.Done():
			_ = stopOrcaWorker(s.run, s.bin, s.dispatchID)
			s.finish(SessionOutcome{Status: StatusCancelled, Reason: "context cancelled before the orca worker finished"})
			return
		case <-s.done:
			return
		case <-ticker.C:
		}
		s.writeHeartbeat(nowUTC())
		w, err := s.fetchWorker(ctx)
		if err != nil {
			failures++
			if failures >= orcaPollFailureLimit {
				// Fence the worker so a retry does not put two in one worktree.
				_ = stopOrcaWorker(s.run, s.bin, s.dispatchID)
				s.finish(SessionOutcome{Status: StatusFailed, Reason: "lost contact with orca: " + err.Error()})
				return
			}
			continue
		}
		failures = 0
		if !w.settled() {
			continue
		}
		s.finish(s.outcomeFor(ctx, w))
		return
	}
}

// orcaWorker is the slice of a worker-list row the session reads.
type orcaWorker struct {
	DispatchID string `json:"dispatchId"`
	State      string `json:"state"`
	Outcome    string `json:"outcome"`
}

// succeeded reports whether Orca recorded the worker as finished successfully.
// Real Orca reports a successful worker as state "succeeded" with no outcome;
// a failed one is state "stopped" with outcome "failed".
func (w orcaWorker) succeeded() bool {
	if strings.ToLower(strings.TrimSpace(w.State)) == "succeeded" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(w.Outcome)) {
	case "succeeded", "success", "completed":
		return true
	}
	return false
}

// settled reports whether the worker has finished, failed or been stopped.
func (w orcaWorker) settled() bool {
	if w.succeeded() {
		return true
	}
	outcome := strings.ToLower(strings.TrimSpace(w.Outcome))
	if outcome != "" && outcome != "in_progress" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(w.State)) {
	case "stopped", "failed", "cancelled", "canceled":
		return true
	}
	return false
}

func (s *orcaSession) fetchWorker(ctx context.Context) (orcaWorker, error) {
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := s.run(callCtx, s.bin, "orchestration", "worker-list", "--run", s.runID, "--json", "--limit", "100")
	if err != nil {
		return orcaWorker{}, err
	}
	var reply struct {
		Result struct {
			Workers []orcaWorker `json:"workers"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &reply); err != nil {
		return orcaWorker{}, fmt.Errorf("worker-list reply unreadable: %w", err)
	}
	for _, w := range reply.Result.Workers {
		if w.DispatchID == s.dispatchID {
			return w, nil
		}
	}
	// Past the first 100 rows: ask for this worker directly. worker-show has
	// the state but no outcome, which is enough to tell running from ended.
	showOut, err := s.run(callCtx, s.bin, "orchestration", "worker-show", "--dispatch", s.dispatchID, "--json")
	if err != nil {
		return orcaWorker{}, fmt.Errorf("worker %s not in worker-list: %w", s.dispatchID, err)
	}
	var show struct {
		Result orcaWorker `json:"result"`
	}
	if err := json.Unmarshal(showOut, &show); err != nil || show.Result.DispatchID != s.dispatchID {
		return orcaWorker{}, fmt.Errorf("worker %s not in worker-list", s.dispatchID)
	}
	return show.Result, nil
}

func (s *orcaSession) outcomeFor(ctx context.Context, w orcaWorker) SessionOutcome {
	if w.succeeded() {
		return SessionOutcome{Status: StatusCompleted, Reason: "orca worker finished", HasDeliverable: true}
	}
	outcome := SessionOutcome{Status: StatusFailed, Reason: "orca worker ended with outcome " + nonEmptyOr(nonEmptyOr(w.Outcome, w.State), "unknown")}
	if tail := s.transcriptTail(ctx); IsUsageLimitText(tail) {
		outcome.UsageLimit = true
		outcome.Reason = "usage limit reached: " + limitLine(tail)
	}
	return outcome
}

// transcriptTail returns the last few non-user transcript messages. The user's
// own first message (the spec) can mention limits, so it is not scanned.
func (s *orcaSession) transcriptTail(ctx context.Context) string {
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := s.run(callCtx, s.bin, "orchestration", "worker-read", "--dispatch", s.dispatchID, "--json", "--limit", "200")
	if err != nil {
		return ""
	}
	var reply struct {
		Result struct {
			Transcript struct {
				Messages []struct {
					Role   string `json:"role"`
					Blocks []struct {
						Text string `json:"text"`
					} `json:"blocks"`
				} `json:"messages"`
			} `json:"transcript"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &reply); err != nil {
		return ""
	}
	var texts []string
	for _, m := range reply.Result.Transcript.Messages {
		if m.Role == "user" {
			continue
		}
		for _, b := range m.Blocks {
			if t := strings.TrimSpace(b.Text); t != "" {
				texts = append(texts, t)
			}
		}
	}
	if len(texts) > orcaTranscriptTail {
		texts = texts[len(texts)-orcaTranscriptTail:]
	}
	return strings.Join(texts, "\n")
}

func (s *orcaSession) finish(outcome SessionOutcome) {
	s.doneOnce.Do(func() {
		s.mu.Lock()
		s.status = outcome.Status.String()
		s.outcome = outcome
		s.mu.Unlock()
		s.closeStreamDone()
		s.recordTerminal(outcome)
		close(s.done)
	})
}

// recordTerminal writes the worker's end into the canonical log, which the
// monitor reads for meta.json: its timestamps give the duration and its type
// the final status. Orca reports no cost, so none is recorded.
func (s *orcaSession) recordTerminal(outcome SessionOutcome) {
	if outcome.Status == StatusCompleted {
		s.appendCanonical(parse.EventComplete, outcome.Reason)
		return
	}
	s.appendCanonical(parse.EventFailed, outcome.Reason)
}

func (s *orcaSession) appendCanonical(kind parse.EventType, message string) {
	if strings.TrimSpace(s.canonicalPath) == "" {
		return
	}
	line, err := json.Marshal(parse.CanonicalEvent{Type: kind, Message: message, Timestamp: nowUTC()})
	if err != nil {
		return
	}
	f, err := os.OpenFile(s.canonicalPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		s.publish(SessionEvent{Type: "warning", Message: "write canonical event: " + err.Error(), Timestamp: nowUTC()})
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

func (s *orcaSession) Terminate() error {
	err := stopOrcaWorker(s.run, s.bin, s.dispatchID)
	s.finish(SessionOutcome{Status: StatusKilled, Reason: "orca worker stopped"})
	return err
}

func (s *orcaSession) ForceKill() error { return s.Terminate() }

func (s *orcaSession) Controller() AgentController { return NoopController() }

func nonEmptyOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

// limitLine returns the first line of text that reads like a usage limit.
func limitLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if IsUsageLimitText(line) {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

var _ Session = (*orcaSession)(nil)

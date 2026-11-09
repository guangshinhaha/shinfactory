package dispatcher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/guangshinhaha/shinfactory/event"
	"github.com/guangshinhaha/shinfactory/internal/shellx"
	"github.com/guangshinhaha/shinfactory/internal/stringx"
	"github.com/guangshinhaha/shinfactory/skill"
)

const (
	orcaStartTimeout        = 2 * time.Minute
	defaultOrcaPollInterval = 5 * time.Second
)

// OrcaDispatcherConfig configures an orca dispatcher.
type OrcaDispatcherConfig struct {
	ProjectDir    string
	RuntimeDir    string
	Bin           string // orca CLI; empty = "orca" on PATH
	RunID         string // Orca run to launch workers in; empty = the factory creates and remembers its own
	SkillResolver skill.Resolver
	PollInterval  time.Duration // 0 = default
	Sink          SessionEventSink
}

// OrcaDispatcher launches each stage as an Orca worker
// (`orca orchestration worker-start`) in the stage's git worktree, so the stage
// shows up in Orca and runs on whichever account Orca has selected.
type OrcaDispatcher struct {
	projectDir    string
	runtimeDir    string
	bin           string
	skillResolver skill.Resolver
	pollInterval  time.Duration
	sink          SessionEventSink

	// run executes the orca CLI. Injected for testing.
	run orcaRunner

	runMu sync.Mutex
	runID string // the Orca run every worker is launched in; empty until ensureRun
}

// orcaRunner runs the orca CLI and returns stdout. On failure the error
// carries stderr so callers can classify it.
type orcaRunner func(ctx context.Context, bin string, args ...string) ([]byte, error)

// NewOrcaDispatcher constructs an orca dispatcher.
func NewOrcaDispatcher(config OrcaDispatcherConfig) *OrcaDispatcher {
	bin := strings.TrimSpace(config.Bin)
	if bin == "" {
		bin = "orca"
	}
	poll := config.PollInterval
	if poll <= 0 {
		poll = defaultOrcaPollInterval
	}
	return &OrcaDispatcher{
		projectDir:    strings.TrimSpace(config.ProjectDir),
		runtimeDir:    strings.TrimSpace(config.RuntimeDir),
		bin:           bin,
		skillResolver: config.SkillResolver,
		pollInterval:  poll,
		sink:          config.Sink,
		run:           runOrcaCLI,
		runID:         strings.TrimSpace(config.RunID),
	}
}

func runOrcaCLI(ctx context.Context, bin string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		return stdout.Bytes(), fmt.Errorf("%w: %s", err, detail)
	}
	return stdout.Bytes(), nil
}

func (d *OrcaDispatcher) Dispatch(ctx context.Context, req DispatchRequest) (Session, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	if reqRuntime := NormalizeRuntime(req.Runtime); reqRuntime != "orca" {
		return nil, fmt.Errorf("runtime %q not supported by orca dispatcher", reqRuntime)
	}
	req.Runtime = "orca"
	agent, err := orcaAgentFor(req.Provider)
	if err != nil {
		return nil, err
	}
	worktree, err := resolveDispatchWorktree(d.projectDir, req)
	if err != nil {
		return nil, err
	}
	req.WorktreePath = worktree
	if d.runtimeDir == "" {
		return nil, fmt.Errorf("runtime directory not set")
	}

	sessionID, err := generateSessionID(req.Name)
	if err != nil {
		return nil, fmt.Errorf("generate session ID: %w", err)
	}
	sessionDir, promptPath, stampedPath, canonicalPath, _ := sessionPaths(d.runtimeDir, sessionID)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		return nil, fmt.Errorf("create session directory: %w", err)
	}
	eventWriter, err := event.NewEventWriter(d.runtimeDir, sessionID)
	if err != nil {
		return nil, fmt.Errorf("create event writer: %w", err)
	}
	skillBundle, err := resolveSkillBundle(d.skillResolver, req)
	if err != nil {
		return nil, err
	}

	spec := d.buildSpec(sessionID, req, skillBundle.SystemPrompt)
	if err := os.WriteFile(promptPath, []byte(req.Prompt), 0o644); err != nil {
		return nil, fmt.Errorf("write prompt file: %w", err)
	}
	specPath := filepath.Join(sessionDir, "orca-spec.md")
	if err := os.WriteFile(specPath, []byte(spec), 0o644); err != nil {
		return nil, fmt.Errorf("write orca spec: %w", err)
	}

	runID, err := d.ensureRun(ctx)
	if err != nil {
		return nil, err
	}
	dispatchID, err := d.startWorker(ctx, runID, sessionID, specPath, agent, req)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "orca.json"),
		[]byte(fmt.Sprintf(`{"dispatch_id":%q}`+"\n", dispatchID)), 0o644); err != nil {
		d.stopWorker(dispatchID)
		return nil, fmt.Errorf("write orca metadata: %w", err)
	}
	if err := writeDispatchMetadata(d.runtimeDir, sessionID, req, nowUTC()); err != nil {
		d.stopWorker(dispatchID)
		return nil, fmt.Errorf("write spawn metadata: %w", err)
	}

	session := newOrcaSession(orcaSessionConfig{
		id:            sessionID,
		dispatchID:    dispatchID,
		runID:         runID,
		bin:           d.bin,
		run:           d.run,
		pollInterval:  d.pollInterval,
		stampedPath:   stampedPath,
		canonicalPath: canonicalPath,
		eventWriter:   eventWriter,
		prompt:        req.Prompt,
		sink:          d.sink,
	})
	session.start(ctx)
	return session, nil
}

// orcaAgentFor maps a factory provider onto an Orca agent id.
func orcaAgentFor(provider string) (string, error) {
	switch p := stringx.Normalize(provider); p {
	case "claude", "codex":
		return p, nil
	default:
		return "", fmt.Errorf("provider %q has no orca agent (valid: claude, codex)", provider)
	}
}

// buildSpec composes the worker's task. An Orca worker has no factory
// environment, so the spec carries the session identity the skills read from
// $NOODLE_SESSION_ID and $NOODLE_PROJECT_DIR.
func (d *OrcaDispatcher) buildSpec(sessionID string, req DispatchRequest, skillSystemPrompt string) string {
	identity := fmt.Sprintf(`## Factory session

This task is one stage of a shinfactory order. The shell has no factory
environment, so wherever a skill or instruction mentions $NOODLE_SESSION_ID,
use %q, and run every `+"`shinfactory`"+` command with the project directory set:

`+"```sh"+`
NOODLE_PROJECT_DIR=%s shinfactory event emit --session %s <event> --payload '<json>'
`+"```"+`

The factory reads `+"`stage_yield`"+` and `+"`stage_fail`"+` from that session; the Orca
`+"`worker_done`"+` message alone does not tell it what happened.`, sessionID, shellx.Quote(d.projectDir), sessionID)
	return stringx.JoinNonEmpty("\n\n", buildSessionPreamble(), skillSystemPrompt, identity, req.Prompt)
}

func (d *OrcaDispatcher) startWorker(ctx context.Context, runID, sessionID, specPath, agent string, req DispatchRequest) (string, error) {
	args := []string{
		"orchestration", "worker-start",
		"--run", runID,
		"--spec-file", specPath,
		"--worktree", "path:" + req.WorktreePath,
		"--agent", agent,
		"--model", strings.TrimSpace(req.Model),
		"--comment", "shinfactory session " + sessionID,
		"--json",
	}
	if title := strings.TrimSpace(req.Title); title != "" {
		args = append(args, "--task-title", title)
	}
	if effort := strings.TrimSpace(req.ReasoningLevel); effort != "" {
		args = append(args, "--effort", effort)
	}
	startCtx, cancel := context.WithTimeout(ctx, orcaStartTimeout)
	defer cancel()
	out, err := d.run(startCtx, d.bin, args...)
	if err != nil {
		if IsUsageLimitText(err.Error()) || IsUsageLimitText(string(out)) {
			return "", UsageLimitError{Runtime: "orca", Detail: strings.TrimSpace(err.Error())}
		}
		return "", fmt.Errorf("orca worker-start failed: %w", err)
	}
	dispatchID, err := parseOrcaDispatchID(out)
	if err != nil {
		return "", fmt.Errorf("orca worker-start reply unreadable: %w", err)
	}
	return dispatchID, nil
}

// stopWorker fences a worker best-effort; used when a launch must be undone.
func (d *OrcaDispatcher) stopWorker(dispatchID string) {
	stopOrcaWorker(d.run, d.bin, dispatchID)
}

func stopOrcaWorker(run orcaRunner, bin, dispatchID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := run(ctx, bin, "orchestration", "worker-stop", "--dispatch", dispatchID, "--json")
	return err
}

// parseOrcaDispatchID finds the dispatch id in a worker-start --json reply
// without pinning the receipt's nesting.
func parseOrcaDispatchID(out []byte) (string, error) {
	var reply any
	if err := json.Unmarshal(bytes.TrimSpace(out), &reply); err != nil {
		return "", err
	}
	if obj, ok := reply.(map[string]any); ok {
		if okVal, present := obj["ok"].(bool); present && !okVal {
			return "", fmt.Errorf("orca reported failure: %s", strings.TrimSpace(string(out)))
		}
	}
	if id := findJSONString(reply, "dispatchId"); id != "" {
		return id, nil
	}
	return "", fmt.Errorf("no dispatchId in reply")
}

func findJSONString(node any, key string) string {
	switch v := node.(type) {
	case map[string]any:
		if s, ok := v[key].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
		for _, child := range v {
			if s := findJSONString(child, key); s != "" {
				return s
			}
		}
	case []any:
		for _, child := range v {
			if s := findJSONString(child, key); s != "" {
				return s
			}
		}
	}
	return ""
}

var _ Dispatcher = (*OrcaDispatcher)(nil)

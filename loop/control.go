package loop

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/guangshinhaha/shinfactory/internal/filex"
	"github.com/guangshinhaha/shinfactory/internal/stringx"
)

func (l *Loop) controlPaths() (controlPath string, ackPath string, lockPath string) {
	return filepath.Join(l.runtimeDir, "control.ndjson"),
		filepath.Join(l.runtimeDir, "control-ack.ndjson"),
		filepath.Join(l.runtimeDir, "control.lock")
}

func (l *Loop) lastAppliedSeqPath() string {
	return filepath.Join(l.runtimeDir, "last-applied-seq")
}

func (l *Loop) hydrateProcessedCommands() error {
	_, ackPath, _ := l.controlPaths()
	file, err := os.Open(ackPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("open control ack file: %w", err)
		}
	} else {
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var ack ControlAck
			if err := json.Unmarshal([]byte(line), &ack); err != nil {
				continue
			}
			if strings.TrimSpace(ack.ID) != "" {
				l.cmds.processedIDs[ack.ID] = struct{}{}
			}
		}
		if err := scanner.Err(); err != nil {
			file.Close()
			return fmt.Errorf("scan control ack file: %w", err)
		}
		file.Close()
	}

	// Load last-applied sequence number.
	seqData, err := os.ReadFile(l.lastAppliedSeqPath())
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("read last-applied-seq: %w", err)
		}
		return nil
	}
	seq, err := strconv.ParseUint(strings.TrimSpace(string(seqData)), 10, 64)
	if err != nil {
		// Corrupt file — start from zero.
		return nil
	}
	l.cmds.lastAppliedSeq = seq
	l.cmds.cmdSeqCounter = seq
	return nil
}

func (l *Loop) processControlCommands() error {
	controlPath, ackPath, lockPath := l.controlPaths()
	if err := os.MkdirAll(l.runtimeDir, 0o755); err != nil {
		return fmt.Errorf("create runtime directory: %w", err)
	}
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open control lock: %w", err)
	}
	defer lock.Close()

	if err := acquireFileLock(lock.Fd()); err != nil {
		return fmt.Errorf("lock control file: %w", err)
	}
	defer func() {
		_ = releaseFileLock(lock.Fd())
	}()

	data, err := os.ReadFile(controlPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read control file: %w", err)
	}
	if len(data) == 0 {
		// Truncating an empty queue is a write the runtime watcher reports as
		// a control command, which starts the next cycle with no wait (#188).
		return nil
	}

	acks := l.processControlLines(data)
	if len(acks) > 0 {
		// A caller reads status.json the moment it sees the ack, so the file
		// must already show what the commands changed.
		// Best effort: the commands are applied, so a failed stamp must not
		// keep the acks back. The cycle's own stamp retries.
		if err := l.stampStatus(); err != nil {
			l.logger.Warn("could not stamp status after control commands", "error", err)
		}
	}
	if l.TestControlAckBarrier != nil {
		l.TestControlAckBarrier()
	}
	if len(acks) > 0 {
		if err := appendAcks(ackPath, acks); err != nil {
			return err
		}
	}
	if err := os.WriteFile(controlPath, []byte{}, 0o644); err != nil {
		return fmt.Errorf("truncate control file: %w", err)
	}
	return nil
}

// processControlLines parses NDJSON command lines, deduplicates, and applies
// each command. Returns the accumulated acks.
func (l *Loop) processControlLines(data []byte) []ControlAck {
	lines := strings.Split(string(data), "\n")
	acks := make([]ControlAck, 0, len(lines))
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		ack := l.processControlLine(line)
		acks = append(acks, ack)
	}
	return acks
}

// processControlLine parses a single NDJSON line into a command, deduplicates,
// and applies it. Returns the ack for this command.
func (l *Loop) processControlLine(line string) ControlAck {
	var cmd ControlCommand
	if err := json.Unmarshal([]byte(line), &cmd); err != nil {
		failure := controlAckFailureForInvalidCommandJSON()
		return ControlAck{
			ID:      "",
			Action:  "unknown",
			Status:  "error",
			Message: "invalid command JSON",
			Failure: &failure,
			At:      l.deps.Now(),
		}
	}
	if cmd.ID == "" {
		cmd.ID = deterministicControlID(cmd)
	}

	// Assign a monotonic sequence number.
	l.cmds.cmdSeqCounter++
	seq := l.cmds.cmdSeqCounter

	// Skip commands already applied (ID-based dedup or sequence-based).
	if _, seen := l.cmds.processedIDs[cmd.ID]; seen {
		return ControlAck{ID: cmd.ID, Action: cmd.Action, Status: "ok", At: l.deps.Now()}
	}
	if seq <= l.cmds.lastAppliedSeq {
		l.cmds.processedIDs[cmd.ID] = struct{}{}
		return ControlAck{ID: cmd.ID, Action: cmd.Action, Status: "ok", At: l.deps.Now()}
	}

	ack := l.applyControlCommand(cmd)
	l.cmds.processedIDs[cmd.ID] = struct{}{}
	l.cmds.lastAppliedSeq = seq
	return ack
}

func deterministicControlID(cmd ControlCommand) string {
	normalized := cmd
	normalized.ID = ""
	if !normalized.At.IsZero() {
		normalized.At = normalized.At.UTC()
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		// Fallback should still be deterministic for identical payloads.
		at := ""
		if !cmd.At.IsZero() {
			at = cmd.At.UTC().Format(time.RFC3339Nano)
		}
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%s:%s:%s:%s",
			cmd.Action,
			cmd.OrderID,
			cmd.Name,
			cmd.Target,
			cmd.TaskKey,
			cmd.Prompt+":"+at,
		)))
		return "cmd-auto-" + hex.EncodeToString(sum[:8])
	}
	sum := sha256.Sum256(encoded)
	return "cmd-auto-" + hex.EncodeToString(sum[:8])
}

func appendAcks(path string, acks []ControlAck) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create ack parent directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open ack file: %w", err)
	}
	defer file.Close()

	for _, ack := range acks {
		line, err := json.Marshal(ack)
		if err != nil {
			return fmt.Errorf("encode ack: %w", err)
		}
		if _, err := file.Write(append(line, '\n')); err != nil {
			return fmt.Errorf("append ack: %w", err)
		}
	}
	return nil
}

func (l *Loop) applyControlCommand(cmd ControlCommand) ControlAck {
	ack := ControlAck{ID: cmd.ID, Action: cmd.Action, Status: "ok", At: l.deps.Now()}
	err := l.dispatchControlCommand(cmd)
	if err != nil {
		ack.Status = "error"
		ack.Message = err.Error()
		if ack.Failure == nil {
			failure := controlAckFailureForCommand(cmd)
			ack.Failure = &failure
		}
		l.logger.Warn("control command failed", "action", cmd.Action, "message", ack.Message)
	} else {
		l.logger.Info("control command", "action", cmd.Action, "status", ack.Status)
	}
	return ack
}

// controlHandlers is the loop's dispatch table: one entry per control action.
// ControlActions lists its keys so the CLI can check it covers every action.
var controlHandlers = map[string]func(l *Loop, cmd ControlCommand) error{
	"pause":  func(l *Loop, _ ControlCommand) error { return l.controlPause() },
	"resume": func(l *Loop, _ ControlCommand) error { return l.controlResume() },
	"drain":  func(l *Loop, _ ControlCommand) error { return l.controlDrain() },
	"skip":   func(l *Loop, cmd ControlCommand) error { return l.controlSkip(cmd.OrderID) },
	"kill":   func(l *Loop, cmd ControlCommand) error { return l.controlKill(cmd.Name) },
	"steer":  func(l *Loop, cmd ControlCommand) error { return l.controlSteer(cmd.Target, cmd.Prompt) },
	"merge":  func(l *Loop, cmd ControlCommand) error { return l.controlMerge(cmd.OrderID) },
	"reject": func(l *Loop, cmd ControlCommand) error { return l.controlReject(cmd.OrderID) },
	"request-changes": func(l *Loop, cmd ControlCommand) error {
		return l.controlRequestChanges(cmd.OrderID, cmd.Prompt)
	},
	"mode":    func(l *Loop, cmd ControlCommand) error { return l.controlMode(cmd.Value) },
	"enqueue": func(l *Loop, cmd ControlCommand) error { return l.controlEnqueue(cmd) },
	"stop-all": func(l *Loop, _ ControlCommand) error {
		l.controlStopAll()
		return nil
	},
	"requeue":   func(l *Loop, cmd ControlCommand) error { return l.controlRequeue(cmd.OrderID) },
	"edit-item": func(l *Loop, cmd ControlCommand) error { return l.controlEditItem(cmd) },
	"reorder":   func(l *Loop, cmd ControlCommand) error { return l.controlReorder(cmd) },
	"stop":      func(l *Loop, cmd ControlCommand) error { return l.controlStop(cmd.Name) },
	"set-max-concurrency": func(l *Loop, cmd ControlCommand) error {
		return l.controlSetMaxConcurrency(cmd.Value)
	},
	"advance":   func(l *Loop, cmd ControlCommand) error { return l.controlAdvance(cmd.OrderID) },
	"add-stage": func(l *Loop, cmd ControlCommand) error { return l.controlAddStage(cmd) },
	"park-review": func(l *Loop, cmd ControlCommand) error {
		return l.controlParkReview(cmd.OrderID, cmd.Prompt)
	},
}

// ControlActions returns the name of every control the loop dispatches, sorted.
func ControlActions() []string {
	names := make([]string, 0, len(controlHandlers))
	for name := range controlHandlers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (l *Loop) dispatchControlCommand(cmd ControlCommand) error {
	handler, ok := controlHandlers[stringx.Normalize(cmd.Action)]
	if !ok {
		return fmt.Errorf("unsupported action")
	}
	return handler(l, cmd)
}

func (l *Loop) controlPause() error {
	l.setState(StatePaused)
	return nil
}

func (l *Loop) controlResume() error {
	l.setState(StateRunning)
	return nil
}

func (l *Loop) controlDrain() error {
	l.setState(StateDraining)
	return nil
}

func (l *Loop) controlKill(name string) error {
	return l.killCook(name)
}

func (l *Loop) controlSteer(target, prompt string) error {
	return l.steer(target, prompt)
}

func (l *Loop) controlStopAll() {
	for _, cook := range l.cooks.activeCooksByOrder {
		if err := cook.session.ForceKill(); err != nil {
			l.logger.Warn("stop-all force kill failed", "session", cook.session.ID(), "error", err)
		}
	}
}

func (l *Loop) controlStop(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("stop target missing")
	}
	for _, cook := range l.cooks.activeCooksByOrder {
		if cook.worktreeName != name && cook.session.ID() != name {
			continue
		}
		controller := cook.session.Controller()
		if !controller.Steerable() {
			return l.controlStopKill(cook)
		}
		// Interrupt gracefully — the session stays alive.
		sessionID := cook.session.ID()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := controller.Interrupt(ctx); err != nil {
				l.logger.Warn("stop interrupt failed",
					"session", sessionID, "error", err)
			}
		}()
		return nil
	}
	return fmt.Errorf("session not found")
}

// controlStopKill is the fallback for non-steerable sessions: kill the process
// and clean up the cook.
func (l *Loop) controlStopKill(cook *cookHandle) error {
	if err := cook.session.ForceKill(); err != nil {
		return fmt.Errorf("force kill session %q failed: %w", cook.session.ID(), err)
	}
	l.trackCookCompleted(cook, StageResult{
		SessionID:   cook.session.ID(),
		Status:      StageResultCancelled,
		CompletedAt: l.deps.Now(),
	})
	delete(l.cooks.activeCooksByOrder, cook.orderID)
	l.cleanupCookWorktree(cook)
	return nil
}

func (l *Loop) writeLastAppliedSeq() error {
	if l.cmds.lastAppliedSeq == 0 {
		return nil
	}
	data := []byte(strconv.FormatUint(l.cmds.lastAppliedSeq, 10) + "\n")
	return filex.WriteFileAtomic(l.lastAppliedSeqPath(), data)
}

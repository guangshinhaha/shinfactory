package loop

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/guangshinhaha/shinfactory/dispatcher"
	"github.com/guangshinhaha/shinfactory/internal/ingest"
	"github.com/guangshinhaha/shinfactory/internal/stringx"
	loopruntime "github.com/guangshinhaha/shinfactory/runtime"
	"github.com/guangshinhaha/shinfactory/worktree"
)

// ensureSkillFresh verifies the skill is resolvable via the registry.
// If not found, it rebuilds the registry once and retries.
// Returns true if the skill exists after potential rebuild.
func (l *Loop) ensureSkillFresh(skillName string) bool {
	if _, ok := l.registry.ByKey(skillName); ok {
		return true
	}
	l.rebuildRegistry()
	_, ok := l.registry.ByKey(skillName)
	return ok
}

type spawnOptions struct {
	attempt     int
	resume      string
	displayName string // preserved across retries; empty = compute from session ID
	steered     bool   // the same attempt resumed with a steer; not a new launch for the spend cap
}

func (l *Loop) atMaxConcurrency() bool {
	maxCooks := l.config.Concurrency.MaxConcurrency
	if maxCooks <= 0 {
		maxCooks = 1
	}
	return len(l.cooks.activeCooksByOrder)+len(l.cooks.adoptedTargets) >= maxCooks
}

func (l *Loop) spawnCook(ctx context.Context, cand dispatchCandidate, order Order, opts spawnOptions) error {
	if isScheduleStage(cand.Stage) {
		return l.spawnSchedule(ctx, order, opts.attempt, opts.resume)
	}

	stage := cand.Stage
	delete(l.stoppedForPerson, cand.OrderID) // a person put it back; a new failure needs its own stop
	l.payStopDebt(cand.OrderID)
	runtimeName := l.stageRuntime(stage)
	if l.runtimeHeld(runtimeName) {
		return nil // out of usage: the stage stays pending until a launch can succeed
	}
	if why := l.overSpendCap(cand.OrderID, spendTaskKey(stage), order.Round); why != "" && !opts.steered {
		return l.stopOverCap(ctx, cand, stage, why)
	}

	// Belt-and-suspenders: give the registry one last chance to pick up
	// the skill before dispatch, in case fsnotify missed an event.
	if skillName := strings.TrimSpace(stage.Skill); skillName != "" {
		l.ensureSkillFresh(skillName)
	}

	name := l.cookWorktreeName(cand.OrderID, cand.StageIndex, stage.TaskKey)

	// A new order's first launch: an earlier order of the same id may have
	// left a branch behind (#186).
	if cand.StageIndex == 0 && opts.attempt == 0 && !opts.steered && order.Round == 0 {
		if err := l.prepareOrderBranch(ctx, name); err != nil {
			return err
		}
	}

	created, err := l.ensureWorktree(name)
	if err != nil {
		return fmt.Errorf("create worktree %s: %w", name, err)
	}

	// Guard: only one cook may use a worktree at a time.
	for _, active := range l.cooks.activeCooksByOrder {
		if active.worktreeName == name {
			return fmt.Errorf("worktree %s already in use by session %s", name, active.session.ID())
		}
	}

	resumePrompt := opts.resume
	worktreePath := l.worktreePath(name)
	if !created {
		// A steer resumes the same attempt; only a retry after failure starts clean.
		if opts.attempt > 0 && !opts.steered {
			resetWorktreeState(worktreePath)
		}
		if hint := worktreeResumeContext(worktreePath, name); hint != "" {
			resumePrompt = stringx.JoinNonEmpty("\n\n", hint, resumePrompt)
		}
	}
	if l.config.Landing.PR() {
		l.clearGateFile(order, stage)
	}
	retryNote := l.retryNote(cand.OrderID, cand.StageIndex)
	resumePrompt = stringx.JoinNonEmpty("\n\n", playbookNote(order, stage), l.gateNote(order, stage), retryNote, resumePrompt)
	rationale := order.Rationale
	if gateStageKey(stage) == gateVerifyKey && l.config.Landing.PR() {
		rationale = "" // a verifier works from the issue, not the scheduler's reasoning
	}
	prompt := buildCookPrompt(cand.OrderID, stage, order.Plan, order.Title, rationale, resumePrompt)

	taskType, _ := l.registry.ByKey(stage.TaskKey)
	stageProvider, stageModel := l.stageRouting(stage)
	req := loopruntime.DispatchRequest{
		Name:         name,
		Prompt:       prompt,
		Provider:     stageProvider,
		Model:        stageModel,
		Skill:        stage.Skill,
		WorktreePath: worktreePath,
		TaskKey:      taskType.Key,
		Runtime:      runtimeName,
		DisplayName:  opts.displayName,
		Title:        order.Title,
		RetryCount:   opts.attempt,
	}
	attemptID := dispatchAttemptID(cand.OrderID, cand.StageIndex, opts.attempt)
	l.emitEvent(ingest.EventDispatchRequested, map[string]any{
		"order_id":    cand.OrderID,
		"stage_index": cand.StageIndex,
		"attempt_id":  attemptID,
	})

	session, fallbackOutcome, err := l.dispatchSession(ctx, req)
	if err != nil {
		return l.handleCookDispatchFailure(cand, stage, name, created, attemptID, err)
	}
	if err := l.ensureOrderStageStatus(cand.OrderID, cand.StageIndex, StageStatusActive); err != nil {
		l.logger.Warn("mirror active stage status failed",
			"order", cand.OrderID,
			"stage", cand.StageIndex,
			"error", err)
	}

	displayName := strings.TrimSpace(opts.displayName)
	if displayName == "" {
		displayName = stringx.KitchenName(session.ID())
	}

	dispatchedRuntime := stringx.Normalize(req.Runtime)
	if fallbackOutcome.Class == AgentStartFailureClassFallback {
		dispatchedRuntime = stringx.Normalize(fallbackOutcome.SelectedRuntime)
	}
	if dispatchedRuntime == "" {
		dispatchedRuntime = "process"
	}

	cook := &cookHandle{
		cookIdentity: cookIdentity{
			orderID:    cand.OrderID,
			stageIndex: cand.StageIndex,
			stage:      stage,
			plan:       order.Plan,
		},
		orderStatus:       order.Status,
		session:           session,
		worktreeName:      name,
		worktreePath:      req.WorktreePath,
		attempt:           opts.attempt,
		generation:        l.nextDispatchGeneration(),
		displayName:       displayName,
		dispatchedRuntime: dispatchedRuntime,
	}
	l.trackCookStarted(cook)
	l.clearRetryNote(cand.OrderID)
	if !opts.steered {
		l.recordStageLaunch(cand.OrderID, spendTaskKey(stage), order.Round)
	}
	l.cooks.activeCooksByOrder[cand.OrderID] = cook
	l.startSessionWatcher(ctx, cook, false)

	// Emit V2 canonical state events for dispatch.
	l.emitEvent(ingest.EventDispatchCompleted, map[string]any{
		"order_id":      cand.OrderID,
		"stage_index":   cand.StageIndex,
		"attempt_id":    attemptID,
		"session_id":    session.ID(),
		"worktree_name": name,
	})

	l.logger.Info("cook dispatched", "order", cand.OrderID, "stage", cand.StageIndex, "session", session.ID(), "worktree", name, "attempt", opts.attempt)
	return nil
}

func (l *Loop) dispatchSession(ctx context.Context, req loopruntime.DispatchRequest) (loopruntime.SessionHandle, RuntimeFallbackOutcome, error) {
	runtimeName := l.dispatchRuntime(req.Runtime)

	runtime := l.deps.Runtimes[runtimeName]
	if runtime == nil {
		notConfigured := newRuntimeNotConfiguredError(runtimeName)
		return nil, RuntimeFallbackOutcome{}, classifyAgentStartFailure(runtimeName, notConfigured)
	}
	if l.runtimeHeld(runtimeName) {
		return nil, RuntimeFallbackOutcome{}, dispatcher.UsageLimitError{Runtime: runtimeName, Detail: "launches held until the limit resets"}
	}

	req.Runtime = runtimeName
	// Every launch passes here, so the deny list cannot be skipped by a new call site.
	req.DenyProviders = l.config.ForeignProviders(req.Provider)
	session, err := runtime.Dispatch(ctx, req)
	if err == nil {
		l.releaseRuntime(runtimeName)
		return session, RuntimeFallbackOutcome{}, nil
	}
	if dispatcher.IsUsageLimit(err) {
		// The same account fails the same way on any fallback, so hold the
		// runtime and let the caller leave the stage pending.
		l.holdRuntime(runtimeName, err.Error())
		return nil, RuntimeFallbackOutcome{}, err
	}
	// No fallback from orca: a plain process would run on the default login
	// while the config says orca. The caller fails the stage with this error.
	if dispatcher.AllowsProcessFallback(runtimeName) {
		if fallback := l.deps.Runtimes["process"]; fallback != nil {
			l.logger.Warn("runtime dispatch failed, falling back to process", "runtime", runtimeName, "error", err)
			req.Runtime = "process"
			req.DispatchWarning = fmt.Sprintf("%s dispatch failed: %v", runtimeName, err)
			outcome := newRuntimeFallbackOutcome(
				runtimeName,
				"process",
				"runtime fallback used process dispatcher",
				err,
			)
			fallbackSession, fallbackErr := fallback.Dispatch(ctx, req)
			if fallbackErr != nil {
				return nil, outcome, classifyAgentStartFailure("process", fallbackErr)
			}
			return fallbackSession, outcome, nil
		}
	}
	return nil, RuntimeFallbackOutcome{}, classifyAgentStartFailure(runtimeName, err)
}

func (l *Loop) handleCookDispatchFailure(cand dispatchCandidate, stage Stage, worktreeName string, created bool, attemptID string, err error) error {
	if created {
		_ = l.deps.Worktree.Cleanup(worktreeName, worktree.CleanupOpts{Force: true})
	}
	if dispatcher.IsUsageLimit(err) {
		// Not a failure of the order: the stage stays pending and the runtime
		// is held (see dispatchSession), so the next launch after the hold resumes it.
		l.logger.Warn("dispatch paused by usage limit",
			"order", cand.OrderID, "stage", cand.StageIndex, "error", err)
		return l.resetStageToPending(cand.OrderID, cand.StageIndex, err.Error())
	}
	envelope, ok := asDispatchFailureEnvelope(err)
	if !ok {
		envelope = classifyAgentStartFailure(l.stageRuntime(stage), err)
	}

	reason := "dispatch failed: " + envelope.Error()
	l.owePersonStop(cand.OrderID, reason)
	if err := l.mutateOrdersState(func(orders *OrdersFile) (bool, error) {
		updated, err := failStage(*orders, cand.OrderID, reason)
		if err != nil {
			return false, err
		}
		*orders = updated
		return true, nil
	}); err != nil {
		return err
	}
	cook := &cookHandle{
		cookIdentity: cookIdentity{
			orderID:    cand.OrderID,
			stageIndex: cand.StageIndex,
			stage:      stage,
		},
	}
	l.recordStageFailure(cook, reason, OrderFailureClassStageTerminal, nil)
	l.emitEvent(ingest.EventStageFailed, map[string]any{
		"order_id":      cand.OrderID,
		"stage_index":   cand.StageIndex,
		"attempt_id":    attemptID,
		"worktree_name": worktreeName,
		"error":         reason,
	})
	l.forwardToScheduler(cook, "dispatch_failed", reason, nil)
	l.classifyOrderHard(
		"cycle.dispatch_terminal",
		OrderFailureClassStageTerminal,
		cand.OrderID,
		cand.StageIndex,
		reason,
		err,
	)
	l.logger.Warn("cook dispatch failed; order marked failed",
		"order", cand.OrderID, "stage", cand.StageIndex, "error", err, "class", envelope.Class)
	l.stopFailedOrder(context.Background(), cand.OrderID, reason)
	return nil
}

func (l *Loop) worktreePath(name string) string {
	return filepath.Join(l.projectDir, ".worktrees", name)
}

// resetWorktreeState discards uncommitted changes in a worktree so the next
// agent starts from a clean working tree. Committed progress on the worktree
// branch is preserved. Errors are logged but not fatal — a dirty worktree is
// better than failing to dispatch.
func resetWorktreeState(worktreePath string) {
	// Reset tracked files to HEAD.
	checkout := exec.Command("git", "-C", worktreePath, "checkout", ".")
	checkout.Stdout, checkout.Stderr = nil, nil
	_ = checkout.Run()

	// Remove untracked files and directories.
	clean := exec.Command("git", "-C", worktreePath, "clean", "-fd")
	clean.Stdout, clean.Stderr = nil, nil
	_ = clean.Run()
}

func (l *Loop) ensureWorktree(name string) (bool, error) {
	path := l.worktreePath(name)
	info, err := os.Stat(path)
	if err == nil {
		if !info.IsDir() {
			return false, fmt.Errorf("worktree path %s is not a directory", path)
		}
		return false, nil
	}
	if !os.IsNotExist(err) {
		return false, fmt.Errorf("stat worktree path %s: %w", path, err)
	}

	var createOpts []worktree.CreateOpts
	if l.config.Landing.PR() {
		// Branch from the remote's current default branch, not local main,
		// which is stale once an earlier order's PR has merged.
		from, err := fetchBase(l.projectDir, l.config.Landing.Base)
		if err != nil {
			return false, err
		}
		createOpts = append(createOpts, worktree.CreateOpts{From: from})
	}
	if err := l.deps.Worktree.Create(name, createOpts...); err != nil {
		if isWorktreeAlreadyExistsError(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func dispatchAttemptID(orderID string, stageIndex, attempt int) string {
	return fmt.Sprintf("%s-%d-attempt-%d", strings.TrimSpace(orderID), stageIndex, attempt)
}

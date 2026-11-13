package loop

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/guangshinhaha/shinfactory/internal/dispatch"
	"github.com/guangshinhaha/shinfactory/mise"
)

const mergeBackpressureLimit = 128

func (l *Loop) buildCycleBrief(ctx context.Context) (mise.Brief, []string, bool, bool, error) {
	l.refreshAdoptedTargets()
	brief, warnings, miseChanged, waiting, err := l.buildBriefWithRetry(ctx)
	if err != nil {
		return mise.Brief{}, warnings, false, false, err
	}
	if waiting {
		return mise.Brief{}, warnings, false, false, nil
	}
	if l.state != StateRunning && l.state != StateIdle {
		return brief, warnings, false, miseChanged, nil
	}
	return brief, warnings, true, miseChanged, nil
}

// mergeOrdersNext reads orders-next.json, validates it, and merges into
// current orders. Returns whether promotion occurred and whether the incoming
// orders array was empty. Does NOT handle promotion side effects (cooldown,
// canonical emission, failure classification).
func (l *Loop) mergeOrdersNext(brief mise.Brief) (mergeResult, error) {
	orders, err := l.currentOrders()
	if err != nil {
		return mergeResult{}, err
	}
	return consumeOrdersNext(l.deps.OrdersNextFile, orders, brief.Backlog)
}

func appendUnique(dst, src []string) []string {
	seen := make(map[string]struct{}, len(dst))
	for _, id := range dst {
		seen[id] = struct{}{}
	}
	for _, id := range src {
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			dst = append(dst, id)
		}
	}
	return dst
}

// handlePromotionResult processes the side effects of an orders-next
// promotion: error classification, cooldown management, and canonical
// event emission.
func (l *Loop) handlePromotionResult(result mergeResult, err error) error {
	if err != nil {
		l.handlePromotionError(err)
		return nil
	}
	if !result.Promoted {
		return nil
	}

	l.logger.Info("orders-next promoted")
	l.schedulePromoted = true
	l.lastPromotionError = ""
	if len(result.Unbuildable) > 0 {
		l.droppedOrderIDs = appendUnique(l.droppedOrderIDs, result.Unbuildable)
		l.logger.Warn("dropped orders that name no ready issue", "orders", strings.Join(result.Unbuildable, ", "))
	}
	if len(result.AmendRejected) > 0 {
		l.logger.Warn("rejected amendment to orders with a stage in flight",
			"orders", strings.Join(result.AmendRejected, ", "),
			"reason", "stage in flight; amend after it finishes")
	}
	if result.EmptyPromotion {
		l.scheduleNothingUntil = l.deps.Now().Add(5 * time.Minute)
		l.logger.Info("schedule produced no orders, entering cooldown")
	} else {
		l.scheduleNothingUntil = time.Time{}
	}
	if err := l.writeOrdersState(result.Orders); err != nil {
		l.handlePromotionError(err)
		return nil
	}
	if err := os.Remove(l.deps.OrdersNextFile); err != nil && !os.IsNotExist(err) {
		l.handlePromotionError(fmt.Errorf("remove orders-next.json: %w", err))
		return nil
	}
	if err := l.cancelSupersededActiveCooks(result.Orders); err != nil {
		l.handlePromotionError(err)
		return nil
	}
	for _, order := range result.Orders.Orders {
		l.syncCanonicalOrderFromLegacy(order)
	}
	if err := l.persistCanonicalCheckpoint(); err != nil {
		l.handlePromotionError(err)
		return nil
	}
	l.emitPromotedOrders()
	if len(result.Unbuildable) > 0 {
		// Emit once the promotion is persisted: a failed step above leaves
		// orders-next.json in place and the retry would emit it again.
		_ = l.events.Emit(LoopEventOrdersUnready, OrdersUnreadyPayload{OrderIDs: result.Unbuildable})
	}
	return nil
}

func (l *Loop) cancelSupersededActiveCooks(orders OrdersFile) error {
	orderByID := make(map[string]Order, len(orders.Orders))
	for _, order := range orders.Orders {
		orderByID[order.ID] = order
	}

	for orderID, cook := range l.cooks.activeCooksByOrder {
		order, ok := orderByID[orderID]
		if !ok {
			if err := l.cancelSupersededCook(orderID, cook, false); err != nil {
				return err
			}
			continue
		}
		_, currentStage := activeStageForOrder(order)
		if currentStage == nil {
			if err := l.cancelSupersededCook(orderID, cook, false); err != nil {
				return err
			}
			continue
		}
		if sameStageDefinition(cook.stage, *currentStage) {
			continue
		}
		if err := l.cancelSupersededCook(orderID, cook, true); err != nil {
			return err
		}
	}
	return nil
}

func (l *Loop) cancelSupersededCook(orderID string, cook *cookHandle, resume bool) error {
	if cook == nil || cook.session == nil {
		delete(l.cooks.activeCooksByOrder, orderID)
		return nil
	}
	l.logger.Info("order amendment superseded active stage, cancelling cook",
		"order", orderID,
		"session", cook.session.ID(),
		"stage", cook.stage.TaskKey)
	if err := cook.session.ForceKill(); err != nil {
		// The worker may still be live. Keep the cook tracked, so nothing
		// respawns into its worktree, and leave the worktree alone.
		return fmt.Errorf("cancelling superseded cook %s for order %s: %w", cook.session.ID(), orderID, err)
	}
	l.trackCookCompleted(cook, StageResult{
		SessionID:   cook.session.ID(),
		Status:      StageResultCancelled,
		CompletedAt: l.deps.Now(),
	})
	delete(l.cooks.activeCooksByOrder, orderID)
	if resume {
		if err := l.ensureOrderStageStatus(orderID, cook.stageIndex, StageStatusPending); err != nil {
			return err
		}
		orders, err := l.currentOrders()
		if err != nil {
			return err
		}
		for _, order := range orders.Orders {
			if order.ID != orderID {
				continue
			}
			l.syncCanonicalOrderFromLegacy(order)
			if err := l.persistCanonicalCheckpoint(); err != nil {
				return err
			}
			break
		}
	}
	l.cleanupCookWorktree(cook)
	return nil
}

// handlePromotionError classifies and emits events for a failed orders-next
// promotion. Always marks schedulePromoted so the schedule order can complete.
func (l *Loop) handlePromotionError(err error) {
	payload := PromotionFailedPayload{Reason: err.Error()}
	if isOrdersNextRejectedError(err) {
		mistake := newSchedulerMistakeEnvelope(SchedulerMistakeReasonOrdersNextRejected)
		l.classifySchedulerMistake(
			"build.promote_orders_next",
			"orders-next promotion failed",
			err,
			SchedulerMistakeReasonOrdersNextRejected,
		)
		payload.AgentMistake = &mistake
		failureMetadata := eventFailureMetadataForLoop(CycleFailureClassDegradeContinue, "", &mistake)
		payload.Failure = &failureMetadata
	} else {
		l.classifyDegrade(
			"build.promote_orders_next",
			"orders-next promotion failed",
			err,
		)
		failureMetadata := eventFailureMetadataForLoop(CycleFailureClassDegradeContinue, "", nil)
		payload.Failure = &failureMetadata
	}
	l.logger.Warn("orders-next promotion failed", "error", err)
	l.lastPromotionError = err.Error()
	_ = l.events.Emit(LoopEventPromotionFailed, payload)
	// Mark promoted so the schedule order can complete and a new
	// schedule can be spawned. Without this, the schedule order
	// stays active forever and the loop deadlocks.
	l.schedulePromoted = true
}

// emitPromotedOrders emits V2 canonical state events for each newly promoted
// order not yet tracked in canonical state.
func (l *Loop) emitPromotedOrders() {
	promotedOrders, _ := l.currentOrders()
	for _, order := range promotedOrders.Orders {
		l.trackCanonicalOrder(order)
	}
}

// ensureScheduleIfNeeded checks whether a schedule order needs to be
// bootstrapped or injected. Handles idle transition, empty-backlog bootstrap,
// and mise-change injection.
func (l *Loop) ensureScheduleIfNeeded(brief mise.Brief, orders *OrdersFile, miseChanged bool) (idle bool, err error) {
	if len(l.cooks.activeCooksByOrder) == 0 && len(l.cooks.adoptedTargets) == 0 && !hasNonScheduleOrders(*orders) {
		idle, err = l.bootstrapScheduleIfEmpty(brief, orders)
		if err != nil || idle {
			return idle, err
		}
	}
	if miseChanged && !l.hasActiveScheduleCook() && !hasScheduleOrder(*orders) {
		orders.Orders = append(orders.Orders, scheduleOrder(""))
		if err := l.writeOrdersState(*orders); err != nil {
			return false, err
		}
		l.logger.Info("mise changed, injecting schedule order")
	}
	return false, nil
}

// bootstrapScheduleIfEmpty handles the case where no non-schedule orders
// exist and no cooks are active. If no schedule order exists, either
// transitions to idle or bootstraps one.
func (l *Loop) bootstrapScheduleIfEmpty(brief mise.Brief, orders *OrdersFile) (idle bool, err error) {
	if hasScheduleOrder(*orders) {
		return false, nil
	}
	if len(brief.Backlog) == 0 || l.scheduleNothingCooldownActive() {
		l.setState(StateIdle)
		return true, nil
	}
	*orders = bootstrapScheduleOrder()
	if err := l.writeOrdersState(*orders); err != nil {
		return false, err
	}
	if len(orders.Orders) > 0 {
		l.trackCanonicalOrder(orders.Orders[len(orders.Orders)-1])
	}
	l.logger.Info("orders empty, bootstrapping schedule")
	return false, nil
}

func (l *Loop) prepareOrdersForCycle(brief mise.Brief, warnings []string, miseChanged bool) (OrdersFile, bool, error) {
	result, err := l.mergeOrdersNext(brief)
	if err := l.handlePromotionResult(result, err); err != nil {
		return OrdersFile{}, false, err
	}

	// Reset cooldown when backlog changes (mise content changed).
	if miseChanged {
		l.scheduleNothingUntil = time.Time{}
	}

	orders, err := l.currentOrders()
	if err != nil {
		return OrdersFile{}, false, err
	}

	orders, err = l.normalizeOrders(orders)
	if err != nil {
		orders, err = l.recoverFromOrdersValidationError(err)
		if err != nil {
			return OrdersFile{}, false, err
		}
	}

	l.emitSyncWarnings(warnings)

	idle, err := l.ensureScheduleIfNeeded(brief, &orders, miseChanged)
	if err != nil {
		return OrdersFile{}, false, err
	}
	if idle {
		return OrdersFile{}, false, nil
	}

	// Leave idle only once the cycle has work; flipping at cycle start made an
	// idle loop log idle<->running every cycle.
	if l.state == StateIdle {
		l.setState(StateRunning)
	}
	l.setOrdersState(orders)
	return orders, true, nil
}

// normalizeOrders validates and normalizes orders, rebuilding the registry
// and auditing on repeated failures.
func (l *Loop) normalizeOrders(orders OrdersFile) (OrdersFile, error) {
	normalizedOrders, changed, normErr := NormalizeAndValidateOrders(orders, l.registry, l.config)
	if normErr != nil {
		l.rebuildRegistry()
		normalizedOrders, changed, normErr = NormalizeAndValidateOrders(orders, l.registry, l.config)
	}
	if normErr != nil {
		l.auditOrders()
		var err error
		orders, err = l.currentOrders()
		if err != nil {
			return OrdersFile{}, err
		}
		normalizedOrders, changed, normErr = NormalizeAndValidateOrders(orders, l.registry, l.config)
	}
	if normErr != nil {
		return OrdersFile{}, normErr
	}
	if !changed {
		return orders, nil
	}
	if err := l.writeOrdersState(normalizedOrders); err != nil {
		return OrdersFile{}, err
	}
	l.logger.Info("orders normalized")
	return normalizedOrders, nil
}

func (l *Loop) recoverFromOrdersValidationError(normErr error) (OrdersFile, error) {
	l.classifySchedulerMistake(
		"build.prepare_orders",
		"orders validation failed, requesting scheduler repair",
		normErr,
		SchedulerMistakeReasonOrdersNextRejected,
	)

	archivedPath := ""
	ordersData, readErr := os.ReadFile(l.deps.OrdersFile)
	if readErr == nil && len(strings.TrimSpace(string(ordersData))) > 0 {
		archivedPath = fmt.Sprintf("%s.bad.%d", l.deps.OrdersFile, l.deps.Now().UnixNano())
		if writeErr := os.WriteFile(archivedPath, ordersData, 0o644); writeErr != nil {
			l.logger.Warn("failed to archive invalid orders state", "error", writeErr)
			archivedPath = ""
		}
	}

	repairMessage := "The loop rejected scheduler-produced orders during preparation.\n" +
		"Fix this issue in your next orders-next.json output:\n" + normErr.Error()
	if archivedPath != "" {
		repairMessage += "\nInvalid orders snapshot: " + archivedPath
	}
	l.lastPromotionError = repairMessage
	l.scheduleNothingUntil = time.Time{}

	repairOrders := bootstrapScheduleOrder()
	if err := l.writeOrdersState(repairOrders); err != nil {
		return OrdersFile{}, fmt.Errorf("recover invalid orders state: %w", err)
	}

	l.logger.Warn("orders validation failed, replaced orders with schedule repair order",
		"error", normErr,
		"archived_orders", archivedPath)
	return repairOrders, nil
}

// emitSyncWarnings emits a degraded event if the sync script reported warnings.
func (l *Loop) emitSyncWarnings(warnings []string) {
	if !hasSyncWarnings(warnings) {
		return
	}
	failureMetadata := eventFailureMetadataForLoop(CycleFailureClassDegradeContinue, "", nil)
	l.logger.Warn("sync script issue, continuing with empty backlog", "warnings", strings.Join(warnings, "; "))
	_ = l.events.Emit(LoopEventSyncDegraded, SyncDegradedPayload{
		Reason:  strings.Join(warnings, "; "),
		Failure: &failureMetadata,
	})
}

func (l *Loop) planCycleSpawns(orders OrdersFile, brief mise.Brief, capacity int) ([]dispatchCandidate, error) {
	if !l.canonicalLoaded {
		if err := l.loadOrBootstrapCanonical(); err != nil {
			return nil, err
		}
	}
	if l.mergeQueue != nil {
		if l.mergeQueue.Pending()+l.mergeQueue.InFlight() > mergeBackpressureLimit {
			return nil, nil
		}
	}

	blockedOrders := make(map[string]string, len(l.cooks.pendingReview)+len(brief.Tickets))
	for targetID := range activeTicketTargetSet(brief) {
		blockedOrders[targetID] = "ticketed"
	}
	for targetID := range l.cooks.pendingReview {
		blockedOrders[targetID] = "pending_review"
	}
	for targetID := range l.evidence.checks {
		blockedOrders[targetID] = "checking"
	}

	plan := dispatch.PlanDispatches(l.canonical, capacity, blockedOrders)
	if len(plan.Candidates) == 0 {
		return nil, nil
	}

	orderMap := make(map[string]Order, len(orders.Orders))
	for _, order := range orders.Orders {
		orderMap[order.ID] = order
	}

	candidates := make([]dispatchCandidate, 0, len(plan.Candidates))
	for _, candidate := range plan.Candidates {
		order, ok := orderMap[candidate.OrderID]
		if !ok {
			return nil, fmt.Errorf("canonical dispatch candidate %q missing from orders state", candidate.OrderID)
		}
		if candidate.StageIndex < 0 || candidate.StageIndex >= len(order.Stages) {
			return nil, fmt.Errorf("canonical dispatch candidate %q stage %d missing from orders state", candidate.OrderID, candidate.StageIndex)
		}
		candidates = append(candidates, dispatchCandidate{
			OrderID:    candidate.OrderID,
			StageIndex: candidate.StageIndex,
			Stage:      order.Stages[candidate.StageIndex],
		})
	}
	return l.withoutWriteClashes(candidates), nil
}

func (l *Loop) spawnPlannedCandidates(ctx context.Context, candidates []dispatchCandidate, orders OrdersFile) error {
	// Build order lookup for candidate dispatch.
	orderMap := make(map[string]Order, len(orders.Orders))
	for _, o := range orders.Orders {
		orderMap[o.ID] = o
	}
	for _, cand := range candidates {
		if l.atMaxConcurrency() {
			break
		}
		order, ok := orderMap[cand.OrderID]
		if !ok {
			continue
		}
		if err := l.spawnCook(ctx, cand, order, spawnOptions{}); err != nil {
			return err
		}
	}
	return nil
}

const (
	defaultBacklogRetryBackoff = time.Second
	// maxBacklogRetryBackoff caps the doubling so a long window keeps probing
	// the network instead of waiting minutes between tries.
	maxBacklogRetryBackoff = 30 * time.Second
)

// transientMarkers are lowercase fragments of network and 5xx failures from the
// backlog adapter (gh, curl, DNS). Anything else, such as bad credentials or a
// missing repo, fails at once.
var transientMarkers = []string{
	"no such host", "temporary failure in name resolution", "could not resolve host",
	"connection refused", "connection reset", "i/o timeout", "operation timed out",
	"tls handshake", "network is unreachable", "no route to host", "unexpected eof",
	"error connecting to", "http 500", "http 502", "http 503", "http 504",
	"500 internal server error", "bad gateway", "service unavailable", "gateway timeout",
	"enotfound", "client.timeout exceeded", "timeout awaiting response headers",
}

func isTransientBacklogError(err error) bool {
	text := strings.ToLower(err.Error())
	for _, marker := range transientMarkers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// backlogRetry tracks a transient backlog sync failure across cycles. The loop
// never sleeps inside a cycle: it records when the next try is due and returns,
// so control commands and status stamps keep running during an outage.
type backlogRetry struct {
	firstFailure time.Time
	nextAttempt  time.Time
	attempts     int
	backoff      time.Duration
}

// buildBriefWithRetry builds the brief. A transient backlog failure starts (or
// continues) a retry that spans cycles, with capped exponential backoff, until
// the configured window has passed since the first failure. While a retry is
// pending it returns waiting=true with a warning and no brief. A persistent
// failure returns an error that says so.
func (l *Loop) buildBriefWithRetry(ctx context.Context) (brief mise.Brief, warnings []string, changed, waiting bool, err error) {
	now := l.deps.Now()
	if r := l.backlogRetry; r != nil && now.Before(r.nextAttempt) {
		return mise.Brief{}, []string{backlogRetryWarning(r, r.nextAttempt.Sub(now))}, false, true, nil
	}
	started := now
	brief, warnings, changed, err = l.deps.Mise.Build(ctx, l.snapshotActiveSummary(), l.snapshotRecentHistory())
	if ctx.Err() != nil {
		return brief, warnings, changed, false, err
	}
	if err == nil || !isTransientBacklogError(err) {
		l.backlogRetry = nil
		return brief, warnings, changed, false, err
	}
	now = l.deps.Now()
	r := l.backlogRetry
	if r == nil {
		r = &backlogRetry{firstFailure: started, backoff: defaultBacklogRetryBackoff}
		l.backlogRetry = r
	}
	r.attempts++
	window := l.config.Backlog.RetryWindow()
	remaining := window - now.Sub(r.firstFailure)
	if remaining <= 0 {
		l.backlogRetry = nil
		return brief, warnings, changed, false, fmt.Errorf("backlog sync still failing after %d attempts over %s: %w",
			r.attempts, now.Sub(r.firstFailure).Round(time.Second), err)
	}
	step := min(r.backoff, maxBacklogRetryBackoff, remaining)
	r.nextAttempt = now.Add(step)
	r.backoff = min(r.backoff*2, maxBacklogRetryBackoff)
	if l.logger != nil {
		l.logger.Warn("backlog sync failed with a transient error; retrying",
			"attempt", r.attempts, "retry_in", step.String(), "cause", err)
	}
	return mise.Brief{}, []string{backlogRetryWarning(r, step)}, false, true, nil
}

func backlogRetryWarning(r *backlogRetry, retryIn time.Duration) string {
	return fmt.Sprintf("backlog sync failing (attempt %d); retrying in %s", r.attempts, retryIn.Round(time.Second))
}

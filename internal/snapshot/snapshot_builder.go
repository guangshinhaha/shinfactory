package snapshot

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/guangshinhaha/shinfactory/event"
	"github.com/guangshinhaha/shinfactory/internal/linestatus"
	"github.com/guangshinhaha/shinfactory/internal/orderx"
	"github.com/guangshinhaha/shinfactory/internal/projection"
	"github.com/guangshinhaha/shinfactory/internal/state"
	"github.com/guangshinhaha/shinfactory/internal/stringx"
	"github.com/guangshinhaha/shinfactory/loop"
)

const defaultContextWindowTokens = 200_000

// LoadSnapshot builds a Snapshot from in-memory LoopState. Session data comes
// from the loop's active cook tracking and recent history — no directory
// scanning or bulk event reading.
func LoadSnapshot(runtimeDir string, now time.Time, state loop.LoopState) (Snapshot, error) {
	sessions := make([]Session, 0, len(state.ActiveCooks)+len(state.RecentHistory))
	active := make([]Session, 0, len(state.ActiveCooks))
	recent := make([]Session, 0, len(state.RecentHistory))
	projected := state.Projection

	reader := event.NewEventReader(runtimeDir)
	for _, cook := range state.ActiveCooks {
		status := stringx.Normalize(cook.Status)
		if status == "" {
			status = "running"
		}
		runtime := stringx.Normalize(cook.Runtime)
		var remoteHost string
		if runtime != "" && runtime != "process" {
			remoteHost = runtime
		}
		session := Session{
			ID:              cook.SessionID,
			DisplayName:     cook.DisplayName,
			Status:          status,
			Runtime:         cook.Runtime,
			Provider:        cook.Provider,
			Model:           cook.Model,
			LastActivity:    now.UTC(),
			WorktreeName:    cook.WorktreeName,
			TaskKey:         cook.TaskKey,
			TotalCostUSD:    cook.TotalCostUSD,
			DurationSeconds: int64(now.Sub(cook.StartedAt).Seconds()),
			RemoteHost:      remoteHost,
		}
		enrichActiveSession(reader, cook.SessionID, &session)
		sessions = append(sessions, session)
		active = append(active, session)
	}

	for _, item := range state.RecentHistory {
		session := Session{
			ID:           item.SessionID,
			DisplayName:  item.SessionID,
			Status:       stringx.Normalize(item.Status),
			LastActivity: item.CompletedAt,
			TaskKey:      item.TaskKey,
		}
		sessions = append(sessions, session)
		recent = append(recent, session)
	}

	sort.SliceStable(active, func(i, j int) bool {
		return active[i].ID < active[j].ID
	})
	sort.SliceStable(recent, func(i, j int) bool {
		if recent[i].LastActivity.Equal(recent[j].LastActivity) {
			return recent[i].ID < recent[j].ID
		}
		return recent[i].LastActivity.After(recent[j].LastActivity)
	})

	// Build a lookup from order ID to active cook session ID.
	cookSessionByOrder := make(map[string]string, len(state.ActiveCooks))
	for _, cook := range state.ActiveCooks {
		cookSessionByOrder[cook.OrderID] = cook.SessionID
	}

	// Build a lookup from (order ID, task key) to completed session ID so
	// stages that finished keep a navigable session_id in the UI.
	type orderStageKey struct{ orderID, taskKey string }
	completedSessionByStage := make(map[orderStageKey]string, len(state.RecentHistory))
	for _, item := range state.RecentHistory {
		if item.OrderID != "" && item.TaskKey != "" && item.SessionID != "" {
			completedSessionByStage[orderStageKey{item.OrderID, item.TaskKey}] = item.SessionID
		}
	}

	orders := make([]Order, 0, len(projected.Orders))
	for _, order := range projected.Orders {
		activeSessionID := cookSessionByOrder[order.ID]
		stages := make([]Stage, 0, len(order.Stages))
		for _, stage := range order.Stages {
			s := Stage{
				TaskKey:   stage.TaskKey,
				Prompt:    stage.Prompt,
				Skill:     stage.Skill,
				Provider:  stage.Provider,
				Model:     stage.Model,
				Runtime:   stage.Runtime,
				Group:     stage.Group,
				Status:    uiStageStatus(stage.Status),
				Extra:     cloneRawMap(stage.Extra),
				SessionID: "",
			}
			if (s.Status == string(orderx.StageStatusActive) || s.Status == string(orderx.StageStatusMerging)) && activeSessionID != "" {
				s.SessionID = activeSessionID
			} else if sid := completedSessionByStage[orderStageKey{order.ID, s.TaskKey}]; sid != "" {
				s.SessionID = sid
			}
			stages = append(stages, s)
		}
		orders = append(orders, Order{
			ID:        order.ID,
			Title:     order.Title,
			Plan:      append([]string(nil), order.Plan...),
			Rationale: order.Rationale,
			Stages:    stages,
			Status:    order.Status,
		})
	}

	// The line overlay refines the projection; an unreadable state file must
	// not blank the dashboard, so it degrades to the projection and warns.
	var warnings []string
	activeOrderIDs := projected.ActiveOrderIDs
	if line, err := linestatus.Read(runtimeDir); err != nil {
		warnings = append(warnings, fmt.Sprintf("dashboard shows the loop's last published state: read line state: %v", err))
	} else {
		orders, activeOrderIDs = overlayLine(orders, activeOrderIDs, line)
	}

	feedEvents := readSteerEvents(filepath.Join(runtimeDir, "control.ndjson"))
	loopEvents := readLoopEvents(runtimeDir)
	feedEvents = append(feedEvents, loopEvents...)
	sort.SliceStable(feedEvents, func(i, j int) bool {
		return feedEvents[i].At.Before(feedEvents[j].At)
	})

	return Snapshot{
		UpdatedAt:          now.UTC(),
		LoopState:          NormalizeLoopState(state.Status),
		Sessions:           sessions,
		Active:             active,
		Recent:             recent,
		Orders:             orders,
		ActiveOrderIDs:     nonNilStrings(activeOrderIDs),
		ActionNeeded:       nonNilStrings(projected.ActionNeeded),
		EventsBySession:    map[string][]EventLine{},
		FeedEvents:         nonNilFeedEvents(feedEvents),
		TotalCostUSD:       state.TotalCostUSD,
		PendingReviews:     projectedPendingReviews(projected.PendingReviews),
		PendingReviewCount: projected.PendingReviewCount,
		Mode:               projected.Mode,
		MaxConcurrency:     state.MaxConcurrency,
		Warnings:           warnings,
	}, nil
}

func projectedPendingReviews(reviews []projection.PendingReviewProjection) []loop.PendingReviewItem {
	items := make([]loop.PendingReviewItem, 0, len(reviews))
	for _, review := range reviews {
		items = append(items, loop.PendingReviewItem{
			OrderID:      review.OrderID,
			StageIndex:   review.StageIndex,
			TaskKey:      review.TaskKey,
			Prompt:       review.Prompt,
			Provider:     review.Provider,
			Model:        review.Model,
			Runtime:      review.Runtime,
			Skill:        review.Skill,
			Plan:         append([]string(nil), review.Plan...),
			WorktreeName: review.WorktreeName,
			WorktreePath: review.WorktreePath,
			SessionID:    review.SessionID,
			Reason:       review.Reason,
		})
	}
	return items
}

func cloneRawMap(src map[string]json.RawMessage) map[string]json.RawMessage {
	if len(src) == 0 {
		return nil
	}
	cloned := make(map[string]json.RawMessage, len(src))
	for key, value := range src {
		cloned[key] = append(json.RawMessage(nil), value...)
	}
	return cloned
}

// enrichActiveSession reads the session's event log and populates
// CurrentAction and ContextWindowUsagePct on the given Session.
func enrichActiveSession(reader *event.EventReader, sessionID string, session *Session) {
	events, err := reader.ReadSession(sessionID, event.EventFilter{})
	if err != nil || len(events) == 0 {
		return
	}

	var totalTokensIn int
	for _, ev := range events {
		switch ev.Type {
		case event.EventAction:
			label, body, _ := formatAction(ev.Payload)
			if body != "" {
				session.CurrentAction = label + " " + body
			}
		case event.EventCost:
			// Cost events are emitted from canonical result events.
			// Treat them as turn completion so stale prior action text does not
			// keep the UI in a perpetual "thinking" state after interrupts.
			session.CurrentAction = ""
			var cost struct {
				TokensIn int `json:"tokens_in"`
			}
			if json.Unmarshal(ev.Payload, &cost) == nil {
				totalTokensIn += cost.TokensIn
			}
		}
	}

	pct := float64(totalTokensIn) / defaultContextWindowTokens * 100
	if pct > 100 {
		pct = 100
	}
	session.ContextWindowUsagePct = pct
}

// NormalizeLoopState maps a raw loop state string to a canonical constant.
func NormalizeLoopState(value string) string {
	switch stringx.Normalize(value) {
	case "idle":
		return LoopStateIdle
	case "running":
		return LoopStateRunning
	case "paused":
		return LoopStatePaused
	case "draining":
		return LoopStateDraining
	default:
		return ""
	}
}

// nonNil helpers ensure slices marshal to [] instead of null in JSON.

func nonNilStrings(s []string) []string {
	if len(s) == 0 {
		return []string{}
	}
	return append([]string(nil), s...)
}

func nonNilFeedEvents(s []FeedEvent) []FeedEvent {
	if s == nil {
		return []FeedEvent{}
	}
	return s
}

func nonNilReviews(s []loop.PendingReviewItem) []loop.PendingReviewItem {
	if s == nil {
		return []loop.PendingReviewItem{}
	}
	return append([]loop.PendingReviewItem(nil), s...)
}

// uiStageStatus maps a stage's lifecycle status onto the statuses the dashboard
// draws: a dispatched, running or parked stage is active, a skipped one is
// cancelled.
func uiStageStatus(status string) string {
	switch state.StageLifecycleStatus(status) {
	case state.StageDispatching, state.StageRunning, state.StageReview:
		return string(orderx.StageStatusActive)
	case state.StageSkipped:
		return string(orderx.StageStatusCancelled)
	default:
		return status
	}
}

// overlayLine makes the dashboard agree with `shinfactory status --json`. The
// loop's published projection can lag the factory's state files and carries no
// resolved routing, so the line wins: it adds orders the projection lacks and
// fills each stage's session, dispatch id, provider and model from the files.
func overlayLine(orders []Order, activeIDs []string, line linestatus.Report) ([]Order, []string) {
	byID := make(map[string]int, len(orders))
	for i, o := range orders {
		byID[o.ID] = i
	}
	active := append([]string(nil), activeIDs...)
	known := make(map[string]bool, len(active))
	for _, id := range active {
		known[id] = true
	}
	lineOrders := make([]linestatus.Order, 0, len(line.Running)+len(line.Queued)+len(line.Parked))
	lineOrders = append(lineOrders, line.Running...)
	lineOrders = append(lineOrders, line.Queued...)
	lineOrders = append(lineOrders, line.Parked...)
	for _, lo := range lineOrders {
		i, ok := byID[lo.ID]
		if !ok {
			orders = append(orders, Order{ID: lo.ID, Title: lo.Title, Status: string(state.OrderActive), Stages: []Stage{}})
			i = len(orders) - 1
			byID[lo.ID] = i
		}
		if !known[lo.ID] {
			known[lo.ID] = true
			active = append(active, lo.ID)
		}
		stages := orders[i].Stages
		for j, ls := range lo.Stages {
			if j >= len(stages) {
				stages = append(stages, Stage{TaskKey: ls.TaskKey, Status: uiStageStatus(ls.Status)})
			}
			if stages[j].TaskKey != ls.TaskKey {
				continue
			}
			if ls.SessionID != "" {
				stages[j].SessionID = ls.SessionID
			}
			if ls.Provider != "" {
				stages[j].Provider = ls.Provider
			}
			if ls.Model != "" {
				stages[j].Model = ls.Model
			}
			stages[j].DispatchID = ls.DispatchID
		}
		orders[i].Stages = stages
	}
	return orders, active
}

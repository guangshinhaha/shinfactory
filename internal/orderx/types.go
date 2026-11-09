package orderx

import (
	"encoding/json"
	"fmt"
	"time"
)

// StageStatus is the lifecycle status of a stage.
type StageStatus string

const (
	StageStatusPending   StageStatus = "pending"
	StageStatusActive    StageStatus = "active"
	StageStatusMerging   StageStatus = "merging"
	StageStatusCompleted StageStatus = "completed"
	StageStatusFailed    StageStatus = "failed"
	StageStatusCancelled StageStatus = "cancelled"
)

// OrderStatus is the lifecycle status of an order.
type OrderStatus string

const (
	OrderStatusActive    OrderStatus = "active"
	OrderStatusCompleted OrderStatus = "completed"
	OrderStatusFailed    OrderStatus = "failed"
)

// Stage is a unit of work within an order (serialization type).
type Stage struct {
	TaskKey     string                     `json:"task_key,omitempty"`
	Prompt      string                     `json:"prompt,omitempty"`
	Skill       string                     `json:"skill,omitempty"`
	Provider    string                     `json:"provider"`
	Model       string                     `json:"model"`
	Runtime     string                     `json:"runtime,omitempty"`
	Group       int                        `json:"group,omitempty"`
	Status      StageStatus                `json:"status"`
	Extra       map[string]json.RawMessage `json:"extra,omitempty"`
	ExtraPrompt string                     `json:"extra_prompt,omitempty"`
}

// Order is a pipeline of stages (serialization type).
type Order struct {
	ID        string   `json:"id"`
	Title     string   `json:"title,omitempty"`
	Plan      []string `json:"plan,omitempty"`
	Rationale string   `json:"rationale,omitempty"`
	// Tier is the order's risk tier: none, low, medium or high. Empty means
	// medium. It sets how deep review goes and whether verify runs.
	Tier string `json:"tier,omitempty"`
	// WorkType is the kind of work the order does: bug, feature or refactor.
	// Empty means the scheduler could not tell, and the stage runs on its own
	// skill text. It picks the playbook an execute stage follows and the
	// evidence the loop checks before accepting it.
	WorkType string `json:"work_type,omitempty"`
	// Round counts the fix rounds a failed review or verify has sent back to
	// execute. Only the loop writes it.
	Round  int         `json:"round,omitempty"`
	Stages []Stage     `json:"stages"`
	Status OrderStatus `json:"status"`
}

// Risk tiers an order can carry.
const (
	TierNone   = "none"
	TierLow    = "low"
	TierMedium = "medium"
	TierHigh   = "high"
)

// ValidateTier returns an error if tier is not empty or a known tier.
func ValidateTier(tier string) error {
	switch tier {
	case "", TierNone, TierLow, TierMedium, TierHigh:
		return nil
	}
	return fmt.Errorf("unknown tier %q (want none, low, medium or high)", tier)
}

// Work types an order can carry.
const (
	WorkTypeBug      = "bug"
	WorkTypeFeature  = "feature"
	WorkTypeRefactor = "refactor"
)

// ValidateWorkType returns an error if workType is not empty or a known type.
func ValidateWorkType(workType string) error {
	switch workType {
	case "", WorkTypeBug, WorkTypeFeature, WorkTypeRefactor:
		return nil
	}
	return fmt.Errorf("unknown work_type %q (want bug, feature or refactor)", workType)
}

// OrdersFile is the top-level orders.json structure (serialization type).
type OrdersFile struct {
	GeneratedAt  time.Time `json:"generated_at"`
	Orders       []Order   `json:"orders"`
	ActionNeeded []string  `json:"action_needed,omitempty"`
}

// CurrentGroup returns the group number of the first stage with status
// pending, active, or merging. Returns -1 if all stages are terminal.
func (o Order) CurrentGroup() int {
	for _, s := range o.Stages {
		switch s.Status {
		case StageStatusPending, StageStatusActive, StageStatusMerging:
			return s.Group
		}
	}
	return -1
}

// StagesInGroup returns the indices of stages belonging to the given group.
func (o Order) StagesInGroup(group int) []int {
	var indices []int
	for i, s := range o.Stages {
		if s.Group == group {
			indices = append(indices, i)
		}
	}
	return indices
}

// ValidateOrderStatus returns an error if the order status is not valid.
func ValidateOrderStatus(status OrderStatus) error {
	switch status {
	case OrderStatusActive, OrderStatusCompleted, OrderStatusFailed:
		return nil
	case "":
		return fmt.Errorf("order status is required")
	default:
		return fmt.Errorf("unknown order status %q", status)
	}
}

// ValidateStageStatus returns an error if the stage status is not valid.
func ValidateStageStatus(status StageStatus) error {
	switch status {
	case StageStatusPending, StageStatusActive, StageStatusMerging, StageStatusCompleted, StageStatusFailed, StageStatusCancelled:
		return nil
	case "":
		return fmt.Errorf("stage status is required")
	default:
		return fmt.Errorf("unknown stage status %q", status)
	}
}

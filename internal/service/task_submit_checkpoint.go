package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"github.com/shopspring/decimal"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

const taskSubmitCheckpointVersion = 1

type TaskSubmitCheckpointState string

const (
	TaskSubmitCheckpointStateInFlight  TaskSubmitCheckpointState = "in_flight"
	TaskSubmitCheckpointStateSucceeded TaskSubmitCheckpointState = "succeeded"

	TaskSubmitOutcomeUnknownMessage = "outcome_unknown: upstream submit result was not confirmed; automatic retry disabled"
)

var ErrTaskSubmitOutcomeUnknown = errors.New(TaskSubmitOutcomeUnknownMessage)

// TaskSubmitCheckpoint fences an upstream submit and stores the normalized
// result required to resume finalization after success.
type TaskSubmitCheckpoint struct {
	Version         int                       `json:"version"`
	LeaseOwner      string                    `json:"lease_owner"`
	State           TaskSubmitCheckpointState `json:"state"`
	InteractionMode model.InteractionMode     `json:"interaction_mode,omitempty"`
	AttemptID       uint                      `json:"attempt_id,omitempty"`
	ProviderTaskID  string                    `json:"provider_task_id,omitempty"`
	URLs            []string                  `json:"urls,omitempty"`
	RevisedPrompt   string                    `json:"revised_prompt,omitempty"`
	HTTPStatus      int                       `json:"http_status,omitempty"`
	DurationMs      int64                     `json:"duration_ms,omitempty"`
	RequestMethod   string                    `json:"request_method,omitempty"`
	RequestPath     string                    `json:"request_path,omitempty"`
	RequestAt       *time.Time                `json:"request_at,omitempty"`
	FailureMessage  string                    `json:"failure_message,omitempty"`
	FinalCost       string                    `json:"final_cost,omitempty"`
}

func NewTaskSubmitInFlightCheckpoint(attemptID uint, finalCost decimal.Decimal) *TaskSubmitCheckpoint {
	return &TaskSubmitCheckpoint{
		State:     TaskSubmitCheckpointStateInFlight,
		AttemptID: attemptID,
		FinalCost: finalCost.String(),
	}
}

func (c *TaskSubmitCheckpoint) IsInFlight() bool {
	return c != nil && c.State == TaskSubmitCheckpointStateInFlight
}

func (c *TaskSubmitCheckpoint) IsSucceeded() bool {
	return c != nil && c.State == TaskSubmitCheckpointStateSucceeded
}

func (c *TaskSubmitCheckpoint) SettlementCost(fallback decimal.Decimal) (decimal.Decimal, error) {
	if c == nil || strings.TrimSpace(c.FinalCost) == "" {
		return fallback, nil
	}
	cost, err := decimal.NewFromString(c.FinalCost)
	if err != nil {
		return decimal.Zero, fmt.Errorf("parse submit checkpoint final cost: %w", err)
	}
	return cost, nil
}

func DecodeTaskSubmitCheckpoint(data datatypes.JSON) (*TaskSubmitCheckpoint, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}

	var checkpoint TaskSubmitCheckpoint
	if err := json.Unmarshal(trimmed, &checkpoint); err != nil {
		return nil, fmt.Errorf("decode task submit checkpoint: %w", err)
	}
	if checkpoint.Version != taskSubmitCheckpointVersion {
		return nil, fmt.Errorf("unsupported task submit checkpoint version %d", checkpoint.Version)
	}
	// Version 1 checkpoints written before submit fencing were only persisted
	// after a confirmed upstream success.
	if checkpoint.State == "" {
		checkpoint.State = TaskSubmitCheckpointStateSucceeded
	}
	if !checkpoint.IsInFlight() && !checkpoint.IsSucceeded() {
		return nil, fmt.Errorf("unsupported task submit checkpoint state %q", checkpoint.State)
	}
	return &checkpoint, nil
}

// ResolveInFlightTaskSubmit converts an ambiguous submit into a terminal,
// non-retryable task failure. A callback that already owns finalization wins.
func (s *TaskService) ResolveInFlightTaskSubmit(taskID uint, lease *TaskWorkerLease) (callbackOwned bool, err error) {
	if taskID == 0 || lease == nil || lease.Owner() == "" || lease.Stage() != TaskWorkerStageSubmit {
		return false, fmt.Errorf("task ID and submit lease are required")
	}
	if err := lease.Check(); err != nil {
		var task model.Task
		if loadErr := model.DB().Select("status").First(&task, taskID).Error; loadErr != nil {
			return false, errors.Join(err, loadErr)
		}
		if task.Status == model.TaskStatusFinalizing || task.Status.IsTerminal() {
			return true, nil
		}
		return false, err
	}

	var task model.Task
	if err := model.DB().Select("status").First(&task, taskID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, ErrTaskNotFound
		}
		return false, err
	}
	if task.Status == model.TaskStatusFinalizing || task.Status.IsTerminal() {
		if task.Status == model.TaskStatusFinalizing {
			if err := s.ClearTaskSubmitCheckpoint(taskID, lease.Owner()); err != nil {
				return false, err
			}
		}
		return true, nil
	}

	committed, err := s.UpdateTaskFail(taskID, TaskSubmitOutcomeUnknownMessage)
	if err != nil {
		return false, err
	}
	if committed {
		return false, ErrTaskSubmitOutcomeUnknown
	}

	if err := model.DB().Select("status").First(&task, taskID).Error; err != nil {
		return false, err
	}
	if task.Status == model.TaskStatusFinalizing || task.Status.IsTerminal() {
		return true, nil
	}
	return false, ErrTaskNotExecutable
}

func (s *TaskService) SaveTaskSubmitCheckpoint(taskID uint, leaseOwner string, checkpoint *TaskSubmitCheckpoint) error {
	if taskID == 0 || leaseOwner == "" || checkpoint == nil {
		return fmt.Errorf("task ID, lease owner and submit checkpoint are required")
	}

	stored := *checkpoint
	stored.Version = taskSubmitCheckpointVersion
	stored.LeaseOwner = leaseOwner
	if stored.State == "" {
		stored.State = TaskSubmitCheckpointStateSucceeded
	}
	if !stored.IsInFlight() && !stored.IsSucceeded() {
		return fmt.Errorf("unsupported task submit checkpoint state %q", stored.State)
	}
	encoded, err := json.Marshal(&stored)
	if err != nil {
		return fmt.Errorf("encode task submit checkpoint: %w", err)
	}

	result := model.DB().Model(&model.Task{}).
		Where("id = ? AND worker_lease_owner = ? AND worker_lease_stage = ? AND worker_lease_expires_at > ?",
			taskID, leaseOwner, TaskWorkerStageSubmit, time.Now()).
		Where("status NOT IN ?", []model.TaskStatus{
			model.TaskStatusSuccess,
			model.TaskStatusFailed,
			model.TaskStatusCancelled,
		}).
		UpdateColumn("submit_checkpoint", datatypes.JSON(encoded))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		return nil
	}

	var task model.Task
	if err := model.DB().Select("id", "status", "worker_lease_owner").First(&task, taskID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrTaskNotFound
		}
		return err
	}
	if task.Status.IsTerminal() {
		return ErrTaskNotExecutable
	}
	return ErrTaskWorkerLeaseLost
}

func (s *TaskService) ClearTaskSubmitCheckpoint(taskID uint, leaseOwner string) error {
	result := model.DB().Model(&model.Task{}).
		Where("id = ? AND worker_lease_owner = ? AND worker_lease_stage = ? AND worker_lease_expires_at > ?",
			taskID, leaseOwner, TaskWorkerStageSubmit, time.Now()).
		UpdateColumn("submit_checkpoint", nil)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		var task model.Task
		if err := model.DB().Select("status", "submit_checkpoint").First(&task, taskID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrTaskNotFound
			}
			return err
		}
		checkpoint := bytes.TrimSpace(task.SubmitCheckpoint)
		if task.Status.IsTerminal() || len(checkpoint) == 0 || bytes.Equal(checkpoint, []byte("null")) {
			return nil
		}
		return ErrTaskWorkerLeaseLost
	}
	return nil
}

func (s *TaskService) CommitTaskSubmitProcessing(taskID uint, leaseOwner, vendorTaskID string) error {
	now := time.Now()
	updates := map[string]any{
		"status":     model.TaskStatusProcessing,
		"started_at": now,
	}
	if vendorTaskID != "" {
		updates["vendor_task_id"] = vendorTaskID
	}
	query := model.DB().Model(&model.Task{}).
		Where("id = ? AND worker_lease_owner = ? AND worker_lease_stage = ? AND worker_lease_expires_at > ? AND status IN ?",
			taskID, leaseOwner, TaskWorkerStageSubmit, time.Now(),
			[]model.TaskStatus{model.TaskStatusPending, model.TaskStatusProcessing})
	if vendorTaskID != "" {
		query = query.Where("vendor_task_id = ? OR vendor_task_id = '' OR vendor_task_id IS NULL", vendorTaskID)
	}
	result := query.Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		return nil
	}
	return ErrTaskWorkerLeaseLost
}

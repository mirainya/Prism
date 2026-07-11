package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/logger"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrTaskNotFound          = errors.New("task not found")
	ErrTaskNotExecutable     = errors.New("task is no longer executable")
	ErrVendorTaskMismatch    = errors.New("vendor task ID does not match callback task")
	ErrInvalidCallbackStatus = errors.New("invalid callback status")
	billingService           = NewBillingService()
)

type CreateTaskRequest struct {
	TaskNo        string
	UserID        uint
	TokenID       uint
	ModelCode     string
	ChannelID     uint
	EndpointID    uint
	AccountID     uint
	RequestParams map[string]any
	MappedParams  map[string]any
	CallbackURL   string
	Cost          decimal.Decimal
}

type TaskService struct{}

func NewTaskService() *TaskService {
	return &TaskService{}
}

// GenerateTaskNo 生成任务编号
func GenerateTaskNo() string {
	return fmt.Sprintf("task_%d_%s", time.Now().UnixMilli(), uuid.New().String()[:8])
}

func (s *TaskService) CreateTask(req *CreateTaskRequest) (*model.Task, error) {
	task, err := s.createTask(model.DB(), req)
	if err != nil {
		return nil, err
	}
	logTaskCreated(task)
	return task, nil
}

func (s *TaskService) createTask(db *gorm.DB, req *CreateTaskRequest) (*model.Task, error) {
	requestParamsJSON, err := json.Marshal(req.RequestParams)
	if err != nil {
		return nil, fmt.Errorf("marshal request params: %w", err)
	}
	mappedParamsJSON, err := json.Marshal(req.MappedParams)
	if err != nil {
		return nil, fmt.Errorf("marshal mapped params: %w", err)
	}

	taskNo := req.TaskNo
	if taskNo == "" {
		taskNo = GenerateTaskNo()
	}
	task := &model.Task{
		TaskNo:        taskNo,
		UserID:        req.UserID,
		TokenID:       req.TokenID,
		ModelCode:     req.ModelCode,
		ChannelID:     req.ChannelID,
		EndpointID:    req.EndpointID,
		AccountID:     req.AccountID,
		RequestParams: requestParamsJSON,
		MappedParams:  mappedParamsJSON,
		Status:        model.TaskStatusPending,
		CallbackURL:   req.CallbackURL,
		Cost:          req.Cost,
	}

	if err := db.Create(task).Error; err != nil {
		return nil, err
	}
	return task, nil
}

func logTaskCreated(task *model.Task) {
	logger.Info("task created",
		zap.Uint("task_id", task.ID),
		zap.String("task_no", task.TaskNo),
		zap.String("model", task.ModelCode))
}

func (s *TaskService) GetTaskByNo(taskNo string) (*model.Task, error) {
	var task model.Task
	err := model.DB().Where("task_no = ?", taskNo).First(&task).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrTaskNotFound
	}
	if err != nil {
		return nil, err
	}
	return &task, nil
}

func (s *TaskService) GetTaskByNoAndUser(taskNo string, userID uint) (*model.Task, error) {
	var task model.Task
	err := model.DB().Where("task_no = ? AND user_id = ?", taskNo, userID).First(&task).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrTaskNotFound
	}
	if err != nil {
		return nil, err
	}
	return &task, nil
}

func (s *TaskService) GetTaskByID(id uint) (*model.Task, error) {
	var task model.Task
	err := model.DB().Where("id = ?", id).First(&task).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrTaskNotFound
	}
	if err != nil {
		return nil, err
	}
	return &task, nil
}

func (s *TaskService) GetTaskByVendorID(vendorTaskID string) (*model.Task, error) {
	var task model.Task
	err := model.DB().Where("vendor_task_id = ?", vendorTaskID).First(&task).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrTaskNotFound
	}
	if err != nil {
		return nil, err
	}
	return &task, nil
}

func (s *TaskService) UpdateTaskStatus(taskID uint, status model.TaskStatus, vendorTaskID string) error {
	updates := map[string]any{
		"status": status,
	}
	if vendorTaskID != "" {
		updates["vendor_task_id"] = vendorTaskID
	}
	if status == model.TaskStatusProcessing {
		now := time.Now()
		updates["started_at"] = now
	}

	logger.Info("task status changed",
		zap.Uint("task_id", taskID),
		zap.String("status", string(status)),
		zap.String("vendor_task_id", vendorTaskID))

	query := model.DB().Model(&model.Task{}).
		Where("id = ? AND status IN ?", taskID,
			[]model.TaskStatus{model.TaskStatusPending, model.TaskStatusProcessing})
	if vendorTaskID != "" {
		query = query.Where("vendor_task_id = ? OR vendor_task_id = '' OR vendor_task_id IS NULL", vendorTaskID)
	}
	result := query.Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 || vendorTaskID == "" {
		return nil
	}

	var task model.Task
	if err := model.DB().Select("status", "vendor_task_id").First(&task, taskID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrTaskNotFound
		}
		return err
	}
	if (task.Status == model.TaskStatusPending || task.Status == model.TaskStatusProcessing) &&
		task.VendorTaskID != "" && task.VendorTaskID != vendorTaskID {
		return ErrVendorTaskMismatch
	}
	return nil
}

func (s *TaskService) UpdateTaskProgress(taskID uint, progress int) error {
	logger.Debug("task progress updated",
		zap.Uint("task_id", taskID),
		zap.Int("progress", progress))

	return model.DB().Model(&model.Task{}).
		Where("id = ? AND status IN ?", taskID,
			[]model.TaskStatus{model.TaskStatusPending, model.TaskStatusProcessing}).
		Update("progress", progress).Error
}

// BeginTaskFinalization atomically reserves a successful upstream result for
// the upload pipeline. Returning ready=true also covers an idempotent retry.
func (s *TaskService) BeginTaskFinalization(taskID uint) (ready bool, err error) {
	result := model.DB().Model(&model.Task{}).
		Where("id = ? AND status IN ?", taskID,
			[]model.TaskStatus{model.TaskStatusPending, model.TaskStatusProcessing}).
		Updates(map[string]any{
			"status":   model.TaskStatusFinalizing,
			"progress": 100,
		})
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected > 0 {
		return true, nil
	}

	var task model.Task
	if err := model.DB().Select("status").First(&task, taskID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, ErrTaskNotFound
		}
		return false, err
	}
	return task.Status == model.TaskStatusFinalizing, nil
}

func (s *TaskService) BindVendorTaskID(taskID uint, vendorTaskID string) error {
	if vendorTaskID == "" {
		return nil
	}
	result := model.DB().Model(&model.Task{}).
		Where("id = ? AND status IN ? AND (vendor_task_id = '' OR vendor_task_id IS NULL)",
			taskID, []model.TaskStatus{
				model.TaskStatusPending,
				model.TaskStatusProcessing,
				model.TaskStatusFinalizing,
			}).
		UpdateColumn("vendor_task_id", vendorTaskID)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		return nil
	}

	var task model.Task
	if err := model.DB().Select("status", "vendor_task_id").First(&task, taskID).Error; err != nil {
		return err
	}
	if task.Status.IsTerminal() {
		return nil
	}
	if task.VendorTaskID != vendorTaskID {
		return ErrVendorTaskMismatch
	}
	return nil
}

// UpdateTaskSuccess commits success and releases the task's account slot in
// one transaction.
func (s *TaskService) UpdateTaskSuccess(taskID uint, result map[string]any, cost decimal.Decimal) (committed bool, err error) {
	return s.updateTaskSuccess(taskID, result, cost,
		[]model.TaskStatus{model.TaskStatusPending, model.TaskStatusProcessing})
}

// CompleteTaskUpload commits results produced by the upload pipeline.
func (s *TaskService) CompleteTaskUpload(taskID uint, result map[string]any, cost decimal.Decimal) (committed bool, err error) {
	return s.updateTaskSuccess(taskID, result, cost, []model.TaskStatus{model.TaskStatusFinalizing})
}

func (s *TaskService) updateTaskSuccess(
	taskID uint,
	result map[string]any,
	cost decimal.Decimal,
	allowed []model.TaskStatus,
) (committed bool, err error) {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return false, fmt.Errorf("marshal task result: %w", err)
	}
	now := time.Now()

	logger.Info("task succeeded", zap.Uint("task_id", taskID))

	err = model.DB().Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&model.Task{}).
			Where("id = ? AND status IN ?", taskID, allowed).
			Updates(map[string]any{
				"status":       model.TaskStatusSuccess,
				"progress":     100,
				"result":       resultJSON,
				"cost":         cost,
				"completed_at": now,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil
		}
		var current model.Task
		if err := tx.Select("account_id").First(&current, taskID).Error; err != nil {
			return err
		}
		if err := releaseTaskAccountSlot(tx, taskID, current.AccountID); err != nil {
			return err
		}
		committed = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return committed, nil
}

// UpdateTaskFail commits failure, refund, and account release atomically.
func (s *TaskService) UpdateTaskFail(taskID uint, errMsg string) (committed bool, err error) {
	return s.updateTaskFail(taskID, errMsg,
		[]model.TaskStatus{model.TaskStatusPending, model.TaskStatusProcessing})
}

// UpdateTaskSubmitFail only fails a task that has not been acknowledged by an
// upstream callback yet.
func (s *TaskService) UpdateTaskSubmitFail(taskID uint, errMsg string) (committed bool, err error) {
	return s.updateTaskFail(taskID, errMsg, []model.TaskStatus{model.TaskStatusPending})
}

// FailTaskUpload is the upload pipeline's terminal failure transition.
func (s *TaskService) FailTaskUpload(taskID uint, errMsg string) (committed bool, err error) {
	return s.updateTaskFail(taskID, errMsg, []model.TaskStatus{model.TaskStatusFinalizing})
}

// UpdateTaskTimeoutFail can release a task stuck in processing or finalizing.
func (s *TaskService) UpdateTaskTimeoutFail(taskID uint, errMsg string) (committed bool, err error) {
	return s.updateTaskFail(taskID, errMsg,
		[]model.TaskStatus{model.TaskStatusProcessing, model.TaskStatusFinalizing})
}

func (s *TaskService) updateTaskFail(
	taskID uint,
	errMsg string,
	allowed []model.TaskStatus,
) (committed bool, err error) {
	now := time.Now()

	logger.Warn("task failed",
		zap.Uint("task_id", taskID),
		zap.String("error", errMsg))

	var task model.Task
	if err := model.DB().First(&task, taskID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, ErrTaskNotFound
		}
		return false, err
	}

	err = model.DB().Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.Task{}).
			Where("id = ? AND status IN ?", taskID, allowed).
			Updates(map[string]any{
				"status":        model.TaskStatusFailed,
				"error_message": errMsg,
				"completed_at":  now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		if err := s.refundTask(tx, &task); err != nil {
			return fmt.Errorf("refund failed task: %w", err)
		}
		var current model.Task
		if err := tx.Select("account_id").First(&current, task.ID).Error; err != nil {
			return err
		}
		if err := releaseTaskAccountSlot(tx, task.ID, current.AccountID); err != nil {
			return err
		}
		committed = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return committed, nil
}

// refundTask runs in the same transaction as the terminal state change. A
// refund error rolls the whole transition back so the caller can retry it.
func (s *TaskService) refundTask(tx *gorm.DB, task *model.Task) error {
	if !task.Cost.GreaterThan(decimal.Zero) || task.Refunded {
		return nil
	}

	if err := billingService.refundWithKeyTx(tx, task.TokenID, task.UserID, task.Cost, task.TaskNo); err != nil {
		return err
	}
	result := tx.Model(&model.Task{}).
		Where("id = ? AND refunded = ?", task.ID, false).
		Update("refunded", true)
	if result.Error != nil {
		return result.Error
	}
	return nil
}

// ReleaseAccountSlot releases a non-terminal task's current account slot. The
// persisted gate makes retries and later terminal transitions idempotent.
func (s *TaskService) ReleaseAccountSlot(taskID uint) error {
	return model.DB().Transaction(func(tx *gorm.DB) error {
		var task model.Task
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("account_id").First(&task, taskID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrTaskNotFound
			}
			return err
		}
		return releaseTaskAccountSlot(tx, taskID, task.AccountID)
	})
}

func releaseTaskAccountSlot(tx *gorm.DB, taskID, accountID uint) error {
	gate := tx.Model(&model.Task{}).
		Where("id = ? AND account_slot_released = ?", taskID, false).
		UpdateColumn("account_slot_released", true)
	if gate.Error != nil {
		return gate.Error
	}
	if gate.RowsAffected == 0 || accountID == 0 {
		return nil
	}
	return tx.Model(&model.ChannelAccount{}).
		Where("id = ? AND current_tasks > 0", accountID).
		UpdateColumn("current_tasks", gorm.Expr("current_tasks - 1")).Error
}

func (s *TaskService) UpdateVendorResponse(taskID uint, resp json.RawMessage) error {
	return model.DB().Model(&model.Task{}).Where("id = ?", taskID).
		Update("vendor_response", resp).Error
}

func (s *TaskService) CancelTask(taskNo string, userID uint) error {
	return model.DB().Transaction(func(tx *gorm.DB) error {
		var task model.Task
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("task_no = ? AND user_id = ?", taskNo, userID).First(&task).Error; err != nil {
			return errors.New("task not found or cannot be cancelled")
		}

		result := tx.Model(&model.Task{}).
			Where("id = ? AND status IN ?", task.ID,
				[]model.TaskStatus{model.TaskStatusPending, model.TaskStatusProcessing, model.TaskStatusFinalizing}).
			Update("status", model.TaskStatusCancelled)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errors.New("task not found or cannot be cancelled")
		}
		if err := s.refundTask(tx, &task); err != nil {
			return fmt.Errorf("refund cancelled task: %w", err)
		}
		return releaseTaskAccountSlot(tx, task.ID, task.AccountID)
	})
}

func (s *TaskService) UpdateCallbackStatus(taskID uint, status model.CallbackStatus, attempts int) error {
	return model.DB().Model(&model.Task{}).Where("id = ?", taskID).Updates(map[string]any{
		"callback_status":   status,
		"callback_attempts": attempts,
	}).Error
}

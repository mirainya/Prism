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
	"gorm.io/datatypes"
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
	AdapterID         uint
	AdapterRevisionID uint
	EndpointSnapshot  datatypes.JSON
	TaskNo            string
	CallID            string
	UserID            uint
	TokenID           uint
	ModelCode         string
	RouteOperation    string
	ChannelID         uint
	EndpointID        uint
	AccountID         uint
	RequestParams     map[string]any
	MappedParams      map[string]any
	CallbackURL       string
	Cost              decimal.Decimal
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
	callbackStatus := model.CallbackStatus("")
	if req.CallbackURL != "" {
		callbackStatus = model.CallbackStatusPending
	}
	task := &model.Task{
		TaskNo:            taskNo,
		AdapterID:         req.AdapterID,
		AdapterRevisionID: req.AdapterRevisionID,
		EndpointSnapshot:  req.EndpointSnapshot,
		CallID:            req.CallID,
		UserID:            req.UserID,
		TokenID:           req.TokenID,
		ModelCode:         req.ModelCode,
		RouteOperation:    req.RouteOperation,
		ChannelID:         req.ChannelID,
		EndpointID:        req.EndpointID,
		AccountID:         req.AccountID,
		RequestParams:     requestParamsJSON,
		MappedParams:      mappedParamsJSON,
		Status:            model.TaskStatusPending,
		CallbackURL:       req.CallbackURL,
		CallbackStatus:    callbackStatus,
		Cost:              req.Cost,
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

func (s *TaskService) GetTaskByNoUserAndToken(taskNo string, userID, tokenID uint) (*model.Task, error) {
	var task model.Task
	err := model.DB().Where(
		"task_no = ? AND user_id = ? AND token_id = ?",
		taskNo, userID, tokenID,
	).First(&task).Error
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
		updates["started_at"] = gorm.Expr("COALESCE(started_at, ?)", now)
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
	progress = normalizeTaskProgress(progress)
	logger.Debug("task progress updated",
		zap.Uint("task_id", taskID),
		zap.Int("progress", progress))

	return model.DB().Model(&model.Task{}).
		Where("id = ? AND status IN ?", taskID,
			[]model.TaskStatus{model.TaskStatusPending, model.TaskStatusProcessing}).
		Where("COALESCE(progress, 0) < ?", progress).
		Update("progress", progress).Error
}

func normalizeTaskProgress(progress int) int {
	if progress < 0 {
		return 0
	}
	if progress > 100 {
		return 100
	}
	return progress
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
	// 终态、实际结算、账号并发释放和 Call 完成在同一事务中提交。
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return false, fmt.Errorf("marshal task result: %w", err)
	}
	now := time.Now()

	logger.Info("task succeeded", zap.Uint("task_id", taskID))

	err = model.DB().Transaction(func(tx *gorm.DB) error {
		var current model.Task
		if err := tx.Select("id", "task_no", "call_id", "user_id", "token_id", "model_code", "endpoint_id", "account_id", "cost").
			First(&current, taskID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrTaskNotFound
			}
			return err
		}
		updates := map[string]any{
			"status":                  model.TaskStatusSuccess,
			"progress":                100,
			"result":                  resultJSON,
			"cost":                    cost,
			"completed_at":            now,
			"submit_checkpoint":       nil,
			"worker_lease_owner":      "",
			"worker_lease_stage":      "",
			"worker_lease_expires_at": nil,
		}
		res := tx.Model(&model.Task{}).
			Where("id = ? AND status IN ?", taskID, allowed).
			Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil
		}
		if current.CallID != "" {
			attemptID, err := latestCallAttemptIDTx(tx, current.CallID)
			if err != nil {
				return fmt.Errorf("load final task attempt: %w", err)
			}
			if err := billingService.settleReservationWithBillingContextTx(
				tx,
				current.TokenID,
				current.UserID,
				current.Cost,
				cost,
				current.TaskNo+":settle",
				BillingContext{
					CallID: current.CallID, AttemptID: attemptID, Phase: model.BillingPhaseSettle,
					PricingSnapshot: taskPricingSnapshotTx(tx, &current, cost),
				},
			); err != nil {
				return fmt.Errorf("settle successful task: %w", err)
			}
			if err := NewAPICallService().CompleteCallTx(tx, current.CallID, &CompleteCallRequest{
				FinalAttemptID: attemptID,
				HTTPStatus:     200,
			}); err != nil {
				return fmt.Errorf("complete task call: %w", err)
			}
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
		[]model.TaskStatus{model.TaskStatusPending, model.TaskStatusProcessing, model.TaskStatusFinalizing})
}

func (s *TaskService) updateTaskFail(
	taskID uint,
	errMsg string,
	allowed []model.TaskStatus,
) (committed bool, err error) {
	// 所有失败入口汇入此处，保证退款、Attempt/Call 终态和账号释放只执行一次。
	now := time.Now()
	errMsg = SanitizeAPICallErrorMessage(errMsg)

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
		updates := map[string]any{
			"status":                  model.TaskStatusFailed,
			"error_message":           errMsg,
			"completed_at":            now,
			"submit_checkpoint":       nil,
			"worker_lease_owner":      "",
			"worker_lease_stage":      "",
			"worker_lease_expires_at": nil,
		}
		result := tx.Model(&model.Task{}).
			Where("id = ? AND status IN ?", taskID, allowed).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		attemptID, err := terminalizeLatestTaskAttemptTx(
			tx, task.CallID, model.APICallAttemptStatusFailed,
			500, "capability_error", "task_failed", errMsg,
		)
		if err != nil {
			return fmt.Errorf("load final task attempt: %w", err)
		}
		if err := s.refundTask(tx, &task, attemptID); err != nil {
			return fmt.Errorf("refund failed task: %w", err)
		}
		if task.CallID != "" {
			if err := NewAPICallService().FailCallTx(tx, task.CallID, &FailCallRequest{
				FinalAttemptID: attemptID,
				HTTPStatus:     500,
				ErrorType:      "capability_error",
				ErrorCode:      "task_failed",
				ErrorMessage:   errMsg,
			}); err != nil {
				return fmt.Errorf("fail task call: %w", err)
			}
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
func (s *TaskService) refundTask(tx *gorm.DB, task *model.Task, attemptID uint) error {
	if !task.Cost.GreaterThan(decimal.Zero) || task.Refunded {
		return nil
	}

	if err := billingService.refundWithBillingContextTx(
		tx,
		task.TokenID,
		task.UserID,
		task.Cost,
		task.TaskNo,
		BillingContext{
			CallID: task.CallID, AttemptID: attemptID, Phase: model.BillingPhaseRefund,
			PricingSnapshot: taskPricingSnapshotTx(tx, task, task.Cost),
		},
	); err != nil {
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
	return tx.Unscoped().Model(&model.ChannelAccount{}).
		Where("id = ? AND current_tasks > 0", accountID).
		UpdateColumn("current_tasks", gorm.Expr("current_tasks - 1")).Error
}

func (s *TaskService) UpdateVendorResponse(taskID uint, resp json.RawMessage) error {
	return model.DB().Model(&model.Task{}).Where("id = ?", taskID).
		Update("vendor_response", resp).Error
}

func (s *TaskService) CancelTask(taskNo string, userID uint) error {
	return s.cancelTask(taskNo, userID, nil)
}

func (s *TaskService) CancelTaskByToken(taskNo string, userID, tokenID uint) error {
	return s.cancelTask(taskNo, userID, &tokenID)
}

func (s *TaskService) cancelTask(taskNo string, userID uint, tokenID *uint) error {
	// 条件状态更新提供幂等取消；已经进入终态的任务不会再次退款。
	return model.DB().Transaction(func(tx *gorm.DB) error {
		var task model.Task
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("task_no = ? AND user_id = ?", taskNo, userID)
		if tokenID != nil {
			query = query.Where("token_id = ?", *tokenID)
		}
		if err := query.First(&task).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("task not found or cannot be cancelled")
			}
			return fmt.Errorf("load task for cancellation: %w", err)
		}

		updates := map[string]any{
			"status":                  model.TaskStatusCancelled,
			"completed_at":            time.Now(),
			"submit_checkpoint":       nil,
			"worker_lease_owner":      "",
			"worker_lease_stage":      "",
			"worker_lease_expires_at": nil,
		}
		result := tx.Model(&model.Task{}).
			Where("id = ? AND status IN ?", task.ID,
				[]model.TaskStatus{model.TaskStatusPending, model.TaskStatusProcessing, model.TaskStatusFinalizing}).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errors.New("task not found or cannot be cancelled")
		}
		attemptID, err := terminalizeLatestTaskAttemptTx(
			tx, task.CallID, model.APICallAttemptStatusCancelled,
			499, "cancelled_error", "task_cancelled", "task cancelled",
		)
		if err != nil {
			return fmt.Errorf("load final task attempt: %w", err)
		}
		if err := s.refundTask(tx, &task, attemptID); err != nil {
			return fmt.Errorf("refund cancelled task: %w", err)
		}
		if task.CallID != "" {
			if err := NewAPICallService().CancelCallTx(tx, task.CallID, &CancelCallRequest{
				FinalAttemptID: attemptID,
				ErrorType:      "cancelled_error",
				ErrorCode:      "task_cancelled",
				ErrorMessage:   "task cancelled",
			}); err != nil {
				return fmt.Errorf("cancel task call: %w", err)
			}
		}
		return releaseTaskAccountSlot(tx, task.ID, task.AccountID)
	})
}

func latestCallAttemptIDTx(tx *gorm.DB, callID string) (uint, error) {
	if callID == "" {
		return 0, nil
	}
	var attempt model.APICallAttempt
	err := tx.Select("id").Where("call_id = ?", callID).Order("attempt_no DESC").First(&attempt).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	return attempt.ID, err
}

func terminalizeLatestTaskAttemptTx(
	tx *gorm.DB,
	callID string,
	status model.APICallAttemptStatus,
	httpStatus int,
	errorType string,
	errorCode string,
	errorMessage string,
) (uint, error) {
	if callID == "" {
		return 0, nil
	}
	var attempt model.APICallAttempt
	err := tx.Where("call_id = ?", callID).Order("attempt_no DESC").First(&attempt).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if attempt.Status != model.APICallAttemptStatusStarted {
		return attempt.ID, nil
	}
	now := time.Now()
	result := tx.Model(&model.APICallAttempt{}).
		Where("id = ? AND status = ?", attempt.ID, model.APICallAttemptStatusStarted).
		Updates(map[string]any{
			"status":          status,
			"http_status":     httpStatus,
			"error_type":      errorType,
			"error_code":      errorCode,
			"error_message":   SanitizeAPICallErrorMessage(errorMessage),
			"error_retryable": false,
			"completed_at":    now,
			"duration_ms":     elapsedMilliseconds(attempt.StartedAt, now),
		})
	if result.Error != nil {
		return 0, result.Error
	}
	return attempt.ID, nil
}

func taskPricingSnapshotTx(tx *gorm.DB, task *model.Task, cost decimal.Decimal) datatypes.JSON {
	if task == nil || task.EndpointID == 0 {
		return nil
	}
	var endpoint model.Endpoint
	if err := tx.First(&endpoint, task.EndpointID).Error; err != nil {
		return nil
	}
	if err := ApplyTaskEndpointSnapshot(task, &endpoint); err != nil {
		return nil
	}
	return capabilityPricingSnapshot(&InvokeRequest{
		Capability:     task.ModelCode,
		Model:          task.ModelCode,
		RouteOperation: task.RouteOperation,
	}, &endpoint, cost)
}

func (s *TaskService) UpdateCallbackStatus(taskID uint, status model.CallbackStatus, attempts int) error {
	return model.DB().Model(&model.Task{}).Where("id = ?", taskID).Updates(map[string]any{
		"callback_status":   status,
		"callback_attempts": attempts,
	}).Error
}

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
)

var (
	ErrTaskNotFound = errors.New("task not found")
	billingService  = NewBillingService()
)

type CreateTaskRequest struct {
	UserID      uint
	TokenID     uint
	ModelCode   string
	ChannelID   uint
	EndpointID  uint
	AccountID   uint
	RequestParams map[string]any
	MappedParams  map[string]any
	CallbackURL   string
	Cost                decimal.Decimal
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
	requestParamsJSON, err := json.Marshal(req.RequestParams)
	if err != nil {
		return nil, fmt.Errorf("marshal request params: %w", err)
	}
	mappedParamsJSON, err := json.Marshal(req.MappedParams)
	if err != nil {
		return nil, fmt.Errorf("marshal mapped params: %w", err)
	}

	task := &model.Task{
		TaskNo:         GenerateTaskNo(),
		UserID:         req.UserID,
		TokenID:        req.TokenID,
		ModelCode:      req.ModelCode,
		ChannelID:      req.ChannelID,
		EndpointID:     req.EndpointID,
		AccountID:      req.AccountID,
		RequestParams:  requestParamsJSON,
		MappedParams:   mappedParamsJSON,
		Status:         model.TaskStatusPending,
		CallbackURL:    req.CallbackURL,
		Cost:           req.Cost,
	}

	if err := model.DB().Create(task).Error; err != nil {
		return nil, err
	}

	logger.Info("task created",
		zap.Uint("task_id", task.ID),
		zap.String("task_no", task.TaskNo),
		zap.String("model", task.ModelCode))

	return task, nil
}

func (s *TaskService) GetTaskByNo(taskNo string) (*model.Task, error) {
	var task model.Task
	err := model.DB().Where("task_no = ?", taskNo).First(&task).Error
	if err != nil {
		return nil, ErrTaskNotFound
	}
	return &task, nil
}

func (s *TaskService) GetTaskByNoAndUser(taskNo string, userID uint) (*model.Task, error) {
	var task model.Task
	err := model.DB().Where("task_no = ? AND user_id = ?", taskNo, userID).First(&task).Error
	if err != nil {
		return nil, ErrTaskNotFound
	}
	return &task, nil
}

func (s *TaskService) GetTaskByID(id uint) (*model.Task, error) {
	var task model.Task
	err := model.DB().Where("id = ?", id).First(&task).Error
	if err != nil {
		return nil, ErrTaskNotFound
	}
	return &task, nil
}

func (s *TaskService) GetTaskByVendorID(vendorTaskID string) (*model.Task, error) {
	var task model.Task
	err := model.DB().Where("vendor_task_id = ?", vendorTaskID).First(&task).Error
	if err != nil {
		return nil, ErrTaskNotFound
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

	return model.DB().Model(&model.Task{}).Where("id = ?", taskID).Updates(updates).Error
}

func (s *TaskService) UpdateTaskProgress(taskID uint, progress int) error {
	logger.Debug("task progress updated",
		zap.Uint("task_id", taskID),
		zap.Int("progress", progress))

	return model.DB().Model(&model.Task{}).Where("id = ?", taskID).
		Update("progress", progress).Error
}

// UpdateTaskSuccess 将任务置为成功。
// 返回 committed=true 表示本次调用真正抢到了终态流转(RowsAffected>0),
// 调用方应据此决定是否递减账号计数,避免多路径并发对同一任务重复递减。
func (s *TaskService) UpdateTaskSuccess(taskID uint, result map[string]any, cost decimal.Decimal) (committed bool, err error) {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return false, fmt.Errorf("marshal task result: %w", err)
	}
	now := time.Now()

	logger.Info("task succeeded", zap.Uint("task_id", taskID))

	// 终态守卫: 仅当任务尚未进入终态时才置成功,避免覆盖已被 timeout 判失败/用户取消的任务
	res := model.DB().Model(&model.Task{}).
		Where("id = ? AND status NOT IN ?", taskID,
			[]model.TaskStatus{model.TaskStatusSuccess, model.TaskStatusFailed, model.TaskStatusCancelled}).
		Updates(map[string]any{
			"status":       model.TaskStatusSuccess,
			"progress":     100,
			"result":       resultJSON,
			"cost":         cost,
			"completed_at": now,
		})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// UpdateTaskFail 将任务置为失败并(在真正抢到终态流转时)退款。
// 返回 committed=true 表示本次调用真正抢到了终态流转,调用方应据此决定是否递减账号计数。
func (s *TaskService) UpdateTaskFail(taskID uint, errMsg string) (committed bool, err error) {
	now := time.Now()

	logger.Warn("task failed",
		zap.Uint("task_id", taskID),
		zap.String("error", errMsg))

	var task model.Task
	if err := model.DB().First(&task, taskID).Error; err != nil {
		return false, ErrTaskNotFound
	}

	// 终态守卫: 仅当任务尚未进入终态时才置为 failed,避免与 timeout/upload 等并发路径竞态覆盖
	result := model.DB().Model(&model.Task{}).
		Where("id = ? AND status NOT IN ?", taskID,
			[]model.TaskStatus{model.TaskStatusSuccess, model.TaskStatusFailed, model.TaskStatusCancelled}).
		Updates(map[string]any{
			"status":        model.TaskStatusFailed,
			"error_message": errMsg,
			"completed_at":  now,
		})
	if result.Error != nil {
		return false, result.Error
	}
	// 未抢到流转(已是终态或已被其他路径处理)则不退款、不递减,避免重复
	if result.RowsAffected == 0 {
		return false, nil
	}

	s.refundTask(&task)
	return true, nil
}

// refundTask 退款并保证幂等:
//   - 用 "id=? AND refunded=false" 原子抢占退款闸门,只有抢到的调用方才执行退款
//   - 退款走 RefundWithKey(task_no 作幂等键),即便闸门失效也不会重复加钱
//   - 退款失败则回滚 refunded 标记,让后续路径/对账可重试
func (s *TaskService) refundTask(task *model.Task) {
	if !task.Cost.GreaterThan(decimal.Zero) {
		return
	}

	// 原子抢占退款闸门
	gate := model.DB().Model(&model.Task{}).
		Where("id = ? AND refunded = ?", task.ID, false).
		Update("refunded", true)
	if gate.Error != nil || gate.RowsAffected == 0 {
		return // 未抢到,已有其他路径负责退款
	}

	if err := billingService.RefundWithKey(task.TokenID, task.UserID, task.Cost, task.TaskNo); err != nil {
		logger.Error("refund failed, rolling back gate",
			zap.Uint("task_id", task.ID),
			zap.Uint("token_id", task.TokenID),
			zap.Uint("user_id", task.UserID),
			zap.String("cost", task.Cost.String()),
			zap.Error(err))
		// 回滚闸门,使退款可被后续重试
		model.DB().Model(&model.Task{}).Where("id = ?", task.ID).Update("refunded", false)
	}
}

func (s *TaskService) UpdateVendorResponse(taskID uint, resp json.RawMessage) error {
	return model.DB().Model(&model.Task{}).Where("id = ?", taskID).
		Update("vendor_response", resp).Error
}

func (s *TaskService) CancelTask(taskNo string, userID uint) error {
	// 先读出任务(用于取消后退款与计数递减)
	var task model.Task
	if err := model.DB().Where("task_no = ? AND user_id = ?", taskNo, userID).First(&task).Error; err != nil {
		return errors.New("task not found or cannot be cancelled")
	}

	// 原子抢占: 仅 pending/processing 可被取消
	result := model.DB().Model(&model.Task{}).
		Where("id = ? AND status IN ?", task.ID,
			[]model.TaskStatus{model.TaskStatusPending, model.TaskStatusProcessing}).
		Update("status", model.TaskStatusCancelled)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("task not found or cannot be cancelled")
	}

	// 抢到取消流转: 退款(幂等) + 递减账号计数
	s.refundTask(&task)
	decrementAccountTasksByID(task.AccountID)
	return nil
}

// decrementAccountTasksByID 递减账号并发计数(与 UnifiedService.decrementAccountTasks 同逻辑)
func decrementAccountTasksByID(accountID uint) {
	model.DB().Model(&model.ChannelAccount{}).
		Where("id = ? AND current_tasks > 0", accountID).
		UpdateColumn("current_tasks", gorm.Expr("current_tasks - 1"))
}

func (s *TaskService) UpdateCallbackStatus(taskID uint, status model.CallbackStatus, attempts int) error {
	return model.DB().Model(&model.Task{}).Where("id = ?", taskID).Updates(map[string]any{
		"callback_status":   status,
		"callback_attempts": attempts,
	}).Error
}

package worker

import (
	"context"
	"time"

	"github.com/hibiken/asynq"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/logger"
	"go.uber.org/zap"
)

// HandleTaskTimeoutCheck 检查超时任务
func HandleTaskTimeoutCheck(ctx context.Context, t *asynq.Task) error {
	logger.Info("checking timeout tasks")
	now := time.Now()
	var recoverable []model.Task
	if err := model.DB().Where("status IN ? AND submit_checkpoint IS NOT NULL",
		[]model.TaskStatus{model.TaskStatusPending, model.TaskStatusProcessing, model.TaskStatusFinalizing}).
		Where("worker_lease_owner = '' OR worker_lease_owner IS NULL OR worker_lease_expires_at IS NULL OR worker_lease_expires_at <= ?", now).
		Find(&recoverable).Error; err != nil {
		return err
	}
	for _, task := range recoverable {
		if err := enqueueTaskSubmit(task.ID); err != nil {
			logger.Error("enqueue submit checkpoint recovery failed", zap.Uint("task_id", task.ID), zap.Error(err))
		}
	}

	// 查找 30 分钟前提交但仍在处理中的任务
	timeout := now.Add(-30 * time.Minute)

	var tasks []model.Task
	err := model.DB().Where("status IN ? AND updated_at < ? AND submit_checkpoint IS NULL",
		[]model.TaskStatus{model.TaskStatusPending, model.TaskStatusProcessing, model.TaskStatusFinalizing},
		timeout,
	).Find(&tasks).Error

	if err != nil {
		logger.Error("query timeout tasks error", zap.Error(err))
		return nil
	}

	for _, task := range tasks {
		logger.Warn("task timeout", zap.Uint("task_id", task.ID), zap.String("task_no", task.TaskNo))
		if _, err := taskService.UpdateTaskTimeoutFail(task.ID, "task timeout"); err != nil {
			return err
		}
	}

	logger.Info("timeout check completed", zap.Int("count", len(tasks)))

	// 顺带清理已到期的账号熔断记录,避免表膨胀
	if n, err := circuitService.CleanExpired(); err != nil {
		logger.Error("clean expired circuit states error", zap.Error(err))
	} else if n > 0 {
		logger.Info("cleaned expired circuit states", zap.Int64("count", n))
	}

	return nil
}

func NewTimeoutCheckTask() *asynq.Task {
	return asynq.NewTask(TypeTaskTimeoutCheck, nil)
}

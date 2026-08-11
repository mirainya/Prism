package worker

import (
	"context"
	"time"

	"github.com/hibiken/asynq"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/video"
	"github.com/mirainya/Prism/pkg/logger"
	"go.uber.org/zap"
)

// HandleTaskTimeoutCheck 检查超时任务
func HandleTaskTimeoutCheck(ctx context.Context, t *asynq.Task) error {
	logger.Info("checking timeout tasks")
	now := time.Now()
	recovered, err := RecoverPendingTaskSubmissions(ctx)
	if err != nil {
		return err
	}
	if recovered > 0 {
		logger.Info("task submit intents recovered", zap.Int("count", recovered))
	}
	recoveredVideos, err := RecoverPendingVideoSubmissions(ctx)
	if err != nil {
		return err
	}
	if recoveredVideos > 0 {
		logger.Info("video submit intents recovered", zap.Int("count", recoveredVideos))
	}
	expiredAssets, err := video.NewAssetService(model.DB()).ExpireReady(ctx)
	if err != nil {
		logger.Error("expire video assets failed", zap.Error(err))
	}
	if expiredAssets > 0 {
		logger.Info("video assets expired", zap.Int64("count", expiredAssets))
	}

	// 查找 30 分钟前提交但仍在处理中的任务
	timeout := now.Add(-30 * time.Minute)

	var tasks []model.Task
	err = model.DB().Where("status IN ? AND updated_at < ? AND submit_checkpoint IS NULL",
		[]model.TaskStatus{model.TaskStatusProcessing, model.TaskStatusFinalizing},
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

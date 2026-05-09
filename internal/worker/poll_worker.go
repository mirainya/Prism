package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider"
	"github.com/mirainya/Prism/pkg/logger"
	"github.com/mirainya/Prism/pkg/queue"
	"go.uber.org/zap"
)

const DefaultMaxPollCount = 360 // 默认最大轮询次数（兜底）
const DefaultPollInterval = 5   // 默认轮询间隔（秒）

func HandleTaskPoll(ctx context.Context, t *asynq.Task) error {
	var payload TaskPollPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("unmarshal payload: %w", err)
	}

	logger.Info("processing poll task", zap.Uint("task_id", payload.TaskID), zap.Int("poll_count", payload.PollCount))

	// 1. 获取任务
	task, err := taskService.GetTaskByID(payload.TaskID)
	if err != nil {
		return fmt.Errorf("get task: %w", err)
	}

	// 任务已完成，不再轮询
	if task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailed {
		return nil
	}

	// 2. 获取渠道能力配置（读取轮询参数）
	var channelCapability model.ChannelCapability
	model.DB().First(&channelCapability, task.ChannelCapabilityID)

	maxPollCount := channelCapability.PollMaxAttempts
	if maxPollCount <= 0 {
		maxPollCount = DefaultMaxPollCount
	}

	pollInterval := channelCapability.PollInterval
	if pollInterval <= 0 {
		pollInterval = DefaultPollInterval
	}

	// 超时保护
	if payload.PollCount >= maxPollCount {
		taskService.UpdateTaskFail(payload.TaskID, "poll timeout")
		decrementAccountTasks(payload.TaskID)
		return nil
	}

	// 3. 获取渠道信息
	var channel model.Channel
	model.DB().First(&channel, task.ChannelID)

	var account model.ChannelAccount
	model.DB().First(&account, task.AccountID)

	// 4. 创建 Provider 并查询进度
	prov, err := provider.NewProvider(&channel, &account, &channelCapability)
	if err != nil {
		return fmt.Errorf("create provider: %w", err)
	}

	result, err := prov.GetProgress(ctx, task.VendorTaskID)
	if err != nil {
		logger.Error("get progress error", zap.Error(err))
		// 继续轮询
		return requeuePoll(payload.TaskID, payload.PollCount+1, pollInterval)
	}

	logger.Info("poll result", zap.String("status", string(result.Status)), zap.Int("progress", result.Progress))

	// 5. 处理结果
	switch result.Status {
	case provider.StatusSuccess:
		// 更新进度
		taskService.UpdateTaskProgress(task.ID, 100)
		// 入队上传任务
		originURL := ""
		if len(result.URLs) > 0 {
			originURL = result.URLs[0]
		}
		return enqueueUpload(task.ID, originURL, result.URLs)

	case provider.StatusFail:
		taskService.UpdateTaskFail(task.ID, result.Error)
		decrementAccountTasks(task.ID)
		return nil

	case provider.StatusProcessing, provider.StatusSubmitted, provider.StatusPending:
		// 更新进度
		taskService.UpdateTaskProgress(task.ID, result.Progress)
		// 继续轮询
		return requeuePoll(payload.TaskID, payload.PollCount+1, pollInterval)
	}

	return nil
}

func requeuePoll(taskID uint, pollCount int, intervalSeconds int) error {
	payload := TaskPollPayload{
		TaskID:    taskID,
		PollCount: pollCount,
	}
	payloadBytes, _ := json.Marshal(payload)
	task := asynq.NewTask(TypeTaskPoll, payloadBytes)
	_, err := queue.Client.Enqueue(task, asynq.ProcessIn(time.Duration(intervalSeconds)*time.Second), asynq.Queue("default"))
	return err
}

func enqueueUpload(taskID uint, originURL string, urls []string) error {
	payload := TaskUploadPayload{
		TaskID:    taskID,
		OriginURL: originURL,
		URLs:      urls,
	}
	payloadBytes, _ := json.Marshal(payload)
	task := asynq.NewTask(TypeTaskUpload, payloadBytes)
	_, err := queue.Client.Enqueue(task, asynq.Queue("default"))
	return err
}

func decrementAccountTasks(taskID uint) {
	task, err := taskService.GetTaskByID(taskID)
	if err == nil {
		strategyService.DecrementAccountTasks(task.AccountID)
	}
}

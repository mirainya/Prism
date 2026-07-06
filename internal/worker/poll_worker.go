package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/hibiken/asynq"
	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider"
	"github.com/mirainya/Prism/pkg/logger"
	"github.com/mirainya/Prism/pkg/queue"
	"go.uber.org/zap"
	"gorm.io/gorm"
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

	// 任务已进入终态(成功/失败/取消),不再轮询
	if task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailed || task.Status == model.TaskStatusCancelled {
		return nil
	}

	// 2. 获取端点配置（读取轮询参数）
	var endpoint model.Endpoint
	if err := model.DB().First(&endpoint, task.EndpointID).Error; err != nil {
		// 端点配置缺失(被删/DB异常)则无法正确轮询,直接判失败,避免用零值配置空转
		logger.Error("poll: load endpoint failed", zap.Uint("task_id", task.ID), zap.Uint("endpoint_id", task.EndpointID), zap.Error(err))
		if committed, _ := taskService.UpdateTaskFail(task.ID, "poll: endpoint config not found"); committed {
			decrementAccountTasks(task.ID)
		}
		return nil
	}

	maxPollCount := endpoint.PollMaxAttempts
	if maxPollCount <= 0 {
		maxPollCount = DefaultMaxPollCount
	}

	pollInterval := endpoint.PollInterval
	if pollInterval <= 0 {
		pollInterval = DefaultPollInterval
	}

	// 超时保护
	if payload.PollCount >= maxPollCount {
		if committed, _ := taskService.UpdateTaskFail(payload.TaskID, "poll timeout"); committed {
			decrementAccountTasks(payload.TaskID)
		}
		return nil
	}

	// 3. 获取渠道信息
	var channel model.Channel
	if err := model.DB().First(&channel, task.ChannelID).Error; err != nil {
		logger.Error("poll: load channel failed", zap.Uint("task_id", task.ID), zap.Uint("channel_id", task.ChannelID), zap.Error(err))
		if committed, _ := taskService.UpdateTaskFail(task.ID, "poll: channel not found"); committed {
			decrementAccountTasks(task.ID)
		}
		return nil
	}

	var account model.ChannelAccount
	if err := model.DB().First(&account, task.AccountID).Error; err != nil {
		logger.Error("poll: load account failed", zap.Uint("task_id", task.ID), zap.Uint("account_id", task.AccountID), zap.Error(err))
		if committed, _ := taskService.UpdateTaskFail(task.ID, "poll: account not found"); committed {
			decrementAccountTasks(task.ID)
		}
		return nil
	}

	// 4. 创建 Provider 并查询进度
	prov, err := provider.NewProvider(&channel, &account, &endpoint)
	if err != nil {
		return fmt.Errorf("create provider: %w", err)
	}

	result, err := prov.GetProgress(ctx, task.VendorTaskID)
	if err != nil {
		// 错误分级: 可恢复错误(网络抖动/408/429/5xx)继续轮询; 不可恢复(4xx 硬错误)快速失败
		if isRetryablePollError(err) {
			logger.Warn("get progress transient error, retry poll", zap.Uint("task_id", payload.TaskID), zap.Error(err))
			return requeuePoll(payload.TaskID, payload.PollCount+1, pollInterval)
		}
		logger.Error("get progress fatal error, fail task", zap.Uint("task_id", payload.TaskID), zap.Error(err))
		if committed, _ := taskService.UpdateTaskFail(payload.TaskID, "poll error: "+err.Error()); committed {
			decrementAccountTasks(payload.TaskID)
		}
		return nil
	}

	logger.Info("poll result", zap.String("status", string(result.Status)), zap.Int("progress", result.Progress))

	// 5. 处理结果
	switch result.Status {
	case provider.StatusSuccess:
		// 更新进度
		taskService.UpdateTaskProgress(task.ID, 100)
		// 入队上传任务
		originURLs := append([]string{}, result.URLs...)
		originURLs = append(originURLs, result.B64Data...)
		originURL := ""
		if len(originURLs) > 0 {
			originURL = originURLs[0]
		}
		return enqueueUpload(task.ID, originURL, originURLs, result.RevisedPrompt)

	case provider.StatusFail:
		if committed, _ := taskService.UpdateTaskFail(task.ID, result.Error); committed {
			decrementAccountTasks(task.ID)
		}
		return nil

	case provider.StatusProcessing, provider.StatusSubmitted, provider.StatusPending:
		// 更新进度
		taskService.UpdateTaskProgress(task.ID, result.Progress)
		// 继续轮询
		return requeuePoll(payload.TaskID, payload.PollCount+1, pollInterval)

	default:
		// 未知状态，继续轮询
		return requeuePoll(payload.TaskID, payload.PollCount+1, pollInterval)
	}
}

// isRetryablePollError 判断轮询错误是否可恢复(应继续轮询)
// 可恢复: 网络抖动/超时(无状态码)、408 请求超时、429 限流、5xx 上游故障
// 不可恢复: 400/401/403/404/422 等 4xx 硬错误(配置/鉴权/任务不存在),快速失败避免空转到 maxPollCount
func isRetryablePollError(err error) bool {
	code := domain.UpstreamStatusCode(err)
	if code == 0 {
		// 无状态码 → 网络层错误(连接失败/超时/读取中断),视为瞬时可恢复
		return true
	}
	if code == http.StatusRequestTimeout || code == http.StatusTooManyRequests {
		return true
	}
	return code >= 500
}

func requeuePoll(taskID uint, pollCount int, intervalSeconds int) error {
	payload := TaskPollPayload{
		TaskID:    taskID,
		PollCount: pollCount,
	}
	payloadBytes, _ := json.Marshal(payload)
	task := asynq.NewTask(TypeTaskPoll, payloadBytes)
	info, err := queue.Client.Enqueue(task, asynq.ProcessIn(time.Duration(intervalSeconds)*time.Second), asynq.Queue("default"))
	if err != nil {
		logger.Error("requeue poll failed", zap.Uint("task_id", taskID), zap.Error(err))
	} else {
		logger.Info("requeue poll ok", zap.Uint("task_id", taskID), zap.Int("poll_count", pollCount), zap.String("queue", info.Queue))
	}
	return err
}

func enqueueUpload(taskID uint, originURL string, urls []string, revisedPrompt ...string) error {
	payload := TaskUploadPayload{
		TaskID:    taskID,
		OriginURL: originURL,
		URLs:      urls,
	}
	if len(revisedPrompt) > 0 {
		payload.RevisedPrompt = revisedPrompt[0]
	}
	payloadBytes, _ := json.Marshal(payload)
	task := asynq.NewTask(TypeTaskUpload, payloadBytes)
	_, err := queue.Client.Enqueue(task, asynq.Queue("default"))
	return err
}

func decrementAccountTasks(taskID uint) {
	task, err := taskService.GetTaskByID(taskID)
	if err == nil {
		model.DB().Model(&model.ChannelAccount{}).
			Where("id = ? AND current_tasks > 0", task.AccountID).
			UpdateColumn("current_tasks", gorm.Expr("current_tasks - 1"))
	}
}

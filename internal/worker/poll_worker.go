package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/hibiken/asynq"
	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider"
	"github.com/mirainya/Prism/internal/service"
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
	lease, acquired, err := service.AcquireTaskWorkerLease(ctx, payload.TaskID, service.TaskWorkerStagePoll)
	if err != nil {
		return fmt.Errorf("acquire poll lease: %w", err)
	}
	if !acquired {
		current, stateErr := taskService.GetTaskByID(payload.TaskID)
		if stateErr != nil {
			return fmt.Errorf("load task after busy poll lease: %w", stateErr)
		}
		if (current.Status == model.TaskStatusProcessing || current.Status == model.TaskStatusFinalizing) &&
			current.PollCursor == payload.PollCount {
			return fmt.Errorf("%w: task %d poll round %d", service.ErrTaskWorkerLeaseBusy, payload.TaskID, payload.PollCount)
		}
		return nil
	}
	defer func() {
		if err := lease.Stop(); err != nil && !errors.Is(err, service.ErrTaskWorkerLeaseLost) {
			logger.Error("release poll lease failed", zap.Uint("task_id", payload.TaskID), zap.Error(err))
		}
	}()
	ctx = lease.Context()

	logger.Info("processing poll task", zap.Uint("task_id", payload.TaskID), zap.Int("poll_count", payload.PollCount))

	// 1. 获取任务
	task, err := taskService.GetTaskByID(payload.TaskID)
	if err != nil {
		return fmt.Errorf("get task: %w", err)
	}

	// 终态任务不再轮询。finalizing 仍可重试查询，以恢复一次失败的上传入队。
	if task.Status.IsTerminal() {
		return nil
	}
	currentRound, err := taskService.CurrentTaskPollRound(task.ID, lease.Owner(), payload.PollCount)
	if err != nil {
		return fmt.Errorf("validate poll round: %w", err)
	}
	if !currentRound && task.PollCursor == -1 {
		currentRound, err = taskService.AdoptLegacyTaskPollRound(task.ID, lease.Owner(), payload.PollCount)
		if err != nil {
			return fmt.Errorf("adopt legacy poll round: %w", err)
		}
	}
	if !currentRound && payload.PollCount == task.PollCursor+1 {
		currentRound, err = taskService.AdoptQueuedTaskPollRound(task.ID, lease.Owner(), payload.PollCount)
		if err != nil {
			return fmt.Errorf("adopt queued poll round: %w", err)
		}
	}
	if !currentRound {
		return nil
	}

	// 2. 获取端点配置（读取轮询参数）
	var endpoint model.Endpoint
	if err := model.DB().First(&endpoint, task.EndpointID).Error; err != nil {
		// 端点配置缺失(被删/DB异常)则无法正确轮询,直接判失败,避免用零值配置空转
		logger.Error("poll: load endpoint failed", zap.Uint("task_id", task.ID), zap.Uint("endpoint_id", task.EndpointID), zap.Error(err))
		if _, failErr := taskService.UpdateTaskFail(task.ID, "poll: endpoint config not found"); failErr != nil {
			return fmt.Errorf("load endpoint: %v; record task failure: %w", err, failErr)
		}
		return nil
	}
	if err := service.ApplyTaskEndpointSnapshot(task, &endpoint); err != nil {
		if _, failErr := taskService.UpdateTaskFail(task.ID, "poll: invalid endpoint snapshot"); failErr != nil {
			return fmt.Errorf("apply endpoint snapshot: %v; record task failure: %w", err, failErr)
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
		_, failErr := taskService.UpdateTaskTimeoutFail(payload.TaskID, "poll timeout")
		if failErr != nil {
			return fmt.Errorf("record poll timeout: %w", failErr)
		}
		return nil
	}

	// 3. 获取渠道信息
	var channel model.Channel
	if err := model.DB().First(&channel, task.ChannelID).Error; err != nil {
		logger.Error("poll: load channel failed", zap.Uint("task_id", task.ID), zap.Uint("channel_id", task.ChannelID), zap.Error(err))
		if _, failErr := taskService.UpdateTaskFail(task.ID, "poll: channel not found"); failErr != nil {
			return fmt.Errorf("load channel: %v; record task failure: %w", err, failErr)
		}
		return nil
	}

	var account model.ChannelAccount
	if err := model.DB().First(&account, task.AccountID).Error; err != nil {
		logger.Error("poll: load account failed", zap.Uint("task_id", task.ID), zap.Uint("account_id", task.AccountID), zap.Error(err))
		if _, failErr := taskService.UpdateTaskFail(task.ID, "poll: account not found"); failErr != nil {
			return fmt.Errorf("load account: %v; record task failure: %w", err, failErr)
		}
		return nil
	}

	// 4. 创建 Provider 并查询进度
	prov, err := newProvider(&channel, &account, &endpoint)
	if err != nil {
		return fmt.Errorf("create provider: %w", err)
	}

	attempt, err := service.StartCapabilityAttempt(task, &endpoint, model.APICallStagePoll)
	if err != nil {
		return fmt.Errorf("start poll attempt: %w", err)
	}
	result, err := prov.GetProgress(ctx, task.VendorTaskID)
	if len(result.RawResponse) > 0 {
		vendorResponse := result.RawResponse
		if !json.Valid(vendorResponse) {
			vendorResponse, _ = json.Marshal(map[string]string{"raw": string(result.RawResponse)})
		}
		if saveErr := taskService.UpdateVendorResponse(task.ID, vendorResponse); saveErr != nil {
			return fmt.Errorf("save poll vendor response: %w", saveErr)
		}
	}
	attemptErr := err
	if attemptErr == nil && result.Status == provider.StatusFail {
		if result.Error == "" {
			result.Error = "upstream task failed"
		}
		attemptErr = errors.New(result.Error)
	}
	if finishErr := service.FinishCapabilityAttempt(
		task,
		&channel,
		&endpoint,
		attempt,
		model.APICallStagePoll,
		result.RequestMetadata,
		attemptErr,
	); finishErr != nil {
		return fmt.Errorf("finish poll attempt: %w", finishErr)
	}
	if leaseErr := lease.Check(); leaseErr != nil {
		terminal, stateErr := taskIsTerminalAfterLeaseLoss(task.ID, leaseErr)
		if stateErr != nil {
			return stateErr
		}
		if terminal {
			return nil
		}
	}
	current, stateErr := taskService.GetTaskByID(task.ID)
	if stateErr != nil {
		return fmt.Errorf("reload task after poll attempt: %w", stateErr)
	}
	if current.Status == model.TaskStatusFinalizing || current.Status.IsTerminal() {
		return nil
	}
	if err != nil {
		// 错误分级: 可恢复错误(网络抖动/408/429/5xx)继续轮询; 不可恢复(4xx 硬错误)快速失败
		if isRetryablePollError(err) {
			logger.Warn("get progress transient error, retry poll", zap.Uint("task_id", payload.TaskID), zap.Error(err))
			if err := requeueTaskPoll(payload.TaskID, payload.PollCount+1, pollInterval); err != nil {
				return err
			}
			return taskService.CompleteTaskPollRound(task.ID, lease.Owner(), payload.PollCount)
		}
		logger.Error("get progress fatal error, fail task", zap.Uint("task_id", payload.TaskID), zap.Error(err))
		_, failErr := taskService.UpdateTaskFail(payload.TaskID, "poll error: "+err.Error())
		if failErr != nil {
			return fmt.Errorf("record poll error: %w", failErr)
		}
		return nil
	}

	logger.Info("poll result", zap.String("status", string(result.Status)), zap.Int("progress", result.Progress))

	// 5. 处理结果
	switch result.Status {
	case provider.StatusSuccess:
		return enqueuePollResult(task.ID, result)

	case provider.StatusFail:
		_, failErr := taskService.UpdateTaskFail(task.ID, result.Error)
		if failErr != nil {
			return fmt.Errorf("record poll failure: %w", failErr)
		}
		return nil

	case provider.StatusProcessing, provider.StatusSubmitted, provider.StatusPending:
		// 更新进度
		if err := taskService.UpdateTaskProgress(task.ID, result.Progress); err != nil {
			return fmt.Errorf("update poll progress: %w", err)
		}
		// 继续轮询
		if err := requeueTaskPoll(payload.TaskID, payload.PollCount+1, pollInterval); err != nil {
			return err
		}
		return taskService.CompleteTaskPollRound(task.ID, lease.Owner(), payload.PollCount)

	default:
		// 未知状态，继续轮询
		if err := requeueTaskPoll(payload.TaskID, payload.PollCount+1, pollInterval); err != nil {
			return err
		}
		return taskService.CompleteTaskPollRound(task.ID, lease.Owner(), payload.PollCount)
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
	info, err := queue.Client.Enqueue(
		task,
		asynq.ProcessIn(time.Duration(intervalSeconds)*time.Second),
		asynq.Queue("default"),
		asynq.TaskID(fmt.Sprintf("task-poll-%d-%d", taskID, pollCount)),
	)
	if errors.Is(err, asynq.ErrTaskIDConflict) {
		return nil
	}
	if err != nil {
		logger.Error("requeue poll failed", zap.Uint("task_id", taskID), zap.Error(err))
	} else {
		logger.Info("requeue poll ok", zap.Uint("task_id", taskID), zap.Int("poll_count", pollCount), zap.String("queue", info.Queue))
	}
	return err
}

var requeueTaskPoll = requeuePoll

var enqueueUpload = queue.EnqueueTaskUpload

func enqueuePollResult(taskID uint, result provider.ProgressResult) error {
	originURLs := append([]string{}, result.URLs...)
	originURLs = append(originURLs, result.B64Data...)
	originURL := ""
	if len(originURLs) > 0 {
		originURL = originURLs[0]
	}
	ready, err := taskService.BeginTaskFinalization(taskID)
	if err != nil {
		return fmt.Errorf("begin task finalization: %w", err)
	}
	if !ready {
		return nil
	}
	return enqueueUpload(taskID, originURL, originURLs, result.RevisedPrompt)
}

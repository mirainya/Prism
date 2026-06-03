package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider"
	"github.com/mirainya/Prism/pkg/config"
	"github.com/mirainya/Prism/pkg/logger"
	"github.com/mirainya/Prism/pkg/queue"
	"go.uber.org/zap"
)

func HandleTaskSubmit(ctx context.Context, t *asynq.Task) error {
	var payload TaskSubmitPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("unmarshal payload: %w", err)
	}

	logger.Info("processing submit task", zap.Uint("task_id", payload.TaskID))

	// 1. 获取任务
	task, err := taskService.GetTaskByID(payload.TaskID)
	if err != nil {
		return fmt.Errorf("get task: %w", err)
	}

	// 2. 获取渠道信息
	var channel model.Channel
	if err := model.DB().First(&channel, task.ChannelID).Error; err != nil {
		return fmt.Errorf("get channel: %w", err)
	}

	var account model.ChannelAccount
	if err := model.DB().First(&account, task.AccountID).Error; err != nil {
		return fmt.Errorf("get account: %w", err)
	}

	var endpoint model.Endpoint
	if err := model.DB().First(&endpoint, task.EndpointID).Error; err != nil {
		return fmt.Errorf("get endpoint: %w", err)
	}

	// 3. 创建 Provider
	prov, err := provider.NewProvider(&channel, &account, &endpoint)
	if err != nil {
		taskService.UpdateTaskFail(task.ID, "create provider error: "+err.Error())
		return nil
	}

	// 4. 解析参数
	var mappedParams map[string]any
	json.Unmarshal(task.MappedParams, &mappedParams)

	// callback 模式：注入 Prism 自身的回调地址
	if endpoint.InteractionMode == model.ModeCallback {
		if mappedParams == nil {
			mappedParams = make(map[string]any)
		}
		mappedParams["callback_url"] = config.C.Server.PublicURL + "/internal/callback/" + channel.Type
	}

	// 5. 提交到上游
	submitReq := provider.SubmitRequest{
		TaskNo:      task.TaskNo,
		Params:      mappedParams,
		CallbackURL: task.CallbackURL,
	}

	result, err := prov.Submit(ctx, submitReq)
	if err != nil {
		taskService.UpdateTaskFail(task.ID, "submit error: "+err.Error())
		decrementAccountTasks(task.ID)
		return nil
	}

	// 6. 根据交互模式处理结果
	switch endpoint.InteractionMode {
	case model.ModePoll:
		taskService.UpdateTaskStatus(task.ID, model.TaskStatusProcessing, result.ProviderTaskID)
		if result.ProviderTaskID == "" {
			taskService.UpdateTaskFail(task.ID, "upstream returned empty task_id, cannot poll")
			decrementAccountTasks(task.ID)
			return nil
		}
		pollPayload := TaskPollPayload{
			TaskID:    task.ID,
			PollCount: 0,
		}
		payloadBytes, _ := json.Marshal(pollPayload)
		pollTask := asynq.NewTask(TypeTaskPoll, payloadBytes)
		info, enqErr := queue.Client.Enqueue(pollTask, asynq.ProcessIn(time.Duration(endpoint.PollInterval)*time.Second), asynq.Queue("default"))
		if enqErr != nil {
			logger.Error("enqueue poll failed", zap.Uint("task_id", task.ID), zap.Error(enqErr))
		} else {
			logger.Info("enqueue poll ok", zap.Uint("task_id", task.ID), zap.String("queue", info.Queue))
		}
	case model.ModeCallback:
		// 伪异步上游：提交响应已直接返回结果(无 task_id 但有图片 URL),直接结算成功，不傻等回调
		if result.ProviderTaskID == "" && len(result.URLs) > 0 {
			taskService.UpdateTaskSuccess(task.ID, map[string]any{"urls": result.URLs}, endpoint.InputPrice)
			decrementAccountTasks(task.ID)
			break
		}
		taskService.UpdateTaskStatus(task.ID, model.TaskStatusProcessing, result.ProviderTaskID)
		// 兜底轮询：OOJJ 回调可能丢失/延迟，配了 poll_path 则同时主动轮询(poll 幂等,不会与回调重复结算)
		if endpoint.PollPath != "" && result.ProviderTaskID != "" {
			interval := endpoint.PollInterval
			if interval <= 0 {
				interval = DefaultPollInterval
			}
			requeuePoll(task.ID, 0, interval)
		}
	default:
		// sync/stream: submit 响应即为最终结果
		taskService.UpdateTaskSuccess(task.ID, map[string]any{"data": result.ProviderTaskID, "urls": result.URLs}, endpoint.InputPrice)
		decrementAccountTasks(task.ID)
	}

	logger.Info("task submitted", zap.Uint("task_id", task.ID), zap.String("vendor_task_id", result.ProviderTaskID))

	return nil
}

func NewTaskSubmit(taskID uint) (*asynq.Task, error) {
	payload := TaskSubmitPayload{TaskID: taskID}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TypeTaskSubmit, payloadBytes), nil
}

func EnqueueTaskSubmit(taskID uint) error {
	task, err := NewTaskSubmit(taskID)
	if err != nil {
		return err
	}
	_, err = queue.Client.Enqueue(task, asynq.Queue("critical"))
	return err
}

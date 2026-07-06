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
	"github.com/mirainya/Prism/pkg/filestorage"
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
		if committed, _ := taskService.UpdateTaskFail(task.ID, "create provider error: "+err.Error()); committed {
			decrementAccountTasks(task.ID)
		}
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
		if committed, _ := taskService.UpdateTaskFail(task.ID, "submit error: "+err.Error()); committed {
			decrementAccountTasks(task.ID)
		}
		return nil
	}

	// 6. 根据交互模式处理结果
	switch endpoint.InteractionMode {
	case model.ModePoll:
		taskService.UpdateTaskStatus(task.ID, model.TaskStatusProcessing, result.ProviderTaskID)
		if result.ProviderTaskID == "" {
			if committed, _ := taskService.UpdateTaskFail(task.ID, "upstream returned empty task_id, cannot poll"); committed {
				decrementAccountTasks(task.ID)
			}
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
			// 入队轮询失败: 任务已提交上游但无法被跟踪,判失败并递减计数,避免卡 Processing 永久泄漏
			logger.Error("enqueue poll failed", zap.Uint("task_id", task.ID), zap.Error(enqErr))
			if committed, _ := taskService.UpdateTaskFail(task.ID, "enqueue poll failed: "+enqErr.Error()); committed {
				decrementAccountTasks(task.ID)
			}
			return nil
		}
		logger.Info("enqueue poll ok", zap.Uint("task_id", task.ID), zap.String("queue", info.Queue))
	case model.ModeCallback:
		if result.ProviderTaskID == "" && (len(result.URLs) > 0 || len(result.B64Data) > 0) {
			originURLs := append([]string{}, result.URLs...)
			originURLs = append(originURLs, result.B64Data...)
			originURL := ""
			if len(originURLs) > 0 {
				originURL = originURLs[0]
			}
			if len(result.B64Data) > 0 {
				taskService.UpdateTaskProgress(task.ID, 100)
				if err := enqueueUpload(task.ID, originURL, originURLs, result.RevisedPrompt); err != nil {
					if committed, _ := taskService.UpdateTaskFail(task.ID, "enqueue upload error: "+err.Error()); committed {
						decrementAccountTasks(task.ID)
					}
				}
				break
			}
			successResult := buildResult(originURL, originURLs)
			if result.RevisedPrompt != "" {
				successResult["revised_prompt"] = result.RevisedPrompt
			}
			if committed, _ := taskService.UpdateTaskSuccess(task.ID, successResult, task.Cost); committed {
				decrementAccountTasks(task.ID)
			}
			break
		}
		taskService.UpdateTaskStatus(task.ID, model.TaskStatusProcessing, result.ProviderTaskID)
		if endpoint.PollPath != "" && result.ProviderTaskID != "" {
			interval := endpoint.PollInterval
			if interval <= 0 {
				interval = DefaultPollInterval
			}
			requeuePoll(task.ID, 0, interval)
		}
	default:
		if result.ProviderTaskID == "" && len(result.URLs) == 0 && len(result.B64Data) == 0 {
			if committed, _ := taskService.UpdateTaskFail(task.ID, "upstream returned empty result"); committed {
				decrementAccountTasks(task.ID)
			}
			break
		}
		urls := append([]string{}, result.URLs...)
		if len(result.B64Data) > 0 {
			transferred, err := resolveSubmitB64ToURLs(ctx, result.B64Data, task.ModelCode)
			if err != nil {
				if committed, _ := taskService.UpdateTaskFail(task.ID, err.Error()); committed {
					decrementAccountTasks(task.ID)
				}
				break
			}
			urls = append(urls, transferred...)
		}
		successResult := map[string]any{"data": result.ProviderTaskID}
		if len(urls) > 0 {
			successResult["url"] = urls[0]
			successResult["urls"] = urls
		}
		if result.RevisedPrompt != "" {
			successResult["revised_prompt"] = result.RevisedPrompt
		}
		if committed, _ := taskService.UpdateTaskSuccess(task.ID, successResult, task.Cost); committed {
			decrementAccountTasks(task.ID)
		}
	}

	logger.Info("task submitted", zap.Uint("task_id", task.ID), zap.String("vendor_task_id", result.ProviderTaskID))

	return nil
}

func resolveSubmitB64ToURLs(ctx context.Context, b64List []string, capabilityCode string) ([]string, error) {
	urls := make([]string, 0, len(b64List))
	for _, b64 := range b64List {
		if b64 == "" {
			continue
		}
		url, err := filestorage.TransferBase64(ctx, b64, capabilityCode)
		if err != nil {
			return nil, fmt.Errorf("transfer base64: %w", err)
		}
		urls = append(urls, url)
	}
	return urls, nil
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

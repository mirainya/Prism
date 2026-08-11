package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hibiken/asynq"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/pkg/filestorage"
	"github.com/mirainya/Prism/pkg/logger"
	"github.com/mirainya/Prism/pkg/queue"
	"go.uber.org/zap"
	"gorm.io/datatypes"
)

func HandleTaskUpload(ctx context.Context, t *asynq.Task) error {
	var payload TaskUploadPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("unmarshal payload: %w", err)
	}

	logger.Info("processing upload task", zap.Uint("task_id", payload.TaskID))

	task, err := taskService.GetTaskByID(payload.TaskID)
	if err != nil {
		return fmt.Errorf("get task: %w", err)
	}
	if task.Status.IsTerminal() {
		if task.Status == model.TaskStatusSuccess &&
			task.CallbackURL != "" &&
			task.CallbackStatus != model.CallbackStatusSuccess {
			return enqueueNotify(task.ID)
		}
		return nil
	}
	ready, err := taskService.BeginTaskFinalization(task.ID)
	if err != nil {
		return fmt.Errorf("begin upload finalization: %w", err)
	}
	if !ready {
		return nil
	}

	// 获取端点配置以获取价格
	var ep model.Endpoint
	if err := model.DB().First(&ep, task.EndpointID).Error; err != nil {
		return fmt.Errorf("get endpoint: %w", err)
	}
	if err := service.ApplyTaskEndpointSnapshot(task, &ep); err != nil {
		return fmt.Errorf("apply endpoint snapshot: %w", err)
	}

	originURLs := payload.URLs
	if len(originURLs) == 0 && payload.OriginURL != "" {
		originURLs = []string{payload.OriginURL}
	}

	finalURLs := make([]string, 0, len(originURLs))
	transferEnabled := isTransferEnabled(ep.ExtraConfig)
	for _, originURL := range originURLs {
		if originURL == "" {
			continue
		}
		finalURL := originURL
		// 裸 base64 绝不落库：无视 transfer_enabled 开关强制转存，失败即判任务失败并退款
		isB64 := filestorage.IsBase64Data(originURL)
		if transferEnabled || isB64 {
			if transferred, err := transferResultFile(ctx, originURL, task.ModelCode); err != nil {
				if isB64 {
					logger.Error("base64 transfer failed", zap.Uint("task_id", task.ID), zap.Error(err))
					_, failErr := taskService.FailTaskUpload(task.ID, "transfer base64 failed: "+err.Error())
					if failErr != nil {
						return fmt.Errorf("record upload failure: %w", failErr)
					}
					return nil
				}
				logger.Error("file transfer failed", zap.Uint("task_id", task.ID), zap.Error(err))
			} else {
				finalURL = transferred
			}
		}
		if finalURL != "" {
			finalURLs = append(finalURLs, finalURL)
		}
	}

	primaryURL := ""
	if len(finalURLs) > 0 {
		primaryURL = finalURLs[0]
	}
	result := buildResult(primaryURL, finalURLs)
	if payload.RevisedPrompt != "" {
		result["revised_prompt"] = payload.RevisedPrompt
	}
	// 结算价统一用扣款时记录的 task.Cost(按 primary 端点价扣的),避免 fallback 到异价端点导致账目不符
	committed, err := taskService.CompleteTaskUpload(task.ID, result, task.Cost)
	if err != nil {
		return fmt.Errorf("complete upload task: %w", err)
	}
	// 如果有回调地址，入队通知任务
	if committed && task.CallbackURL != "" {
		if err := enqueueNotify(task.ID); err != nil {
			return fmt.Errorf("enqueue notify: %w", err)
		}
	}

	logger.Info("task upload completed", zap.Uint("task_id", task.ID), zap.String("final_url", primaryURL))

	return nil
}

func transferResultFile(ctx context.Context, originURL string, capabilityCode string) (string, error) {
	if filestorage.IsBase64Data(originURL) {
		return filestorage.TransferBase64(ctx, originURL, capabilityCode)
	}
	return filestorage.TransferURL(ctx, originURL, capabilityCode)
}

// buildResult 构建结果对象
func buildResult(primaryURL string, urls []string) map[string]any {
	result := map[string]any{
		"url": primaryURL,
	}
	if len(urls) > 0 {
		result["urls"] = urls
	}
	return result
}

func isTransferEnabled(extraConfig datatypes.JSON) bool {
	if len(extraConfig) == 0 {
		return false
	}
	var cfg map[string]any
	if err := json.Unmarshal(extraConfig, &cfg); err != nil {
		return false
	}
	if v, ok := cfg["transfer_enabled"]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

func enqueueNotify(taskID uint) error {
	payload := TaskNotifyPayload{TaskID: taskID}
	payloadBytes, _ := json.Marshal(payload)
	task := asynq.NewTask(TypeTaskNotify, payloadBytes)
	_, err := queue.Client.Enqueue(task,
		asynq.MaxRetry(maxNotifyRetries),
		asynq.Queue("notify"),
		asynq.TaskID(fmt.Sprintf("task-notify-%d", taskID)),
	)
	if errors.Is(err, asynq.ErrTaskIDConflict) {
		return nil
	}
	return err
}

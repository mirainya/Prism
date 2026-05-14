package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hibiken/asynq"
	"github.com/mirainya/Prism/internal/model"
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

	// 获取端点配置以获取价格
	var ep model.Endpoint
	model.DB().First(&ep, task.EndpointID)

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
		if transferEnabled {
			if transferred, err := transferResultFile(ctx, originURL, task.ModelCode); err != nil {
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
	taskService.UpdateTaskSuccess(task.ID, result, ep.InputPrice)
	decrementAccountTasks(task.ID)

	// 如果有回调地址，入队通知任务
	if task.CallbackURL != "" {
		enqueueNotify(task.ID)
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
		asynq.MaxRetry(5),
		asynq.Queue("notify"),
	)
	return err
}

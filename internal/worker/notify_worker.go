package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/hibiken/asynq"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/logger"
	"go.uber.org/zap"
)

const maxNotifyRetries = 5

// 复用 HTTP Client，避免每次回调都创建新连接
var notifyClient = &http.Client{Timeout: 10 * time.Second}

type CallbackPayload struct {
	TaskID   string         `json:"task_id"`
	Status   string         `json:"status"`
	Progress int            `json:"progress"`
	Result   map[string]any `json:"result,omitempty"`
	Error    string         `json:"error,omitempty"`
}

func HandleTaskNotify(ctx context.Context, t *asynq.Task) error {
	var payload TaskNotifyPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("unmarshal payload: %w", err)
	}

	retried, _ := asynq.GetRetryCount(ctx)
	maxRetry, _ := asynq.GetMaxRetry(ctx)

	logger.Info("processing notify task",
		zap.Uint("task_id", payload.TaskID),
		zap.Int("attempt", retried+1),
	)

	task, err := taskService.GetTaskByID(payload.TaskID)
	if err != nil {
		return fmt.Errorf("get task: %w", err)
	}

	if task.CallbackURL == "" {
		return nil
	}

	// 构造回调内容
	callbackData := CallbackPayload{
		TaskID:   task.TaskNo,
		Status:   string(task.Status),
		Progress: task.Progress,
	}

	if task.Status == model.TaskStatusSuccess {
		var result map[string]any
		json.Unmarshal(task.Result, &result)
		callbackData.Result = result
	} else if task.Status == model.TaskStatusFailed {
		callbackData.Error = task.ErrorMessage
	}

	// 发送回调
	bodyBytes, _ := json.Marshal(callbackData)

	attempt := task.CallbackAttempts + 1

	resp, err := notifyClient.Post(task.CallbackURL, "application/json", bytes.NewReader(bodyBytes))
	if err != nil {
		logger.Error("callback failed",
			zap.Error(err),
			zap.Int("attempt", retried+1),
		)
		taskService.UpdateCallbackStatus(task.ID, model.CallbackStatusFailed, attempt)
		// 最后一次重试也失败，不再返回 error（避免进入死信队列后无意义重试）
		if retried >= maxRetry {
			logger.Error("callback permanently failed, exhausted all retries",
				zap.Uint("task_id", task.ID),
				zap.String("callback_url", task.CallbackURL),
			)
			return nil
		}
		return fmt.Errorf("callback error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		logger.Error("callback returned error",
			zap.Int("status", resp.StatusCode),
			zap.Int("attempt", retried+1),
		)
		taskService.UpdateCallbackStatus(task.ID, model.CallbackStatusFailed, attempt)
		if retried >= maxRetry {
			logger.Error("callback permanently failed, exhausted all retries",
				zap.Uint("task_id", task.ID),
				zap.Int("last_status", resp.StatusCode),
			)
			return nil
		}
		return fmt.Errorf("callback returned %d", resp.StatusCode)
	}

	taskService.UpdateCallbackStatus(task.ID, model.CallbackStatusSuccess, attempt)
	logger.Info("callback sent", zap.Uint("task_id", task.ID))

	return nil
}

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
	"github.com/mirainya/Prism/pkg/safeurl"
	"go.uber.org/zap"
)

const maxNotifyRetries = 5

// 复用 HTTP Client，避免每次回调都创建新连接
var notifyClient = safeurl.NewClient(10 * time.Second)

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
	if task.CallbackStatus == model.CallbackStatusSuccess {
		return nil
	}

	if task.CallbackURL == "" {
		return nil
	}
	if err := safeurl.Validate(ctx, task.CallbackURL); err != nil {
		return recordCallbackFailure(task, retried, maxRetry, fmt.Errorf("unsafe callback URL: %w", err))
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

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, task.CallbackURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return recordCallbackFailure(task, retried, maxRetry, fmt.Errorf("build callback request: %w", err))
	}
	request.Header.Set("Content-Type", "application/json")
	resp, err := notifyClient.Do(request)
	if err != nil {
		logger.Error("callback failed",
			zap.Error(err),
			zap.Int("attempt", retried+1),
		)
		return recordCallbackFailure(task, retried, maxRetry, fmt.Errorf("callback error: %w", err))
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		logger.Error("callback returned error",
			zap.Int("status", resp.StatusCode),
			zap.Int("attempt", retried+1),
		)
		return recordCallbackFailure(task, retried, maxRetry, fmt.Errorf("callback returned %d", resp.StatusCode))
	}

	if err := taskService.UpdateCallbackStatus(task.ID, model.CallbackStatusSuccess, attempt); err != nil {
		return fmt.Errorf("record successful callback delivery: %w", err)
	}
	logger.Info("callback sent", zap.Uint("task_id", task.ID))

	return nil
}

func recordCallbackFailure(task *model.Task, retried, maxRetry int, deliveryErr error) error {
	attempt := task.CallbackAttempts + 1
	if err := taskService.UpdateCallbackStatus(task.ID, model.CallbackStatusFailed, attempt); err != nil {
		return fmt.Errorf("%v; record callback failure: %w", deliveryErr, err)
	}
	if retried >= maxRetry {
		logger.Error("callback permanently failed, exhausted all retries",
			zap.Uint("task_id", task.ID),
			zap.Error(deliveryErr),
		)
		return nil
	}
	return deliveryErr
}

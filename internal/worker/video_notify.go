package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/video"
	"github.com/mirainya/Prism/pkg/logger"
	"github.com/mirainya/Prism/pkg/safeurl"
	"go.uber.org/zap"
)

func HandleVideoNotify(ctx context.Context, t *asynq.Task) error {
	var payload VideoNotifyPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("unmarshal payload: %w", err)
	}

	db := model.DB()
	var task video.VideoTask
	if err := db.First(&task, "id = ?", payload.TaskID).Error; err != nil {
		return fmt.Errorf("load task: %w", err)
	}

	if task.CallbackURL == "" {
		return nil
	}
	if err := safeurl.Validate(ctx, task.CallbackURL); err != nil {
		return fmt.Errorf("unsafe video callback URL: %w", err)
	}

	body := map[string]any{
		"task_id": task.ID,
		"status":  string(task.Status),
		"model":   task.Model,
	}
	if task.Status == video.VideoTaskStatusCompleted && task.ResultJSON != nil {
		var result json.RawMessage
		_ = json.Unmarshal(task.ResultJSON, &result)
		body["result"] = result
	}
	if task.ErrorMessage != "" {
		body["error"] = task.ErrorMessage
	}

	payload_bytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal callback body: %w", err)
	}

	client := safeurl.NewClient(10 * time.Second)
	resp, err := client.Post(task.CallbackURL, "application/json", bytes.NewReader(payload_bytes))
	if err != nil {
		logger.Warn("video notify callback failed", zap.String("task_id", task.ID), zap.Error(err))
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		logger.Warn("video notify callback non-2xx", zap.String("task_id", task.ID), zap.Int("status", resp.StatusCode))
		return fmt.Errorf("callback returned %d", resp.StatusCode)
	}

	logger.Info("video notify sent", zap.String("task_id", task.ID))
	return nil
}

package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/video"
	"github.com/mirainya/Prism/pkg/logger"
	"github.com/mirainya/Prism/pkg/queue"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

const (
	VideoDefaultMaxPoll   = 720
	VideoDefaultPollDelay = 10
)

func HandleVideoPoll(ctx context.Context, t *asynq.Task) error {
	var payload VideoPollPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("unmarshal payload: %w", err)
	}
	logger.Info("video poll", zap.String("task_id", payload.TaskID), zap.Int("poll_count", payload.PollCount))

	db := model.DB()
	var task video.VideoTask
	if err := db.First(&task, "id = ?", payload.TaskID).Error; err != nil {
		return fmt.Errorf("load task: %w", err)
	}
	if task.Status.IsTerminal() {
		return nil
	}
	if task.Status != video.VideoTaskStatusSubmitted && task.Status != video.VideoTaskStatusTracking {
		return nil
	}
	lease, acquired, err := video.AcquireVideoWorkerLease(ctx, task.ID, video.VideoWorkerStagePoll)
	if err != nil {
		return fmt.Errorf("acquire video poll lease: %w", err)
	}
	if !acquired {
		return fmt.Errorf("%w: task %s", video.ErrVideoWorkerLeaseBusy, task.ID)
	}
	defer func() {
		if stopErr := lease.Stop(); stopErr != nil && !errors.Is(stopErr, video.ErrVideoWorkerLeaseLost) {
			logger.Error("release video poll lease failed", zap.String("task_id", task.ID), zap.Error(stopErr))
		}
	}()
	ctx = lease.Context()
	if err := db.WithContext(ctx).First(&task, "id = ?", payload.TaskID).Error; err != nil {
		return fmt.Errorf("reload leased video task: %w", err)
	}
	if task.Status.IsTerminal() {
		return nil
	}
	if payload.PollCount >= VideoDefaultMaxPoll {
		return videoPollFail(ctx, db, &task, "poll timeout: max attempts reached")
	}

	channel, key, _, err := video.LoadVideoTaskRoute(db.WithContext(ctx), &task)
	if err != nil {
		return videoPollFail(ctx, db, &task, "load route: "+err.Error())
	}
	eng := videoEngine
	if eng == nil {
		return fmt.Errorf("video engine not initialized")
	}
	adapter := eng.Registry().Get(channel.AdapterType, channel, key)
	if adapter == nil {
		return videoPollFail(ctx, db, &task, "unknown adapter: "+channel.AdapterType)
	}

	progress, err := adapter.Poll(ctx, task.ProviderTaskID)
	if err != nil {
		logger.Warn("video poll error, will retry", zap.String("task_id", task.ID), zap.Error(err))
		if leaseErr := lease.Check(); leaseErr != nil {
			return fmt.Errorf("check video poll lease after error: %w", leaseErr)
		}
		result := db.WithContext(ctx).Model(&video.VideoTask{}).
			Where("id = ? AND status IN ? AND worker_lease = ? AND worker_lease_stage = ? AND worker_lease_expires_at > ?",
				task.ID, []video.VideoTaskStatus{video.VideoTaskStatusSubmitted, video.VideoTaskStatusTracking},
				lease.Owner(), video.VideoWorkerStagePoll, time.Now()).
			Update("poll_count", payload.PollCount+1)
		if result.Error != nil {
			return fmt.Errorf("save video poll retry: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return nil
		}
		return queue.EnqueueVideoPoll(task.ID, payload.PollCount+1, VideoDefaultPollDelay)
	}
	if err := lease.Check(); err != nil {
		return fmt.Errorf("check video poll lease: %w", err)
	}
	if err := video.SaveProviderMetadata(ctx, task.ID, progress.Metadata); err != nil {
		return fmt.Errorf("save video provider metadata: %w", err)
	}
	switch progress.Status {
	case video.VideoTaskStatusCompleted:
		materialized, err := video.MaterializeGenerationResult(ctx, channel, progress.Result)
		if err != nil {
			return videoPollFail(ctx, db, &task, err.Error())
		}
		completed, err := video.CompleteTask(ctx, task.ID, task.ProviderTaskID, materialized, payload.PollCount+1)
		if err != nil {
			return fmt.Errorf("complete video task: %w", err)
		}
		if completed {
			eng.Router().ReleaseConcurrency(ctx, task.KeyID)
			if task.CallbackURL != "" {
				_ = queue.EnqueueVideoNotify(task.ID)
			}
		}
		logger.Info("video completed", zap.String("task_id", task.ID))
		return nil
	case video.VideoTaskStatusFailed:
		message := progress.Error
		if message == "" {
			message = "upstream video task failed"
		}
		return videoPollFail(ctx, db, &task, message)
	case video.VideoTaskStatusCancelled:
		cancelled, err := video.CancelTask(ctx, task.ID)
		if err != nil {
			return fmt.Errorf("cancel video task: %w", err)
		}
		if cancelled {
			eng.Router().ReleaseConcurrency(ctx, task.KeyID)
			if task.CallbackURL != "" {
				_ = queue.EnqueueVideoNotify(task.ID)
			}
		}
		return nil
	default:
		updates := map[string]any{"poll_count": payload.PollCount + 1}
		if progress.Percent > 0 {
			updates["progress"] = progress.Percent
		}
		if task.Status == video.VideoTaskStatusSubmitted {
			updates["status"] = video.VideoTaskStatusTracking
		}
		result := db.WithContext(ctx).Model(&video.VideoTask{}).
			Where("id = ? AND status IN ? AND worker_lease = ? AND worker_lease_stage = ? AND worker_lease_expires_at > ?",
				task.ID, []video.VideoTaskStatus{video.VideoTaskStatusSubmitted, video.VideoTaskStatusTracking},
				lease.Owner(), video.VideoWorkerStagePoll, time.Now()).
			Updates(updates)
		if result.Error != nil {
			return fmt.Errorf("update video progress: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return nil
		}
		return queue.EnqueueVideoPoll(task.ID, payload.PollCount+1, VideoDefaultPollDelay)
	}
}

func videoPollFail(ctx context.Context, db *gorm.DB, task *video.VideoTask, message string) error {
	logger.Error("video poll failed", zap.String("task_id", task.ID), zap.String("error", message))
	failed, err := video.FailTask(ctx, task.ID, message)
	if err != nil {
		return fmt.Errorf("fail video task: %w", err)
	}
	if failed && videoEngine != nil {
		videoEngine.Router().ReleaseConcurrency(context.Background(), task.KeyID)
	}
	if failed && task.CallbackURL != "" {
		_ = queue.EnqueueVideoNotify(task.ID)
	}
	return nil
}

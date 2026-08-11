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

var videoEngine *video.Engine

func HandleVideoSubmit(ctx context.Context, t *asynq.Task) error {
	var payload VideoSubmitPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("unmarshal payload: %w", err)
	}
	logger.Info("video submit start", zap.String("task_id", payload.TaskID))

	db := model.DB()
	var task video.VideoTask
	if err := db.First(&task, "id = ?", payload.TaskID).Error; err != nil {
		return fmt.Errorf("load task: %w", err)
	}
	if task.Status.IsTerminal() {
		return nil
	}
	if task.Status == video.VideoTaskStatusSubmitted || task.Status == video.VideoTaskStatusTracking {
		return queue.EnqueueVideoPoll(task.ID, task.PollCount)
	}
	if task.Status != video.VideoTaskStatusQueued {
		return nil
	}

	lease, acquired, err := video.AcquireVideoWorkerLease(ctx, task.ID, video.VideoWorkerStageSubmit)
	if err != nil {
		return fmt.Errorf("acquire video submit lease: %w", err)
	}
	if !acquired {
		if err := db.First(&task, "id = ?", payload.TaskID).Error; err != nil {
			return fmt.Errorf("reload busy video task: %w", err)
		}
		if task.Status == video.VideoTaskStatusSubmitted || task.Status == video.VideoTaskStatusTracking {
			return queue.EnqueueVideoPoll(task.ID, task.PollCount)
		}
		if task.Status.IsTerminal() {
			return nil
		}
		return fmt.Errorf("%w: task %s", video.ErrVideoWorkerLeaseBusy, task.ID)
	}
	defer func() {
		if stopErr := lease.Stop(); stopErr != nil && !errors.Is(stopErr, video.ErrVideoWorkerLeaseLost) {
			logger.Error("release video submit lease failed", zap.String("task_id", task.ID), zap.Error(stopErr))
		}
	}()
	ctx = lease.Context()
	if err := db.WithContext(ctx).First(&task, "id = ?", payload.TaskID).Error; err != nil {
		return fmt.Errorf("reload leased video task: %w", err)
	}
	if task.Status != video.VideoTaskStatusQueued {
		return nil
	}

	var channel video.VideoChannel
	if err := db.WithContext(ctx).First(&channel, task.ChannelID).Error; err != nil {
		return videoSubmitFail(ctx, db, &task, "channel not found: "+err.Error())
	}
	var key video.VideoChannelKey
	if err := db.WithContext(ctx).First(&key, task.KeyID).Error; err != nil {
		return videoSubmitFail(ctx, db, &task, "key not found: "+err.Error())
	}

	eng := videoEngine
	if eng == nil {
		return fmt.Errorf("video engine not initialized")
	}
	adapter := eng.Registry().Get(channel.AdapterType, &channel, &key)
	if adapter == nil {
		return videoSubmitFail(ctx, db, &task, "unknown adapter: "+channel.AdapterType)
	}
	attempt, err := video.StartCallAttempt(ctx, &task, &channel, &key, adapter)
	if err != nil {
		return videoSubmitFail(ctx, db, &task, "start call attempt: "+err.Error())
	}
	attemptID := uint(0)
	if attempt != nil {
		attemptID = attempt.ID
	}
	if task.ProviderTaskID != "" {
		if err := video.CommitVideoSubmission(ctx, task.ID, lease.Owner(), task.ProviderTaskID, attemptID); err != nil {
			return fmt.Errorf("restore submitted video task: %w", err)
		}
		return queue.EnqueueVideoPoll(task.ID, task.PollCount)
	}

	genReq := &video.GenerateRequest{
		Model: task.Model, Prompt: task.Prompt, Resolution: task.Resolution,
		Ratio: task.Ratio, Duration: task.Duration, Audio: task.GenerateAudio,
		TaskMode: task.TaskMode, TaskID: task.ID, TokenID: task.TokenID,
		Channel: &channel, Key: &key,
	}
	if len(task.ContentJSON) > 0 {
		if err := json.Unmarshal(task.ContentJSON, &genReq.Content); err != nil {
			return videoSubmitFail(ctx, db, &task, "decode content: "+err.Error())
		}
	}
	if len(task.ParamsJSON) > 0 {
		if err := json.Unmarshal(task.ParamsJSON, &genReq.Params); err != nil {
			return videoSubmitFail(ctx, db, &task, "decode params: "+err.Error())
		}
	}
	if err := video.ResolveGenerateRequestAssets(ctx, db, &channel, &key, task.ID, task.TokenID, genReq); err != nil {
		if video.IsRetryableProviderError(err) {
			return fmt.Errorf("retry video asset resolution: %w", err)
		}
		return videoSubmitFail(ctx, db, &task, err.Error())
	}

	providerRequest, err := adapter.BuildRequest(ctx, genReq)
	if err != nil {
		return videoSubmitFail(ctx, db, &task, "build request: "+err.Error())
	}
	previousCheckpoint, err := video.DecodeVideoSubmitCheckpoint(task.SubmitCheckpoint)
	if err != nil {
		return videoSubmitFail(ctx, db, &task, "decode submit checkpoint: "+err.Error())
	}
	attempts := 1
	if previousCheckpoint != nil {
		attempts = previousCheckpoint.Attempts + 1
	}
	checkpoint := &video.VideoSubmitCheckpoint{
		Version: 1, State: "in_flight", RequestID: task.ID,
		AttemptID: attemptID, Attempts: attempts, StartedAt: time.Now(),
	}
	if err := video.SaveVideoSubmitCheckpoint(ctx, task.ID, lease.Owner(), checkpoint); err != nil {
		return fmt.Errorf("save video submit checkpoint: %w", err)
	}
	result, err := adapter.Submit(ctx, providerRequest)
	if err != nil {
		eng.Router().RecordFailure(ctx, key.ID)
		if video.IsRetryableProviderError(err) && attempts <= queue.DefaultMaxRetry() {
			return fmt.Errorf("retry video submit: %w", err)
		}
		return videoSubmitFail(ctx, db, &task, "submit: "+err.Error())
	}
	eng.Router().RecordSuccess(ctx, key.ID)
	if err := lease.Check(); err != nil {
		return fmt.Errorf("check video submit lease: %w", err)
	}

	if result.Status == video.VideoTaskStatusCompleted {
		completed, err := video.CompleteTask(ctx, task.ID, result.ProviderTaskID, result.Result, 0)
		if err != nil {
			return fmt.Errorf("complete video task: %w", err)
		}
		if completed {
			eng.Router().ReleaseConcurrency(ctx, key.ID)
			if task.CallbackURL != "" {
				_ = queue.EnqueueVideoNotify(task.ID)
			}
		}
		return nil
	}
	if result.Status == video.VideoTaskStatusFailed {
		return videoSubmitFail(ctx, db, &task, "upstream task failed during submit")
	}
	if result.Status == video.VideoTaskStatusCancelled {
		cancelled, err := video.CancelTask(ctx, task.ID)
		if err != nil {
			return fmt.Errorf("cancel video task: %w", err)
		}
		if cancelled {
			eng.Router().ReleaseConcurrency(ctx, key.ID)
			if task.CallbackURL != "" {
				_ = queue.EnqueueVideoNotify(task.ID)
			}
		}
		return nil
	}

	if err := video.CommitVideoSubmission(ctx, task.ID, lease.Owner(), result.ProviderTaskID, attemptID); err != nil {
		if errors.Is(err, video.ErrVideoTaskNotExecutable) {
			cancelVideoSubmissionAfterRace(adapter, result.ProviderTaskID, result.Status)
			return nil
		}
		return fmt.Errorf("commit video submission: %w", err)
	}
	if err := queue.EnqueueVideoPoll(task.ID, 0, 5); err != nil {
		return fmt.Errorf("enqueue poll: %w", err)
	}
	logger.Info("video submitted", zap.String("task_id", task.ID), zap.String("provider_task_id", result.ProviderTaskID))
	return nil
}

func cancelVideoSubmissionAfterRace(adapter video.Adapter, providerTaskID string, status video.VideoTaskStatus) {
	canceller, ok := adapter.(video.Canceller)
	if !ok || providerTaskID == "" || !canceller.CanCancel(status) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := canceller.Cancel(ctx, providerTaskID); err != nil {
		logger.Error("cancel raced video submission failed", zap.String("provider_task_id", providerTaskID), zap.Error(err))
	}
}

func videoSubmitFail(ctx context.Context, db *gorm.DB, task *video.VideoTask, message string) error {
	logger.Error("video submit failed", zap.String("task_id", task.ID), zap.String("error", message))
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

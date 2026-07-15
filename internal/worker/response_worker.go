package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	responsepipeline "github.com/mirainya/Prism/internal/gateway/responses"
	"github.com/mirainya/Prism/internal/service"
)

const (
	staleForegroundCallBatchSize    = 100
	conversationProjectionBatchSize = 100
)

func HandleResponseBackground(ctx context.Context, task *asynq.Task) error {
	var payload ResponseBackgroundPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("decode response background payload: %w", err)
	}
	if payload.ResponseID == "" {
		return fmt.Errorf("response_id is required")
	}
	if responsePipe == nil {
		return fmt.Errorf("Gateway V2 response pipeline is not initialized")
	}
	attempt, _ := asynq.GetRetryCount(ctx)
	maxRetry, _ := asynq.GetMaxRetry(ctx)
	err := responsePipe.ExecuteBackground(ctx, payload.ResponseID, attempt >= maxRetry, attempt)
	if responsepipeline.IsPermanentBackgroundError(err) {
		return fmt.Errorf("%w: %v", asynq.SkipRetry, err)
	}
	return err
}

func HandleResponseRecovery(ctx context.Context, _ *asynq.Task) error {
	var recoveryErr error
	if _, err := responsepipeline.ReconcilePendingResponseRefunds(ctx); err != nil {
		recoveryErr = errors.Join(recoveryErr, err)
	}
	if _, err := responsepipeline.RequeueQueuedBackground(ctx); err != nil {
		recoveryErr = errors.Join(recoveryErr, err)
	}
	calls := service.NewAPICallService()
	for {
		reconciled, err := calls.ReconcileStaleForegroundCalls(ctx, time.Now().Add(-time.Hour), staleForegroundCallBatchSize)
		if err != nil {
			recoveryErr = errors.Join(recoveryErr, err)
			break
		}
		if reconciled < staleForegroundCallBatchSize {
			break
		}
	}
	for {
		reconciled, err := service.ReconcilePendingAPIConversations(ctx, conversationProjectionBatchSize)
		if err != nil {
			recoveryErr = errors.Join(recoveryErr, err)
			break
		}
		if reconciled < conversationProjectionBatchSize {
			break
		}
	}
	return recoveryErr
}

func NewResponseRecoveryTask() *asynq.Task {
	return asynq.NewTask(TypeResponseRecovery, nil)
}

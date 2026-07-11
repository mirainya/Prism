package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hibiken/asynq"
	responsepipeline "github.com/mirainya/Prism/internal/gateway/responses"
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
	if _, err := responsepipeline.ReconcilePendingResponseRefunds(ctx); err != nil {
		return err
	}
	_, err := responsepipeline.RequeueQueuedBackground(ctx)
	return err
}

func NewResponseRecoveryTask() *asynq.Task {
	return asynq.NewTask(TypeResponseRecovery, nil)
}

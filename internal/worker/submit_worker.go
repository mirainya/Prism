package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hibiken/asynq"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/pkg/auth"
	"github.com/mirainya/Prism/pkg/config"
	"github.com/mirainya/Prism/pkg/filestorage"
	"github.com/mirainya/Prism/pkg/logger"
	"github.com/mirainya/Prism/pkg/queue"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func HandleTaskSubmit(ctx context.Context, t *asynq.Task) error {
	var payload TaskSubmitPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("unmarshal payload: %w", err)
	}
	lease, acquired, err := service.AcquireTaskWorkerLease(ctx, payload.TaskID, service.TaskWorkerStageSubmit)
	if err != nil {
		return fmt.Errorf("acquire submit lease: %w", err)
	}
	if !acquired {
		return nil
	}
	defer func() {
		if err := lease.Stop(); err != nil && !errors.Is(err, service.ErrTaskWorkerLeaseLost) {
			logger.Error("release submit lease failed", zap.Uint("task_id", payload.TaskID), zap.Error(err))
		}
	}()
	ctx = lease.Context()

	logger.Info("processing submit task", zap.Uint("task_id", payload.TaskID))

	// 1. 获取任务
	task, err := taskService.GetTaskByID(payload.TaskID)
	if err != nil {
		return fmt.Errorf("get task: %w", err)
	}
	checkpoint, err := service.DecodeTaskSubmitCheckpoint(task.SubmitCheckpoint)
	if err != nil {
		return fmt.Errorf("load submit checkpoint: %w", err)
	}
	if checkpoint == nil && task.Status != model.TaskStatusPending {
		return nil
	}
	if checkpoint != nil && checkpoint.LeaseOwner != lease.Owner() {
		if err := taskService.SaveTaskSubmitCheckpoint(task.ID, lease.Owner(), checkpoint); err != nil {
			return fmt.Errorf("adopt submit checkpoint: %w", err)
		}
		checkpoint.LeaseOwner = lease.Owner()
	}
	if checkpoint != nil && checkpoint.IsInFlight() {
		callbackOwned, resolveErr := taskService.ResolveInFlightTaskSubmit(task.ID, lease)
		if callbackOwned || errors.Is(resolveErr, service.ErrTaskSubmitOutcomeUnknown) {
			return nil
		}
		return fmt.Errorf("resolve in-flight submit checkpoint: %w", resolveErr)
	}

	// 2. 获取渠道信息
	channel, account, endpoint, configErr := loadTaskSubmitConfiguration(task)
	if configErr != nil {
		if checkpoint != nil && checkpoint.IsSucceeded() && errors.Is(configErr, gorm.ErrRecordNotFound) {
			return finalizeSucceededSubmitWithoutConfiguration(task, checkpoint)
		}
		return configErr
	}

	var attempt *model.APICallAttempt
	var result provider.SubmitResult
	if checkpoint != nil {
		attempt, err = loadSubmitCheckpointAttempt(task, checkpoint.AttemptID)
		if err != nil {
			return err
		}
		result = submitResultFromCheckpoint(checkpoint)
	} else {
		result, attempt, checkpoint, err = submitTaskUpstream(ctx, lease, task, channel, account, endpoint)
		if err != nil {
			return err
		}
		if checkpoint == nil {
			return nil
		}
	}

	if err := lease.Check(); err != nil {
		terminal, stateErr := taskIsTerminalAfterLeaseLoss(task.ID, err)
		if stateErr != nil {
			return stateErr
		}
		if terminal {
			return nil
		}
	}
	if finishErr := finishCapabilityAttempt(
		task,
		channel,
		endpoint,
		attempt,
		model.APICallStageSubmit,
		result.RequestMetadata,
		nil,
	); finishErr != nil {
		return fmt.Errorf("finish successful submit attempt: %w", finishErr)
	}
	current, err := taskService.GetTaskByID(task.ID)
	if err != nil {
		return fmt.Errorf("reload task after submit attempt: %w", err)
	}
	if current.Status == model.TaskStatusFinalizing || current.Status.IsTerminal() {
		if !current.Status.IsTerminal() {
			if err := taskService.ClearTaskSubmitCheckpoint(task.ID, lease.Owner()); err != nil {
				return fmt.Errorf("clear callback-owned submit checkpoint: %w", err)
			}
		}
		return nil
	}
	if checkpoint.FailureMessage != "" {
		if _, failErr := taskService.UpdateTaskFail(task.ID, checkpoint.FailureMessage); failErr != nil {
			return fmt.Errorf("record submit result processing failure: %w", failErr)
		}
		return nil
	}
	finalCost, err := checkpoint.SettlementCost(task.Cost)
	if err != nil {
		return err
	}

	// 6. 根据交互模式处理结果
	switch endpoint.InteractionMode {
	case model.ModePoll:
		if result.ProviderTaskID == "" {
			if _, failErr := taskService.UpdateTaskFail(task.ID, "upstream returned empty task_id, cannot poll"); failErr != nil {
				return fmt.Errorf("record empty poll task ID: %w", failErr)
			}
			return nil
		}
		if err := taskService.CommitTaskSubmitProcessing(task.ID, lease.Owner(), result.ProviderTaskID); err != nil {
			return fmt.Errorf("mark poll task processing: %w", err)
		}
		interval := endpoint.PollInterval
		if interval <= 0 {
			interval = DefaultPollInterval
		}
		if enqErr := requeueTaskPoll(task.ID, 0, interval); enqErr != nil {
			// Keep the checkpoint so Asynq or the timeout checker can retry the enqueue.
			logger.Error("enqueue poll failed", zap.Uint("task_id", task.ID), zap.Error(enqErr))
			return fmt.Errorf("enqueue initial poll: %w", enqErr)
		}
		if err := taskService.ClearTaskSubmitCheckpoint(task.ID, lease.Owner()); err != nil {
			return fmt.Errorf("clear enqueued poll checkpoint: %w", err)
		}
	case model.ModeCallback:
		if result.ProviderTaskID == "" && len(result.URLs) > 0 {
			urls := append([]string{}, result.URLs...)
			originURL := ""
			if len(urls) > 0 {
				originURL = urls[0]
			}
			successResult := buildResult(originURL, urls)
			if result.RevisedPrompt != "" {
				successResult["revised_prompt"] = result.RevisedPrompt
			}
			if _, err := taskService.UpdateTaskSuccess(task.ID, successResult, finalCost); err != nil {
				return fmt.Errorf("complete callback submit task: %w", err)
			}
			break
		}
		if err := taskService.CommitTaskSubmitProcessing(task.ID, lease.Owner(), result.ProviderTaskID); err != nil {
			return fmt.Errorf("mark callback task processing: %w", err)
		}
		if endpoint.PollPath != "" && result.ProviderTaskID != "" {
			interval := endpoint.PollInterval
			if interval <= 0 {
				interval = DefaultPollInterval
			}
			if err := requeueTaskPoll(task.ID, 0, interval); err != nil {
				return fmt.Errorf("enqueue callback fallback poll: %w", err)
			}
		}
		if err := taskService.ClearTaskSubmitCheckpoint(task.ID, lease.Owner()); err != nil {
			return fmt.Errorf("clear callback submit checkpoint: %w", err)
		}
	default:
		if result.ProviderTaskID == "" && len(result.URLs) == 0 {
			if _, failErr := taskService.UpdateTaskFail(task.ID, "upstream returned empty result"); failErr != nil {
				return fmt.Errorf("record empty upstream result: %w", failErr)
			}
			break
		}
		urls := append([]string{}, result.URLs...)
		successResult := map[string]any{"data": result.ProviderTaskID}
		if len(urls) > 0 {
			successResult["url"] = urls[0]
			successResult["urls"] = urls
		}
		if result.RevisedPrompt != "" {
			successResult["revised_prompt"] = result.RevisedPrompt
		}
		if _, err := taskService.UpdateTaskSuccess(task.ID, successResult, finalCost); err != nil {
			return fmt.Errorf("complete submitted task: %w", err)
		}
	}

	logger.Info("task submitted", zap.Uint("task_id", task.ID), zap.String("vendor_task_id", result.ProviderTaskID))

	return nil
}

func submitTaskUpstream(
	ctx context.Context,
	lease *service.TaskWorkerLease,
	task *model.Task,
	channel *model.Channel,
	account *model.ChannelAccount,
	endpoint *model.Endpoint,
) (provider.SubmitResult, *model.APICallAttempt, *service.TaskSubmitCheckpoint, error) {
	prov, err := newProvider(channel, account, endpoint)
	if err != nil {
		if _, failErr := taskService.UpdateTaskFail(task.ID, "create provider error: "+err.Error()); failErr != nil {
			return provider.SubmitResult{}, nil, nil, fmt.Errorf("record provider creation failure: %w", failErr)
		}
		return provider.SubmitResult{}, nil, nil, nil
	}

	var mappedParams map[string]any
	_ = json.Unmarshal(task.MappedParams, &mappedParams)
	if endpoint.InteractionMode == model.ModeCallback {
		if mappedParams == nil {
			mappedParams = make(map[string]any)
		}
		callbackSecret, err := service.NewChannelService().EnsureCallbackSecret(channel.ID)
		if err != nil {
			if _, failErr := taskService.UpdateTaskFail(task.ID, "prepare callback authentication: "+err.Error()); failErr != nil {
				return provider.SubmitResult{}, nil, nil, fmt.Errorf("record callback authentication failure: %w", failErr)
			}
			return provider.SubmitResult{}, nil, nil, nil
		}
		mappedParams["callback_url"] = auth.BuildSignedCallbackURL(
			config.C.Server.PublicURL,
			channel.Type,
			channel.ID,
			task.TaskNo,
			callbackSecret,
		)
	}

	attempt, err := service.StartCapabilityAttempt(task, endpoint, model.APICallStageSubmit)
	if err != nil {
		return provider.SubmitResult{}, nil, nil, fmt.Errorf("start submit attempt: %w", err)
	}
	attemptID := uint(0)
	if attempt != nil {
		attemptID = attempt.ID
	}
	inFlightCheckpoint := service.NewTaskSubmitInFlightCheckpoint(attemptID, endpoint.InputPrice)
	if err := saveTaskSubmitCheckpoint(task.ID, lease.Owner(), inFlightCheckpoint); err != nil {
		return provider.SubmitResult{}, attempt, nil, fmt.Errorf("save in-flight submit checkpoint: %w", err)
	}
	if err := lease.Check(); err != nil {
		terminal, stateErr := taskIsTerminalAfterLeaseLoss(task.ID, err)
		if stateErr != nil {
			return provider.SubmitResult{}, attempt, nil, stateErr
		}
		if terminal {
			return provider.SubmitResult{}, attempt, nil, nil
		}
	}
	result, submitErr := prov.Submit(ctx, provider.SubmitRequest{
		TaskNo:      task.TaskNo,
		Params:      mappedParams,
		CallbackURL: task.CallbackURL,
	})
	if err := lease.Check(); err != nil {
		terminal, stateErr := taskIsTerminalAfterLeaseLoss(task.ID, err)
		if stateErr != nil {
			return result, attempt, nil, stateErr
		}
		if terminal {
			return result, attempt, nil, nil
		}
	}
	if submitErr != nil {
		if !service.CapabilityRequestReceivedHTTPResponse(result.RequestMetadata, submitErr) {
			if finishErr := finishCapabilityAttempt(
				task,
				channel,
				endpoint,
				attempt,
				model.APICallStageSubmit,
				result.RequestMetadata,
				submitErr,
			); finishErr != nil {
				logger.Error("finish ambiguous submit attempt failed",
					zap.Uint("task_id", task.ID), zap.String("call_id", task.CallID), zap.Error(finishErr))
			}
			callbackOwned, resolveErr := taskService.ResolveInFlightTaskSubmit(task.ID, lease)
			if callbackOwned || errors.Is(resolveErr, service.ErrTaskSubmitOutcomeUnknown) {
				return result, attempt, nil, nil
			}
			return result, attempt, nil, fmt.Errorf("resolve ambiguous submit: %w", resolveErr)
		}
		if clearErr := taskService.ClearTaskSubmitCheckpoint(task.ID, lease.Owner()); clearErr != nil {
			return result, attempt, nil, fmt.Errorf("clear failed submit checkpoint: %w", clearErr)
		}
		current, stateErr := taskService.GetTaskByID(task.ID)
		if stateErr != nil {
			return result, attempt, nil, fmt.Errorf("reload task after failed submit: %w", stateErr)
		}
		if current.Status == model.TaskStatusFinalizing || current.Status.IsTerminal() {
			return result, attempt, nil, nil
		}
		if finishErr := finishCapabilityAttempt(
			task,
			channel,
			endpoint,
			attempt,
			model.APICallStageSubmit,
			result.RequestMetadata,
			submitErr,
		); finishErr != nil {
			return result, attempt, nil, fmt.Errorf("finish failed submit attempt: %w", finishErr)
		}
		if _, failErr := taskService.UpdateTaskSubmitFail(task.ID, "submit error: "+submitErr.Error()); failErr != nil {
			return result, attempt, nil, fmt.Errorf("record submit failure: %w", failErr)
		}
		return result, attempt, nil, nil
	}

	failureMessage := normalizeSubmitCheckpointResult(ctx, task, endpoint, &result)
	checkpoint := &service.TaskSubmitCheckpoint{
		LeaseOwner:      lease.Owner(),
		State:           service.TaskSubmitCheckpointStateSucceeded,
		InteractionMode: endpoint.InteractionMode,
		ProviderTaskID:  result.ProviderTaskID,
		HTTPStatus:      result.RequestMetadata.StatusCode,
		DurationMs:      result.RequestMetadata.DurationMs,
		RequestMethod:   result.RequestMetadata.Method,
		RequestPath:     service.SanitizeCapabilityRequestPath(result.RequestMetadata.RequestPath),
		FailureMessage:  failureMessage,
		FinalCost:       endpoint.InputPrice.String(),
	}
	if !result.RequestMetadata.RequestAt.IsZero() {
		requestAt := result.RequestMetadata.RequestAt
		checkpoint.RequestAt = &requestAt
	}
	if attempt != nil {
		checkpoint.AttemptID = attempt.ID
	}
	if submitUsesImmediateResult(endpoint, &result) && failureMessage == "" {
		checkpoint.URLs = append([]string(nil), result.URLs...)
		checkpoint.RevisedPrompt = result.RevisedPrompt
	}
	if err := saveTaskSubmitCheckpoint(task.ID, lease.Owner(), checkpoint); err != nil {
		current, stateErr := taskService.GetTaskByID(task.ID)
		if stateErr == nil && (current.Status == model.TaskStatusFinalizing || current.Status.IsTerminal()) {
			return result, attempt, nil, nil
		}
		if stateErr != nil {
			return result, attempt, nil, fmt.Errorf("save successful submit checkpoint: %v; reload task: %w", err, stateErr)
		}
		return result, attempt, nil, fmt.Errorf("save successful submit checkpoint: %w", err)
	}
	return result, attempt, checkpoint, nil
}

func loadTaskSubmitConfiguration(task *model.Task) (*model.Channel, *model.ChannelAccount, *model.Endpoint, error) {
	if task == nil {
		return nil, nil, nil, service.ErrTaskNotFound
	}
	var channel model.Channel
	if err := model.DB().First(&channel, task.ChannelID).Error; err != nil {
		return nil, nil, nil, fmt.Errorf("get channel: %w", err)
	}
	var account model.ChannelAccount
	if err := model.DB().First(&account, task.AccountID).Error; err != nil {
		return nil, nil, nil, fmt.Errorf("get account: %w", err)
	}
	var endpoint model.Endpoint
	if err := model.DB().First(&endpoint, task.EndpointID).Error; err != nil {
		return nil, nil, nil, fmt.Errorf("get endpoint: %w", err)
	}
	return &channel, &account, &endpoint, nil
}

func finalizeSucceededSubmitWithoutConfiguration(task *model.Task, checkpoint *service.TaskSubmitCheckpoint) error {
	if task == nil || checkpoint == nil || !checkpoint.IsSucceeded() {
		return errors.New("successful submit checkpoint is required")
	}
	attempt, attemptErr := loadSubmitCheckpointAttempt(task, checkpoint.AttemptID)
	if attemptErr == nil && attempt != nil {
		if err := service.NewAPICallService().CompleteAttempt(attempt.ID, &service.CompleteAttemptRequest{
			HTTPStatus: checkpoint.HTTPStatus, RequestPath: checkpoint.RequestPath,
			DurationMs: checkpoint.DurationMs,
		}); err != nil {
			attemptErr = err
		}
	}
	if attemptErr != nil {
		if _, err := taskService.UpdateTaskFail(task.ID, "task execution configuration was removed after upstream submission"); err != nil {
			return errors.Join(attemptErr, err)
		}
		return nil
	}
	if checkpoint.FailureMessage != "" {
		_, err := taskService.UpdateTaskFail(task.ID, checkpoint.FailureMessage)
		return err
	}
	finalCost, err := checkpoint.SettlementCost(task.Cost)
	if err != nil {
		return err
	}

	urls := append([]string(nil), checkpoint.URLs...)
	canComplete := len(urls) > 0 && checkpoint.InteractionMode != model.ModePoll
	if !canComplete && (checkpoint.InteractionMode == model.ModeSync || checkpoint.InteractionMode == model.ModeStream) {
		canComplete = checkpoint.ProviderTaskID != ""
	}
	if !canComplete {
		_, err := taskService.UpdateTaskFail(task.ID, "task execution configuration was removed after upstream submission")
		return err
	}

	result := map[string]any{"data": checkpoint.ProviderTaskID}
	if len(urls) > 0 {
		result["url"] = urls[0]
		result["urls"] = urls
	}
	if checkpoint.RevisedPrompt != "" {
		result["revised_prompt"] = checkpoint.RevisedPrompt
	}
	_, err = taskService.UpdateTaskSuccess(task.ID, result, finalCost)
	return err
}

func normalizeSubmitCheckpointResult(
	ctx context.Context,
	task *model.Task,
	endpoint *model.Endpoint,
	result *provider.SubmitResult,
) string {
	if result == nil || len(result.B64Data) == 0 || !submitUsesImmediateResult(endpoint, result) {
		return ""
	}
	transferred, err := resolveSubmitB64ToURLs(ctx, result.B64Data, task.ModelCode)
	result.B64Data = nil
	if err != nil {
		return service.SanitizeAPICallErrorMessage(err.Error())
	}
	result.URLs = append(result.URLs, transferred...)
	return ""
}

func submitUsesImmediateResult(endpoint *model.Endpoint, result *provider.SubmitResult) bool {
	if endpoint == nil || result == nil || endpoint.InteractionMode == model.ModePoll {
		return false
	}
	return endpoint.InteractionMode != model.ModeCallback || result.ProviderTaskID == ""
}

func submitResultFromCheckpoint(checkpoint *service.TaskSubmitCheckpoint) provider.SubmitResult {
	result := provider.SubmitResult{
		RequestMetadata: provider.RequestMetadata{
			Method:      checkpoint.RequestMethod,
			RequestPath: checkpoint.RequestPath,
			StatusCode:  checkpoint.HTTPStatus,
			DurationMs:  checkpoint.DurationMs,
		},
		ProviderTaskID: checkpoint.ProviderTaskID,
		URLs:           append([]string(nil), checkpoint.URLs...),
		RevisedPrompt:  checkpoint.RevisedPrompt,
	}
	if checkpoint.RequestAt != nil {
		result.RequestMetadata.RequestAt = *checkpoint.RequestAt
	}
	return result
}

func loadSubmitCheckpointAttempt(task *model.Task, attemptID uint) (*model.APICallAttempt, error) {
	if attemptID == 0 {
		if task.CallID == "" {
			return nil, nil
		}
		return nil, fmt.Errorf("submit checkpoint for call %s has no attempt", task.CallID)
	}

	var attempt model.APICallAttempt
	if err := model.DB().Where(
		"id = ? AND call_id = ? AND route_kind = ? AND stage = ?",
		attemptID,
		task.CallID,
		model.APICallRouteCapability,
		model.APICallStageSubmit,
	).First(&attempt).Error; err != nil {
		return nil, fmt.Errorf("load submit checkpoint attempt: %w", err)
	}
	return &attempt, nil
}

func taskIsTerminalAfterLeaseLoss(taskID uint, leaseErr error) (bool, error) {
	current, err := taskService.GetTaskByID(taskID)
	if err != nil {
		return false, fmt.Errorf("reload task after worker lease loss: %w", errors.Join(leaseErr, err))
	}
	if current.Status.IsTerminal() {
		return true, nil
	}
	return false, fmt.Errorf("task worker lease lost: %w", leaseErr)
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

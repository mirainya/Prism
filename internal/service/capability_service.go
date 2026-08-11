package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider"
	"github.com/mirainya/Prism/pkg/logger"
	"github.com/mirainya/Prism/pkg/queue"
	"github.com/mirainya/Prism/pkg/safeurl"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// InvokeRequest 能力调用请求
type InvokeRequest struct {
	UserID          uint
	TokenID         uint
	CallID          string
	RequestID       string
	Endpoint        string
	Operation       string
	RouteOperation  string
	Capability      string
	Channel         string
	Model           string
	InteractionMode string
	CallbackURL     string
	Params          map[string]any
	// Async=true 时,sync/stream 模式不阻塞等待出图，而是提交到持久队列，
	// 立即返回 task_no，由前端轮询取终态。
	// 仅 playground 置 true;对外 OpenAI 等接口须同步返图,保持 false。
	Async bool
	// EventSink 非 nil 时，SSE 上游的每个事件原始 payload 会写入此通道。
	// 用于 stream=true 的图像接口把进度事件透传给客户端。
	EventSink chan<- []byte
}

// InvokeResponse 能力调用响应
type InvokeResponse struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
	CallID string `json:"-"`
}

var (
	enqueueCallbackUpload              = queue.EnqueueTaskUpload
	enqueueCapabilityTask              = queue.EnqueueTaskSubmit
	enqueueCapabilitySubmitRecovery    = queue.EnqueueTaskSubmit
	finishSynchronousCapabilityAttempt = FinishCapabilityAttempt
	saveSynchronousSubmitCheckpoint    = func(taskID uint, leaseOwner string, checkpoint *TaskSubmitCheckpoint) error {
		return NewTaskService().SaveTaskSubmitCheckpoint(taskID, leaseOwner, checkpoint)
	}
	errNoAvailableAccount    = errors.New("no available account")
	ErrInvalidCallbackURL    = errors.New("invalid callback URL")
	ErrVideoEndpointRequired = errors.New("video generation requires POST /v1/videos/generations")
)

// Invoke 调用能力
func (s *UnifiedService) Invoke(ctx context.Context, req *InvokeRequest) (*InvokeResponse, error) {
	if req == nil {
		return nil, errors.New("invoke request is required")
	}
	ensureInvokeIdentity(req)
	if err := rejectLegacyVideoInvoke(req); err != nil {
		s.recordCapabilityCallFailure(req, err)
		return nil, err
	}
	req.CallbackURL = strings.TrimSpace(req.CallbackURL)
	if err := validateCallbackURL(ctx, req.CallbackURL); err != nil {
		s.recordCapabilityCallFailure(req, err)
		return nil, err
	}
	// 查找该能力所有可用端点(priority 降序),供跨端点/渠道 fallback
	endpoints, err := s.findEndpointsForCapability(req)
	if err != nil {
		s.recordCapabilityCallFailure(req, err)
		return nil, err
	}
	task, chosen, channel, account, err := s.reserveInitialCapabilityTask(req, endpoints)
	if err != nil {
		s.recordCapabilityCallFailure(req, err)
		if errors.Is(err, ErrInsufficientTokenBalance) || errors.Is(err, ErrInsufficientUserBalance) {
			return nil, ErrInsufficientTokenBalance
		}
		return nil, err
	}
	// 仅前台 sync/stream 请求在当前 HTTP 生命周期内执行。Playground 的
	// Async 请求与 poll/callback 一样交给 Worker，进程重启后仍可恢复。
	if (chosen.InteractionMode == model.ModeSync || chosen.InteractionMode == model.ModeStream) && !req.Async {
		return s.executeSyncWithFallback(ctx, task, req, endpoints, chosen, &channel, account)
	}

	// 异步模式入队(跨渠道异步 fallback 属 worker 层,本期不做)
	if err := enqueueCapabilityTask(task.ID); err != nil {
		// Task 行是持久投递意图。Redis 暂时不可用时保留预留和非终态，
		// 启动恢复与定时恢复会使用确定性队列 ID 重新投递。
		logger.Error("enqueue submit failed", zap.Uint("task_id", task.ID), zap.Error(err))
	}

	return &InvokeResponse{
		TaskID: task.TaskNo,
		Status: string(task.Status),
		CallID: req.CallID,
	}, nil
}

func rejectLegacyVideoInvoke(req *InvokeRequest) error {
	if req == nil {
		return nil
	}
	modelCodes := make([]string, 0, 2)
	for _, candidate := range []string{req.Capability, req.Model} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || containsString(modelCodes, candidate) {
			continue
		}
		modelCodes = append(modelCodes, candidate)
	}
	if len(modelCodes) == 0 {
		return nil
	}
	var count int64
	if err := model.DB().Model(&model.Model{}).
		Where("type = ? AND code IN ?", model.ModelTypeVideo, modelCodes).
		Count(&count).Error; err != nil {
		return fmt.Errorf("check capability model type: %w", err)
	}
	if count > 0 {
		return ErrVideoEndpointRequired
	}
	return nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func validateCallbackURL(ctx context.Context, rawURL string) error {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil
	}
	if err := safeurl.Validate(ctx, rawURL); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidCallbackURL, err)
	}
	return nil
}

func ensureInvokeIdentity(req *InvokeRequest) {
	if req == nil {
		return
	}
	if strings.TrimSpace(req.CallID) == "" {
		req.CallID = GenerateAPICallID()
	}
	if strings.TrimSpace(req.RequestID) == "" {
		req.RequestID = GenerateRequestID()
	}
}

func capabilityStartCallRequest(req *InvokeRequest, resourceID string, background bool) *StartCallRequest {
	callModel := strings.TrimSpace(req.Model)
	if callModel == "" {
		callModel = strings.TrimSpace(req.Capability)
	}
	endpoint := strings.TrimSpace(req.Endpoint)
	if endpoint == "" {
		endpoint = "/v1/capabilities/" + strings.TrimSpace(req.Capability)
	}
	operation := strings.TrimSpace(req.Operation)
	if operation == "" {
		operation = "capability.invoke"
	}
	start := &StartCallRequest{
		ID:         req.CallID,
		RequestID:  req.RequestID,
		UserID:     req.UserID,
		TokenID:    req.TokenID,
		Endpoint:   endpoint,
		Operation:  operation,
		Model:      callModel,
		Background: background,
	}
	if resourceID != "" {
		start.ResourceType = "task"
		start.ResourceID = resourceID
	}
	return start
}

func capabilityPricingSnapshot(req *InvokeRequest, endpoint *model.Endpoint, cost decimal.Decimal) datatypes.JSON {
	if endpoint == nil {
		return nil
	}
	value, err := json.Marshal(map[string]any{
		"capability":       strings.TrimSpace(req.Capability),
		"model":            strings.TrimSpace(req.Model),
		"route_operation":  strings.TrimSpace(req.RouteOperation),
		"endpoint_id":      endpoint.ID,
		"model_code":       endpoint.ModelCode,
		"vendor_model":     endpoint.VendorModel,
		"price_mode":       endpoint.PriceMode,
		"input_price":      endpoint.InputPrice.String(),
		"output_price":     endpoint.OutputPrice.String(),
		"reserved_cost":    cost.String(),
		"fallback_pricing": "initial_selected_endpoint",
	})
	if err != nil {
		return nil
	}
	return datatypes.JSON(value)
}

func (s *UnifiedService) recordCapabilityCallFailure(req *InvokeRequest, cause error) {
	if req == nil || cause == nil {
		return
	}
	httpStatus := 500
	errorType := "capability_error"
	errorCode := "capability_failed"
	if errors.Is(cause, ErrInvalidCallbackURL) {
		httpStatus = 400
		errorType = "invalid_request_error"
		errorCode = "invalid_callback_url"
	} else if errors.Is(cause, ErrVideoEndpointRequired) {
		httpStatus = 400
		errorType = "invalid_request_error"
		errorCode = "video_endpoint_required"
	} else if errors.Is(cause, ErrInsufficientTokenBalance) || errors.Is(cause, ErrInsufficientUserBalance) {
		httpStatus = 400
		errorType = "billing_error"
		errorCode = "insufficient_quota"
	} else if errors.Is(cause, errNoAvailableAccount) ||
		strings.Contains(cause.Error(), "no available endpoint") ||
		strings.Contains(cause.Error(), "no available channel") {
		errorCode = "model_unavailable"
	}

	err := model.DB().Transaction(func(tx *gorm.DB) error {
		if _, err := NewAPICallService().StartCallTx(
			tx,
			capabilityStartCallRequest(req, "", req.Async),
		); err != nil {
			return err
		}
		return NewAPICallService().FailCallTx(tx, req.CallID, &FailCallRequest{
			HTTPStatus:   httpStatus,
			ErrorType:    errorType,
			ErrorCode:    errorCode,
			ErrorMessage: cause.Error(),
		})
	})
	if err != nil {
		logger.Error("record capability call failure failed",
			zap.String("call_id", req.CallID), zap.Error(err))
	}
}

func (s *UnifiedService) reserveInitialCapabilityTask(
	req *InvokeRequest,
	endpoints []model.Endpoint,
) (*model.Task, *model.Endpoint, model.Channel, *model.ChannelAccount, error) {
	ensureInvokeIdentity(req)
	taskNo := GenerateTaskNo()
	var task *model.Task
	var chosen *model.Endpoint
	var channel model.Channel
	var account *model.ChannelAccount

	// Call、余额预授权、Task 和账号并发占用必须一起成功，避免产生无任务扣款或无扣款任务。
	err := model.DB().Transaction(func(tx *gorm.DB) error {
		for i := range endpoints {
			ep := &endpoints[i]
			var candidateChannel model.Channel
			if err := tx.First(&candidateChannel, ep.ChannelID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					continue
				}
				return err
			}
			candidateAccount, err := s.selectAccountForEndpointTx(tx, ep, nil)
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					continue
				}
				return err
			}
			chosen = ep
			channel = candidateChannel
			account = candidateAccount
			break
		}
		if chosen == nil {
			return errNoAvailableAccount
		}
		background := req.Async || chosen.InteractionMode == model.ModePoll || chosen.InteractionMode == model.ModeCallback
		if _, err := NewAPICallService().StartCallTx(
			tx,
			capabilityStartCallRequest(req, taskNo, background),
		); err != nil {
			return err
		}
		cost := chosen.InputPrice
		adapterID, adapterRevisionID, endpointSnapshot, err := snapshotEndpointExecutionTx(tx, chosen)
		if err != nil {
			return fmt.Errorf("snapshot endpoint execution: %w", err)
		}
		if err := s.billingService.deductWithBillingContextTx(
			tx, req.TokenID, req.UserID, cost, taskNo+":reserve", BillingContext{
				CallID:          req.CallID,
				Phase:           model.BillingPhaseReserve,
				PricingSnapshot: capabilityPricingSnapshot(req, chosen, cost),
			},
		); err != nil {
			return err
		}

		task, err = NewTaskService().createTask(tx, &CreateTaskRequest{
			TaskNo:            taskNo,
			CallID:            req.CallID,
			UserID:            req.UserID,
			TokenID:           req.TokenID,
			ModelCode:         req.Capability,
			RouteOperation:    req.RouteOperation,
			ChannelID:         channel.ID,
			EndpointID:        chosen.ID,
			AccountID:         account.ID,
			AdapterID:         adapterID,
			AdapterRevisionID: adapterRevisionID,
			EndpointSnapshot:  endpointSnapshot,
			RequestParams:     req.Params,
			MappedParams:      mapParams(req.Params, chosen.ParamMapping),
			CallbackURL:       req.CallbackURL,
			Cost:              cost,
		})
		return err
	})
	if err != nil {
		return nil, nil, model.Channel{}, nil, err
	}
	logTaskCreated(task)
	return task, chosen, channel, account, nil
}

// executeSyncWithFallback 同步执行任务:
//
//	外层遍历 sync/stream 端点(跨渠道 fallback),内层对每个端点渠道换账号重试(熔断驱动)。
//	startEP/startCh/startAcc 是调用方已选定的首选端点/渠道/账号(已建入 task,首轮直接复用)。
func (s *UnifiedService) executeSyncWithFallback(ctx context.Context, task *model.Task, req *InvokeRequest, endpoints []model.Endpoint, startEP *model.Endpoint, startCh *model.Channel, startAcc *model.ChannelAccount) (*InvokeResponse, error) {
	taskSvc := NewTaskService()
	circuitSvc := NewAccountCircuitService()
	lease, acquired, err := AcquireTaskWorkerLease(ctx, task.ID, TaskWorkerStageSubmit)
	if err != nil {
		return nil, fmt.Errorf("acquire synchronous submit lease: %w", err)
	}
	if !acquired {
		return nil, ErrTaskNotExecutable
	}
	defer func() {
		if lease == nil {
			return
		}
		if stopErr := lease.Stop(); stopErr != nil && !errors.Is(stopErr, ErrTaskWorkerLeaseLost) {
			logger.Error("release synchronous submit lease failed",
				zap.Uint("task_id", task.ID), zap.Error(stopErr))
		}
	}()
	ctx = lease.Context()
	currentTask, err := taskSvc.GetTaskByID(task.ID)
	if err != nil {
		return nil, fmt.Errorf("reload synchronous capability task: %w", err)
	}
	task = currentTask
	checkpoint, err := DecodeTaskSubmitCheckpoint(task.SubmitCheckpoint)
	if err != nil {
		return nil, fmt.Errorf("load synchronous submit checkpoint: %w", err)
	}
	if checkpoint != nil {
		// 检查点表示上游提交已经开始或成功；恢复时先判定旧结果，不能直接发起第二次提交。
		if checkpoint.LeaseOwner != lease.Owner() {
			if err := taskSvc.SaveTaskSubmitCheckpoint(task.ID, lease.Owner(), checkpoint); err != nil {
				return nil, fmt.Errorf("adopt synchronous submit checkpoint: %w", err)
			}
		}
		if checkpoint.IsInFlight() {
			callbackOwned, resolveErr := taskSvc.ResolveInFlightTaskSubmit(task.ID, lease)
			if callbackOwned {
				return nil, ErrTaskNotExecutable
			}
			if errors.Is(resolveErr, ErrTaskSubmitOutcomeUnknown) {
				return nil, resolveErr
			}
			return nil, recoverSynchronousSubmit(task.ID, &lease,
				fmt.Errorf("resolve in-flight synchronous submit: %w", resolveErr))
		}
		return nil, recoverSynchronousSubmit(task.ID, &lease,
			fmt.Errorf("synchronous submit already succeeded; queued task recovery"))
	}
	accountPerEndpoint := 3
	var lastErr error
	started := false

	// 外层按端点优先级切换渠道，内层最多尝试三个未熔断账号。
	for i := range endpoints {
		ep := &endpoints[i]
		if ep.InteractionMode != model.ModeSync && ep.InteractionMode != model.ModeStream {
			continue
		}
		if ep.ID == startEP.ID {
			started = true
		}
		if !started {
			continue
		}

		var channel model.Channel
		var account *model.ChannelAccount
		isStart := ep.ID == startEP.ID
		if isStart {
			channel, account = *startCh, startAcc
		} else {
			if err := model.DB().First(&channel, ep.ChannelID).Error; err != nil {
				continue
			}
		}

		mappedParams := mapParams(req.Params, ep.ParamMapping)
		var excludeAccountIDs []uint

		for attempt := 0; attempt < accountPerEndpoint; attempt++ {
			if account == nil {
				next, err := s.selectAndAssignAccountForEndpoint(task.ID, ep, excludeAccountIDs)
				if err != nil {
					if errors.Is(err, ErrTaskNotExecutable) {
						return nil, err
					}
					if errors.Is(err, gorm.ErrRecordNotFound) {
						break
					}
					if _, failErr := taskSvc.UpdateTaskFail(task.ID, "select fallback account: "+err.Error()); failErr != nil {
						return nil, fmt.Errorf("select fallback account: %v; record task failure: %w", err, failErr)
					}
					return nil, fmt.Errorf("select fallback account: %w", err)
				}
				account = next
				task.EndpointID = ep.ID
				task.ChannelID = channel.ID
				task.AccountID = account.ID
				task.AccountSlotReleased = false
			}

			if err := s.ensureTaskAccountExecutable(task.ID, ep, account.ID); err != nil {
				if errors.Is(err, ErrTaskNotExecutable) {
					return nil, err
				}
				if _, failErr := taskSvc.UpdateTaskFail(task.ID, "validate task execution account: "+err.Error()); failErr != nil {
					return nil, fmt.Errorf("validate task execution account: %v; record task failure: %w", err, failErr)
				}
				return nil, fmt.Errorf("validate task execution account: %w", err)
			}

			result, callAttempt, inFlightCheckpoint, submitErr := s.doSubmit(
				ctx, lease, task, ep, &channel, account, mappedParams, req.EventSink,
			)
			if inFlightCheckpoint != nil {
				if leaseErr := lease.Check(); leaseErr != nil {
					current, stateErr := taskSvc.GetTaskByID(task.ID)
					if stateErr != nil {
						return nil, recoverSynchronousSubmit(task.ID, &lease,
							fmt.Errorf("reload task after synchronous submit lease loss: %w", errors.Join(leaseErr, stateErr)))
					}
					if current.Status == model.TaskStatusFinalizing || current.Status.IsTerminal() {
						return nil, ErrTaskNotExecutable
					}
					return nil, recoverSynchronousSubmit(task.ID, &lease,
						fmt.Errorf("synchronous submit lease lost: %w", leaseErr))
				}
			}
			if submitErr != nil {
				if inFlightCheckpoint != nil && !CapabilityRequestReceivedHTTPResponse(result.RequestMetadata, submitErr) {
					if callAttempt != nil {
						if finishErr := finishSynchronousCapabilityAttempt(
							task,
							&channel,
							ep,
							callAttempt,
							model.APICallStageSubmit,
							result.RequestMetadata,
							submitErr,
						); finishErr != nil {
							logger.Error("finish ambiguous synchronous submit attempt failed",
								zap.Uint("task_id", task.ID), zap.String("call_id", task.CallID), zap.Error(finishErr))
						}
					}
					callbackOwned, resolveErr := taskSvc.ResolveInFlightTaskSubmit(task.ID, lease)
					if callbackOwned {
						return nil, ErrTaskNotExecutable
					}
					if errors.Is(resolveErr, ErrTaskSubmitOutcomeUnknown) {
						return nil, resolveErr
					}
					return nil, recoverSynchronousSubmit(task.ID, &lease,
						fmt.Errorf("resolve ambiguous synchronous submit: %w", resolveErr))
				}
				if inFlightCheckpoint != nil {
					if clearErr := taskSvc.ClearTaskSubmitCheckpoint(task.ID, lease.Owner()); clearErr != nil {
						return nil, recoverSynchronousSubmit(task.ID, &lease,
							fmt.Errorf("clear failed synchronous submit checkpoint: %w", clearErr))
					}
					current, stateErr := taskSvc.GetTaskByID(task.ID)
					if stateErr != nil {
						return nil, fmt.Errorf("reload task after failed synchronous submit: %w", stateErr)
					}
					if current.Status == model.TaskStatusFinalizing || current.Status.IsTerminal() {
						return nil, ErrTaskNotExecutable
					}
				}
				if callAttempt != nil {
					if finishErr := finishSynchronousCapabilityAttempt(
						task,
						&channel,
						ep,
						callAttempt,
						model.APICallStageSubmit,
						result.RequestMetadata,
						submitErr,
					); finishErr != nil {
						logger.Error("finish failed synchronous submit attempt",
							zap.Uint("task_id", task.ID), zap.String("call_id", task.CallID), zap.Error(finishErr))
					}
				}
				lastErr = submitErr
				if releaseErr := taskSvc.ReleaseAccountSlot(task.ID); releaseErr != nil {
					return nil, fmt.Errorf("upstream call failed: %v; release account slot: %w", submitErr, releaseErr)
				}
				task.AccountSlotReleased = true
				circuitSvc.MarkUnavailable(account.ID, ep.ModelCode, submitErr)
				excludeAccountIDs = append(excludeAccountIDs, account.ID)
				logger.Warn("sync attempt failed, trying next account/endpoint",
					zap.Uint("task_id", task.ID), zap.Uint("endpoint_id", ep.ID),
					zap.Uint("account_id", account.ID), zap.Error(submitErr))
				account = nil
				continue
			}

			if result.ProviderTaskID == "" && len(result.URLs) == 0 && len(result.B64Data) == 0 {
				lastErr = fmt.Errorf("upstream returned empty result")
				if clearErr := taskSvc.ClearTaskSubmitCheckpoint(task.ID, lease.Owner()); clearErr != nil {
					return nil, recoverSynchronousSubmit(task.ID, &lease,
						fmt.Errorf("clear empty synchronous submit checkpoint: %w", clearErr))
				}
				current, stateErr := taskSvc.GetTaskByID(task.ID)
				if stateErr != nil {
					return nil, fmt.Errorf("reload task after empty synchronous submit: %w", stateErr)
				}
				if current.Status == model.TaskStatusFinalizing || current.Status.IsTerminal() {
					return nil, ErrTaskNotExecutable
				}
				if finishErr := finishSynchronousCapabilityAttempt(
					task,
					&channel,
					ep,
					callAttempt,
					model.APICallStageSubmit,
					result.RequestMetadata,
					lastErr,
				); finishErr != nil {
					logger.Error("finish empty synchronous submit attempt failed",
						zap.Uint("task_id", task.ID), zap.String("call_id", task.CallID), zap.Error(finishErr))
				}
				if releaseErr := taskSvc.ReleaseAccountSlot(task.ID); releaseErr != nil {
					return nil, fmt.Errorf("empty upstream result; release account slot: %w", releaseErr)
				}
				task.AccountSlotReleased = true
				circuitSvc.MarkUnavailable(account.ID, ep.ModelCode, lastErr)
				excludeAccountIDs = append(excludeAccountIDs, account.ID)
				logger.Warn("sync attempt returned empty result, trying next account/endpoint",
					zap.Uint("task_id", task.ID), zap.Uint("endpoint_id", ep.ID),
					zap.Uint("account_id", account.ID), zap.Error(lastErr))
				account = nil
				continue
			}

			urls := append([]string{}, result.URLs...)
			var resultProcessingErr error
			if len(result.B64Data) > 0 {
				transferred, err := resolveB64ToURLs(ctx, result.B64Data, ep.ModelCode)
				if err != nil {
					resultProcessingErr = err
				} else {
					urls = append(urls, transferred...)
				}
			}

			checkpoint := synchronousSubmitCheckpoint(
				lease.Owner(), callAttempt, result, urls, ep.InteractionMode, ep.InputPrice, resultProcessingErr,
			)
			if err := saveSynchronousSubmitCheckpoint(task.ID, lease.Owner(), checkpoint); err != nil {
				return nil, recoverSynchronousSubmit(task.ID, &lease,
					fmt.Errorf("save successful synchronous submit checkpoint: %w", err))
			}
			if finishErr := finishSynchronousCapabilityAttempt(
				task,
				&channel,
				ep,
				callAttempt,
				model.APICallStageSubmit,
				result.RequestMetadata,
				nil,
			); finishErr != nil {
				return nil, recoverSynchronousSubmit(task.ID, &lease,
					fmt.Errorf("finish successful synchronous submit attempt: %w", finishErr))
			}
			if resultProcessingErr != nil {
				if _, failErr := taskSvc.UpdateTaskFail(task.ID, resultProcessingErr.Error()); failErr != nil {
					return nil, recoverSynchronousSubmit(task.ID, &lease,
						fmt.Errorf("transfer result: %v; record task failure: %w", resultProcessingErr, failErr))
				}
				return nil, resultProcessingErr
			}

			successResult := map[string]any{"data": result.ProviderTaskID}
			if len(urls) > 0 {
				successResult["url"] = urls[0]
				successResult["urls"] = urls
			}
			if result.RevisedPrompt != "" {
				successResult["revised_prompt"] = result.RevisedPrompt
			}

			// Billing is settled against the endpoint that actually produced the result.
			committed, err := taskSvc.UpdateTaskSuccess(task.ID, successResult, ep.InputPrice)
			if err != nil {
				return nil, recoverSynchronousSubmit(task.ID, &lease,
					fmt.Errorf("complete synchronous task: %w", err))
			}
			if !committed {
				return nil, ErrTaskNotExecutable
			}
			return &InvokeResponse{
				TaskID: task.TaskNo,
				Status: string(model.TaskStatusSuccess),
				CallID: req.CallID,
			}, nil
		}
		account = nil
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("no available endpoint/account for capability: %s", req.Capability)
	}
	if _, failErr := taskSvc.UpdateTaskFail(task.ID, lastErr.Error()); failErr != nil {
		return nil, fmt.Errorf("%v; record task failure: %w", lastErr, failErr)
	}
	return nil, lastErr
}

func synchronousSubmitCheckpoint(
	leaseOwner string,
	attempt *model.APICallAttempt,
	result provider.SubmitResult,
	urls []string,
	interactionMode model.InteractionMode,
	finalCost decimal.Decimal,
	resultErr error,
) *TaskSubmitCheckpoint {
	checkpoint := &TaskSubmitCheckpoint{
		LeaseOwner:      leaseOwner,
		State:           TaskSubmitCheckpointStateSucceeded,
		InteractionMode: interactionMode,
		ProviderTaskID:  result.ProviderTaskID,
		URLs:            append([]string(nil), urls...),
		RevisedPrompt:   result.RevisedPrompt,
		HTTPStatus:      result.RequestMetadata.StatusCode,
		DurationMs:      result.RequestMetadata.DurationMs,
		RequestMethod:   result.RequestMetadata.Method,
		RequestPath:     SanitizeCapabilityRequestPath(result.RequestMetadata.RequestPath),
		FinalCost:       finalCost.String(),
	}
	if attempt != nil {
		checkpoint.AttemptID = attempt.ID
	}
	if !result.RequestMetadata.RequestAt.IsZero() {
		requestAt := result.RequestMetadata.RequestAt
		checkpoint.RequestAt = &requestAt
	}
	if resultErr != nil {
		checkpoint.FailureMessage = SanitizeAPICallErrorMessage(resultErr.Error())
	}
	return checkpoint
}

func recoverSynchronousSubmit(taskID uint, lease **TaskWorkerLease, cause error) error {
	recoveryErr := cause
	if lease != nil && *lease != nil {
		if stopErr := (*lease).Stop(); stopErr != nil && !errors.Is(stopErr, ErrTaskWorkerLeaseLost) {
			recoveryErr = fmt.Errorf("%v; release synchronous submit lease: %w", recoveryErr, stopErr)
		}
		*lease = nil
	}
	if err := enqueueCapabilitySubmitRecovery(taskID); err != nil {
		return fmt.Errorf("%v; enqueue synchronous submit recovery: %w", recoveryErr, err)
	}
	return recoveryErr
}

// doSubmit executes one upstream submit without changing task state or account counters.
func (s *UnifiedService) doSubmit(ctx context.Context, lease *TaskWorkerLease, task *model.Task, endpoint *model.Endpoint, channel *model.Channel, account *model.ChannelAccount, mappedParams map[string]any, eventSink chan<- []byte) (provider.SubmitResult, *model.APICallAttempt, *TaskSubmitCheckpoint, error) {
	// multipart 端点：将参数中的文件 URL 下载并转为 @base64:filename:data 格式
	resolvedParams, err := resolveFileParams(ctx, mappedParams, endpoint, endpoint.ModelCode)
	if err != nil {
		return provider.SubmitResult{}, nil, nil, fmt.Errorf("resolve file params: %w", err)
	}

	prov, err := provider.NewProvider(channel, account, endpoint)
	if err != nil {
		return provider.SubmitResult{}, nil, nil, fmt.Errorf("create provider error: %w", err)
	}
	attempt, err := StartCapabilityAttempt(task, endpoint, model.APICallStageSubmit)
	if err != nil {
		return provider.SubmitResult{}, nil, nil, fmt.Errorf("start submit attempt: %w", err)
	}
	attemptID := uint(0)
	if attempt != nil {
		attemptID = attempt.ID
	}
	checkpoint := NewTaskSubmitInFlightCheckpoint(attemptID, endpoint.InputPrice)
	if err := NewTaskService().SaveTaskSubmitCheckpoint(task.ID, lease.Owner(), checkpoint); err != nil {
		return provider.SubmitResult{}, attempt, nil, fmt.Errorf("save in-flight submit checkpoint: %w", err)
	}
	if err := lease.Check(); err != nil {
		return provider.SubmitResult{}, attempt, checkpoint, fmt.Errorf("verify submit lease: %w", err)
	}
	result, submitErr := prov.Submit(ctx, provider.SubmitRequest{
		TaskNo:    task.TaskNo,
		Params:    resolvedParams,
		EventSink: eventSink,
	})
	return result, attempt, checkpoint, submitErr
}

// GetTask 获取任务状态
func (s *UnifiedService) GetTask(ctx context.Context, taskNo string, userID uint) (*model.Task, error) {
	taskSvc := NewTaskService()
	return taskSvc.GetTaskByNoAndUser(taskNo, userID)
}

// GetTaskForToken applies the token ownership boundary used by Playground.
// Public task APIs intentionally retain their existing user-level semantics.
func (s *UnifiedService) GetTaskForToken(ctx context.Context, taskNo string, userID, tokenID uint) (*model.Task, error) {
	taskSvc := NewTaskService()
	return taskSvc.GetTaskByNoUserAndToken(taskNo, userID, tokenID)
}

// CancelTask 取消任务
func (s *UnifiedService) CancelTask(ctx context.Context, taskNo string, userID uint) error {
	taskSvc := NewTaskService()
	return taskSvc.CancelTask(taskNo, userID)
}

func (s *UnifiedService) CancelTaskForToken(ctx context.Context, taskNo string, userID, tokenID uint) error {
	taskSvc := NewTaskService()
	return taskSvc.CancelTaskByToken(taskNo, userID, tokenID)
}

// HandleCallback 处理供应商回调
func (s *UnifiedService) HandleCallback(ctx context.Context, authenticatedTask *model.Task, body map[string]any) error {
	if authenticatedTask == nil || authenticatedTask.ID == 0 {
		return errors.New("missing authenticated callback task")
	}
	taskSvc := NewTaskService()
	task, err := taskSvc.GetTaskByID(authenticatedTask.ID)
	if err != nil {
		return err
	}
	if task.TaskNo != authenticatedTask.TaskNo || task.ChannelID != authenticatedTask.ChannelID {
		return ErrTaskNotFound
	}
	if task.Status.IsTerminal() {
		return nil
	}

	var endpoint model.Endpoint
	if err := model.DB().First(&endpoint, task.EndpointID).Error; err != nil {
		return err
	}
	if err := ApplyTaskEndpointSnapshot(task, &endpoint); err != nil {
		return err
	}
	if endpoint.ChannelID != task.ChannelID {
		return errors.New("callback endpoint does not belong to task channel")
	}

	// 用配置驱动的解析器解析回调（读取 response/callback mapping 的 value_mapping、url、error）
	var channel model.Channel
	if err := model.DB().First(&channel, task.ChannelID).Error; err != nil {
		return err
	}
	var account model.ChannelAccount
	if err := model.DB().First(&account, task.AccountID).Error; err != nil {
		return err
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return err
	}
	prov, err := provider.NewProvider(&channel, &account, &endpoint)
	if err != nil {
		return err
	}
	parsed, providerTaskID, err := prov.ParseCallback(ctx, bodyBytes)
	if err != nil {
		return err
	}
	switch parsed.Status {
	case provider.StatusFail, provider.StatusSuccess,
		provider.StatusPending, provider.StatusSubmitted, provider.StatusProcessing:
	default:
		return ErrInvalidCallbackStatus
	}
	if providerTaskID != "" {
		if task.VendorTaskID != "" && task.VendorTaskID != providerTaskID {
			return ErrVendorTaskMismatch
		}
		if err := taskSvc.BindVendorTaskID(task.ID, providerTaskID); err != nil {
			return err
		}
	}
	if err := AcknowledgeCapabilitySubmitAttempt(task); err != nil {
		return fmt.Errorf("acknowledge submit attempt from callback: %w", err)
	}

	switch parsed.Status {
	case provider.StatusFail:
		errMsg := parsed.Error
		if errMsg == "" {
			errMsg = "upstream task failed"
		}
		_, err := taskSvc.UpdateTaskFail(task.ID, errMsg)
		if err != nil {
			return err
		}
	case provider.StatusSuccess:
		// 复用上传流水线（转存+通知），与轮询成功路径保持一致
		originURLs := append([]string{}, parsed.URLs...)
		originURLs = append(originURLs, parsed.B64Data...)
		originURL := ""
		if len(originURLs) > 0 {
			originURL = originURLs[0]
		}
		ready, err := taskSvc.BeginTaskFinalization(task.ID)
		if err != nil {
			return err
		}
		if !ready {
			return nil
		}
		if err := enqueueCallbackUpload(task.ID, originURL, originURLs, parsed.RevisedPrompt); err != nil {
			logger.Error("enqueue upload from callback failed", zap.Uint("task_id", task.ID), zap.Error(err))
			return fmt.Errorf("enqueue upload from callback: %w", err)
		}
		// 入队成功: 终态与账号计数递减由 upload_worker 统一处理,此处不再递减(避免重复)
	case provider.StatusPending, provider.StatusSubmitted, provider.StatusProcessing:
		// 处理中：仅更新进度，不结算
		if err := taskSvc.UpdateTaskStatus(task.ID, model.TaskStatusProcessing, providerTaskID); err != nil {
			return err
		}
		if err := taskSvc.UpdateTaskProgress(task.ID, parsed.Progress); err != nil {
			return err
		}
	}

	return nil
}

// findEndpointsForCapability 返回该能力所有可用端点,按 priority 降序(供跨端点/渠道 fallback)
func (s *UnifiedService) findEndpointsForCapability(req *InvokeRequest) ([]model.Endpoint, error) {
	var requestedChannelID uint
	if req.Channel != "" {
		var channel model.Channel
		err := model.DB().Where("type = ? AND status = 1", req.Channel).First(&channel).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("no available channel: %s", req.Channel)
		}
		if err != nil {
			return nil, fmt.Errorf("find requested channel: %w", err)
		}
		requestedChannelID = channel.ID
	}
	buildQuery := func() *gorm.DB {
		query := model.DB().Where("status = 1")
		if requestedChannelID != 0 {
			query = query.Where("channel_id = ?", requestedChannelID)
		}
		if req.InteractionMode != "" {
			query = query.Where("interaction_mode = ?", req.InteractionMode)
		}
		return query
	}

	var endpoints []model.Endpoint
	requestedModel := strings.TrimSpace(req.Model)
	capability := strings.TrimSpace(req.Capability)
	if requestedModel != "" && requestedModel != capability {
		findRequestedModel := func(modelName string) error {
			endpoints = nil
			err := buildQuery().
				Where("(model_code = ? OR vendor_model = ?)", modelName, modelName).
				Order("priority DESC, id ASC").
				Find(&endpoints).Error
			if err != nil {
				return err
			}
			endpoints = filterEndpointsByRouteOperation(endpoints, req.RouteOperation)
			return nil
		}
		if err := findRequestedModel(requestedModel); err != nil {
			return nil, fmt.Errorf("find endpoints for requested model: %w", err)
		}
		if len(endpoints) > 0 {
			return endpoints, nil
		}

		if aliasTarget, err := findConfiguredModelAlias(requestedModel); err != nil {
			return nil, fmt.Errorf("find configured model alias: %w", err)
		} else if aliasTarget != "" {
			if err := findRequestedModel(aliasTarget); err != nil {
				return nil, fmt.Errorf("find endpoints for configured model alias: %w", err)
			}
			if len(endpoints) > 0 {
				return endpoints, nil
			}
		}
	}

	if err := buildQuery().Where("model_code = ?", capability).Order("priority DESC, id ASC").Find(&endpoints).Error; err != nil {
		return nil, fmt.Errorf("find endpoints for capability: %w", err)
	}
	endpoints = filterEndpointsByRouteOperation(endpoints, req.RouteOperation)
	if len(endpoints) == 0 {
		if requestedModel != "" {
			return nil, fmt.Errorf("no available endpoint for model: %s", requestedModel)
		}
		return nil, fmt.Errorf("no available endpoint for capability: %s", capability)
	}
	return endpoints, nil
}

func filterEndpointsByRouteOperation(endpoints []model.Endpoint, routeOperation string) []model.Endpoint {
	routeOperation = strings.TrimSpace(routeOperation)
	if routeOperation == "" {
		return endpoints
	}
	filtered := make([]model.Endpoint, 0, len(endpoints))
	for i := range endpoints {
		if endpointSupportsRouteOperation(&endpoints[i], routeOperation) {
			filtered = append(filtered, endpoints[i])
		}
	}
	return filtered
}

func endpointSupportsRouteOperation(endpoint *model.Endpoint, routeOperation string) bool {
	if endpoint == nil {
		return false
	}
	routeOperation = strings.TrimSpace(routeOperation)
	if declared := endpointDeclaredRouteOperations(endpoint); len(declared) > 0 {
		for _, operation := range declared {
			if operation == routeOperation {
				return true
			}
		}
		return false
	}
	switch routeOperation {
	case RouteOperationImagesEdit:
		return endpointImageEditPath(endpoint) || endpoint.ImageEdit() != nil
	case RouteOperationImagesGenerate:
		return !endpointImageEditPath(endpoint)
	default:
		return true
	}
}

func endpointImageEditPath(endpoint *model.Endpoint) bool {
	path := strings.TrimSpace(endpoint.RequestPath)
	if index := strings.IndexByte(path, '?'); index >= 0 {
		path = path[:index]
	}
	path = strings.TrimRight(strings.ToLower(path), "/")
	return path == "/v1/images/edits" || strings.HasSuffix(path, "/images/edits")
}

func findConfiguredModelAlias(requestedModel string) (string, error) {
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" {
		return "", nil
	}

	var models []model.Model
	if err := model.DB().Where("status = 1").Order("sort DESC, code ASC").Find(&models).Error; err != nil {
		return "", err
	}
	matchedCode := ""
	for _, candidate := range models {
		var aliases []string
		if len(candidate.Aliases) == 0 || json.Unmarshal(candidate.Aliases, &aliases) != nil {
			continue
		}
		for _, alias := range aliases {
			if strings.EqualFold(strings.TrimSpace(alias), requestedModel) {
				if matchedCode != "" && matchedCode != candidate.Code {
					return "", fmt.Errorf("model alias %q is configured for multiple models", requestedModel)
				}
				matchedCode = candidate.Code
			}
		}
	}
	return matchedCode, nil
}

func mapParams(params map[string]any, mapping datatypes.JSON) map[string]any {
	if len(mapping) == 0 {
		return params
	}

	var structured struct {
		FieldMapping   map[string]string `json:"field_mapping"`
		FixedParams    map[string]any    `json:"fixed_params"`
		ComputedParams map[string]string `json:"computed_params"`
	}
	if err := json.Unmarshal(mapping, &structured); err == nil && (structured.FieldMapping != nil || structured.FixedParams != nil || structured.ComputedParams != nil) {
		result := make(map[string]any)
		if structured.FieldMapping != nil {
			for k, v := range params {
				if mapped, ok := structured.FieldMapping[k]; ok {
					result[mapped] = v
				} else {
					result[k] = v
				}
			}
		} else {
			for k, v := range params {
				result[k] = v
			}
		}
		for k, v := range structured.FixedParams {
			if _, exists := result[k]; !exists {
				result[k] = v
			}
		}
		// 计算参数: 用模板从原始参数拼接目标字段(如 "{width}x{height}" → size)
		// 仅当模板引用的所有字段都存在时才生成,避免产出残缺值
		for targetField, template := range structured.ComputedParams {
			if computed, ok := computeParam(template, params); ok {
				result[targetField] = computed
			}
		}
		return result
	}

	var m map[string]string
	if err := json.Unmarshal(mapping, &m); err != nil {
		return params
	}
	result := make(map[string]any)
	for k, v := range params {
		if mapped, ok := m[k]; ok {
			result[mapped] = v
		} else {
			result[k] = v
		}
	}
	return result
}

// computeParamPattern 匹配模板中的 {field} 占位符
var computeParamPattern = regexp.MustCompile(`\{(\w+)\}`)

// computeParam 用原始参数填充模板占位符(如 "{width}x{height}")
// 仅当模板引用的所有字段都存在时返回 (结果, true); 缺任一字段返回 ("", false)
func computeParam(template string, params map[string]any) (string, bool) {
	complete := true
	result := computeParamPattern.ReplaceAllStringFunc(template, func(match string) string {
		key := match[1 : len(match)-1]
		if val, ok := params[key]; ok {
			return fmt.Sprintf("%v", val)
		}
		complete = false
		return ""
	})
	if !complete {
		return "", false
	}
	return result, true
}

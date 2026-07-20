package responses

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/gateway/canonical"
	openairesponses "github.com/mirainya/Prism/internal/gateway/codec/openai_responses"
	"github.com/mirainya/Prism/internal/gateway/engine"
	"github.com/mirainya/Prism/internal/gateway/limits"
	chatpipeline "github.com/mirainya/Prism/internal/gateway/pipeline"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/model"
	protocol "github.com/mirainya/Prism/internal/provider/responses"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/pkg/queue"
	"github.com/shopspring/decimal"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

var enqueueResponseBackground = queue.EnqueueResponseBackground

const backgroundResponseLeaseDuration = 30 * time.Minute

var newBackgroundResponseLeaseOwner = func() string { return uuid.NewString() }

func acquireBackgroundResponseLease(ctx context.Context, responseID string, attempt int) (*model.AIResponse, bool, error) {
	// 条件更新同时完成抢占与 queued -> in_progress，只有一个 Worker 能取得同一 Response。
	now := time.Now()
	owner := newBackgroundResponseLeaseOwner()
	expiresAt := now.Add(backgroundResponseLeaseDuration)
	result := model.DB().WithContext(ctx).Model(&model.AIResponse{}).
		Where("id = ? AND background = ? AND status IN ?", responseID, true, []string{"queued", "in_progress", "result_ready", "finalizing"}).
		Where("lease_owner = '' OR lease_expires_at IS NULL OR lease_expires_at <= ?", now).
		Updates(map[string]any{
			"status":      gorm.Expr("CASE WHEN status = 'queued' THEN 'in_progress' ELSE status END"),
			"lease_owner": owner, "lease_expires_at": &expiresAt,
			"execution_attempt": attempt + 1,
		})
	if result.Error != nil {
		return nil, false, result.Error
	}
	var record model.AIResponse
	if err := model.DB().WithContext(ctx).Where("id = ?", responseID).First(&record).Error; err != nil {
		return nil, false, err
	}
	if result.RowsAffected == 0 {
		return &record, false, nil
	}
	return &record, true, nil
}

func releaseBackgroundResponseLease(responseID, owner string, requeue bool) error {
	if responseID == "" || owner == "" {
		return nil
	}
	updates := map[string]any{"lease_owner": "", "lease_expires_at": nil}
	if requeue {
		updates["status"] = gorm.Expr("CASE WHEN status = 'in_progress' THEN 'queued' ELSE status END")
	}
	return model.DB().Model(&model.AIResponse{}).
		Where("id = ? AND lease_owner = ?", responseID, owner).
		Updates(updates).Error
}

func (p *Pipeline) createV2(ctx context.Context, userID, tokenID uint, req *protocol.Request, idempotencyKey, requestID string, conversationID uint, thinkingLevel string) (out *Result, returnErr error) {
	// 顺序不可交换：先取得幂等所有权，再解析续话和文件，最后创建持久资源并执行。
	// 这样并发的同 key 请求只会有一个进入上游。
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if len(idempotencyKey) > 128 {
		return nil, domain.ErrBadRequest("Idempotency-Key must not exceed 128 bytes")
	}
	callID := ""
	var idempotencyClaim *responseIdempotencyClaim
	idempotencyTransferred := false
	defer func() {
		if idempotencyClaim != nil && !idempotencyTransferred {
			releaseErr := releaseResponseIdempotency(idempotencyClaim)
			returnErr = errors.Join(returnErr, idempotencyClaim.Err(), releaseErr)
		}
		returnErr = withResponseCallError(callID, returnErr)
	}()
	originalRequestJSON, _ := json.Marshal(req)
	idempotencyRequestJSON := responseIdempotencyRequestJSON(originalRequestJSON, conversationID)
	requestHash := hashResponseRequest(idempotencyRequestJSON)
	originalInput := datatypes.JSON(append([]byte(nil), req.Input...))
	publicPreviousResponseID := req.PreviousResponseID
	conversationProjection, projectionErr := newResponseConversationProjection(req, conversationID)
	if projectionErr != nil {
		return nil, projectionErr
	}
	if conversationID > 0 {
		if err := service.ValidateAPIConversationID(conversationID, userID, tokenID); err != nil {
			if errors.Is(err, service.ErrConversationNotFound) {
				return nil, domain.ErrBadRequest("conversation was not found")
			}
			return nil, err
		}
	}
	store := req.Store == nil || *req.Store
	if req.Background && strings.TrimSpace(thinkingLevel) != "" {
		return nil, domain.ErrBadRequest("model thinking levels are not supported for background responses")
	}
	executionCtx := ctx
	if idempotencyKey != "" {
		claim, existing, err := acquireResponseIdempotency(ctx, tokenID, idempotencyKey, idempotencyRequestJSON)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			return p.recordIdempotentReplay(existing, requestID, originalRequestJSON)
		}
		idempotencyClaim = claim
		executionCtx = claim.Context()
		if store {
			if existing, err := findIdempotentResponse(executionCtx, tokenID, idempotencyKey, idempotencyRequestJSON); err != nil || existing != nil {
				if err != nil {
					return nil, err
				}
				return p.recordIdempotentReplay(existing, requestID, originalRequestJSON)
			}
		}
	}
	previous, err := loadPreviousResponse(tokenID, publicPreviousResponseID)
	if err != nil {
		return nil, err
	}
	if err := validateInputFiles(tokenID, req.Input); err != nil {
		return nil, err
	}
	if req.Background {
		// 后台请求只提交持久执行意图；实际 Engine 调用由 Worker 在独立生命周期中完成。
		result, err := p.enqueueBackgroundV2(executionCtx, userID, tokenID, req, originalRequestJSON, idempotencyRequestJSON, requestHash, originalInput, publicPreviousResponseID, idempotencyKey, requestID, conversationID, conversationProjection, idempotencyClaim)
		if err == nil && idempotencyClaim != nil {
			idempotencyTransferred = true
		}
		return result, err
	}

	record, err := createRecord(userID, tokenID, req, originalRequestJSON, requestHash, originalInput, publicPreviousResponseID, idempotencyKey, requestID, nil, conversationProjection, conversationID)
	if err != nil {
		if idempotencyKey != "" && store {
			if existing, lookupErr := findIdempotentResponse(executionCtx, tokenID, idempotencyKey, idempotencyRequestJSON); lookupErr != nil || existing != nil {
				if lookupErr != nil {
					return nil, lookupErr
				}
				return p.recordIdempotentReplay(existing, requestID, originalRequestJSON)
			}
		}
		return nil, err
	}
	callID = record.CallID
	requestID, err = ensureResponseCall(record)
	if err != nil {
		return nil, errors.Join(err, p.recordResponseFailure(record, err, false, conversationProjection))
	}
	options := p.v2ExecuteOptions(record, req, previous, requestID, originalRequestJSON, thinkingLevel)
	if req.Stream {
		// 流式结果由 Handler 写出后确认交付，Pipeline 此时只转移 Engine Stream 的所有权。
		executionRequest := cloneResponseRequest(req)
		executionRequest.PreviousResponseID = ""
		executionRequest.Store = boolPointer(record.Store)
		canonicalRequest, decodeErr := openairesponses.DecodeRequest(*executionRequest)
		if decodeErr != nil {
			return nil, errors.Join(decodeErr, p.recordResponseFailure(record, decodeErr, false, conversationProjection))
		}
		result, executeErr := p.engine.Execute(executionCtx, canonicalRequest, options)
		if executeErr != nil {
			return nil, errors.Join(executeErr, p.recordResponseFailure(record, executeErr, false, conversationProjection))
		}
		if result == nil || result.Stream == nil {
			executeErr = errors.New("Gateway V2 returned no Responses stream")
			return nil, errors.Join(executeErr, p.recordResponseFailure(record, executeErr, false, conversationProjection))
		}
		if err := updateV2RecordRoute(record, result.Route, result.RequestLogID); err != nil {
			deliveryErr := result.Stream.Abort(err, false)
			return nil, errors.Join(err, deliveryErr, p.recordResponseFailure(record, err, false, conversationProjection))
		}
		idempotencyTransferred = idempotencyClaim != nil
		return &Result{
			V2Stream: result.Stream, Record: record, CallID: record.CallID,
			AttemptID:                result.AttemptID,
			PublicPreviousResponseID: publicPreviousResponseID,
			idempotencyClaim:         idempotencyClaim,
			conversation:             conversationProjection,
		}, nil
	}

	executionRequest := cloneResponseRequest(req)
	executionRequest.PreviousResponseID = ""
	executionRequest.Store = boolPointer(record.Store)
	result, err := p.v2.Execute(executionCtx, executionRequest, record.ID, options)
	if err != nil {
		return nil, errors.Join(err, p.recordResponseFailure(record, err, false, conversationProjection))
	}
	record.ProviderResponseID = result.ProviderResponseID
	record.RequestLogID = result.RequestLogID
	completedProjection := conversationProjection.withResponse(result.CanonicalResponse)
	stageResponseConversationOutputBestEffort(record, completedProjection)
	if err := updateV2RecordRoute(record, result.Route, result.RequestLogID); err != nil {
		deliveryErr := result.execution.FailDelivery(err, false)
		return nil, errors.Join(err, deliveryErr, p.recordResponseFailure(record, err, false, completedProjection))
	}
	setPublicPreviousResponseID(result.Response, publicPreviousResponseID)
	if err := persistCompletedRecord(record, result.Response, idempotencyClaim); err != nil {
		deliveryErr := result.execution.FailDelivery(err, false)
		return nil, errors.Join(err, deliveryErr, p.recordResponseFailure(record, err, false, completedProjection))
	}
	stageResponseConversationOutputBestEffort(record, completedProjection)
	idempotencyTransferred = idempotencyClaim != nil
	p.recordDownstreamResponse(record.CallID, latestResponseAttemptIDTx(model.DB(), record.CallID), result.Response)
	return &Result{
		Response: result.Response, Record: record, CallID: record.CallID,
		AttemptID: result.AttemptID, execution: result.execution,
		conversation: completedProjection,
	}, nil
}

func (p *Pipeline) v2ExecuteOptions(record *model.AIResponse, request *protocol.Request, previous *model.AIResponse, requestID string, downstreamRequest []byte, thinkingLevel string, billingKeys ...string) engine.ExecuteOptions {
	base := cloneResponseRequest(request)
	base.Store = boolPointer(record.Store)
	billingKey := record.ID
	if len(billingKeys) > 0 && billingKeys[0] != "" {
		billingKey = billingKeys[0]
	}
	return engine.ExecuteOptions{
		UserID: record.UserID, TokenID: record.TokenID,
		CallID: record.CallID, RequestID: requestID, DownstreamEndpoint: "/v1/responses",
		DownstreamRequest: downstreamRequest, ResourceType: "response", ResourceID: record.ID,
		KeepCallOpenOnError: record.Background, DeferCallCompletion: true,
		ProjectConversation: true, BillingKey: billingKey, MaxAttempts: 3,
		PrepareRoute: func(_ context.Context, _ canonical.Request, route *routing.RouteResult) (canonical.Request, error) {
			attempt := cloneResponseRequest(base)
			if err := prepareContinuation(attempt, previous, route); err != nil {
				return canonical.Request{}, err
			}
			if err := resolveInputFiles(record.TokenID, attempt); err != nil {
				return canonical.Request{}, err
			}
			decoded, err := openairesponses.DecodeRequest(*attempt)
			if err != nil {
				return canonical.Request{}, err
			}
			decoded = limits.ApplyModelMaxOutputTokens(decoded, route.ModelName)
			if strings.TrimSpace(thinkingLevel) != "" {
				decoded, err = chatpipeline.ApplyModelThinkingLevel(decoded, route.ModelName, route.Transport, thinkingLevel)
				if err != nil {
					return canonical.Request{}, err
				}
			}
			if attempt.PreviousResponseID != "" {
				decoded.TransportHints = []string{string(route.Transport)}
			}
			return decoded, nil
		},
	}
}

func boolPointer(value bool) *bool { return &value }

func loadPreviousResponse(tokenID uint, id string) (*model.AIResponse, error) {
	if strings.TrimSpace(id) == "" {
		return nil, nil
	}
	previous := &model.AIResponse{}
	if err := model.DB().Where("id = ? AND token_id = ? AND store = 1", id, tokenID).First(previous).Error; err != nil {
		return nil, domain.ErrBadRequest("previous_response_id was not found")
	}
	return previous, nil
}

func updateV2RecordRoute(record *model.AIResponse, route *routing.RouteResult, requestLogID uint) error {
	if record == nil || route == nil {
		return errors.New("Gateway V2 route is missing")
	}
	record.ChannelID = route.ChannelID
	record.KeyID = route.KeyID
	record.UpstreamTransport = route.Transport
	record.RequestLogID = requestLogID
	return model.DB().Model(record).Updates(map[string]any{
		"channel_id": route.ChannelID, "key_id": route.KeyID, "upstream_transport": route.Transport,
		"request_log_id": requestLogID,
	}).Error
}

func (p *Pipeline) enqueueBackgroundV2(ctx context.Context, userID, tokenID uint, req *protocol.Request, requestJSON, idempotencyRequestJSON []byte, requestHash string, inputItems datatypes.JSON, publicPreviousResponseID, idempotencyKey, requestID string, conversationID uint, conversationProjection *responseConversationProjection, idempotencyClaim *responseIdempotencyClaim) (out *Result, returnErr error) {
	callID := ""
	defer func() {
		returnErr = withResponseCallError(callID, returnErr)
	}()
	responseID := "resp_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	storedKey := idempotencyKey
	if storedKey == "" {
		storedKey = "internal:" + responseID
	}
	metadata, _ := json.Marshal(req.Metadata)
	record := &model.AIResponse{
		ID: responseID, UserID: userID, TokenID: tokenID, Model: req.Model, Status: "queued",
		Background: true, Store: true, PreviousResponseID: publicPreviousResponseID,
		RequestJSON: requestJSON, RequestHash: requestHash, InputItems: inputItems, Metadata: metadata,
		IdempotencyKey: storedKey, CreatedAt: time.Now(),
	}
	queued := &protocol.Response{
		ID: responseID, Object: "response", CreatedAt: record.CreatedAt.Unix(), Status: "queued",
		Background: true, Model: req.Model, Store: true, Output: json.RawMessage(`[]`), Tools: json.RawMessage(`[]`),
	}
	setPublicPreviousResponseID(queued, publicPreviousResponseID)
	record.ResponseJSON = mustJSON(queued)
	if err := createResponseWithCall(record, requestID, false, conversationProjection, conversationID); err != nil {
		if idempotencyKey != "" {
			if existing, lookupErr := findIdempotentResponse(ctx, tokenID, idempotencyKey, idempotencyRequestJSON); lookupErr != nil || existing != nil {
				if lookupErr != nil {
					return nil, lookupErr
				}
				return p.recordIdempotentReplay(existing, requestID, requestJSON)
			}
		}
		return nil, err
	}
	callID = record.CallID
	p.callService().RecordPayloadBestEffort(&model.APICallPayload{
		CallID: record.CallID, Kind: model.APICallPayloadRequest,
		ContentType: "application/json", Data: requestJSON,
	})
	if err := enqueueResponseBackground(responseID); err != nil {
		return nil, errors.Join(err, p.recordResponseFailure(record, err, false, conversationProjection))
	}
	if err := completeResponseIdempotency(idempotencyClaim, record.ID, queued); err != nil {
		return nil, err
	}
	p.recordDownstreamResponse(record.CallID, 0, queued)
	return &Result{Response: queued, Record: record, CallID: record.CallID}, nil
}

func (p *Pipeline) executeBackgroundV2(ctx context.Context, responseID string, finalAttempt bool, attempt int) error {
	// 后台执行分成“取得上游结果”和“发布资源终态”两阶段；检查点允许后者失败后单独重试。
	record, acquired, err := acquireBackgroundResponseLease(ctx, responseID, attempt)
	if err != nil {
		return err
	}
	if !acquired || responseTerminal(record.Status) {
		if storedResponseTerminal(record) {
			projectResponseConversationBestEffort(record)
		}
		return nil
	}
	leaseOwner := record.LeaseOwner
	leaseReleased := false
	defer func() {
		if !leaseReleased {
			_ = releaseBackgroundResponseLease(record.ID, leaseOwner, false)
		}
	}()

	requestID, err := ensureResponseCall(record)
	if err != nil {
		return p.failBackgroundV2(record, err, true, finalAttempt)
	}
	if record.Status == "result_ready" || record.Status == "finalizing" {
		// 已有检查点时严禁再次请求上游，否则可能重复扣费并生成两个 Provider 响应。
		err := p.finalizeCheckpointedBackgroundResponse(record)
		if err == nil {
			leaseReleased = true
		}
		return err
	}
	if err := p.reconcileV2BackgroundReservations(record); err != nil {
		return err
	}
	var request protocol.Request
	if err := json.Unmarshal(record.RequestJSON, &request); err != nil {
		return p.failBackgroundV2(record, err, true, finalAttempt)
	}
	previous, err := loadPreviousResponse(record.TokenID, record.PreviousResponseID)
	if err != nil {
		return p.failBackgroundV2(record, err, true, finalAttempt)
	}
	request.Background = false
	request.Stream = false
	request.Store = boolPointer(record.Store)
	execCtx, cancel := context.WithCancel(ctx)
	backgroundCancels.Store(record.ID, cancel)
	defer backgroundCancels.Delete(record.ID)
	defer cancel()
	billingKey := fmt.Sprintf("%s:background:%d", record.ID, attempt+1)
	executionRequest := cloneResponseRequest(&request)
	executionRequest.PreviousResponseID = ""
	result, err := p.v2.Execute(execCtx, executionRequest, record.ID, p.v2ExecuteOptions(record, &request, previous, requestID, nil, "", billingKey))
	if err != nil {
		if isResponseCancelled(record.ID) {
			return p.recordResponseCancellation(record)
		}
		if errors.Is(err, context.Canceled) {
			return err
		}
		permanent := errors.Is(err, routing.ErrModelNotFound) ||
			errors.Is(err, routing.ErrCapabilityUnavailable) ||
			errors.Is(err, routing.ErrNoCompatibleTransport) ||
			errors.Is(err, service.ErrInsufficientTokenBalance) || errors.Is(err, service.ErrInsufficientUserBalance)
		failureErr := p.failBackgroundV2(record, err, permanent, finalAttempt)
		if permanent || finalAttempt || failureErr == nil {
			leaseReleased = true
		}
		return failureErr
	}
	record.ProviderResponseID = result.ProviderResponseID
	record.RequestLogID = result.RequestLogID
	backgroundProjection := (&responseConversationProjection{}).withResponse(result.CanonicalResponse)
	stageResponseConversationOutputBestEffort(record, backgroundProjection)
	if isResponseCancelled(record.ID) {
		deliveryErr := result.execution.CancelDelivery(context.Canceled, false)
		return errors.Join(deliveryErr, p.recordResponseCancellation(record, backgroundProjection))
	}
	result.Response.Background = true
	result.Response.Store = true
	setPublicPreviousResponseID(result.Response, record.PreviousResponseID)
	if err := p.checkpointBackgroundResponse(record, leaseOwner, result); err != nil {
		cancelled := errors.Is(err, context.Canceled) || isResponseCancelled(record.ID)
		var deliveryErr error
		if cancelled {
			deliveryErr = result.execution.CancelDelivery(err, false)
		} else {
			deliveryErr = result.execution.FailDelivery(err, false)
		}
		if cancelled {
			return errors.Join(err, deliveryErr, p.recordResponseCancellation(record, backgroundProjection))
		}
		return errors.Join(err, deliveryErr, p.recordResponseFailure(record, err, false, backgroundProjection))
	}
	if err := result.execution.CompleteDelivery(); err != nil {
		return err
	}
	if err := p.finalizeCheckpointedBackgroundResponse(record); err != nil {
		return err
	}
	leaseReleased = true
	p.recordDownstreamResponse(record.CallID, latestResponseAttemptIDTx(model.DB(), record.CallID), result.Response)
	return nil
}

func (p *Pipeline) checkpointBackgroundResponse(record *model.AIResponse, leaseOwner string, result *V2Result) error {
	if record == nil || result == nil || result.Response == nil || result.Route == nil {
		return errors.New("background response checkpoint is incomplete")
	}
	record.ProviderResponseID = result.ProviderResponseID
	record.RequestLogID = result.RequestLogID
	stageResponseConversationOutputBestEffort(record, (&responseConversationProjection{}).withResponse(result.CanonicalResponse))
	responseJSON, err := json.Marshal(result.Response)
	if err != nil {
		return err
	}
	usageJSON, err := json.Marshal(result.Response.Usage)
	if err != nil {
		return err
	}
	now := time.Now()
	updates := map[string]any{
		"status": "result_ready", "provider_response_id": result.ProviderResponseID,
		"channel_id": result.Route.ChannelID, "key_id": result.Route.KeyID,
		"upstream_transport": result.Route.Transport, "request_log_id": result.RequestLogID,
		"response_json": responseJSON, "output_items": result.Response.Output,
		"usage_json": usageJSON, "result_ready_at": &now,
	}
	// lease_owner 放入条件可阻止过期 Worker 覆盖后来 Worker 已接管的结果。
	update := model.DB().Model(&model.AIResponse{}).
		Where("id = ? AND lease_owner = ? AND status = ?", record.ID, leaseOwner, "in_progress").
		Updates(updates)
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected == 0 {
		if isResponseCancelled(record.ID) {
			return context.Canceled
		}
		return errors.New("background response lease was lost before checkpoint")
	}
	record.Status = "result_ready"
	record.ProviderResponseID = result.ProviderResponseID
	record.ChannelID = result.Route.ChannelID
	record.KeyID = result.Route.KeyID
	record.UpstreamTransport = result.Route.Transport
	record.RequestLogID = result.RequestLogID
	record.ResponseJSON = responseJSON
	record.OutputItems = datatypes.JSON(append([]byte(nil), result.Response.Output...))
	record.UsageJSON = usageJSON
	record.ResultReadyAt = &now
	return nil
}

func (p *Pipeline) finalizeCheckpointedBackgroundResponse(record *model.AIResponse) error {
	if record == nil || len(record.ResponseJSON) == 0 {
		return errors.New("background response checkpoint is missing")
	}
	var response protocol.Response
	if err := json.Unmarshal(record.ResponseJSON, &response); err != nil {
		return fmt.Errorf("decode background response checkpoint: %w", err)
	}
	stageResponseConversationReadyBestEffort(record, nil)
	// result_ready -> finalizing 是发布终态的独占权；未抢到的 Worker 只观察当前结果。
	claimed, err := claimResponseFinalization(record)
	if err != nil {
		return err
	}
	if !claimed {
		var current model.AIResponse
		if err := model.DB().First(&current, "id = ?", record.ID).Error; err != nil {
			return err
		}
		if responseTerminal(current.Status) {
			if storedResponseTerminal(&current) {
				projectResponseConversationBestEffort(&current)
			}
			return nil
		}
		return errors.New("background response finalization was not claimed")
	}
	if err := completeRecord(record, &response); err != nil {
		_ = model.DB().Model(&model.AIResponse{}).
			Where("id = ? AND status = ? AND lease_owner = ?", record.ID, "finalizing", record.LeaseOwner).
			Update("status", "result_ready").Error
		return err
	}
	projectResponseConversationBestEffort(record)
	return nil
}

func (p *Pipeline) reconcileV2BackgroundReservations(record *model.AIResponse) error {
	return reconcileV2BackgroundReservations(p.billing, record)
}

func reconcileV2BackgroundReservations(billing *service.BillingService, record *model.AIResponse) error {
	if billing == nil || record == nil {
		return nil
	}
	var reservations []model.BillingLog
	prefix := record.ID + ":background:%"
	legacyKey := record.ID + ":reserve"
	if err := model.DB().Where("(idempotent_key = ? OR idempotent_key LIKE ?) AND idempotent_key LIKE ? AND type = ?", legacyKey, prefix, "%:reserve", model.BillingTypeDeduct).Find(&reservations).Error; err != nil {
		return err
	}
	for _, reserve := range reservations {
		key := strings.TrimSuffix(reserve.IdempotentKey, ":reserve")
		var settled int64
		if err := model.DB().Model(&model.BillingLog{}).Where("idempotent_key = ?", key+":settle").Count(&settled).Error; err != nil {
			return err
		}
		if settled == 0 {
			if err := billing.SettleReservationWithBillingContext(
				record.TokenID, record.UserID, reserve.Amount, decimal.Zero, key+":settle",
				service.BillingContext{
					CallID: record.CallID, AttemptID: reserve.AttemptID,
					Phase: model.BillingPhaseRefund, PricingSnapshot: reserve.PricingSnapshot,
				},
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *Pipeline) failBackgroundV2(record *model.AIResponse, err error, permanent, finalAttempt bool) error {
	if isResponseCancelled(record.ID) {
		return nil
	}
	if permanent || finalAttempt {
		stateErr := p.recordResponseFailure(record, err, !permanent)
		err = errors.Join(err, stateErr)
		if permanent {
			return &permanentBackgroundError{err: err}
		}
		return err
	}
	_ = model.DB().Model(record).Where("status = ?", "in_progress").Updates(map[string]any{
		"status": "queued", "lease_owner": "", "lease_expires_at": nil,
	}).Error
	return err
}

func responseTerminal(status string) bool {
	return status == "completed" || status == "failed" || status == "incomplete" || status == "cancelled" ||
		status == "cancelling" || strings.HasPrefix(status, "refund_pending_")
}

func (p *Pipeline) ProxyV2Stream(ctx context.Context, writer http.ResponseWriter, result *Result, request *protocol.Request) (returnErr error) {
	if result == nil || result.V2Stream == nil || result.Record == nil {
		return errors.New("Gateway V2 Responses stream is missing")
	}
	claim := result.idempotencyClaim
	defer func() {
		if claim != nil {
			releaseErr := releaseResponseIdempotency(claim)
			returnErr = errors.Join(returnErr, claim.Err(), releaseErr)
		}
	}()
	streamCtx := ctx
	if claim != nil {
		streamCtx = claim.Context()
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.WriteHeader(http.StatusOK)
	summary, err := ConsumeV2StreamWithOptions(streamCtx, writer, result.V2Stream, V2StreamPublicOptions{
		ResponseID: result.Record.ID, Model: request.Model, CreatedAt: result.Record.CreatedAt.Unix(), PreviousResponseID: result.PublicPreviousResponseID,
		Store: result.Record.Store, Background: result.Record.Background,
		PreserveNativeRaw: result.Record.UpstreamTransport == model.UpstreamTransportOpenAIResponses || result.Record.UpstreamTransport == model.UpstreamTransportVolcengineV3,
	})
	streamProjection := responseProjectionFromStreamSummary(result.conversation, summary)
	stageResponseConversationOutputBestEffort(result.Record, streamProjection)
	if err != nil {
		deliveryErr := result.V2Stream.FailDelivery(err, ctx.Err() != nil)
		if responseAPICallStatus(result.Record.CallID) == model.APICallStatusCancelled {
			return errors.Join(err, deliveryErr, p.recordResponseCancellation(result.Record, streamProjection))
		}
		return errors.Join(err, deliveryErr, p.recordResponseFailure(result.Record, err, false, streamProjection))
	}
	response, err := publicV2StreamResponse(summary, result.Record, request, result.PublicPreviousResponseID)
	if err != nil {
		deliveryErr := result.V2Stream.FailDelivery(err, false)
		return errors.Join(err, deliveryErr, p.recordResponseFailure(result.Record, err, false, streamProjection))
	}
	result.Record.ProviderResponseID = summary.ProviderResponseID
	if err := completeRecordWithProjection(result.Record, response, streamProjection, claim); err != nil {
		deliveryErr := result.V2Stream.FailDelivery(err, false)
		return errors.Join(err, deliveryErr, p.recordResponseFailure(result.Record, err, false, streamProjection))
	}
	if err := result.V2Stream.CompleteDelivery(); err != nil {
		return err
	}
	claim = nil
	result.idempotencyClaim = nil
	projectResponseConversationBestEffort(result.Record)
	p.recordDownstreamResponse(result.Record.CallID, latestResponseAttemptIDTx(model.DB(), result.Record.CallID), response)
	return nil
}

func ProxyIdempotentReplay(writer http.ResponseWriter, response *protocol.Response) error {
	if response == nil {
		return errors.New("cached idempotent response is missing")
	}
	eventType := "response.completed"
	switch response.Status {
	case "incomplete":
		eventType = "response.incomplete"
	case "failed":
		eventType = "response.failed"
	}
	payload, err := json.Marshal(struct {
		Type           string             `json:"type"`
		SequenceNumber int                `json:"sequence_number"`
		Response       *protocol.Response `json:"response"`
	}{Type: eventType, Response: response})
	if err != nil {
		return err
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.WriteHeader(http.StatusOK)
	frame := fmt.Sprintf("event: %s\ndata: %s\n\n", eventType, payload)
	written, err := io.WriteString(writer, frame)
	if err == nil && written != len(frame) {
		err = io.ErrShortWrite
	}
	return err
}

func (p *Pipeline) recordDownstreamResponse(callID string, attemptID uint, response *protocol.Response) {
	if response == nil || strings.TrimSpace(callID) == "" {
		return
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return
	}
	p.callService().RecordPayloadBestEffort(&model.APICallPayload{
		CallID: callID, AttemptID: attemptID, Kind: model.APICallPayloadResponse,
		ContentType: "application/json", Data: encoded,
	})
}

func publicV2StreamResponse(summary *V2StreamSummary, record *model.AIResponse, request *protocol.Request, previousID string) (*protocol.Response, error) {
	canonicalResponse := canonical.Response{ID: record.ID, Model: request.Model, CreatedAt: record.CreatedAt.Unix(), Status: "completed"}
	if summary != nil && summary.Response != nil {
		canonicalResponse = *summary.Response
		canonicalResponse.ID = record.ID
		canonicalResponse.Model = request.Model
		canonicalResponse.CreatedAt = record.CreatedAt.Unix()
	}
	if summary != nil {
		if summary.Usage != nil {
			canonicalResponse.Usage = summary.Usage
		}
		if summary.Error != nil {
			canonicalResponse.Error = summary.Error
		}
		switch summary.Terminal {
		case canonical.EventFailed, canonical.EventError:
			canonicalResponse.Status = "failed"
		case canonical.EventIncomplete:
			canonicalResponse.Status = "incomplete"
		}
	}
	encoded, err := openairesponses.EncodeResponseJSON(canonicalResponse)
	if err != nil {
		return nil, fmt.Errorf("encode streamed response: %w", err)
	}
	var response protocol.Response
	if err := json.Unmarshal(encoded, &response); err != nil {
		return nil, err
	}
	response.ID, response.Model = record.ID, request.Model
	response.Store, response.Background = record.Store, record.Background
	setPublicPreviousResponseID(&response, previousID)
	applyResponseRequestFields(&response, request)
	return &response, nil
}

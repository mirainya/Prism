package responses

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/gateway/canonical"
	openairesponses "github.com/mirainya/Prism/internal/gateway/codec/openai_responses"
	"github.com/mirainya/Prism/internal/gateway/engine"
	"github.com/mirainya/Prism/internal/gateway/limits"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/model"
	protocol "github.com/mirainya/Prism/internal/provider/responses"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/pkg/queue"
	"github.com/shopspring/decimal"
	"gorm.io/datatypes"
)

func (p *Pipeline) createV2(ctx context.Context, userID, tokenID uint, req *protocol.Request, idempotencyKey string) (*Result, error) {
	originalRequestJSON, _ := json.Marshal(req)
	originalInput := datatypes.JSON(append([]byte(nil), req.Input...))
	publicPreviousResponseID := req.PreviousResponseID
	if idempotencyKey != "" {
		if existing, err := findIdempotentResponse(tokenID, idempotencyKey, originalRequestJSON); err != nil || existing != nil {
			return existing, err
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
		return p.enqueueBackgroundV2(userID, tokenID, req, originalRequestJSON, originalInput, publicPreviousResponseID, idempotencyKey)
	}

	record, err := createRecord(userID, tokenID, req, originalRequestJSON, originalInput, publicPreviousResponseID, idempotencyKey, nil)
	if err != nil {
		if idempotencyKey != "" {
			if existing, lookupErr := findIdempotentResponse(tokenID, idempotencyKey, originalRequestJSON); lookupErr != nil || existing != nil {
				return existing, lookupErr
			}
		}
		return nil, err
	}
	options := p.v2ExecuteOptions(record, req, previous)
	if req.Stream {
		executionRequest := cloneResponseRequest(req)
		executionRequest.PreviousResponseID = ""
		canonicalRequest, decodeErr := openairesponses.DecodeRequest(*executionRequest)
		if decodeErr != nil {
			markFailed(record, decodeErr)
			return nil, decodeErr
		}
		result, executeErr := p.engine.Execute(ctx, canonicalRequest, options)
		if executeErr != nil {
			markFailed(record, executeErr)
			return nil, executeErr
		}
		if result == nil || result.Stream == nil {
			executeErr = errors.New("Gateway V2 returned no Responses stream")
			markFailed(record, executeErr)
			return nil, executeErr
		}
		if err := updateV2RecordRoute(record, result.Route, result.RequestLogID); err != nil {
			_ = result.Stream.Close()
			markFailed(record, err)
			return nil, err
		}
		return &Result{V2Stream: result.Stream, Record: record, PublicPreviousResponseID: publicPreviousResponseID}, nil
	}

	executionRequest := cloneResponseRequest(req)
	executionRequest.PreviousResponseID = ""
	result, err := p.v2.Execute(ctx, executionRequest, record.ID, options)
	if err != nil {
		markFailed(record, err)
		return nil, err
	}
	if err := updateV2RecordRoute(record, result.Route, result.RequestLogID); err != nil {
		markFailed(record, err)
		return nil, err
	}
	record.ProviderResponseID = result.ProviderResponseID
	setPublicPreviousResponseID(result.Response, publicPreviousResponseID)
	if err := completeRecord(record, result.Response); err != nil {
		return nil, err
	}
	return &Result{Response: result.Response, Record: record}, nil
}

func (p *Pipeline) v2ExecuteOptions(record *model.AIResponse, request *protocol.Request, previous *model.AIResponse, billingKeys ...string) engine.ExecuteOptions {
	base := cloneResponseRequest(request)
	billingKey := record.ID
	if len(billingKeys) > 0 && billingKeys[0] != "" {
		billingKey = billingKeys[0]
	}
	return engine.ExecuteOptions{
		UserID: record.UserID, TokenID: record.TokenID, BillingKey: billingKey, MaxAttempts: 3,
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
			if attempt.PreviousResponseID != "" {
				decoded.TransportHints = []string{string(route.Transport)}
			}
			return decoded, nil
		},
	}
}

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

func (p *Pipeline) enqueueBackgroundV2(userID, tokenID uint, req *protocol.Request, requestJSON []byte, inputItems datatypes.JSON, publicPreviousResponseID, idempotencyKey string) (*Result, error) {
	responseID := "resp_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	storedKey := idempotencyKey
	if storedKey == "" {
		storedKey = "internal:" + responseID
	}
	metadata, _ := json.Marshal(req.Metadata)
	record := &model.AIResponse{
		ID: responseID, UserID: userID, TokenID: tokenID, Model: req.Model, Status: "queued",
		Background: true, Store: true, PreviousResponseID: publicPreviousResponseID,
		RequestJSON: requestJSON, InputItems: inputItems, Metadata: metadata,
		IdempotencyKey: storedKey, CreatedAt: time.Now(),
	}
	queued := &protocol.Response{
		ID: responseID, Object: "response", CreatedAt: record.CreatedAt.Unix(), Status: "queued",
		Background: true, Model: req.Model, Store: true, Output: json.RawMessage(`[]`), Tools: json.RawMessage(`[]`),
	}
	setPublicPreviousResponseID(queued, publicPreviousResponseID)
	record.ResponseJSON = mustJSON(queued)
	if err := model.DB().Create(record).Error; err != nil {
		if idempotencyKey != "" {
			if existing, lookupErr := findIdempotentResponse(tokenID, idempotencyKey, requestJSON); lookupErr != nil || existing != nil {
				return existing, lookupErr
			}
		}
		return nil, err
	}
	if err := queue.EnqueueResponseBackground(responseID); err != nil {
		markFailed(record, err)
		return nil, err
	}
	return &Result{Response: queued, Record: record}, nil
}

func (p *Pipeline) executeBackgroundV2(ctx context.Context, responseID string, finalAttempt bool, attempt int) error {
	var record model.AIResponse
	if err := model.DB().Where("id = ?", responseID).First(&record).Error; err != nil {
		return err
	}
	if responseTerminal(record.Status) {
		return nil
	}
	if err := p.reconcileV2BackgroundReservations(&record); err != nil {
		return err
	}
	var request protocol.Request
	if err := json.Unmarshal(record.RequestJSON, &request); err != nil {
		return p.failBackgroundV2(&record, err, true, finalAttempt)
	}
	previous, err := loadPreviousResponse(record.TokenID, record.PreviousResponseID)
	if err != nil {
		return p.failBackgroundV2(&record, err, true, finalAttempt)
	}
	request.Background = false
	request.Stream = false
	update := model.DB().Model(&record).Where("status IN ?", []string{"queued", "in_progress"}).Updates(map[string]any{
		"status": "in_progress", "error_json": nil, "completed_at": nil,
	})
	if update.Error != nil || update.RowsAffected == 0 {
		return update.Error
	}
	record.Status = "in_progress"
	execCtx, cancel := context.WithCancel(ctx)
	backgroundCancels.Store(record.ID, cancel)
	defer backgroundCancels.Delete(record.ID)
	defer cancel()
	billingKey := fmt.Sprintf("%s:background:%d", record.ID, attempt+1)
	executionRequest := cloneResponseRequest(&request)
	executionRequest.PreviousResponseID = ""
	result, err := p.v2.Execute(execCtx, executionRequest, record.ID, p.v2ExecuteOptions(&record, &request, previous, billingKey))
	if err != nil {
		if isResponseCancelled(record.ID) || errors.Is(err, context.Canceled) {
			return nil
		}
		permanent := errors.Is(err, routing.ErrModelNotFound) ||
			errors.Is(err, routing.ErrCapabilityUnavailable) ||
			errors.Is(err, routing.ErrNoCompatibleTransport) ||
			errors.Is(err, service.ErrInsufficientTokenBalance) || errors.Is(err, service.ErrInsufficientUserBalance)
		return p.failBackgroundV2(&record, err, permanent, finalAttempt)
	}
	if isResponseCancelled(record.ID) {
		return nil
	}
	if err := updateV2RecordRoute(&record, result.Route, result.RequestLogID); err != nil {
		return p.failBackgroundV2(&record, err, false, finalAttempt)
	}
	record.ProviderResponseID = result.ProviderResponseID
	result.Response.Background = true
	result.Response.Store = true
	setPublicPreviousResponseID(result.Response, record.PreviousResponseID)
	claimed, err := claimResponseFinalization(&record)
	if err != nil {
		return p.failBackgroundV2(&record, err, false, finalAttempt)
	}
	if !claimed {
		return nil
	}
	if err := completeRecord(&record, result.Response); err != nil {
		return p.failBackgroundV2(&record, err, false, finalAttempt)
	}
	return nil
}

func (p *Pipeline) reconcileV2BackgroundReservations(record *model.AIResponse) error {
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
			if err := p.billing.SettleReservation(record.TokenID, record.UserID, reserve.Amount, decimal.Zero, key+":settle"); err != nil {
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
		markFailed(record, err)
		if permanent {
			return &permanentBackgroundError{err: err}
		}
		return err
	}
	_ = model.DB().Model(record).Where("status = ?", "in_progress").Update("status", "queued").Error
	return err
}

func responseTerminal(status string) bool {
	return status == "completed" || status == "failed" || status == "incomplete" || status == "cancelled" ||
		status == "cancelling" || strings.HasPrefix(status, "refund_pending_")
}

func (p *Pipeline) ProxyV2Stream(ctx context.Context, writer http.ResponseWriter, result *Result, request *protocol.Request) error {
	if result == nil || result.V2Stream == nil || result.Record == nil {
		return errors.New("Gateway V2 Responses stream is missing")
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.WriteHeader(http.StatusOK)
	summary, err := ConsumeV2StreamWithOptions(ctx, writer, result.V2Stream, V2StreamPublicOptions{
		ResponseID: result.Record.ID, Model: request.Model, CreatedAt: result.Record.CreatedAt.Unix(), PreviousResponseID: result.PublicPreviousResponseID,
		Store: result.Record.Store, Background: result.Record.Background,
		PreserveNativeRaw: result.Record.UpstreamTransport == model.UpstreamTransportOpenAIResponses || result.Record.UpstreamTransport == model.UpstreamTransportVolcengineV3,
	})
	if err != nil {
		markFailed(result.Record, err)
		return err
	}
	response, err := publicV2StreamResponse(summary, result.Record, request, result.PublicPreviousResponseID)
	if err != nil {
		markFailed(result.Record, err)
		return err
	}
	result.Record.ProviderResponseID = summary.ProviderResponseID
	return completeRecord(result.Record, response)
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

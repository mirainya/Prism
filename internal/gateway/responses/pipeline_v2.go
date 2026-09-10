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

	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/gateway/canonical"
	openairesponses "github.com/mirainya/Prism/internal/gateway/codec/openai_responses"
	"github.com/mirainya/Prism/internal/gateway/engine"
	"github.com/mirainya/Prism/internal/gateway/execution"
	"github.com/mirainya/Prism/internal/gateway/limits"
	chatpipeline "github.com/mirainya/Prism/internal/gateway/pipeline"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/model"
	protocol "github.com/mirainya/Prism/internal/provider/responses"
	"github.com/mirainya/Prism/internal/service"
	"gorm.io/datatypes"
)

func (p *Pipeline) createV2(ctx context.Context, userID, tokenID uint, req *protocol.Request, idempotencyKey, requestID string, conversationID uint, thinkingLevel string) (out *Result, returnErr error) {
	callID := ""
	defer func() {
		returnErr = withResponseCallError(callID, returnErr)
	}()
	originalRequestJSON, _ := json.Marshal(req)
	idempotencyRequestJSON := responseIdempotencyRequestJSON(originalRequestJSON, conversationID)
	idempotency, err := prepareResponseIdempotency(ctx, tokenID, idempotencyKey, idempotencyRequestJSON)
	if err != nil {
		return nil, err
	}
	if replay, err := findResponseIdempotentReplay(ctx, userID, idempotency); err != nil || replay != nil {
		return replay, err
	}
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
	if req.Background && strings.TrimSpace(thinkingLevel) != "" {
		return nil, domain.ErrBadRequest("model thinking levels are not supported for background responses")
	}
	if err := validateInputFiles(tokenID, req.Input); err != nil {
		return nil, err
	}
	if req.Background {
		// 后台请求只提交持久执行意图；实际 Engine 调用由 Worker 在独立生命周期中完成。
		return p.enqueueBackgroundV2(ctx, userID, tokenID, req, originalRequestJSON, idempotencyRequestJSON, requestHash, originalInput, publicPreviousResponseID, idempotency)
	}
	previous, previousResourceID, err := loadPreviousResponse(ctx, userID, tokenID, publicPreviousResponseID)
	if err != nil {
		return nil, err
	}

	record := newResponseRecord(userID, tokenID, req, originalRequestJSON, requestHash, originalInput, publicPreviousResponseID)
	record.CallID = service.GenerateUnifiedCallID()
	if strings.TrimSpace(requestID) == "" {
		requestID = service.GenerateRequestID()
	}
	callID = record.CallID
	options, err := p.v2ExecuteOptions(record, req, previous, requestID, originalRequestJSON, thinkingLevel, conversationProjection)
	if err != nil {
		return nil, errors.Join(err, p.failV2Record(record, err, conversationProjection))
	}
	options.ResourceType = "response"
	options.ResourceID = record.ID
	options.PreviousResourceID = previousResourceID
	options.ResourceSummary = map[string]any{
		"model": record.Model, "background": false, "store": record.Store,
	}
	options.Idempotency = idempotency
	options.EnforceIdempotencyPolicy = true
	if req.Stream {
		// 流式结果由 Handler 写出后确认交付，Pipeline 此时只转移 Engine Stream 的所有权。
		executionRequest := cloneResponseRequest(req)
		executionRequest.PreviousResponseID = ""
		executionRequest.Store = boolPointer(record.Store)
		canonicalRequest, decodeErr := openairesponses.DecodeRequest(*executionRequest)
		if decodeErr != nil {
			return nil, errors.Join(decodeErr, p.failV2Record(record, decodeErr, conversationProjection))
		}
		result, executeErr := p.engine.Execute(ctx, canonicalRequest, options)
		if executeErr != nil {
			if replay, ok, replayErr := responseReplayFromExecutionError(ctx, userID, tokenID, executeErr); ok {
				return replay, replayErr
			}
			executeErr = responseIdempotencyError(executeErr)
			return nil, errors.Join(executeErr, p.failV2Record(record, executeErr, conversationProjection))
		}
		if result == nil || result.Stream == nil {
			executeErr = errors.New("Gateway V2 returned no Responses stream")
			return nil, errors.Join(executeErr, p.failV2Record(record, executeErr, conversationProjection))
		}
		if err := p.updateV2RecordRoute(record, result.Route, result.RequestLogID); err != nil {
			deliveryErr := result.Stream.Abort(err, false)
			return nil, errors.Join(err, deliveryErr, p.failV2Record(record, err, conversationProjection))
		}
		return &Result{
			V2Stream: result.Stream, Record: record, CallID: record.CallID,
			AttemptID:                result.AttemptID,
			PublicPreviousResponseID: publicPreviousResponseID,
			conversation:             conversationProjection,
			unifiedResource:          true,
		}, nil
	}

	executionRequest := cloneResponseRequest(req)
	executionRequest.PreviousResponseID = ""
	executionRequest.Store = boolPointer(record.Store)
	result, err := p.v2.Execute(ctx, executionRequest, record.ID, options)
	if err != nil {
		if replay, ok, replayErr := responseReplayFromExecutionError(ctx, userID, tokenID, err); ok {
			return replay, replayErr
		}
		err = responseIdempotencyError(err)
		return nil, errors.Join(err, p.failV2Record(record, err, conversationProjection))
	}
	record.ProviderResponseID = result.ProviderResponseID
	record.RequestLogID = result.RequestLogID
	completedProjection := conversationProjection.withResponse(result.CanonicalResponse)
	stageResponseConversationOutputBestEffort(record, completedProjection)
	if err := p.updateV2RecordRoute(record, result.Route, result.RequestLogID); err != nil {
		deliveryErr := result.execution.FailDelivery(err, false)
		return nil, errors.Join(err, deliveryErr, p.failV2Record(record, err, completedProjection))
	}
	setPublicPreviousResponseID(result.Response, publicPreviousResponseID)
	if err := p.completeV2Record(record, result.Response); err != nil {
		deliveryErr := result.execution.FailDelivery(err, false)
		return nil, errors.Join(err, deliveryErr, p.failV2Record(record, err, completedProjection))
	}
	stageResponseConversationOutputBestEffort(record, completedProjection)
	return &Result{
		Response: result.Response, Record: record, CallID: record.CallID,
		AttemptID: result.AttemptID, execution: result.execution,
		conversation:    completedProjection,
		unifiedResource: true,
	}, nil
}

func (p *Pipeline) v2ExecuteOptions(record *model.AIResponse, request *protocol.Request, previous *model.AIResponse, requestID string, downstreamRequest []byte, thinkingLevel string, projection *responseConversationProjection, billingKeys ...string) (engine.ExecuteOptions, error) {
	base := cloneResponseRequest(request)
	base.Store = boolPointer(record.Store)
	conversationInput, err := responseConversationInputRequest(record, projection)
	if err != nil {
		return engine.ExecuteOptions{}, err
	}
	billingKey := record.ID
	if len(billingKeys) > 0 && billingKeys[0] != "" {
		billingKey = billingKeys[0]
	}
	return engine.ExecuteOptions{
		UserID: record.UserID, TokenID: record.TokenID,
		CallID: record.CallID, RequestID: requestID, DownstreamEndpoint: "/v1/responses",
		DownstreamRequest:   downstreamRequest,
		ConversationID:      conversationInput.ConversationID,
		KeepCallOpenOnError: record.Background, DeferCallCompletion: true,
		ProjectConversation: true, ConversationInput: &conversationInput,
		BillingKey: billingKey, MaxAttempts: 3,
		PrepareRoute: func(ctx context.Context, _ canonical.Request, route *routing.RouteResult) (canonical.Request, error) {
			attempt := cloneResponseRequest(base)
			if err := prepareContinuation(attempt, previous, route); err != nil {
				return canonical.Request{}, err
			}
			if err := resolveInputFiles(ctx, record.TokenID, attempt); err != nil {
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
	}, nil
}

func boolPointer(value bool) *bool { return &value }

func loadPreviousResponse(ctx context.Context, userID, tokenID uint, id string) (*model.AIResponse, *uint64, error) {
	if strings.TrimSpace(id) == "" {
		return nil, nil, nil
	}
	if previous, resourceID, err := loadUnifiedResponseRecord(ctx, userID, tokenID, id, true); err == nil {
		return previous, &resourceID, nil
	}
	return nil, nil, domain.ErrBadRequest("previous_response_id was not found")
}

func (p *Pipeline) updateV2RecordRoute(record *model.AIResponse, route *routing.RouteResult, requestLogID uint) error {
	if record == nil || route == nil {
		return errors.New("Gateway V2 route is missing")
	}
	record.ChannelID = route.ChannelID
	record.KeyID = route.KeyID
	record.UpstreamTransport = route.Transport
	record.RequestLogID = requestLogID
	return nil
}

func (p *Pipeline) failV2Record(record *model.AIResponse, cause error, projection *responseConversationProjection) error {
	_ = projection
	if record == nil {
		return nil
	}
	record.Status = "failed"
	record.ErrorJSON = mustJSON(responseErrorFromError(cause))
	now := time.Now()
	record.CompletedAt = &now
	return nil
}

func (p *Pipeline) cancelV2Record(record *model.AIResponse, projection *responseConversationProjection) error {
	_ = projection
	if record != nil {
		record.Status = "cancelled"
		now := time.Now()
		record.CompletedAt = &now
	}
	return nil
}

func (p *Pipeline) completeV2Record(record *model.AIResponse, response *protocol.Response) error {
	if record == nil || response == nil {
		return errors.New("response record and body are required")
	}
	status := response.Status
	if status == "" {
		status = "completed"
		response.Status = status
	}
	responseJSON, err := json.Marshal(response)
	if err != nil {
		return err
	}
	usageJSON, err := json.Marshal(response.Usage)
	if err != nil {
		return err
	}
	record.Status = status
	record.ResponseJSON = responseJSON
	record.OutputItems = append(datatypes.JSON(nil), response.Output...)
	record.UsageJSON = usageJSON
	now := time.Now()
	record.CompletedAt = &now
	return nil
}

func (p *Pipeline) enqueueBackgroundV2(ctx context.Context, userID, tokenID uint, req *protocol.Request, requestJSON, idempotencyRequestJSON []byte, requestHash string, inputItems datatypes.JSON, publicPreviousResponseID string, idempotency *repository.IdempotencyInput) (out *Result, returnErr error) {
	responseID := newResponseID()
	metadata, _ := json.Marshal(req.Metadata)
	record := &model.AIResponse{
		ID: responseID, UserID: userID, TokenID: tokenID, Model: req.Model, Status: "queued",
		Background: true, Store: true, PreviousResponseID: publicPreviousResponseID,
		RequestJSON: requestJSON, RequestHash: requestHash, InputItems: inputItems, Metadata: metadata,
		CallID: responseID, CreatedAt: time.Now(),
	}
	executionRequest := cloneResponseRequest(req)
	executionRequest.Background, executionRequest.Stream = false, false
	executionRequest.PreviousResponseID = ""
	executionRequest.Store = boolPointer(true)
	canonicalRequest, err := openairesponses.DecodeRequest(*executionRequest)
	if err != nil {
		return nil, err
	}
	canonicalRequest, mediaAssetIDs, err := prepareBackgroundCanonical(ctx, userID, tokenID, publicPreviousResponseID, canonicalRequest)
	if err != nil {
		return nil, err
	}
	canonicalRequest = limits.ApplyModelMaxOutputTokens(canonicalRequest, req.Model)
	plan, err := p.engine.PlanDeferred(ctx, canonicalRequest, engine.ExecuteOptions{CallID: responseID})
	if err != nil {
		return nil, err
	}
	encodedRequest, err := json.Marshal(backgroundRequestEnvelope{Version: 1, Request: *cloneResponseRequest(req), Canonical: plan.Request})
	if err != nil {
		return nil, err
	}
	submission, err := submitUnifiedBackgroundResponse(ctx, record, plan.Route, encodedRequest, idempotencyRequestJSON, mediaAssetIDs, idempotency, publicPreviousResponseID)
	if err != nil {
		return nil, err
	}
	if submission.PublicID != "" {
		record.ID, record.CallID = submission.PublicID, submission.PublicID
		responseID = submission.PublicID
	}
	if submission.Reused {
		response, getErr := getUnifiedResponse(ctx, userID, tokenID, responseID)
		if getErr != nil {
			return nil, getErr
		}
		return &Result{Response: response, Record: record, CallID: responseID, IdempotentReplay: true}, nil
	}
	queued := &protocol.Response{
		ID: responseID, Object: "response", CreatedAt: record.CreatedAt.Unix(), Status: "queued",
		Background: true, Model: req.Model, Store: true, Output: json.RawMessage(`[]`), Tools: json.RawMessage(`[]`),
	}
	setPublicPreviousResponseID(queued, publicPreviousResponseID)
	record.ResponseJSON = mustJSON(queued)
	return &Result{Response: queued, Record: record, CallID: record.CallID}, nil
}

func (p *Pipeline) ProxyV2Stream(ctx context.Context, writer http.ResponseWriter, result *Result, request *protocol.Request) (returnErr error) {
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
	streamProjection := responseProjectionFromStreamSummary(result.conversation, summary)
	stageResponseConversationOutputBestEffort(result.Record, streamProjection)
	if err != nil {
		clientDisconnected := ctx.Err() != nil || errors.Is(err, errV2StreamDownstreamWrite)
		deliveryErr := result.V2Stream.FailDelivery(err, clientDisconnected)
		if clientDisconnected || responseCallStatus(result.Record.CallID) == execution.CallCancelled {
			return errors.Join(err, deliveryErr, p.cancelV2Record(result.Record, streamProjection))
		}
		return errors.Join(err, deliveryErr, p.failV2Record(result.Record, err, streamProjection))
	}
	response, err := publicV2StreamResponse(summary, result.Record, request, result.PublicPreviousResponseID)
	if err != nil {
		deliveryErr := result.V2Stream.FailDelivery(err, false)
		return errors.Join(err, deliveryErr, p.failV2Record(result.Record, err, streamProjection))
	}
	result.Record.ProviderResponseID = summary.ProviderResponseID
	completeErr := p.completeV2Record(result.Record, response)
	if completeErr != nil {
		deliveryErr := result.V2Stream.FailDelivery(completeErr, false)
		return errors.Join(completeErr, deliveryErr, p.failV2Record(result.Record, completeErr, streamProjection))
	}
	if err := result.V2Stream.CompleteDelivery(); err != nil {
		return err
	}
	projectResponseConversationBestEffort(result.Record)
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

package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/gateway/canonical"
	codec "github.com/mirainya/Prism/internal/gateway/codec/anthropic"
	"github.com/mirainya/Prism/internal/gateway/engine"
	"github.com/mirainya/Prism/internal/gateway/limits"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/gateway/transport"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
)

type AnthropicHandler struct{ engine *engine.Engine }

func NewAnthropicHandler(executionEngine *engine.Engine) *AnthropicHandler {
	if executionEngine == nil {
		return nil
	}
	return &AnthropicHandler{engine: executionEngine}
}

func (h *AnthropicHandler) Messages(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxPublicConversationRequestBytes)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", "request body is invalid")
		return
	}
	if requestJSONHasField(body, "conversation_id") {
		writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", "conversation_id must be provided through X-Prism-Conversation-ID")
		return
	}
	request, err := codec.DecodeRequestJSON(body)
	if err != nil {
		writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	token := middleware.GetToken(c)
	conversationID, err := parsePrismConversationID("", c.GetHeader(prismConversationIDHeader))
	if err != nil {
		writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if err := service.ValidateAPIConversationID(conversationID, token.UserID, token.ID); err != nil {
		if errors.Is(err, service.ErrConversationNotFound) {
			writeAnthropicError(c, http.StatusNotFound, "not_found_error", "conversation not found")
		} else {
			writeAnthropicError(c, http.StatusInternalServerError, "api_error", "failed to validate conversation")
		}
		return
	}
	if conversationID > 0 {
		c.Header(prismConversationIDHeader, strconv.FormatUint(uint64(conversationID), 10))
	}
	callID := "call_" + uuid.NewString()
	requestID := middleware.GetRequestID(c.Request.Context())
	c.Header("X-Prism-Call-ID", callID)
	projectionBase := service.ConversationProjectionRequest{
		UserID: token.UserID, TokenID: token.ID, Model: request.Model,
		CallID: callID, ConversationID: conversationID, InputItems: request.Items,
	}
	store := request.Store != nil && *request.Store
	if err := createAPIConversationCall(&service.StartCallRequest{
		ID: callID, RequestID: requestID, UserID: token.UserID, TokenID: token.ID,
		Endpoint: "/v1/messages", Operation: string(transport.OperationMessages), Model: request.Model,
		IsStream: request.Stream, Store: store, ConversationID: conversationID,
	}, service.ConversationProjectionInputRequest{
		ConversationID: conversationID, InputItems: canonical.CloneItems(request.Items),
	}); err != nil {
		writeAnthropicError(c, http.StatusInternalServerError, "api_error", "failed to initialize conversation history")
		return
	}
	result, err := h.engine.Execute(c.Request.Context(), request, engine.ExecuteOptions{
		UserID: token.UserID, TokenID: token.ID,
		CallID: callID, RequestID: requestID, DownstreamEndpoint: "/v1/messages",
		DownstreamRequest:   body,
		ConversationID:      conversationID,
		ProjectConversation: true,
		DeferCallCompletion: true,
		BillingKey:          requestID, MaxAttempts: 3,
		PrepareRoute: func(_ context.Context, candidate canonical.Request, route *routing.RouteResult) (canonical.Request, error) {
			return limits.ApplyModelMaxOutputTokens(candidate, route.ModelName), nil
		},
	})
	if err != nil {
		writeAnthropicExecutionError(c, err)
		projectAPIConversationBestEffort("project failed anthropic message", projectionBase)
		return
	}
	if result.RequestLogID > 0 {
		c.Header("X-Prism-Request-Log-ID", strconv.FormatUint(uint64(result.RequestLogID), 10))
	}
	if result.Stream != nil {
		h.writeStream(c, request.Model, result.Stream, result.RequestLogID, projectionBase)
		return
	}
	if result.Response == nil {
		stageAPIConversationOutputBestEffort("stage failed anthropic message output", service.ConversationProjectionOutputRequest{
			CallID: result.CallID, OutputItems: []canonical.Item{}, RequestLogID: result.RequestLogID,
		})
		deliveryErr := result.FailDelivery(errors.New("Gateway V2 returned no response"), false)
		logDeliveryError("fail anthropic message delivery", result.CallID, deliveryErr)
		writeAnthropicError(c, http.StatusBadGateway, "api_error", "Gateway V2 returned no response")
		if deliveryErr == nil {
			projectCanonicalResponseBestEffort("project failed anthropic message", projectionBase,
				canonical.Response{}, result.RequestLogID, "", result.Route.KeyID, result.Route.Transport)
		}
		return
	}
	result.Response.Model = request.Model
	canonicalResponse := cloneAnthropicCanonicalResponse(*result.Response)
	stageAPIConversationOutputBestEffort("stage anthropic message output", service.ConversationProjectionOutputRequest{
		CallID: result.CallID, OutputItems: canonical.CloneItems(canonicalResponse.Output),
		RequestLogID: result.RequestLogID, ProviderResponseID: canonicalProviderResponseID(canonicalResponse),
		FinishReason: canonicalResponse.FinishReason,
	})
	encoded, err := codec.EncodeResponseJSON(*result.Response)
	if err != nil {
		deliveryErr := result.FailDelivery(err, false)
		logDeliveryError("fail anthropic message delivery", result.CallID, deliveryErr)
		writeAnthropicError(c, http.StatusBadGateway, "api_error", err.Error())
		if deliveryErr == nil {
			projectCanonicalResponseBestEffort("project failed anthropic message", projectionBase,
				canonicalResponse, result.RequestLogID, "", result.Route.KeyID, result.Route.Transport)
		}
		return
	}
	c.Header("Content-Type", "application/json; charset=utf-8")
	c.Status(http.StatusOK)
	written, writeErr := c.Writer.Write(encoded)
	if written > 0 {
		captured := written
		if captured > len(encoded) {
			captured = len(encoded)
		}
		service.NewAPICallService().RecordPayloadBestEffort(&model.APICallPayload{
			CallID: result.CallID, AttemptID: result.AttemptID,
			Kind: model.APICallPayloadResponse, ContentType: "application/json", Data: encoded[:captured],
		})
	}
	if writeErr == nil && written != len(encoded) {
		writeErr = io.ErrShortWrite
	}
	if writeErr != nil {
		deliveryErr := result.FailDelivery(writeErr, true)
		logDeliveryError("fail anthropic call delivery", result.CallID, deliveryErr)
		if deliveryErr == nil {
			projectCanonicalResponseBestEffort("project aborted anthropic message", projectionBase,
				canonicalResponse, result.RequestLogID, "", result.Route.KeyID, result.Route.Transport)
		}
		return
	}
	deliveryErr := result.CompleteDelivery()
	logDeliveryError("complete anthropic call delivery", result.CallID, deliveryErr)
	if deliveryErr == nil {
		projectCanonicalResponseBestEffort("project anthropic message", projectionBase,
			canonicalResponse, result.RequestLogID, "", result.Route.KeyID, result.Route.Transport)
	}
}

func (h *AnthropicHandler) writeStream(
	c *gin.Context,
	publicModel string,
	stream *engine.StreamResult,
	requestLogID uint,
	projectionBase service.ConversationProjectionRequest,
) {
	defer stream.Close()
	capture := service.NewAPICallService().NewPayloadCaptureBestEffort(
		stream.CallID, stream.AttemptID, model.APICallPayloadResponse, "text/event-stream",
	)
	defer capture.SaveBestEffort()
	encoder := codec.NewSSEEncoder(publicModel)
	upstreamTransport := transport.ID("")
	if stream.Route != nil {
		upstreamTransport = stream.Route.Transport
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		stageAnthropicStreamOutput("stage failed anthropic stream output", stream, requestLogID)
		deliveryErr := stream.Abort(errors.New("downstream response writer does not support streaming"), false)
		logDeliveryError("abort anthropic stream delivery", stream.CallID, deliveryErr)
		projectAnthropicStream("project failed anthropic stream", projectionBase, stream, requestLogID)
		return
	}
	for {
		event, err := stream.Next(c.Request.Context())
		if errors.Is(err, io.EOF) {
			stageAnthropicStreamOutput("stage anthropic stream output", stream, requestLogID)
			deliveryErr := stream.CompleteDelivery()
			logDeliveryError("complete anthropic stream delivery", stream.CallID, deliveryErr)
			if deliveryErr == nil {
				projectAnthropicStream("project anthropic stream", projectionBase, stream, requestLogID)
			}
			return
		}
		if err != nil {
			stageAnthropicStreamOutput("stage failed anthropic stream output", stream, requestLogID)
			deliveryErr := stream.FailDelivery(err, c.Request.Context().Err() != nil)
			logDeliveryError("fail anthropic stream delivery", stream.CallID, deliveryErr)
			if deliveryErr == nil {
				projectAnthropicStream("project failed anthropic stream", projectionBase, stream, requestLogID)
			}
			return
		}
		if !normalizeAnthropicEvent(&event, publicModel, upstreamTransport) {
			continue
		}
		frame, encodeErr := encoder.Encode(event)
		if encodeErr != nil {
			stageAnthropicStreamOutput("stage failed anthropic stream output", stream, requestLogID)
			deliveryErr := stream.Abort(encodeErr, false)
			logDeliveryError("abort anthropic stream delivery", stream.CallID, deliveryErr)
			projectAnthropicStream("project failed anthropic stream", projectionBase, stream, requestLogID)
			return
		}
		if len(frame) == 0 {
			continue
		}
		written, writeErr := c.Writer.Write(frame)
		if written > 0 && capture != nil {
			captured := written
			if captured > len(frame) {
				captured = len(frame)
			}
			_, _ = capture.Write(frame[:captured])
		}
		if writeErr != nil {
			stageAnthropicStreamOutput("stage aborted anthropic stream output", stream, requestLogID)
			deliveryErr := stream.Abort(writeErr, true)
			logDeliveryError("abort anthropic stream delivery", stream.CallID, deliveryErr)
			projectAnthropicStream("project aborted anthropic stream", projectionBase, stream, requestLogID)
			return
		}
		if written != len(frame) {
			stageAnthropicStreamOutput("stage aborted anthropic stream output", stream, requestLogID)
			deliveryErr := stream.Abort(io.ErrShortWrite, true)
			logDeliveryError("abort anthropic stream delivery", stream.CallID, deliveryErr)
			projectAnthropicStream("project aborted anthropic stream", projectionBase, stream, requestLogID)
			return
		}
		flusher.Flush()
	}
}

func stageAnthropicStreamOutput(action string, stream *engine.StreamResult, requestLogID uint) canonical.Response {
	response := stream.CanonicalResponse()
	stageAPIConversationOutputBestEffort(action, service.ConversationProjectionOutputRequest{
		CallID: stream.CallID, OutputItems: canonical.CloneItems(response.Output),
		RequestLogID: requestLogID, ProviderResponseID: canonicalProviderResponseID(response),
		FinishReason: response.FinishReason,
	})
	return response
}

func projectAnthropicStream(action string, projection service.ConversationProjectionRequest, stream *engine.StreamResult, requestLogID uint) {
	response := stream.CanonicalResponse()
	keyID := uint(0)
	upstreamTransport := model.UpstreamTransport("")
	if stream.Route != nil {
		keyID = stream.Route.KeyID
		upstreamTransport = stream.Route.Transport
	}
	projectCanonicalResponseBestEffort(action, projection, response, requestLogID,
		canonicalProviderResponseID(response), keyID, upstreamTransport)
}

func cloneAnthropicCanonicalResponse(source canonical.Response) canonical.Response {
	clone := source
	clone.Output = canonical.CloneItems(source.Output)
	return clone
}

func normalizeAnthropicEvent(event *canonical.Event, publicModel string, upstreamTransport transport.ID) bool {
	if event == nil {
		return false
	}
	if event.Type == canonical.EventRaw {
		return upstreamTransport == transport.AnthropicMessages
	}
	event.Raw = nil
	event.RawType = ""
	if event.Response != nil {
		event.Response.Model = publicModel
	}
	return true
}

func writeAnthropicExecutionError(c *gin.Context, err error) {
	status := domain.UpstreamStatusCode(err)
	if status == 0 {
		status = http.StatusBadGateway
	}
	message := err.Error()
	errorType := "api_error"
	if details := transport.DetailsFromError(err); details != nil {
		if details.Message != "" {
			message = details.Message
		}
		if details.Type != "" {
			errorType = details.Type
		}
	}
	if errors.Is(err, routing.ErrModelNotFound) {
		status, errorType = http.StatusNotFound, "not_found_error"
	} else if errors.Is(err, routing.ErrCapabilityUnavailable) || errors.Is(err, routing.ErrNoCompatibleTransport) || errors.Is(err, engine.ErrNoTransportPlan) {
		status, errorType = http.StatusBadRequest, "invalid_request_error"
		message = "The requested model does not support this Anthropic request"
	} else if errors.Is(err, routing.ErrNoRoute) {
		status, errorType = http.StatusServiceUnavailable, "overloaded_error"
	} else if status == http.StatusUnauthorized {
		errorType = "authentication_error"
	} else if status == http.StatusForbidden {
		errorType = "permission_error"
	} else if status == http.StatusTooManyRequests {
		errorType = "rate_limit_error"
	}
	writeAnthropicError(c, status, errorType, message)
}

func writeAnthropicError(c *gin.Context, status int, errorType, message string) {
	c.JSON(status, gin.H{
		"type": "error", "error": gin.H{"type": errorType, "message": message},
		"request_id": middleware.GetRequestID(c.Request.Context()),
	})
}

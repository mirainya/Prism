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
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32*1024*1024)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", "request body is invalid")
		return
	}
	request, err := codec.DecodeRequestJSON(body)
	if err != nil {
		writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	callID := "call_" + uuid.NewString()
	requestID := middleware.GetRequestID(c.Request.Context())
	c.Header("X-Prism-Call-ID", callID)
	token := middleware.GetToken(c)
	result, err := h.engine.Execute(c.Request.Context(), request, engine.ExecuteOptions{
		UserID: token.UserID, TokenID: token.ID,
		CallID: callID, RequestID: requestID, DownstreamEndpoint: "/v1/messages",
		DownstreamRequest:   body,
		DeferCallCompletion: true,
		BillingKey:          requestID, MaxAttempts: 3,
		PrepareRoute: func(_ context.Context, candidate canonical.Request, route *routing.RouteResult) (canonical.Request, error) {
			return limits.ApplyModelMaxOutputTokens(candidate, route.ModelName), nil
		},
	})
	if err != nil {
		writeAnthropicExecutionError(c, err)
		return
	}
	if result.RequestLogID > 0 {
		c.Header("X-Prism-Request-Log-ID", strconv.FormatUint(uint64(result.RequestLogID), 10))
	}
	if result.Stream != nil {
		h.writeStream(c, request.Model, result.Stream)
		return
	}
	if result.Response == nil {
		_ = result.FailDelivery(errors.New("Gateway V2 returned no response"), false)
		writeAnthropicError(c, http.StatusBadGateway, "api_error", "Gateway V2 returned no response")
		return
	}
	result.Response.Model = request.Model
	encoded, err := codec.EncodeResponseJSON(*result.Response)
	if err != nil {
		_ = result.FailDelivery(err, false)
		writeAnthropicError(c, http.StatusBadGateway, "api_error", err.Error())
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
		logDeliveryError("fail anthropic call delivery", result.CallID, result.FailDelivery(writeErr, true))
		return
	}
	logDeliveryError("complete anthropic call delivery", result.CallID, result.CompleteDelivery())
}

func (h *AnthropicHandler) writeStream(c *gin.Context, publicModel string, stream *engine.StreamResult) {
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
		logDeliveryError("abort anthropic stream delivery", stream.CallID,
			stream.Abort(errors.New("downstream response writer does not support streaming"), false))
		return
	}
	for {
		event, err := stream.Next(c.Request.Context())
		if errors.Is(err, io.EOF) {
			logDeliveryError("complete anthropic stream delivery", stream.CallID, stream.CompleteDelivery())
			return
		}
		if err != nil {
			logDeliveryError("fail anthropic stream delivery", stream.CallID,
				stream.FailDelivery(err, c.Request.Context().Err() != nil))
			return
		}
		if !normalizeAnthropicEvent(&event, publicModel, upstreamTransport) {
			continue
		}
		frame, encodeErr := encoder.Encode(event)
		if encodeErr != nil {
			logDeliveryError("abort anthropic stream delivery", stream.CallID, stream.Abort(encodeErr, false))
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
			logDeliveryError("abort anthropic stream delivery", stream.CallID, stream.Abort(writeErr, true))
			return
		}
		if written != len(frame) {
			logDeliveryError("abort anthropic stream delivery", stream.CallID, stream.Abort(io.ErrShortWrite, true))
			return
		}
		flusher.Flush()
	}
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
	} else if errors.Is(err, routing.ErrCapabilityUnavailable) || errors.Is(err, routing.ErrNoCompatibleTransport) {
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

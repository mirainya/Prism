package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/gateway/canonical"
	codec "github.com/mirainya/Prism/internal/gateway/codec/anthropic"
	"github.com/mirainya/Prism/internal/gateway/engine"
	"github.com/mirainya/Prism/internal/gateway/limits"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/gateway/transport"
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
	token := middleware.GetToken(c)
	result, err := h.engine.Execute(c.Request.Context(), request, engine.ExecuteOptions{
		UserID: token.UserID, TokenID: token.ID,
		BillingKey: middleware.GetRequestID(c.Request.Context()), MaxAttempts: 3,
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
		writeAnthropicError(c, http.StatusBadGateway, "api_error", "Gateway V2 returned no response")
		return
	}
	result.Response.Model = request.Model
	encoded, err := codec.EncodeResponseJSON(*result.Response)
	if err != nil {
		writeAnthropicError(c, http.StatusBadGateway, "api_error", err.Error())
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", encoded)
}

func (h *AnthropicHandler) writeStream(c *gin.Context, publicModel string, stream *engine.StreamResult) {
	defer stream.Close()
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
		return
	}
	for {
		event, err := stream.Next(c.Request.Context())
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			return
		}
		if !normalizeAnthropicEvent(&event, publicModel, upstreamTransport) {
			continue
		}
		frame, encodeErr := encoder.Encode(event)
		if encodeErr != nil {
			return
		}
		if len(frame) == 0 {
			continue
		}
		if _, err = c.Writer.Write(frame); err != nil {
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

package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/api/openaierror"
	"github.com/mirainya/Prism/internal/domain"
	responsepipeline "github.com/mirainya/Prism/internal/gateway/responses"
	"github.com/mirainya/Prism/internal/gateway/routing"
	gatewaytransport "github.com/mirainya/Prism/internal/gateway/transport"
	protocol "github.com/mirainya/Prism/internal/provider/responses"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/pkg/httputil"
	"gorm.io/gorm"
)

type ResponsesHandler struct{ pipe *responsepipeline.Pipeline }

func NewResponsesHandler(pipe *responsepipeline.Pipeline) *ResponsesHandler {
	return &ResponsesHandler{pipe: pipe}
}

func (h *ResponsesHandler) Create(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxPublicConversationRequestBytes)
	var req protocol.Request
	if err := decodeStrictJSON(c, &req); err != nil {
		message, param, code := describeJSONError(err)
		openaierror.InvalidRequest(c, message, param, code)
		return
	}
	if message, param, code := validateResponseRequest(&req); message != "" {
		openaierror.InvalidRequest(c, message, param, code)
		return
	}
	token := middleware.GetToken(c)
	conversationID, err := parsePrismConversationID("", c.GetHeader(prismConversationIDHeader))
	if err != nil {
		param := prismConversationIDHeader
		openaierror.InvalidRequest(c, err.Error(), &param, "invalid_conversation_id")
		return
	}
	if err := service.ValidateAPIConversationID(conversationID, token.UserID, token.ID); err != nil {
		param := prismConversationIDHeader
		if errors.Is(err, service.ErrConversationNotFound) {
			openaierror.Write(c, http.StatusNotFound, "conversation not found", "invalid_request_error", &param, "conversation_not_found")
		} else {
			openaierror.Write(c, http.StatusInternalServerError, "failed to validate conversation", "server_error", &param, "conversation_validation_failed")
		}
		return
	}
	if conversationID > 0 {
		c.Header(prismConversationIDHeader, strconv.FormatUint(uint64(conversationID), 10))
	}
	result, err := h.pipe.CreateWithOptions(c.Request.Context(), token.UserID, token.ID, &req,
		c.GetHeader("Idempotency-Key"), responsepipeline.CreateOptions{
			RequestID: middleware.GetRequestID(c.Request.Context()), ConversationID: conversationID,
		})
	if err != nil {
		if callID := responsepipeline.CallIDFromError(err); callID != "" {
			c.Header("X-Prism-Call-ID", callID)
		}
		respondResponsesError(c, err)
		return
	}
	setResponsesRecordHeaders(c, result)
	if result.V2Stream != nil {
		if err := h.pipe.ProxyV2Stream(c.Request.Context(), c.Writer, result, &req); err != nil {
			logDeliveryError("finalize responses stream delivery", result.CallID, err)
			if !c.Writer.Written() {
				respondResponsesError(c, err)
			}
		}
		return
	}
	if req.Stream && result.IdempotentReplay {
		if err := responsepipeline.ProxyIdempotentReplay(c.Writer, result.Response); err != nil {
			logDeliveryError("fail responses replay delivery", result.CallID, result.FailDelivery(err, true))
			if !c.Writer.Written() {
				respondResponsesError(c, err)
			}
			return
		}
		logDeliveryError("complete responses replay delivery", result.CallID, result.CompleteDelivery())
		return
	}
	encoded, err := json.Marshal(result.Response)
	finalizeDelivery := !req.Background || result.IdempotentReplay
	if err != nil {
		if finalizeDelivery {
			logDeliveryError("fail responses call delivery", result.CallID, result.FailDelivery(err, false))
		}
		respondResponsesError(c, err)
		return
	}
	c.Header("Content-Type", "application/json; charset=utf-8")
	c.Status(http.StatusOK)
	written, writeErr := c.Writer.Write(encoded)
	if writeErr == nil && written != len(encoded) {
		writeErr = io.ErrShortWrite
	}
	if writeErr != nil {
		if finalizeDelivery {
			logDeliveryError("fail responses call delivery", result.CallID, result.FailDelivery(writeErr, true))
		}
		return
	}
	if finalizeDelivery {
		logDeliveryError("complete responses call delivery", result.CallID, result.CompleteDelivery())
	}
}

func setResponsesRecordHeaders(c *gin.Context, result *responsepipeline.Result) {
	if result == nil || result.Record == nil {
		return
	}
	record := result.Record
	callID := result.CallID
	if callID == "" {
		callID = record.CallID
	}
	if record.RequestLogID > 0 && callID == record.CallID {
		c.Header("X-Prism-Request-Log-ID", strconv.FormatUint(uint64(record.RequestLogID), 10))
	}
	if callID != "" {
		c.Header("X-Prism-Call-ID", callID)
	}
}

func (h *ResponsesHandler) Get(c *gin.Context) {
	response, err := h.pipe.Get(middleware.GetTokenID(c), c.Param("id"))
	if err != nil {
		respondResponsesError(c, err)
		return
	}
	c.JSON(http.StatusOK, response)
}

func (h *ResponsesHandler) Delete(c *gin.Context) {
	if err := h.pipe.Delete(middleware.GetTokenID(c), c.Param("id")); err != nil {
		respondResponsesError(c, err)
		return
	}
	c.JSON(http.StatusOK, protocol.Deleted{ID: c.Param("id"), Object: "response.deleted", Deleted: true})
}

func (h *ResponsesHandler) Cancel(c *gin.Context) {
	response, err := h.pipe.Cancel(middleware.GetTokenID(c), c.Param("id"))
	if err != nil {
		respondResponsesError(c, err)
		return
	}
	c.JSON(http.StatusOK, response)
}

func (h *ResponsesHandler) InputItems(c *gin.Context) {
	limit := 20
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			param := "limit"
			openaierror.InvalidRequest(c, "limit must be between 1 and 100", &param, "invalid_value")
			return
		}
		limit = parsed
	}
	order := strings.ToLower(strings.TrimSpace(c.DefaultQuery("order", "desc")))
	if order != "asc" && order != "desc" {
		param := "order"
		openaierror.InvalidRequest(c, "order must be asc or desc", &param, "invalid_value")
		return
	}
	items, err := h.pipe.InputItems(middleware.GetTokenID(c), c.Param("id"), responsepipeline.InputItemsOptions{Limit: limit, Order: order, After: strings.TrimSpace(c.Query("after"))})
	if err != nil {
		respondResponsesError(c, err)
		return
	}
	c.JSON(http.StatusOK, items)
}

func validateResponseRequest(req *protocol.Request) (string, *string, any) {
	param := func(value string) *string { return &value }
	if strings.TrimSpace(req.Model) == "" {
		return "model is required", param("model"), "missing_required_parameter"
	}
	if len(bytes.TrimSpace(req.Input)) == 0 || bytes.Equal(bytes.TrimSpace(req.Input), []byte("null")) {
		return "input is required", param("input"), "missing_required_parameter"
	}
	if message, param, code := validateRawJSONFields(req); message != "" {
		return message, param, code
	}
	if !json.Valid(req.Input) {
		return "input must be valid JSON", param("input"), "invalid_json"
	}
	var input any
	decoder := json.NewDecoder(bytes.NewReader(req.Input))
	decoder.UseNumber()
	if err := decoder.Decode(&input); err != nil {
		return "input is invalid", param("input"), "invalid_type"
	}
	switch value := input.(type) {
	case string:
		if value == "" {
			return "input must not be empty", param("input"), "invalid_value"
		}
	case []any:
		if len(value) == 0 {
			return "input must not be empty", param("input"), "invalid_value"
		}
		for _, item := range value {
			object, ok := item.(map[string]any)
			if !ok {
				p := "input"
				return "input items must be objects", &p, "invalid_type"
			}
			if _, hasRole := object["role"].(string); !hasRole {
				if _, ok := object["type"].(string); !ok {
					p := "input"
					return "input item type is required", &p, "missing_required_parameter"
				}
			}
		}
	default:
		return "input must be a string or an array", param("input"), "invalid_type"
	}
	if req.MaxOutputTokens < 0 {
		return "max_output_tokens must be non-negative", param("max_output_tokens"), "invalid_value"
	}
	if req.MaxToolCalls != nil && *req.MaxToolCalls < 1 {
		return "max_tool_calls must be positive", param("max_tool_calls"), "invalid_value"
	}
	if req.TopLogprobs != nil && (*req.TopLogprobs < 0 || *req.TopLogprobs > 20) {
		return "top_logprobs must be between 0 and 20", param("top_logprobs"), "invalid_value"
	}
	if req.Background && req.Stream {
		return "background and stream cannot both be true", param("background"), "invalid_value"
	}
	if req.Background && req.Store != nil && !*req.Store {
		return "background responses must be stored", param("store"), "invalid_value"
	}
	return "", nil, nil
}

func validateRawJSONFields(req *protocol.Request) (string, *string, any) {
	fields := []struct {
		name     string
		value    json.RawMessage
		expected string
	}{{"tools", req.Tools, "array"}, {"tool_choice", req.ToolChoice, "any"}, {"reasoning", req.Reasoning, "object"}, {"thinking", req.Thinking, "object"}, {"caching", req.Caching, "object"}, {"text", req.Text, "object"}, {"conversation", req.Conversation, "any"}, {"prompt", req.Prompt, "object"}, {"stream_options", req.StreamOptions, "object"}, {"context_management", req.ContextManagement, "object_or_array"}, {"session", req.Session, "object"}}
	for _, field := range fields {
		trimmed := bytes.TrimSpace(field.value)
		if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
			continue
		}
		if !json.Valid(trimmed) {
			name := field.name
			return field.name + " must be valid JSON", &name, "invalid_json"
		}
		var value any
		_ = json.Unmarshal(trimmed, &value)
		if field.expected == "array" {
			if _, ok := value.([]any); !ok {
				name := field.name
				return field.name + " must be an array", &name, "invalid_type"
			}
		}
		if field.expected == "object" {
			if _, ok := value.(map[string]any); !ok {
				name := field.name
				return field.name + " must be an object", &name, "invalid_type"
			}
		}
		if field.expected == "object_or_array" {
			if _, objectOK := value.(map[string]any); !objectOK {
				if _, arrayOK := value.([]any); !arrayOK {
					name := field.name
					return field.name + " must be an object or an array", &name, "invalid_type"
				}
			}
		}
	}
	if len(req.Tools) > 0 {
		var tools []struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(req.Tools, &tools) == nil {
			for i, tool := range tools {
				if tool.Type == "file_search" {
					name := fmt.Sprintf("tools[%d].type", i)
					return "file_search requires Prism-owned vector stores, which are not configured", &name, "unsupported_tool"
				}
			}
		}
	}
	return "", nil, nil
}

func respondResponsesError(c *gin.Context, err error) {
	if appErr, ok := domain.IsAppError(err); ok {
		openaierror.Write(c, appErr.HTTPStatus, appErr.Message, "invalid_request_error", nil, appErr.Code)
		return
	}
	if details := gatewaytransport.DetailsFromError(err); details != nil {
		status := details.Status
		if status == 0 {
			status = domain.UpstreamStatusCode(err)
		}
		if status == 0 {
			status = http.StatusBadGateway
		}
		errorType := details.Type
		if errorType == "" {
			switch status {
			case http.StatusUnauthorized:
				errorType = "authentication_error"
			case http.StatusForbidden:
				errorType = "permission_error"
			case http.StatusTooManyRequests:
				errorType = "rate_limit_error"
			default:
				if status >= 500 {
					errorType = "server_error"
				} else {
					errorType = "invalid_request_error"
				}
			}
		}
		message := details.Message
		if message == "" {
			message = err.Error()
		}
		code := any(details.Code)
		if code == "" {
			code = "upstream_error"
		}
		var param *string
		if value, ok := details.Param.(string); ok && value != "" {
			param = &value
		}
		openaierror.Write(c, status, message, errorType, param, code)
		return
	}
	var upstreamErr *httputil.HTTPError
	if errors.As(err, &upstreamErr) {
		errorType := upstreamErr.Type
		if errorType == "" {
			switch upstreamErr.Status {
			case http.StatusUnauthorized:
				errorType = "authentication_error"
			case http.StatusForbidden:
				errorType = "permission_error"
			case http.StatusTooManyRequests:
				errorType = "rate_limit_error"
			default:
				if upstreamErr.Status >= 500 {
					errorType = "server_error"
				} else {
					errorType = "invalid_request_error"
				}
			}
		}
		code := upstreamErr.Code
		if code == nil {
			code = "upstream_error"
		}
		openaierror.Write(c, upstreamErr.Status, upstreamErr.Message, errorType, upstreamErr.Param, code)
		return
	}
	if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, routing.ErrModelNotFound) {
		openaierror.Write(c, http.StatusNotFound, "Response or model not found", "invalid_request_error", nil, "not_found")
		return
	}
	if errors.Is(err, routing.ErrCapabilityUnavailable) || errors.Is(err, routing.ErrNoCompatibleTransport) {
		openaierror.InvalidRequest(c, "The requested model does not support this Responses request", nil, "unsupported_model_capability")
		return
	}
	if errors.Is(err, routing.ErrNoRoute) {
		openaierror.Write(c, http.StatusServiceUnavailable, "The requested model is temporarily unavailable", "server_error", nil, "model_unavailable")
		return
	}
	openaierror.Write(c, http.StatusBadGateway, "Responses request failed", "server_error", nil, "upstream_error")
}

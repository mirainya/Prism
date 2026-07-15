// Package handler 提供聊天网关的 gin handler。
package handler

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/api/openaierror"
	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/gateway/pipeline"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/gateway/stream"
	"github.com/mirainya/Prism/internal/gateway/transport"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider/chat"
	"github.com/mirainya/Prism/internal/service"
)

// ChatHandler 持有 pipeline。
type ChatHandler struct {
	pipe *pipeline.Pipeline
}

type payloadStreamWriter struct {
	writer   stream.Writer
	capture  io.Writer
	writeErr error
}

func (w *payloadStreamWriter) Write(data []byte) (int, error) {
	written, err := w.writer.Write(data)
	if err != nil {
		w.writeErr = err
	} else if written != len(data) {
		w.writeErr = io.ErrShortWrite
	}
	if written > 0 && w.capture != nil {
		captured := written
		if captured > len(data) {
			captured = len(data)
		}
		_, _ = w.capture.Write(data[:captured])
	}
	return written, err
}

func (w *payloadStreamWriter) Flush() { w.writer.Flush() }

type stopSequences []string

func (s *stopSequences) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		*s = nil
		return nil
	}
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*s = []string{single}
		return nil
	}
	var multiple []string
	if err := json.Unmarshal(data, &multiple); err != nil {
		return errors.New("stop must be a string or an array of strings")
	}
	*s = multiple
	return nil
}

// NewChatHandler 构造。
func NewChatHandler(pipe *pipeline.Pipeline) *ChatHandler {
	return &ChatHandler{pipe: pipe}
}

// Completions POST /v1/chat/completions
func (h *ChatHandler) Completions(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxPublicConversationRequestBytes)
	var req struct {
		Model               string                `json:"model" binding:"required"`
		Messages            []chat.ChatMessage    `json:"messages" binding:"required,min=1"`
		Temperature         *float64              `json:"temperature"`
		MaxTokens           *int                  `json:"max_tokens"`
		MaxCompletionTokens *int                  `json:"max_completion_tokens"`
		TopP                *float64              `json:"top_p"`
		FrequencyPenalty    *float64              `json:"frequency_penalty"`
		PresencePenalty     *float64              `json:"presence_penalty"`
		Stop                stopSequences         `json:"stop"`
		Stream              bool                  `json:"stream"`
		StreamOptions       *chat.StreamOptions   `json:"stream_options"`
		N                   *int                  `json:"n"`
		Logprobs            *bool                 `json:"logprobs"`
		TopLogprobs         *int                  `json:"top_logprobs"`
		Tools               []chat.ToolDefinition `json:"tools"`
		ToolChoice          any                   `json:"tool_choice"`
		ParallelToolCalls   *bool                 `json:"parallel_tool_calls"`
		ResponseFormat      *chat.ResponseFormat  `json:"response_format"`
		Seed                *int                  `json:"seed"`
		User                string                `json:"user"`
		Modalities          []string              `json:"modalities"`
		Audio               *chat.AudioConfig     `json:"audio"`
		Prediction          *chat.Prediction      `json:"prediction"`
		Store               *bool                 `json:"store"`
		Metadata            map[string]string     `json:"metadata"`
		ServiceTier         *string               `json:"service_tier"`
		ReasoningEffort     *string               `json:"reasoning_effort"`
		ConversationID      json.RawMessage       `json:"conversation_id"`
		// PreviousResponseID 客户端托管的火山 B 模式续话:带上次响应的 provider_response_id,
		// 上游只需处理本轮新消息即可省 token(自愈失效见 pipeline.Complete)。
		PreviousResponseID string `json:"previous_response_id"`
	}
	if err := decodeStrictJSON(c, &req); err != nil {
		message, param, code := describeJSONError(err)
		openaierror.InvalidRequest(c, message, param, code)
		return
	}
	if strings.TrimSpace(req.Model) == "" {
		param := "model"
		openaierror.InvalidRequest(c, "model is required", &param, "missing_required_parameter")
		return
	}
	if len(req.Messages) == 0 {
		param := "messages"
		openaierror.InvalidRequest(c, "messages must contain at least one message", &param, "missing_required_parameter")
		return
	}
	if message, param, code := validateChatMessages(req.Messages); message != "" {
		openaierror.InvalidRequest(c, message, &param, code)
		return
	}
	if message, param, code := validateChatOptions(req.MaxTokens, req.MaxCompletionTokens, req.Stream, req.StreamOptions, req.N, req.Logprobs, req.TopLogprobs, req.Tools, req.Modalities, req.Audio, req.Prediction); message != "" {
		openaierror.InvalidRequest(c, message, &param, code)
		return
	}
	maxTokens := 0
	if req.MaxTokens != nil {
		maxTokens = *req.MaxTokens
	}

	token := middleware.GetToken(c)
	bodyConversationID, err := parseJSONConversationID(req.ConversationID)
	if err != nil {
		param := "conversation_id"
		openaierror.InvalidRequest(c, err.Error(), &param, "invalid_conversation_id")
		return
	}
	conversationID, err := parsePrismConversationID(bodyConversationID, c.GetHeader(prismConversationIDHeader))
	if err != nil {
		param := "conversation_id"
		openaierror.InvalidRequest(c, err.Error(), &param, "invalid_conversation_id")
		return
	}
	if err := service.ValidateAPIConversationID(conversationID, token.UserID, token.ID); err != nil {
		param := "conversation_id"
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
	resolvedMessages, err := resolveOwnedChatFiles(token.ID, req.Messages)
	if err != nil {
		param := "messages"
		openaierror.InvalidRequest(c, err.Error(), &param, "file_not_found")
		return
	}
	callID := "call_" + uuid.NewString()
	requestID := middleware.GetRequestID(c.Request.Context())
	downstreamRequest, _ := json.Marshal(req)
	c.Header("X-Prism-Call-ID", callID)
	completionReq := &service.CompletionRequest{
		UserID:               token.UserID,
		TokenID:              token.ID,
		CallID:               callID,
		RequestID:            requestID,
		DownstreamEndpoint:   "/v1/chat/completions",
		DownstreamRequest:    downstreamRequest,
		ConversationRecordID: conversationID,
		Model:                req.Model,
		Messages:             resolvedMessages,
		Temperature:          req.Temperature,
		MaxTokens:            maxTokens,
		MaxCompletionTokens:  req.MaxCompletionTokens,
		TopP:                 req.TopP,
		FrequencyPenalty:     req.FrequencyPenalty,
		PresencePenalty:      req.PresencePenalty,
		Stop:                 []string(req.Stop),
		Stream:               req.Stream,
		StreamSpecified:      true,
		StreamOptions:        req.StreamOptions,
		N:                    req.N,
		Logprobs:             req.Logprobs,
		TopLogprobs:          req.TopLogprobs,
		Tools:                req.Tools,
		ToolChoice:           req.ToolChoice,
		ParallelToolCalls:    req.ParallelToolCalls,
		ResponseFormat:       req.ResponseFormat,
		Seed:                 req.Seed,
		User:                 req.User,
		Modalities:           req.Modalities,
		Audio:                req.Audio,
		Prediction:           req.Prediction,
		Store:                req.Store,
		Metadata:             req.Metadata,
		ServiceTier:          req.ServiceTier,
		ReasoningEffort:      req.ReasoningEffort,
		PreviousResponseID:   req.PreviousResponseID,
	}
	canonicalRequest, err := pipeline.CanonicalChatRequest(completionReq, req.Messages, req.Model)
	if err != nil {
		respondChatPipelineError(c, err)
		return
	}
	store := req.Store != nil && *req.Store
	if err := createAPIConversationCall(&service.StartCallRequest{
		ID: callID, RequestID: requestID, UserID: token.UserID, TokenID: token.ID,
		Endpoint: "/v1/chat/completions", Operation: string(transport.OperationChat), Model: req.Model,
		IsStream: req.Stream, Store: store, ConversationID: conversationID,
	}, service.ConversationProjectionInputRequest{
		ConversationID: conversationID, PreviousResponseID: req.PreviousResponseID,
		InputItems: canonical.CloneItems(canonicalRequest.Items),
	}); err != nil {
		respondChatPipelineError(c, err)
		return
	}
	projectionBase := service.ConversationProjectionRequest{
		UserID: token.UserID, TokenID: token.ID, Model: req.Model, CallID: callID,
		ConversationID: conversationID, PreviousResponseID: req.PreviousResponseID,
		InputItems: canonicalRequest.Items,
	}
	project := func(action string, requestLogID uint, providerResponseID string, keyID uint, upstreamTransport model.UpstreamTransport, response *service.CompletionResponse, streamResponse *canonical.Response) {
		projection := projectionBase
		projection.RequestLogID = requestLogID
		projection.ProviderResponseID = providerResponseID
		projection.Provenance = service.ConversationProvenance{KeyID: keyID, Transport: upstreamTransport}
		if response != nil && response.CanonicalResponse != nil {
			projection.OutputItems = response.CanonicalResponse.Output
			projection.FinishReason = response.CanonicalResponse.FinishReason
		}
		if streamResponse != nil {
			projection.OutputItems = streamResponse.Output
			projection.FinishReason = streamResponse.FinishReason
			if projection.ProviderResponseID == "" {
				projection.ProviderResponseID = canonicalProviderResponseID(*streamResponse)
			}
		}
		projectAPIConversationBestEffort(action, projection)
	}
	if req.Stream {
		session, err := h.pipe.StreamComplete(c.Request.Context(), completionReq)
		if err != nil {
			respondChatPipelineError(c, err)
			project("project failed chat stream", 0, "", 0, "", nil, nil)
			return
		}
		defer session.Cleanup()
		if session.RequestLogID() > 0 {
			c.Header("X-Prism-Request-Log-ID", strconv.FormatUint(uint64(session.RequestLogID()), 10))
		}

		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")
		c.Status(http.StatusOK)

		callService := service.NewAPICallService()
		capture := callService.NewPayloadCaptureBestEffort(
			session.CallID(), session.AttemptID(), model.APICallPayloadResponse, "text/event-stream",
		)
		defer capture.SaveBestEffort()
		writer := &payloadStreamWriter{writer: stream.Writer(c.Writer), capture: capture}
		_, streamErr := stream.ProxyStream(writer, session.UpstreamResp.Body)
		session.Cleanup()
		clientDisconnected := writer.writeErr != nil || c.Request.Context().Err() != nil
		canonicalResponse := session.CanonicalResponse()
		stageAPIConversationOutputBestEffort("stage chat stream output", service.ConversationProjectionOutputRequest{
			CallID: session.CallID(), OutputItems: canonical.CloneItems(canonicalResponse.Output),
			RequestLogID: session.RequestLogID(), ProviderResponseID: canonicalProviderResponseID(canonicalResponse),
			FinishReason: canonicalResponse.FinishReason,
		})
		providerResponseID, finalizeErr := session.FinalizeStreamDelivery(streamErr, clientDisconnected)
		logDeliveryError("finalize chat stream delivery", session.CallID(), finalizeErr)
		if finalizeErr == nil {
			project("project chat stream", session.RequestLogID(), providerResponseID,
				session.ProviderKeyID(), session.UpstreamTransport(), nil, &canonicalResponse)
		}
		return
	}

	chatResp, err := h.pipe.Complete(c.Request.Context(), completionReq)
	if err != nil {
		respondChatPipelineError(c, err)
		project("project failed chat completion", 0, "", 0, "", nil, nil)
		return
	}
	if chatResp.RequestLogID > 0 {
		c.Header("X-Prism-Request-Log-ID", strconv.FormatUint(uint64(chatResp.RequestLogID), 10))
	}
	if conversationID > 0 {
		chatResp.ConversationID = strconv.FormatUint(uint64(conversationID), 10)
	}
	if chatResp.CanonicalResponse != nil {
		stageAPIConversationOutputBestEffort("stage chat completion output", service.ConversationProjectionOutputRequest{
			CallID: chatResp.CallID, OutputItems: canonical.CloneItems(chatResp.CanonicalResponse.Output),
			RequestLogID: chatResp.RequestLogID, ProviderResponseID: chatResp.ProviderResponseID,
			FinishReason: chatResp.CanonicalResponse.FinishReason,
		})
	}
	encoded, err := json.Marshal(chatResp)
	if err != nil {
		deliveryErr := failChatDelivery(chatResp, err, false)
		logDeliveryError("fail chat completion delivery", chatResp.CallID, deliveryErr)
		respondChatPipelineError(c, err)
		if deliveryErr == nil {
			project("project failed chat completion", chatResp.RequestLogID, chatResp.ProviderResponseID,
				chatResp.ProviderKeyID, chatResp.UpstreamTransport, chatResp, nil)
		}
		return
	}
	service.NewAPICallService().RecordPayloadBestEffort(&model.APICallPayload{
		CallID: chatResp.CallID, AttemptID: chatResp.AttemptID,
		Kind: model.APICallPayloadResponse, ContentType: "application/json", Data: encoded,
	})
	c.Header("Content-Type", "application/json; charset=utf-8")
	c.Status(http.StatusOK)
	written, writeErr := c.Writer.Write(encoded)
	if writeErr == nil && written != len(encoded) {
		writeErr = io.ErrShortWrite
	}
	if writeErr != nil {
		deliveryErr := failChatDelivery(chatResp, writeErr, true)
		logDeliveryError("fail chat completion delivery", chatResp.CallID, deliveryErr)
		if deliveryErr == nil {
			project("project aborted chat completion", chatResp.RequestLogID, chatResp.ProviderResponseID,
				chatResp.ProviderKeyID, chatResp.UpstreamTransport, chatResp, nil)
		}
		return
	}
	deliveryErr := completeChatDelivery(chatResp)
	logDeliveryError("complete chat call delivery", chatResp.CallID, deliveryErr)
	if deliveryErr == nil {
		project("project chat completion", chatResp.RequestLogID, chatResp.ProviderResponseID,
			chatResp.ProviderKeyID, chatResp.UpstreamTransport, chatResp, nil)
	}
}

func completeChatDelivery(response *service.CompletionResponse) error {
	if response == nil || response.CallID == "" {
		return nil
	}
	if response.CompleteDelivery != nil {
		return response.CompleteDelivery()
	}
	completion := &service.CompleteCallRequest{
		FinalAttemptID: response.AttemptID, HTTPStatus: http.StatusOK,
		ProviderResponseID: response.ProviderResponseID, CompleteStartedAttempt: true,
		ConversationProjection: chatConversationProjectionOutput(response),
	}
	if response.Usage != nil {
		completion.InputTokens = response.Usage.PromptTokens
		completion.OutputTokens = response.Usage.CompletionTokens
		completion.TotalTokens = response.Usage.TotalTokens
		if raw, err := json.Marshal(response.Usage); err == nil {
			completion.UsageJSON = raw
		}
	}
	return service.NewAPICallService().CompleteCall(response.CallID, completion)
}

func failChatDelivery(response *service.CompletionResponse, err error, clientDisconnected bool) error {
	if response == nil || response.CallID == "" {
		return nil
	}
	if err == nil {
		err = errors.New("downstream response delivery failed")
	}
	if response.FailDelivery != nil {
		return response.FailDelivery(err, clientDisconnected)
	}
	projection := chatConversationProjectionOutput(response)
	if clientDisconnected {
		return service.NewAPICallService().CancelCall(response.CallID, &service.CancelCallRequest{
			FinalAttemptID: response.AttemptID, ErrorType: "cancelled", ErrorCode: "client_disconnected",
			ErrorMessage: err.Error(), ClientDisconnected: true, ConversationProjection: projection,
		})
	}
	return service.NewAPICallService().FailCall(response.CallID, &service.FailCallRequest{
		FinalAttemptID: response.AttemptID, HTTPStatus: http.StatusBadGateway,
		ErrorType: "server_error", ErrorCode: "downstream_delivery_failed", ErrorMessage: err.Error(),
		ConversationProjection: projection,
	})
}

func chatConversationProjectionOutput(response *service.CompletionResponse) *service.ConversationProjectionOutputRequest {
	if response == nil || response.CanonicalResponse == nil {
		return nil
	}
	providerResponseID := response.ProviderResponseID
	if providerResponseID == "" {
		providerResponseID = canonicalProviderResponseID(*response.CanonicalResponse)
	}
	return &service.ConversationProjectionOutputRequest{
		CallID: response.CallID, OutputItems: canonical.CloneItems(response.CanonicalResponse.Output),
		RequestLogID: response.RequestLogID, ProviderResponseID: providerResponseID,
		FinishReason: response.CanonicalResponse.FinishReason,
	}
}

func validateChatMessages(messages []chat.ChatMessage) (message, param, code string) {
	for i, msg := range messages {
		messageParam := fmt.Sprintf("messages[%d]", i)
		switch msg.Role {
		case "system", "developer", "user", "assistant", "tool":
		default:
			return "unsupported message role: " + msg.Role, messageParam + ".role", "invalid_value"
		}
		if msg.Role == "tool" && strings.TrimSpace(msg.ToolCallID) == "" {
			return "tool_call_id is required for tool messages", messageParam + ".tool_call_id", "missing_required_parameter"
		}
		switch content := msg.Content.(type) {
		case string:
		case nil:
			if msg.Role != "assistant" || (len(msg.ToolCalls) == 0 && msg.Refusal == nil && len(msg.Audio) == 0) {
				return "message content is required", messageParam + ".content", "missing_required_parameter"
			}
		case []any:
			if len(content) == 0 {
				return "message content must not be empty", messageParam + ".content", "invalid_value"
			}
			for j, rawPart := range content {
				partParam := fmt.Sprintf("%s.content[%d]", messageParam, j)
				part, ok := rawPart.(map[string]any)
				if !ok {
					return "content part must be an object", partParam, "invalid_type"
				}
				partType, ok := part["type"].(string)
				if !ok || partType == "" {
					return "content part type is required", partParam + ".type", "missing_required_parameter"
				}
				switch partType {
				case "text":
					if key := unknownObjectKey(part, "type", "text"); key != "" {
						return "unsupported parameter: " + key, partParam + "." + key, "unsupported_parameter"
					}
					if _, ok := part["text"].(string); !ok {
						return "text is required for text content", partParam + ".text", "missing_required_parameter"
					}
				case "image_url":
					if key := unknownObjectKey(part, "type", "image_url"); key != "" {
						return "unsupported parameter: " + key, partParam + "." + key, "unsupported_parameter"
					}
					image, ok := part["image_url"].(map[string]any)
					if !ok {
						return "image_url must be an object", partParam + ".image_url", "invalid_type"
					}
					if url, ok := image["url"].(string); !ok || strings.TrimSpace(url) == "" {
						return "image URL is required", partParam + ".image_url.url", "missing_required_parameter"
					}
					if key := unknownObjectKey(image, "url", "detail"); key != "" {
						return "unsupported parameter: " + key, partParam + ".image_url." + key, "unsupported_parameter"
					}
				case "file":
					if key := unknownObjectKey(part, "type", "file"); key != "" {
						return "unsupported parameter: " + key, partParam + "." + key, "unsupported_parameter"
					}
					file, ok := part["file"].(map[string]any)
					if !ok {
						return "file must be an object", partParam + ".file", "invalid_type"
					}
					if !hasNonEmptyString(file, "file_id") && !hasNonEmptyString(file, "file_data") {
						return "file_id or file_data is required", partParam + ".file", "missing_required_parameter"
					}
					if key := unknownObjectKey(file, "file_id", "file_data", "filename"); key != "" {
						return "unsupported parameter: " + key, partParam + ".file." + key, "unsupported_parameter"
					}
				case "file_url":
					if key := unknownObjectKey(part, "type", "file_url"); key != "" {
						return "unsupported parameter: " + key, partParam + "." + key, "unsupported_parameter"
					}
					file, ok := part["file_url"].(map[string]any)
					if !ok || !hasNonEmptyString(file, "url") {
						return "file URL is required", partParam + ".file_url.url", "missing_required_parameter"
					}
					if key := unknownObjectKey(file, "url", "content_type"); key != "" {
						return "unsupported parameter: " + key, partParam + ".file_url." + key, "unsupported_parameter"
					}
				case "input_audio":
					if key := unknownObjectKey(part, "type", "input_audio"); key != "" {
						return "unsupported parameter: " + key, partParam + "." + key, "unsupported_parameter"
					}
					audio, ok := part["input_audio"].(map[string]any)
					if !ok {
						return "input_audio must be an object", partParam + ".input_audio", "invalid_type"
					}
					if !hasNonEmptyString(audio, "data") {
						return "audio data is required", partParam + ".input_audio.data", "missing_required_parameter"
					}
					if !hasNonEmptyString(audio, "format") {
						return "audio format is required", partParam + ".input_audio.format", "missing_required_parameter"
					}
					if key := unknownObjectKey(audio, "data", "format"); key != "" {
						return "unsupported parameter: " + key, partParam + ".input_audio." + key, "unsupported_parameter"
					}
				default:
					return "unsupported content type: " + partType, partParam + ".type", "unsupported_multimodal_input"
				}
			}
		default:
			return "message content must be a string or an array", messageParam + ".content", "invalid_type"
		}
	}
	return "", "", ""
}

func resolveOwnedChatFiles(tokenID uint, messages []chat.ChatMessage) ([]chat.ChatMessage, error) {
	result := append([]chat.ChatMessage(nil), messages...)
	for messageIndex := range result {
		parts, ok := result[messageIndex].Content.([]any)
		if !ok {
			continue
		}
		resolved := append([]any(nil), parts...)
		for partIndex, raw := range resolved {
			part, ok := raw.(map[string]any)
			if !ok || part["type"] != "file" {
				continue
			}
			fileObject, ok := part["file"].(map[string]any)
			if !ok {
				continue
			}
			fileID, ok := fileObject["file_id"].(string)
			if !ok || fileID == "" {
				continue
			}
			var file model.AIFile
			if err := model.DB().Select("id", "filename", "mime_type", "content").Where("id = ? AND token_id = ?", fileID, tokenID).First(&file).Error; err != nil {
				return nil, fmt.Errorf("file_id %q was not found", fileID)
			}
			newFile := make(map[string]any, len(fileObject)+1)
			for key, value := range fileObject {
				newFile[key] = value
			}
			delete(newFile, "file_id")
			mimeType := strings.TrimSpace(file.MimeType)
			if mimeType == "" {
				mimeType = "application/octet-stream"
			}
			newFile["file_data"] = "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(file.Content)
			if _, ok := newFile["filename"]; !ok {
				newFile["filename"] = file.Filename
			}
			newPart := make(map[string]any, len(part))
			for key, value := range part {
				newPart[key] = value
			}
			newPart["file"] = newFile
			resolved[partIndex] = newPart
		}
		result[messageIndex].Content = resolved
	}
	return result, nil
}

func validateChatOptions(maxTokens, maxCompletionTokens *int, stream bool, streamOptions *chat.StreamOptions, n *int, logprobs *bool, topLogprobs *int, tools []chat.ToolDefinition, modalities []string, audio *chat.AudioConfig, prediction *chat.Prediction) (message, param, code string) {
	if maxTokens != nil && maxCompletionTokens != nil {
		return "max_tokens and max_completion_tokens cannot both be set", "max_completion_tokens", "invalid_value"
	}
	if stream && n != nil && *n != 1 {
		return "streaming with n greater than 1 is not supported by this gateway", "n", "unsupported_value"
	}
	if maxTokens != nil && *maxTokens <= 0 {
		return "max_tokens must be greater than 0", "max_tokens", "invalid_value"
	}
	if maxCompletionTokens != nil && *maxCompletionTokens <= 0 {
		return "max_completion_tokens must be greater than 0", "max_completion_tokens", "invalid_value"
	}
	if streamOptions != nil && !stream {
		return "stream_options may only be set when stream is true", "stream_options", "invalid_value"
	}
	if n != nil && (*n < 1 || *n > chat.MaxSupportedChoices) {
		return fmt.Sprintf("n must be between 1 and %d", chat.MaxSupportedChoices), "n", "invalid_value"
	}
	if topLogprobs != nil {
		if *topLogprobs < 0 || *topLogprobs > 20 {
			return "top_logprobs must be between 0 and 20", "top_logprobs", "invalid_value"
		}
		if logprobs == nil || !*logprobs {
			return "top_logprobs requires logprobs to be true", "top_logprobs", "invalid_value"
		}
	}
	for i, tool := range tools {
		base := fmt.Sprintf("tools[%d]", i)
		if tool.Type != "function" {
			return "only function tools are supported", base + ".type", "unsupported_value"
		}
		if strings.TrimSpace(tool.Function.Name) == "" {
			return "function name is required", base + ".function.name", "missing_required_parameter"
		}
	}
	wantsAudio := false
	for i, modality := range modalities {
		switch modality {
		case "text":
		case "audio":
			wantsAudio = true
		default:
			return "unsupported modality: " + modality, fmt.Sprintf("modalities[%d]", i), "unsupported_value"
		}
	}
	if wantsAudio && audio == nil {
		return "audio is required when audio output is requested", "audio", "missing_required_parameter"
	}
	if audio != nil {
		if !wantsAudio {
			return "audio requires the audio modality", "audio", "invalid_value"
		}
		if strings.TrimSpace(audio.Format) == "" || audio.Voice == nil || (isString(audio.Voice) && strings.TrimSpace(audio.Voice.(string)) == "") {
			return "audio format and voice are required", "audio", "missing_required_parameter"
		}
	}
	if prediction != nil {
		if prediction.Type != "content" {
			return "only content predictions are supported", "prediction.type", "unsupported_value"
		}
		if prediction.Content == nil {
			return "prediction content is required", "prediction.content", "missing_required_parameter"
		}
		if _, ok := prediction.Content.(string); !ok {
			return "prediction content must be a string", "prediction.content", "unsupported_value"
		}
	}
	return "", "", ""
}

func isString(value any) bool {
	_, ok := value.(string)
	return ok
}

func unknownObjectKey(object map[string]any, allowed ...string) string {
	known := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		known[key] = struct{}{}
	}
	for key := range object {
		if _, ok := known[key]; !ok {
			return key
		}
	}
	return ""
}

func hasNonEmptyString(object map[string]any, name string) bool {
	value, ok := object[name].(string)
	return ok && strings.TrimSpace(value) != ""
}

func respondChatPipelineError(c *gin.Context, err error) {
	if errors.Is(err, routing.ErrModelNotFound) {
		param := "model"
		openaierror.Write(c, http.StatusNotFound, "The requested model does not exist", "invalid_request_error", &param, "model_not_found")
		return
	}
	if errors.Is(err, routing.ErrCapabilityUnavailable) || errors.Is(err, routing.ErrNoCompatibleTransport) {
		openaierror.InvalidRequest(c, "The requested model does not support all features used by this request", nil, "unsupported_model_capability")
		return
	}
	if errors.Is(err, routing.ErrNoRoute) {
		openaierror.Write(c, http.StatusServiceUnavailable, "The requested model is temporarily unavailable", "server_error", nil, "model_unavailable")
		return
	}
	if errors.Is(err, service.ErrInsufficientTokenBalance) ||
		errors.Is(err, service.ErrInsufficientUserBalance) {
		openaierror.Write(c, http.StatusTooManyRequests, err.Error(), "insufficient_quota", nil, "insufficient_quota")
		return
	}
	if appErr, ok := domain.IsAppError(err); ok {
		errorType := "server_error"
		if appErr.HTTPStatus >= 400 && appErr.HTTPStatus < 500 {
			errorType = "invalid_request_error"
		}
		openaierror.Write(c, appErr.HTTPStatus, appErr.Message, errorType, nil, appErr.Code)
		return
	}
	status := domain.UpstreamStatusCode(err)
	if status >= 400 && status < 500 {
		openaierror.Write(c, status, "upstream rejected the request", "invalid_request_error", nil, "upstream_error")
		return
	}
	openaierror.Write(c, http.StatusBadGateway, "upstream request failed", "server_error", nil, "upstream_error")
}

func decodeStrictJSON(c *gin.Context, dst any) error {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return errors.New("request body must contain a single JSON object")
		}
		return err
	}
	return nil
}

func describeJSONError(err error) (string, *string, any) {
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return fmt.Sprintf("invalid JSON at byte %d", syntaxErr.Offset), nil, "invalid_json"
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		param := typeErr.Field
		return fmt.Sprintf("invalid type for %s", typeErr.Field), &param, "invalid_type"
	}
	const unknownPrefix = "json: unknown field "
	if strings.HasPrefix(err.Error(), unknownPrefix) {
		field := strings.Trim(strings.TrimPrefix(err.Error(), unknownPrefix), `"`)
		return fmt.Sprintf("unsupported parameter: %s", field), &field, "unsupported_parameter"
	}
	if errors.Is(err, io.EOF) {
		return "request body is required", nil, "invalid_json"
	}
	return err.Error(), nil, "invalid_json"
}

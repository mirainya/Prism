package service

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider/chat"
	"github.com/mirainya/Prism/pkg/logger"
	"go.uber.org/zap"
)

func (s *UnifiedService) createRequestLog(
	conversation *model.Conversation,
	modelCode string,
	channel *model.Channel,
	account *model.ChannelAccount,
	endpoint *model.Endpoint,
	req *chat.ChatRequest,
	isStream bool,
) (*model.ChannelRequestLog, error) {
	if channel == nil || account == nil || endpoint == nil || req == nil {
		return nil, nil
	}
	requestBodyBytes, _ := json.Marshal(req)
	headers := s.buildHeaders(&RoutingResult{Endpoint: endpoint, Channel: channel, Account: account})
	headersJSON, _ := json.Marshal(maskSensitiveHeaders(headers))
	requestURL := strings.TrimSuffix(channel.BaseURL, "/") + endpoint.RequestPath
	conversationID := uint(0)
	if conversation != nil {
		conversationID = conversation.ID
	}
	log := &model.ChannelRequestLog{
		ConversationID: conversationID,
		ChannelID:      channel.ID,
		AccountID:      account.ID,
		CapabilityCode: modelCode,
		RequestType:    model.RequestTypeChat,
		IsStream:       isStream,
		ModelCode:      modelCode,
		VendorModel:    endpoint.VendorModel,
		RequestPath:    endpoint.RequestPath,
		Method:         http.MethodPost,
		URL:            requestURL,
		RequestHeaders: string(headersJSON),
		RequestBody:    string(requestBodyBytes),
		RequestAt:      time.Now(),
	}
	if err := s.requestLogService.Create(log); err != nil {
		return nil, err
	}
	return log, nil
}

func (s *UnifiedService) finalizeRequestLog(
	requestLog *model.ChannelRequestLog,
	channel *model.Channel,
	endpoint *model.Endpoint,
	resp *chat.ChatResponse,
	streamResult *StreamAggregationResult,
	latencyMs int64,
	reqErr error,
) {
	if requestLog == nil {
		return
	}
	updates := map[string]any{
		"duration_ms": latencyMs,
	}
	statusCode := http.StatusOK
	if reqErr != nil {
		statusCode = http.StatusInternalServerError
		updates["error_message"] = reqErr.Error()
	}
	updates["status_code"] = statusCode
	if resp != nil {
		respBody, _ := json.Marshal(resp)
		updates["response_body"] = string(respBody)
		if resp.Usage != nil {
			updates["usage_prompt_tokens"] = resp.Usage.PromptTokens
			updates["usage_completion_tokens"] = resp.Usage.CompletionTokens
			updates["usage_total_tokens"] = resp.Usage.TotalTokens
		}
		if len(resp.Choices) > 0 {
			updates["finish_reason"] = resp.Choices[0].FinishReason
			updates["response_preview"] = truncateString(resp.Choices[0].Message.ContentText(), 500)
		}
	}
	if streamResult != nil {
		if streamResult.Usage != nil {
			updates["usage_prompt_tokens"] = streamResult.Usage.PromptTokens
			updates["usage_completion_tokens"] = streamResult.Usage.CompletionTokens
			updates["usage_total_tokens"] = streamResult.Usage.TotalTokens
		}
		if streamResult.FinishReason != "" {
			updates["finish_reason"] = streamResult.FinishReason
		}
		if streamResult.ResponsePreview != "" {
			updates["response_preview"] = streamResult.ResponsePreview
		}
		if streamResult.ResponseBody != "" {
			updates["response_body"] = streamResult.ResponseBody
		}
		if streamResult.ErrorMessage != "" {
			updates["error_message"] = streamResult.ErrorMessage
		}
	}
	if endpoint != nil {
		updates["vendor_model"] = endpoint.VendorModel
		updates["request_path"] = endpoint.RequestPath
	}
	if channel != nil {
		updates["url"] = strings.TrimSuffix(channel.BaseURL, "/") + requestLog.RequestPath
	}
	if err := s.requestLogService.Update(requestLog.ID, updates); err != nil {
		logger.Warn("update request log failed", zap.Error(err), zap.Uint("request_log_id", requestLog.ID))
	}
}

func (s *UnifiedService) buildDebugDetail(
	conversation *model.Conversation,
	requestLog *model.ChannelRequestLog,
	channel *model.Channel,
	endpoint *model.Endpoint,
	latencyMs int64,
) *PlaygroundDebugDetail {
	if requestLog == nil {
		return nil
	}
	var requestBody map[string]any
	if requestLog.RequestBody != "" {
		_ = json.Unmarshal([]byte(requestLog.RequestBody), &requestBody)
	}
	var requestHeaders map[string]string
	if requestLog.RequestHeaders != "" {
		_ = json.Unmarshal([]byte(requestLog.RequestHeaders), &requestHeaders)
	}
	var responseBody any
	if requestLog.ResponseBody != "" {
		if err := json.Unmarshal([]byte(requestLog.ResponseBody), &responseBody); err != nil {
			responseBody = requestLog.ResponseBody
		}
	}
	debug := &PlaygroundDebugDetail{
		RequestLogID:    requestLog.ID,
		Status:          "completed",
		ModelCode:       requestLog.ModelCode,
		VendorModel:     requestLog.VendorModel,
		ChannelID:       requestLog.ChannelID,
		AccountID:       requestLog.AccountID,
		RequestPath:     requestLog.RequestPath,
		IsStream:        requestLog.IsStream,
		LatencyMs:       latencyMs,
		StatusCode:      requestLog.StatusCode,
		ErrorMessage:    requestLog.ErrorMessage,
		FinishReason:    requestLog.FinishReason,
		ResponsePreview: requestLog.ResponsePreview,
		RequestHeaders:  requestHeaders,
		RequestBody:     requestBody,
		ResponseBody:    responseBody,
		Usage: &chat.ChatUsage{
			PromptTokens:     requestLog.UsagePromptTokens,
			CompletionTokens: requestLog.UsageCompletionTokens,
			TotalTokens:      requestLog.UsageTotalTokens,
		},
	}
	if conversation != nil {
		debug.ConversationID = conversation.ID
		if conversation.LastStatus != "" {
			debug.Status = conversation.LastStatus
		}
	}
	if channel != nil {
		debug.ChannelName = channel.Name
		debug.ChannelType = channel.Type
	}
	if endpoint != nil {
		debug.RequestPath = endpoint.RequestPath
		debug.VendorModel = endpoint.VendorModel
	}
	if requestLog.ErrorMessage != "" {
		debug.Status = "failed"
	}
	if debug.Usage != nil && debug.Usage.TotalTokens == 0 && debug.Usage.PromptTokens == 0 && debug.Usage.CompletionTokens == 0 {
		debug.Usage = nil
	}
	return debug
}

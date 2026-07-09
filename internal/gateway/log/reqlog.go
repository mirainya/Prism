// Package log 负责网关 chat 请求日志的创建与结算(写 channel_request_logs)。
package log

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/gateway/stream"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider/chat"
	"github.com/mirainya/Prism/pkg/logger"
	"go.uber.org/zap"
)

// Create 创建请求日志(发上游前),返回记录以便结束时 Finalize。
func Create(conversationID uint, route *routing.RouteResult, req *chat.ChatRequest, headers map[string]string, isStream bool) *model.ChannelRequestLog {
	if route == nil || req == nil {
		return nil
	}
	reqBody, _ := json.Marshal(req)
	headersJSON, _ := json.Marshal(maskHeaders(headers))
	url := strings.TrimSuffix(route.BaseURL, "/") + reqPath(route)

	log := &model.ChannelRequestLog{
		ConversationID: conversationID,
		ChannelID:      route.ChannelID,
		AccountID:      route.KeyID,
		CapabilityCode: route.ModelName,
		RequestType:    model.RequestTypeChat,
		IsStream:       isStream,
		ModelCode:      route.ModelName,
		VendorModel:    route.VendorModel,
		RequestPath:    reqPath(route),
		Method:         http.MethodPost,
		URL:            url,
		RequestHeaders: string(headersJSON),
		RequestBody:    string(reqBody),
		RequestAt:      time.Now(),
	}
	if err := model.DB().Create(log).Error; err != nil {
		logger.Warn("gw create request log failed", zap.Error(err))
		return nil
	}
	return log
}

// Finalize 结束时更新日志:耗时、状态、usage、响应预览。resp/agg 二选一(非流/流)。
func Finalize(log *model.ChannelRequestLog, resp *chat.ChatResponse, agg *stream.AggregationResult, latencyMs int64, reqErr error) {
	if log == nil {
		return
	}
	updates := map[string]any{"duration_ms": latencyMs}
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
			updates["response_preview"] = truncate(resp.Choices[0].Message.ContentText(), 500)
		}
	}
	if agg != nil {
		if agg.Usage != nil {
			updates["usage_prompt_tokens"] = agg.Usage.PromptTokens
			updates["usage_completion_tokens"] = agg.Usage.CompletionTokens
			updates["usage_total_tokens"] = agg.Usage.TotalTokens
		}
		if agg.FinishReason != "" {
			updates["finish_reason"] = agg.FinishReason
		}
		if agg.ResponsePreview != "" {
			updates["response_preview"] = agg.ResponsePreview
		}
		if agg.ResponseBody != "" {
			updates["response_body"] = agg.ResponseBody
		}
		if agg.ErrorMessage != "" {
			updates["error_message"] = agg.ErrorMessage
		}
	}
	if err := model.DB().Model(&model.ChannelRequestLog{}).Where("id = ?", log.ID).Updates(updates).Error; err != nil {
		logger.Warn("gw finalize request log failed", zap.Error(err), zap.Uint("id", log.ID))
	}
}

func reqPath(route *routing.RouteResult) string {
	switch route.Protocol {
	case model.ProtocolAnthropic:
		return "/v1/messages"
	case model.ProtocolVolcengine:
		return "/api/v3/responses"
	default:
		return "/v1/chat/completions"
	}
}

func maskHeaders(h map[string]string) map[string]string {
	if h == nil {
		return nil
	}
	out := make(map[string]string, len(h))
	for k, v := range h {
		lk := strings.ToLower(k)
		if lk == "authorization" || lk == "x-api-key" || strings.Contains(lk, "key") {
			if len(v) > 12 {
				out[k] = v[:8] + "***"
			} else {
				out[k] = "***"
			}
			continue
		}
		out[k] = v
	}
	return out
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

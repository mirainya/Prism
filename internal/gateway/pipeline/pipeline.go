// Package pipeline 编排 chat 请求:选路 → 构造上游请求 → 适配器分发 → 结算 → 日志。
// 拥有重试循环与熔断/回退。取代老 UnifiedService 的 Complete/StreamComplete。
package pipeline

import (
	"fmt"

	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/gateway/engine"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider/chat"
	"github.com/mirainya/Prism/internal/service"
)

// Pipeline chat 编排器。无状态,可单例。
type Pipeline struct {
	v2 *engine.Engine
}

// New 构造 pipeline。
func New(executionEngine *engine.Engine) *Pipeline {
	if executionEngine == nil {
		panic("Gateway V2 engine is required")
	}
	return &Pipeline{v2: executionEngine}
}

// buildChatRequest 从 CompletionRequest + 路由结果构造 canonical ChatRequest。
func (p *Pipeline) buildChatRequest(req *service.CompletionRequest, route *routing.RouteResult) (*chat.ChatRequest, error) {
	chatReq := &chat.ChatRequest{
		Model:               route.VendorModel,
		Messages:            req.Messages,
		Temperature:         req.Temperature,
		MaxTokens:           req.MaxTokens,
		MaxCompletionTokens: req.MaxCompletionTokens,
		TopP:                req.TopP,
		FrequencyPenalty:    req.FrequencyPenalty,
		PresencePenalty:     req.PresencePenalty,
		Stop:                req.Stop,
		StreamOptions:       req.StreamOptions,
		N:                   req.N,
		Logprobs:            req.Logprobs,
		TopLogprobs:         req.TopLogprobs,
		Tools:               req.Tools,
		ToolChoice:          req.ToolChoice,
		ParallelToolCalls:   req.ParallelToolCalls,
		ResponseFormat:      req.ResponseFormat,
		Seed:                req.Seed,
		User:                req.User,
		Modalities:          req.Modalities,
		Audio:               req.Audio,
		Prediction:          req.Prediction,
		Store:               req.Store,
		Metadata:            cloneStringMap(req.Metadata),
		ServiceTier:         req.ServiceTier,
	}

	// 元数据面:思考档(模型默认 + 请求覆盖)。缺失不影响。
	if meta := loadModelMeta(route.ModelName); meta != nil {
		if meta.MaxTokens > 0 {
			if chatReq.MaxCompletionTokens != nil && *chatReq.MaxCompletionTokens > meta.MaxTokens {
				capped := meta.MaxTokens
				chatReq.MaxCompletionTokens = &capped
			} else if chatReq.MaxCompletionTokens == nil && (chatReq.MaxTokens == 0 || chatReq.MaxTokens > meta.MaxTokens) {
				chatReq.MaxTokens = meta.MaxTokens
			}
		}
		thinking := parseThinkingConfig(meta.ThinkingConfig)
		if req.ReasoningEffort != nil && thinking != nil && !thinking.Locked && thinking.findOption(*req.ReasoningEffort) == nil {
			return nil, domain.ErrBadRequest(fmt.Sprintf("reasoning_effort %q is not configured for this model", *req.ReasoningEffort))
		}
		applyThinking(chatReq, thinking, req.ReasoningEffort)
	} else if req.ReasoningEffort != nil {
		switch route.Protocol {
		case model.ProtocolOpenAI, model.ProtocolCustom, model.ProtocolVolcengine:
			chatReq.ExtraBody = map[string]any{"reasoning_effort": *req.ReasoningEffort}
		default:
			return nil, domain.ErrBadRequest("reasoning_effort requires a model thinking configuration for this provider")
		}
	}

	// temperature 与 top_p 上游不兼容守卫(沿用老逻辑)
	// 渠道级 image_to_base64
	if b, _ := route.ChannelConfig["image_to_base64"].(bool); b {
		chatReq.Messages = convertImageURLsToBase64(chatReq.Messages)
	}

	return chatReq, nil
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

// buildUpstreamRequest 组装适配器入参。

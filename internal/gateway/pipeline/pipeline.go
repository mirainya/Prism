// Package pipeline 编排 chat 请求:选路 → 构造上游请求 → 适配器分发 → 结算 → 日志。
// 拥有重试循环与熔断/回退。取代老 UnifiedService 的 Complete/StreamComplete。
package pipeline

import (
	"github.com/mirainya/Prism/internal/gateway/adapter"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/provider/chat"
	"github.com/mirainya/Prism/internal/service"
)

const maxRetries = 3

// Pipeline chat 编排器。无状态,可单例。
type Pipeline struct {
	router  *routing.Router
	circuit *routing.Circuit
	billing *service.BillingService
}

// New 构造 pipeline。
func New() *Pipeline {
	return &Pipeline{
		router:  routing.NewRouter(),
		circuit: routing.NewCircuit(),
		billing: service.NewBillingService(),
	}
}

// buildChatRequest 从 CompletionRequest + 路由结果构造 canonical ChatRequest。
func (p *Pipeline) buildChatRequest(req *service.CompletionRequest, route *routing.RouteResult) *chat.ChatRequest {
	chatReq := &chat.ChatRequest{
		Model:            route.VendorModel,
		Messages:         req.Messages,
		Temperature:      req.Temperature,
		MaxTokens:        req.MaxTokens,
		TopP:             req.TopP,
		FrequencyPenalty: req.FrequencyPenalty,
		PresencePenalty:  req.PresencePenalty,
		Stop:             req.Stop,
		Tools:            req.Tools,
		ToolChoice:       req.ToolChoice,
		ResponseFormat:   req.ResponseFormat,
		Seed:             req.Seed,
		User:             req.User,
	}

	// 元数据面:思考档(模型默认 + 请求覆盖)。缺失不影响。
	if meta := loadModelMeta(route.ModelName); meta != nil {
		if meta.MaxTokens > 0 && (chatReq.MaxTokens == 0 || chatReq.MaxTokens > meta.MaxTokens) {
			chatReq.MaxTokens = meta.MaxTokens
		}
		applyThinking(chatReq, parseThinkingConfig(meta.ThinkingConfig), req.ReasoningEffort)
	}

	// temperature 与 top_p 上游不兼容守卫(沿用老逻辑)
	if chatReq.Temperature != nil && chatReq.TopP != nil {
		chatReq.TopP = nil
	}

	// 渠道级 image_to_base64
	if b, _ := route.ChannelConfig["image_to_base64"].(bool); b {
		chatReq.Messages = convertImageURLsToBase64(chatReq.Messages)
	}

	return chatReq
}

// buildUpstreamRequest 组装适配器入参。
func (p *Pipeline) buildUpstreamRequest(req *service.CompletionRequest, route *routing.RouteResult, chatReq *chat.ChatRequest) *adapter.UpstreamRequest {
	return &adapter.UpstreamRequest{
		Chat:               chatReq,
		VendorModel:        route.VendorModel,
		APIKey:             route.APIKey,
		BaseURL:            route.BaseURL,
		ExtraHeaders:       route.ExtraHeaders,
		PreviousResponseID: req.PreviousResponseID,
		NewMessages:        req.NewMessages,
	}
}

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider/chat"
	"github.com/mirainya/Prism/pkg/httputil"
	"github.com/mirainya/Prism/pkg/logger"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// UnifiedService 统一执行引擎
type UnifiedService struct {
	billingService    *BillingService
	requestLogService *RequestLogService
	circuitService    *AccountCircuitService
}

func NewUnifiedService() *UnifiedService {
	return &UnifiedService{
		billingService:    NewBillingService(),
		requestLogService: NewRequestLogService(),
		circuitService:    NewAccountCircuitService(),
	}
}

// RoutingResult 路由结果
type RoutingResult struct {
	Endpoint *model.Endpoint
	Channel  *model.Channel
	Account  *model.ChannelAccount
	Cleanup  func()
	once     sync.Once
}

// StreamSession 流式会话
type StreamSession struct {
	UpstreamResp  *http.Response
	Route         *RoutingResult
	OriginalModel string
	OriginalReq   *CompletionRequest
	NewMessages   []chat.ChatMessage
	Conversation  *model.Conversation
	StartedAt     time.Time
	RequestLog    *model.ChannelRequestLog
	CleanupFunc   func()
}

// NewChatService returns UnifiedService (backward compat alias)
func NewChatService() *UnifiedService {
	return NewUnifiedService()
}

// NewCapabilityService returns UnifiedService (backward compat alias)
func NewCapabilityService() *UnifiedService {
	return NewUnifiedService()
}

// Route 根据 model_code 选择 endpoint + channel + account
func (s *UnifiedService) Route(tokenID uint, modelCode string, excludeChannelIDs []uint, excludeAccountIDs []uint) (*RoutingResult, error) {
	endpoint, err := s.selectEndpoint(tokenID, modelCode, excludeChannelIDs)
	if err != nil {
		return nil, err
	}

	var channel model.Channel
	if err := model.DB().Where("id = ? AND status = 1", endpoint.ChannelID).First(&channel).Error; err != nil {
		return nil, fmt.Errorf("channel not found: %d", endpoint.ChannelID)
	}

	account, err := s.selectAccount(channel.ID, modelCode, excludeAccountIDs)
	if err != nil {
		return nil, fmt.Errorf("no available account for channel: %s", channel.Type)
	}

	result := &RoutingResult{
		Endpoint: endpoint,
		Channel:  &channel,
		Account:  account,
	}
	result.Cleanup = func() {
		result.once.Do(func() {
			s.decrementAccountTasks(account.ID)
		})
	}
	return result, nil
}

// Complete 非流式对话补全（带 fallback）
func (s *UnifiedService) Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error) {
	// 保存本轮新消息（用于后续写入对话记录）
	newMessages := req.Messages

	// 加载对话历史并标准化
	var conv *model.Conversation
	if req.ConversationID != "" {
		loaded, err := s.loadConversation(req.ConversationID, req.TokenID)
		if err == nil {
			conv = loaded
			history, _ := s.loadMessages(conv.ID, req.Model)
			if conv.SystemPrompt != "" {
				history = append([]chat.ChatMessage{{Role: model.RoleSystem, Content: conv.SystemPrompt}}, history...)
			}
			req.Messages = append(history, req.Messages...)
			// 有状态对话(B模式)：存在有效 response_id 时只发新消息，历史由上游维护
			if conv.ProviderResponseID != "" {
				req.PreviousResponseID = conv.ProviderResponseID
				req.NewMessages = newMessages
			}
		}
	}

	var excludeChannelIDs []uint
	var excludeAccountIDs []uint
	maxRetries := 3
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		route, err := s.Route(req.TokenID, req.Model, excludeChannelIDs, excludeAccountIDs)
		if err != nil {
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, err
		}

		resp, reqLog, err := s.doComplete(ctx, req, route)
		if err != nil {
			route.Cleanup()
			lastErr = err
			// B模式自愈：previous_response_id 失效 → 清空并用本地全量历史(A)重试同一渠道
			if IsPreviousResponseNotFound(err) && req.PreviousResponseID != "" {
				logger.Warn("previous_response_id expired, falling back to full history (A)",
					zap.String("conversation_id", req.ConversationID))
				req.PreviousResponseID = ""
				req.NewMessages = nil
				continue
			}
			// 账号级熔断: 401/403/404/429 视为该 key 对该模型不可用 → 熔断+排除该账号重试
			if shouldBreak, _ := domain.ClassifyUpstreamError(err); shouldBreak {
				s.circuitService.MarkUnavailable(route.Account.ID, req.Model, err)
				excludeAccountIDs = append(excludeAccountIDs, route.Account.ID)
				logger.Warn("complete attempt failed (account circuit), trying next account",
					zap.Int("attempt", attempt+1), zap.Uint("account_id", route.Account.ID), zap.Error(err))
				continue
			}
			// 其他 4xx(如 400/422 请求本身问题): 立即返回,重试无益
			if strings.Contains(err.Error(), "http error: 4") {
				return nil, err
			}
			// 5xx: 不排除 channel，短暂延迟后重试
			if !strings.Contains(err.Error(), "status 5") && !strings.Contains(err.Error(), "http error: 5") {
				excludeChannelIDs = append(excludeChannelIDs, route.Endpoint.ChannelID)
			} else {
				time.Sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
			}
			logger.Warn("complete attempt failed, trying fallback",
				zap.Int("attempt", attempt+1), zap.Error(err))
			continue
		}

		route.Cleanup()
		s.chargeTokenUsage(req.TokenID, req.UserID, resp.Usage, route.Endpoint)

		// 保存对话记录
		if conv == nil {
			conv = s.findOrCreateConversation(req.UserID, req.TokenID, req.Model, newMessages)
			// open API 场景：只保存最后一条 user message（增量）
			newMessages = lastUserMessage(newMessages)
		}
		if conv != nil {
			logID := requestLogID(reqLog)
			chatResp := &chat.ChatResponse{Usage: resp.Usage, Choices: resp.Choices}
			s.saveConversationMessages(conv, newMessages, chatResp, route.Endpoint, route.Account, 0, decimal.Zero, logID)
			resp.ConversationID = fmt.Sprint(conv.ID)
			// 回写 conversation_id 到请求日志
			if reqLog != nil {
				model.DB().Model(reqLog).Update("conversation_id", conv.ID)
			}
			// 有状态对话：回写本轮返回的 response_id，供下一轮 B 模式使用
			if resp.ProviderResponseID != "" && resp.ProviderResponseID != conv.ProviderResponseID {
				model.DB().Model(&model.Conversation{}).Where("id = ?", conv.ID).
					Update("provider_response_id", resp.ProviderResponseID)
			}
		}

		return resp, nil
	}

	return nil, fmt.Errorf("upstream service unavailable for model: %s", req.Model)
}

// StreamComplete 流式对话补全
func (s *UnifiedService) StreamComplete(ctx context.Context, req *CompletionRequest) (*StreamSession, error) {
	// 保存本轮新消息
	newMessages := req.Messages

	// 加载对话历史并标准化
	var conv *model.Conversation
	if req.ConversationID != "" {
		loaded, err := s.loadConversation(req.ConversationID, req.TokenID)
		if err == nil {
			conv = loaded
			history, _ := s.loadMessages(conv.ID, req.Model)
			if conv.SystemPrompt != "" {
				history = append([]chat.ChatMessage{{Role: model.RoleSystem, Content: conv.SystemPrompt}}, history...)
			}
			req.Messages = append(history, req.Messages...)
			// 有状态对话(B模式)：存在有效 response_id 时只发新消息，历史由上游维护
			if conv.ProviderResponseID != "" {
				req.PreviousResponseID = conv.ProviderResponseID
				req.NewMessages = newMessages
			}
		}
	}

	var excludeChannelIDs []uint
	var excludeAccountIDs []uint
	maxRetries := 3
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		route, err := s.Route(req.TokenID, req.Model, excludeChannelIDs, excludeAccountIDs)
		if err != nil {
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, err
		}

		resp, reqLog, err := s.doStreamComplete(ctx, req, route)
		if err != nil {
			route.Cleanup()
			lastErr = err
			// B模式自愈：previous_response_id 失效 → 清空并用本地全量历史(A)重试同一渠道
			if IsPreviousResponseNotFound(err) && req.PreviousResponseID != "" {
				logger.Warn("previous_response_id expired (stream), falling back to full history (A)",
					zap.String("conversation_id", req.ConversationID))
				req.PreviousResponseID = ""
				req.NewMessages = nil
				continue
			}
			// 账号级熔断: 401/403/404/429 视为该 key 对该模型不可用 → 熔断+排除该账号重试
			if shouldBreak, _ := domain.ClassifyUpstreamError(err); shouldBreak {
				s.circuitService.MarkUnavailable(route.Account.ID, req.Model, err)
				excludeAccountIDs = append(excludeAccountIDs, route.Account.ID)
				logger.Warn("stream attempt failed (account circuit), trying next account",
					zap.Int("attempt", attempt+1), zap.Uint("account_id", route.Account.ID), zap.Error(err))
				continue
			}
			// 其他 4xx(如 400/422 请求本身问题): 立即返回,重试无益
			if strings.Contains(err.Error(), "http error: 4") {
				return nil, err
			}
			// 5xx: 不排除 channel，短暂延迟后重试
			if !strings.Contains(err.Error(), "status 5") && !strings.Contains(err.Error(), "http error: 5") {
				excludeChannelIDs = append(excludeChannelIDs, route.Endpoint.ChannelID)
			} else {
				time.Sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
			}
			logger.Warn("stream attempt failed, trying fallback",
				zap.Int("attempt", attempt+1), zap.Error(err))
			continue
		}

		return &StreamSession{
			UpstreamResp:  resp,
			Route:         route,
			OriginalModel: req.Model,
			OriginalReq:   req,
			NewMessages:   newMessages,
			Conversation:  conv,
			StartedAt:     time.Now(),
			RequestLog:    reqLog,
			CleanupFunc:   route.Cleanup,
		}, nil
	}

	return nil, fmt.Errorf("upstream service unavailable for model: %s", req.Model)
}

// FinalizeStream 流式结束后计费
func (s *UnifiedService) FinalizeStream(session *StreamSession, result *StreamAggregationResult, err error) (*StreamAggregationResult, error) {
	defer session.Route.Cleanup()

	latency := time.Since(session.StartedAt).Milliseconds()
	s.finalizeRequestLog(session.RequestLog, session.Route.Channel, session.Route.Endpoint, nil, result, latency, err)

	if err != nil || result == nil {
		return result, err
	}

	usage := &chat.ChatUsage{
		PromptTokens:     result.PromptTokens,
		CompletionTokens: result.CompletionTokens,
		TotalTokens:      result.TotalTokens,
	}
	if result.Usage != nil {
		usage = result.Usage
	}
	s.chargeTokenUsage(session.OriginalReq.TokenID, session.OriginalReq.UserID, usage, session.Route.Endpoint)

	// 保存对话记录
	conv := session.Conversation
	req := session.OriginalReq
	newMsgs := session.NewMessages
	if conv == nil {
		conv = s.findOrCreateConversation(req.UserID, req.TokenID, req.Model, newMsgs)
		newMsgs = lastUserMessage(newMsgs)
	}
	if conv != nil {
		chatResp := &chat.ChatResponse{
			Usage: usage,
			Choices: []chat.ChatChoice{{
				Message: chat.ChatMessage{
					Role:             "assistant",
					Content:          result.AssistantContent,
					ReasoningContent: result.ReasoningContent,
				},
				FinishReason: result.FinishReason,
			}},
		}
		logID := requestLogID(session.RequestLog)
		s.saveConversationMessages(conv, newMsgs, chatResp, session.Route.Endpoint, session.Route.Account, latency, decimal.Zero, logID)
		// 有状态对话：回写本轮返回的 response_id，供下一轮 B 模式使用
		if result.ProviderResponseID != "" && result.ProviderResponseID != conv.ProviderResponseID {
			model.DB().Model(&model.Conversation{}).Where("id = ?", conv.ID).
				Update("provider_response_id", result.ProviderResponseID)
		}
		// 回写 conversation_id 到请求日志
		if session.RequestLog != nil {
			model.DB().Model(session.RequestLog).Update("conversation_id", conv.ID)
		}
	}

	return result, nil
}

// ListModels 列出可用模型
func (s *UnifiedService) ListModels(ctx context.Context) ([]model.Model, error) {
	var models []model.Model
	if err := model.DB().Where("status = 1").Order("sort DESC, created_at DESC").Find(&models).Error; err != nil {
		return nil, err
	}
	return models, nil
}

func (s *UnifiedService) GetModelDetail(ctx context.Context, code string) (*model.Model, error) {
	var m model.Model
	if err := model.DB().Where("code = ? AND status = 1", code).First(&m).Error; err != nil {
		return nil, err
	}
	return &m, nil
}

// ListPlaygroundModels 列出 playground 可用模型
func (s *UnifiedService) ListPlaygroundModels(ctx context.Context, tokenID uint) ([]ModelInfo, error) {
	var models []model.Model
	if err := model.DB().Where("status = 1 AND type = 'chat'").Order("sort DESC, created_at DESC").Find(&models).Error; err != nil {
		return nil, err
	}

	// 查询所有启用的 endpoint，聚合流式支持
	var endpoints []model.Endpoint
	model.DB().Where("status = 1").Select("model_code, supports_stream, default_stream").Find(&endpoints)
	type streamInfo struct {
		supportsStream bool
		defaultStream  bool
	}
	streamMap := make(map[string]*streamInfo)
	for _, ep := range endpoints {
		info := streamMap[ep.ModelCode]
		if info == nil {
			info = &streamInfo{}
			streamMap[ep.ModelCode] = info
		}
		if ep.SupportsStream {
			info.supportsStream = true
		}
		if ep.DefaultStream {
			info.defaultStream = true
		}
	}

	result := make([]ModelInfo, 0, len(models))
	for _, m := range models {
		info := streamMap[m.Code]
		mi := ModelInfo{
			ID:      m.Code,
			Object:  "model",
			Created: m.CreatedAt.Unix(),
			OwnedBy: m.Provider,
		}
		if info != nil {
			mi.SupportsStream = info.supportsStream
			mi.DefaultStream = info.defaultStream
		}
		// 从 Features JSON 解析能力标签
		if len(m.Features) > 0 {
			var features []string
			if json.Unmarshal(m.Features, &features) == nil {
				for _, f := range features {
					switch f {
					case "tools":
						mi.SupportsTools = true
					case "vision":
						mi.SupportsMultimodal = true
					}
				}
			}
		}
		// 暴露思考档位供 playground 渲染选择器
		if cfg := parseThinkingConfig(m.ThinkingConfig); cfg != nil {
			ti := &ThinkingInfo{Default: cfg.Default, Locked: cfg.Locked}
			for _, o := range cfg.Options {
				ti.Options = append(ti.Options, ThinkingLevelInfo{Label: o.Label, Value: o.Value})
			}
			mi.Thinking = ti
		}
		result = append(result, mi)
	}
	return result, nil
}

type ModelInfo struct {
	ID                     string `json:"id"`
	Object                 string `json:"object"`
	Created                int64  `json:"created"`
	OwnedBy                string `json:"owned_by"`
	SupportsStream         bool   `json:"supports_stream"`
	DefaultStream          bool   `json:"default_stream"`
	SupportsTools          bool   `json:"supports_tools"`
	SupportsResponseFormat bool   `json:"supports_response_format"`
	SupportsMultimodal     bool   `json:"supports_multimodal"`
	Thinking               *ThinkingInfo `json:"thinking,omitempty"`
}

// ThinkingInfo 暴露给前端的思考档位信息(不含 body)
type ThinkingInfo struct {
	Default string              `json:"default"`
	Locked  bool                `json:"locked"`
	Options []ThinkingLevelInfo `json:"options"`
}

type ThinkingLevelInfo struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// --- 内部方法 ---

func (s *UnifiedService) doComplete(ctx context.Context, req *CompletionRequest, route *RoutingResult) (*CompletionResponse, *model.ChannelRequestLog, error) {
	chatReq := s.buildChatRequest(req, route)
	chatReq.Stream = false

	if channelConfigBool(route.Channel, "image_to_base64") {
		chatReq.Messages = convertImageURLsToBase64(chatReq.Messages)
	}

	reqLog, _ := s.createRequestLog(nil, req.Model, route.Channel, route.Account, route.Endpoint, chatReq, false)

	// 协议适配：Anthropic 使用不同的请求体格式
	var reqBody any = chatReq
	if isAnthropicProtocol(route.Endpoint) {
		reqBody = toAnthropicRequestBody(chatReq)
	} else if isVolcengineProtocol(route.Endpoint) {
		reqBody = toVolcengineRequestBody(chatReq, req.PreviousResponseID, req.NewMessages)
	}

	url := route.Channel.BaseURL + resolveRequestPath(route.Endpoint)
	headers := s.buildHeaders(route)

	ctx, cancel := context.WithTimeout(ctx, time.Duration(route.Endpoint.Timeout)*time.Second)
	defer cancel()

	start := time.Now()
	respBody, err := httputil.PostJSON(ctx, url, reqBody, headers)
	latency := time.Since(start).Milliseconds()

	if err != nil {
		s.finalizeRequestLog(reqLog, route.Channel, route.Endpoint, nil, nil, latency, err)
		return nil, nil, fmt.Errorf("request failed: %w", err)
	}

	// 协议适配：Anthropic 响应解析
	if isAnthropicProtocol(route.Endpoint) {
		compResp, chatResp, err := parseAnthropicNonStreamResponse(respBody, req.Model)
		if err != nil {
			s.finalizeRequestLog(reqLog, route.Channel, route.Endpoint, nil, nil, latency, err)
			return nil, nil, err
		}
		s.finalizeRequestLog(reqLog, route.Channel, route.Endpoint, chatResp, nil, latency, nil)
		return compResp, reqLog, nil
	}

	// 协议适配：Volcengine Responses 响应解析
	if isVolcengineProtocol(route.Endpoint) {
		compResp, chatResp, respID, err := parseVolcengineNonStreamResponse(respBody, req.Model)
		if err != nil {
			s.finalizeRequestLog(reqLog, route.Channel, route.Endpoint, nil, nil, latency, err)
			return nil, nil, err
		}
		compResp.ProviderResponseID = respID
		s.finalizeRequestLog(reqLog, route.Channel, route.Endpoint, chatResp, nil, latency, nil)
		return compResp, reqLog, nil
	}

	var chatResp CompletionResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		s.finalizeRequestLog(reqLog, route.Channel, route.Endpoint, nil, nil, latency, err)
		return nil, nil, fmt.Errorf("unmarshal response failed: %w", err)
	}

	chatResp.Model = req.Model
	s.finalizeRequestLog(reqLog, route.Channel, route.Endpoint, &chat.ChatResponse{
		Usage:   chatResp.Usage,
		Choices: chatResp.Choices,
	}, nil, latency, nil)
	return &chatResp, reqLog, nil
}

func (s *UnifiedService) doStreamComplete(ctx context.Context, req *CompletionRequest, route *RoutingResult) (*http.Response, *model.ChannelRequestLog, error) {
	chatReq := s.buildChatRequest(req, route)
	chatReq.Stream = true
	chatReq.StreamOptions = &chat.StreamOptions{IncludeUsage: true}

	if channelConfigBool(route.Channel, "image_to_base64") {
		chatReq.Messages = convertImageURLsToBase64(chatReq.Messages)
	}

	reqLog, _ := s.createRequestLog(nil, req.Model, route.Channel, route.Account, route.Endpoint, chatReq, true)

	// 协议适配：Anthropic 使用不同的请求体格式
	var reqBody any = chatReq
	if isAnthropicProtocol(route.Endpoint) {
		reqBody = toAnthropicRequestBody(chatReq)
	} else if isVolcengineProtocol(route.Endpoint) {
		reqBody = toVolcengineRequestBody(chatReq, req.PreviousResponseID, req.NewMessages)
	}

	url := route.Channel.BaseURL + resolveRequestPath(route.Endpoint)
	headers := s.buildHeaders(route)

	resp, err := httputil.PostJSONStream(ctx, url, reqBody, headers)
	if err != nil {
		s.finalizeRequestLog(reqLog, route.Channel, route.Endpoint, nil, nil, 0, err)
		return nil, nil, fmt.Errorf("stream request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		streamErr := fmt.Errorf("upstream returned status %d, body: %s", resp.StatusCode, string(errBody))
		s.finalizeRequestLog(reqLog, route.Channel, route.Endpoint, nil, nil, 0, streamErr)
		return nil, nil, streamErr
	}

	// 协议适配：将 Anthropic SSE 事件流转换为 OpenAI SSE 格式
	if isAnthropicProtocol(route.Endpoint) {
		resp.Body = newAnthropicStreamAdapter(resp.Body, req.Model)
	} else if isVolcengineProtocol(route.Endpoint) {
		resp.Body = newVolcengineStreamAdapter(resp.Body, req.Model)
	}

	return resp, reqLog, nil
}

func (s *UnifiedService) buildChatRequest(req *CompletionRequest, route *RoutingResult) *chat.ChatRequest {
	maxTokens := req.MaxTokens

	// 查询模型的 max_tokens 限制和思考配置
	var mdl model.Model
	if err := model.DB().Select("max_tokens", "thinking_config").Where("code = ?", req.Model).First(&mdl).Error; err == nil {
		if mdl.MaxTokens > 0 && (maxTokens == 0 || maxTokens > mdl.MaxTokens) {
			maxTokens = mdl.MaxTokens
		}
	}

	// 部分上游不允许同时指定 temperature 和 top_p，保留 temperature
	topP := req.TopP
	if req.Temperature != nil && req.TopP != nil {
		topP = nil
	}

	chatReq := &chat.ChatRequest{
		Model:            route.Endpoint.VendorModel,
		Messages:         req.Messages,
		Temperature:      req.Temperature,
		MaxTokens:        maxTokens,
		TopP:             topP,
		FrequencyPenalty: req.FrequencyPenalty,
		PresencePenalty:  req.PresencePenalty,
		Stop:             req.Stop,
		Tools:            req.Tools,
		ToolChoice:       req.ToolChoice,
		ResponseFormat:   req.ResponseFormat,
		Seed:             req.Seed,
		User:             req.User,
	}

	// 合并 extra_config 中的 extra_body
	if route.Endpoint.ExtraConfig != nil {
		var cfg map[string]any
		if json.Unmarshal(route.Endpoint.ExtraConfig, &cfg) == nil {
			if extraBody, ok := cfg["extra_body"].(map[string]any); ok {
				// 防止 extra_body 重新引入 top_p 导致冲突
				if chatReq.Temperature != nil {
					delete(extraBody, "top_p")
				}
				chatReq.ExtraBody = extraBody
			}
		}
	}

	// 思考模式:模型默认档位 + 请求级覆盖(未锁定时)。thinking_config 为空则不注入
	applyThinking(chatReq, parseThinkingConfig(mdl.ThinkingConfig), req.ReasoningEffort)

	return chatReq
}

func (s *UnifiedService) buildHeaders(route *RoutingResult) map[string]string {
	headers := map[string]string{}

	// 鉴权
	switch route.Endpoint.AuthLocation {
	case "header":
		key := route.Endpoint.AuthKey
		if key == "" {
			key = "Authorization"
		}
		prefix := route.Endpoint.AuthValuePrefix
		if prefix == "" && !isAnthropicProtocol(route.Endpoint) {
			prefix = "Bearer "
		}
		headers[key] = prefix + route.Account.APIKey
	default:
		headers["Authorization"] = "Bearer " + route.Account.APIKey
	}

	// 额外请求头
	if route.Endpoint.ExtraHeaders != nil {
		var extra map[string]string
		if json.Unmarshal(route.Endpoint.ExtraHeaders, &extra) == nil {
			for k, v := range extra {
				headers[k] = v
			}
		}
	}

	// Anthropic 协议自动注入版本头
	if isAnthropicProtocol(route.Endpoint) {
		if _, ok := headers["anthropic-version"]; !ok {
			headers["anthropic-version"] = "2023-06-01"
		}
	}

	return headers
}

func (s *UnifiedService) selectEndpoint(tokenID uint, modelCode string, excludeChannelIDs []uint) (*model.Endpoint, error) {
	priorityKey := "chat:" + modelCode

	type candidate struct {
		model.Endpoint
		TokenPriority *int
	}

	var candidates []candidate
	query := model.DB().Table("endpoints").
		Select("endpoints.*, tcp.priority AS token_priority").
		Joins("JOIN channels c ON c.id = endpoints.channel_id AND c.status = 1 AND c.deleted_at IS NULL").
		Joins("LEFT JOIN token_channel_priorities tcp ON tcp.channel_id = endpoints.channel_id AND tcp.token_id = ? AND tcp.capability_code = ?", tokenID, priorityKey).
		Where("endpoints.model_code = ? AND endpoints.status = 1 AND endpoints.deleted_at IS NULL", modelCode)

	if len(excludeChannelIDs) > 0 {
		query = query.Where("endpoints.channel_id NOT IN ?", excludeChannelIDs)
	}

	result := query.Scan(&candidates)
	if result.Error != nil {
		logger.Error("selectEndpoint: query error", zap.Error(result.Error), zap.String("modelCode", modelCode))
		return nil, result.Error
	}
	if len(candidates) == 0 {
		// 做一次简单验证查询
		var count int64
		model.DB().Table("endpoints").Where("model_code = ? AND status = 1", modelCode).Count(&count)
		logger.Warn("selectEndpoint: no candidates found",
			zap.Uint("tokenID", tokenID),
			zap.String("modelCode", modelCode),
			zap.String("priorityKey", priorityKey),
			zap.Int64("directCount", count),
			zap.Any("excludeChannelIDs", excludeChannelIDs))
		return nil, fmt.Errorf("no available endpoint for model: %s", modelCode)
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		lp, rp := candidates[i].TokenPriority, candidates[j].TokenPriority
		if lp != nil && rp != nil && *lp != *rp {
			return *lp < *rp
		}
		if lp != nil && rp == nil {
			return true
		}
		if lp == nil && rp != nil {
			return false
		}
		if candidates[i].Priority != candidates[j].Priority {
			return candidates[i].Priority > candidates[j].Priority
		}
		return candidates[i].ID < candidates[j].ID
	})

	selected := candidates[0].Endpoint
	return &selected, nil
}

// selectAccount 在渠道内选择一个可用账号
// modelCode: 用于按账号 supported_models 白名单过滤 + 排除对该模型熔断中的账号
// excludeAccountIDs: 本轮重试已尝试过的账号,避免重复命中
func (s *UnifiedService) selectAccount(channelID uint, modelCode string, excludeAccountIDs []uint) (*model.ChannelAccount, error) {
	var accounts []model.ChannelAccount

	err := model.DB().Transaction(func(tx *gorm.DB) error {
		q := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("channel_id = ? AND status = 1", channelID).
			Where("max_tasks = 0 OR current_tasks < max_tasks")

		// 白名单: supported_models 为空(NULL/[]/null)=支持所有; 非空则必须包含 modelCode
		if modelCode != "" {
			q = q.Where(
				"supported_models IS NULL OR JSON_LENGTH(supported_models) = 0 OR JSON_CONTAINS(supported_models, ?)",
				fmt.Sprintf("%q", modelCode),
			)
			// 排除对该模型熔断中的账号
			q = q.Where(
				"id NOT IN (SELECT account_id FROM account_model_states WHERE model_code = ? AND disabled_until > ?)",
				modelCode, time.Now(),
			)
		}
		// 排除本轮已尝试的账号
		if len(excludeAccountIDs) > 0 {
			q = q.Where("id NOT IN ?", excludeAccountIDs)
		}

		if err := q.Find(&accounts).Error; err != nil {
			return err
		}
		if len(accounts) == 0 {
			return gorm.ErrRecordNotFound
		}

		// 加权随机选择
		selected := &accounts[0]
		totalWeight := 0
		for i := range accounts {
			w := accounts[i].Weight
			if w <= 0 {
				w = 1
			}
			totalWeight += w
		}
		r := rand.Intn(totalWeight)
		cumulative := 0
		for i := range accounts {
			w := accounts[i].Weight
			if w <= 0 {
				w = 1
			}
			cumulative += w
			if r < cumulative {
				selected = &accounts[i]
				break
			}
		}

		accounts[0] = *selected
		return tx.Model(selected).UpdateColumn("current_tasks", gorm.Expr("current_tasks + 1")).Error
	})

	if err != nil {
		return nil, err
	}
	return &accounts[0], nil
}

func (s *UnifiedService) decrementAccountTasks(accountID uint) {
	model.DB().Model(&model.ChannelAccount{}).
		Where("id = ? AND current_tasks > 0", accountID).
		UpdateColumn("current_tasks", gorm.Expr("current_tasks - 1"))
}

func (s *UnifiedService) chargeTokenUsage(tokenID, userID uint, usage *chat.ChatUsage, endpoint *model.Endpoint) {
	if usage == nil || endpoint.PriceMode != model.PriceModeToken {
		return
	}

	inputCost := decimal.NewFromInt(int64(usage.PromptTokens)).
		Mul(endpoint.InputPrice).
		Div(decimal.NewFromInt(1_000_000))
	outputCost := decimal.NewFromInt(int64(usage.CompletionTokens)).
		Mul(endpoint.OutputPrice).
		Div(decimal.NewFromInt(1_000_000))
	totalCost := inputCost.Add(outputCost)

	if totalCost.IsPositive() {
		if err := s.billingService.Deduct(tokenID, userID, totalCost); err != nil {
			logger.Warn("charge failed", zap.Uint("token_id", tokenID), zap.Error(err))
		}
	}
}

// resolveProtocol 返回端点的协议类型
func resolveProtocol(endpoint *model.Endpoint) model.Protocol {
	if endpoint.Protocol != "" {
		return endpoint.Protocol
	}
	return model.ProtocolOpenAI
}

func isAnthropicProtocol(endpoint *model.Endpoint) bool {
	return resolveProtocol(endpoint) == model.ProtocolAnthropic
}

func isVolcengineProtocol(endpoint *model.Endpoint) bool {
	return resolveProtocol(endpoint) == model.ProtocolVolcengine
}

func channelConfigBool(ch *model.Channel, key string) bool {
	if len(ch.Config) == 0 {
		return false
	}
	var cfg map[string]any
	if json.Unmarshal(ch.Config, &cfg) != nil {
		return false
	}
	v, _ := cfg[key].(bool)
	return v
}

func resolveRequestPath(endpoint *model.Endpoint) string {
	if isAnthropicProtocol(endpoint) {
		return "/v1/messages"
	}
	return endpoint.RequestPath
}

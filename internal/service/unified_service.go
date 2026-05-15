package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

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
}

func NewUnifiedService() *UnifiedService {
	return &UnifiedService{
		billingService:    NewBillingService(),
		requestLogService: NewRequestLogService(),
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
func (s *UnifiedService) Route(tokenID uint, modelCode string, excludeChannelIDs []uint) (*RoutingResult, error) {
	endpoint, err := s.selectEndpoint(tokenID, modelCode, excludeChannelIDs)
	if err != nil {
		return nil, err
	}

	var channel model.Channel
	if err := model.DB().Where("id = ? AND status = 1", endpoint.ChannelID).First(&channel).Error; err != nil {
		return nil, fmt.Errorf("channel not found: %d", endpoint.ChannelID)
	}

	account, err := s.selectAccount(channel.ID)
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
	var excludeChannelIDs []uint
	maxRetries := 3

	for attempt := 0; attempt < maxRetries; attempt++ {
		route, err := s.Route(req.TokenID, req.Model, excludeChannelIDs)
		if err != nil {
			return nil, err
		}

		resp, err := s.doComplete(ctx, req, route)
		if err != nil {
			route.Cleanup()
			// 4xx 客户端错误不应 fallback，直接返回
			if strings.Contains(err.Error(), "http error: 4") {
				return nil, err
			}
			excludeChannelIDs = append(excludeChannelIDs, route.Endpoint.ChannelID)
			logger.Warn("complete attempt failed, trying fallback",
				zap.Int("attempt", attempt+1), zap.Error(err))
			continue
		}

		route.Cleanup()
		s.chargeTokenUsage(req.TokenID, req.UserID, resp.Usage, route.Endpoint)
		return resp, nil
	}

	return nil, fmt.Errorf("all attempts failed for model: %s", req.Model)
}

// StreamComplete 流式对话补全
func (s *UnifiedService) StreamComplete(ctx context.Context, req *CompletionRequest) (*StreamSession, error) {
	var excludeChannelIDs []uint
	maxRetries := 3

	for attempt := 0; attempt < maxRetries; attempt++ {
		route, err := s.Route(req.TokenID, req.Model, excludeChannelIDs)
		if err != nil {
			return nil, err
		}

		resp, reqLog, err := s.doStreamComplete(ctx, req, route)
		if err != nil {
			route.Cleanup()
			excludeChannelIDs = append(excludeChannelIDs, route.Endpoint.ChannelID)
			logger.Warn("stream attempt failed, trying fallback",
				zap.Int("attempt", attempt+1), zap.Error(err))
			continue
		}

		return &StreamSession{
			UpstreamResp:  resp,
			Route:         route,
			OriginalModel: req.Model,
			OriginalReq:   req,
			StartedAt:     time.Now(),
			RequestLog:    reqLog,
			CleanupFunc:   route.Cleanup,
		}, nil
	}

	return nil, fmt.Errorf("all attempts failed for model: %s", req.Model)
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
	s.chargeTokenUsage(session.OriginalReq.TokenID, session.OriginalReq.UserID, usage, session.Route.Endpoint)
	return result, nil
}

// ListModels 列出可用模型
func (s *UnifiedService) ListModels(ctx context.Context) ([]model.Model, error) {
	var models []model.Model
	if err := model.DB().Where("status = 1 AND type = 'chat'").Find(&models).Error; err != nil {
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
	if err := model.DB().Where("status = 1 AND type = 'chat'").Find(&models).Error; err != nil {
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
}

// --- 内部方法 ---

func (s *UnifiedService) doComplete(ctx context.Context, req *CompletionRequest, route *RoutingResult) (*CompletionResponse, error) {
	chatReq := s.buildChatRequest(req, route)
	chatReq.Stream = false

	reqLog, _ := s.createRequestLog(nil, req.Model, route.Channel, route.Account, route.Endpoint, chatReq, false)

	url := route.Channel.BaseURL + route.Endpoint.RequestPath
	headers := s.buildHeaders(route)

	ctx, cancel := context.WithTimeout(ctx, time.Duration(route.Endpoint.Timeout)*time.Second)
	defer cancel()

	start := time.Now()
	respBody, err := httputil.PostJSON(ctx, url, chatReq, headers)
	latency := time.Since(start).Milliseconds()

	if err != nil {
		s.finalizeRequestLog(reqLog, route.Channel, route.Endpoint, nil, nil, latency, err)
		return nil, fmt.Errorf("request failed: %w", err)
	}

	var chatResp CompletionResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		s.finalizeRequestLog(reqLog, route.Channel, route.Endpoint, nil, nil, latency, err)
		return nil, fmt.Errorf("unmarshal response failed: %w", err)
	}

	chatResp.Model = req.Model
	s.finalizeRequestLog(reqLog, route.Channel, route.Endpoint, &chat.ChatResponse{
		Usage:   chatResp.Usage,
		Choices: chatResp.Choices,
	}, nil, latency, nil)
	return &chatResp, nil
}

func (s *UnifiedService) doStreamComplete(ctx context.Context, req *CompletionRequest, route *RoutingResult) (*http.Response, *model.ChannelRequestLog, error) {
	chatReq := s.buildChatRequest(req, route)
	chatReq.Stream = true

	reqLog, _ := s.createRequestLog(nil, req.Model, route.Channel, route.Account, route.Endpoint, chatReq, true)

	url := route.Channel.BaseURL + route.Endpoint.RequestPath
	headers := s.buildHeaders(route)

	resp, err := httputil.PostJSONStream(ctx, url, chatReq, headers)
	if err != nil {
		s.finalizeRequestLog(reqLog, route.Channel, route.Endpoint, nil, nil, 0, err)
		return nil, nil, fmt.Errorf("stream request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		streamErr := fmt.Errorf("upstream returned status %d", resp.StatusCode)
		s.finalizeRequestLog(reqLog, route.Channel, route.Endpoint, nil, nil, 0, streamErr)
		return nil, nil, streamErr
	}

	return resp, reqLog, nil
}

func (s *UnifiedService) buildChatRequest(req *CompletionRequest, route *RoutingResult) *chat.ChatRequest {
	maxTokens := req.MaxTokens

	// 查询模型的 max_tokens 限制，如果设置了则裁剪
	var mdl model.Model
	if err := model.DB().Select("max_tokens").Where("code = ?", req.Model).First(&mdl).Error; err == nil {
		if mdl.MaxTokens > 0 && maxTokens > mdl.MaxTokens {
			maxTokens = mdl.MaxTokens
		}
	}

	chatReq := &chat.ChatRequest{
		Model:            route.Endpoint.VendorModel,
		Messages:         req.Messages,
		Temperature:      req.Temperature,
		MaxTokens:        maxTokens,
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

	// 合并 extra_config 中的 extra_body
	if route.Endpoint.ExtraConfig != nil {
		var cfg map[string]any
		if json.Unmarshal(route.Endpoint.ExtraConfig, &cfg) == nil {
			if extraBody, ok := cfg["extra_body"].(map[string]any); ok {
				chatReq.ExtraBody = extraBody
			}
		}
	}

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
		if prefix == "" {
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
		Where("endpoints.model_code = ? AND endpoints.status = 1", modelCode)

	if len(excludeChannelIDs) > 0 {
		query = query.Where("endpoints.channel_id NOT IN ?", excludeChannelIDs)
	}

	result := query.Scan(&candidates)
	if result.Error != nil {
		logger.Error("selectEndpoint: query error", zap.Error(result.Error), zap.String("modelCode", modelCode))
		return nil, result.Error
	}
	if len(candidates) == 0 {
		logger.Warn("selectEndpoint: no candidates found",
			zap.Uint("tokenID", tokenID),
			zap.String("modelCode", modelCode),
			zap.String("priorityKey", priorityKey),
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

func (s *UnifiedService) selectAccount(channelID uint) (*model.ChannelAccount, error) {
	var accounts []model.ChannelAccount

	err := model.DB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("channel_id = ? AND status = 1", channelID).
			Where("max_tasks = 0 OR current_tasks < max_tasks").
			Find(&accounts).Error; err != nil {
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

func (s *UnifiedService) saveMessages(session *StreamSession, result *StreamAggregationResult) {
	// 保存用户消息和助手回复到 conversations/messages 表
	// 复用现有逻辑，后续实现
}

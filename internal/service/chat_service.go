package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider/chat"
	"github.com/mirainya/Prism/pkg/logger"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

type ChatService struct {
	billingService    *BillingService
	requestLogService *RequestLogService
}

func NewChatService() *ChatService {
	return &ChatService{
		billingService:    NewBillingService(),
		requestLogService: NewRequestLogService(),
	}
}

// CompletionRequest 对话补全请求
type CompletionRequest struct {
	UserID           uint
	TokenID          uint
	Model            string
	Messages         []chat.ChatMessage
	Temperature      float64
	MaxTokens        int
	TopP             float64
	FrequencyPenalty float64
	PresencePenalty  float64
	Stop             []string
	Stream           bool
	StreamSpecified  bool
	Tools            []chat.ToolDefinition
	ToolChoice       any
	ResponseFormat   *chat.ResponseFormat
	Seed             *int
	User             string
	ConversationID   string
}

// CompletionResponse 对话补全响应
type CompletionResponse struct {
	ID             string                 `json:"id"`
	Object         string                 `json:"object"`
	Created        int64                  `json:"created"`
	Model          string                 `json:"model"`
	ConversationID string                 `json:"conversation_id,omitempty"`
	Choices        []chat.ChatChoice      `json:"choices"`
	Usage          *chat.ChatUsage        `json:"usage,omitempty"`
	Debug          *PlaygroundDebugDetail `json:"debug,omitempty"`
}

type StreamCompletionSession struct {
	UpstreamResp  *http.Response
	Conversation  *model.Conversation
	Channel       *model.Channel
	Account       *model.ChannelAccount
	ModelChannel  *model.ChatModelChannel
	ChatRequest   *chat.ChatRequest
	RequestLog    *model.ChannelRequestLog
	StartedAt     time.Time
	CleanupFunc   func()
	OriginalModel string
	OriginalReq   *CompletionRequest
}

type StreamAggregationResult struct {
	AssistantContent string
	ReasoningContent string
	FinishReason     string
	Usage            *chat.ChatUsage
	ErrorMessage     string
	ResponsePreview  string
	ResponseBody     string
}

type PlaygroundDebugDetail struct {
	ConversationID  uint              `json:"conversation_id,omitempty"`
	RequestLogID    uint              `json:"request_log_id,omitempty"`
	Status          string            `json:"status,omitempty"`
	ModelCode       string            `json:"model_code,omitempty"`
	VendorModel     string            `json:"vendor_model,omitempty"`
	ChannelID       uint              `json:"channel_id,omitempty"`
	ChannelName     string            `json:"channel_name,omitempty"`
	ChannelType     string            `json:"channel_type,omitempty"`
	AccountID       uint              `json:"account_id,omitempty"`
	RequestPath     string            `json:"request_path,omitempty"`
	IsStream        bool              `json:"is_stream"`
	LatencyMs       int64             `json:"latency_ms,omitempty"`
	StatusCode      int               `json:"status_code,omitempty"`
	ErrorMessage    string            `json:"error_message,omitempty"`
	FinishReason    string            `json:"finish_reason,omitempty"`
	ResponsePreview string            `json:"response_preview,omitempty"`
	RequestHeaders  map[string]string `json:"request_headers,omitempty"`
	RequestBody     map[string]any    `json:"request_body,omitempty"`
	ResponseBody    any               `json:"response_body,omitempty"`
	Usage           *chat.ChatUsage   `json:"usage,omitempty"`
}

// prepareResult prepare 方法的返回结果
type prepareResult struct {
	provider        chat.ChatProvider
	chatReq         *chat.ChatRequest
	channel         *model.Channel
	account         *model.ChannelAccount
	modelChannel    *model.ChatModelChannel
	conversation    *model.Conversation
	effectiveStream bool
	cleanupFunc     func()
}

// prepare 公共逻辑：查模型、选渠道、选账号、构建 ChatRequest
func (s *ChatService) prepare(ctx context.Context, req *CompletionRequest) (*prepareResult, error) {
	var chatModel model.ChatModel
	if err := model.DB().Where("code = ? AND status = 1", req.Model).First(&chatModel).Error; err != nil {
		return nil, fmt.Errorf("model not found: %s", req.Model)
	}

	modelChannel, err := s.selectModelChannel(req.TokenID, req.Model)
	if err != nil {
		return nil, err
	}

	var channel model.Channel
	if err := model.DB().Where("id = ? AND status = 1", modelChannel.ChannelID).First(&channel).Error; err != nil {
		return nil, fmt.Errorf("channel not found: %d", modelChannel.ChannelID)
	}

	strategyService := NewStrategyService()
	accountResult, err := strategyService.SelectAccount(channel.ID)
	if err != nil {
		return nil, fmt.Errorf("no available account for channel: %s", channel.Type)
	}
	account := *accountResult.Account

	var conversation *model.Conversation
	messages := req.Messages
	if req.ConversationID != "" {
		conversation, err = s.loadConversation(req.ConversationID, req.TokenID)
		if err == nil && conversation != nil {
			historyMessages, historyErr := s.loadMessages(conversation.ID)
			if historyErr != nil {
				return nil, fmt.Errorf("load conversation messages failed: %w", historyErr)
			}
			messages = append(historyMessages, req.Messages...)
		}
	}
	if conversation == nil {
		conversation = s.createConversation(req.UserID, req.TokenID, req.Model, req.Messages)
	}

	providerConfig := chat.ProviderConfig{
		BaseURL:      channel.BaseURL,
		APIKey:       account.APIKey,
		VendorModel:  modelChannel.VendorModel,
		RequestPath:  modelChannel.RequestPath,
		Timeout:      time.Duration(modelChannel.Timeout) * time.Second,
		ExtraHeaders: parseExtraHeaders(modelChannel.ExtraHeaders),
	}
	provider, err := chat.GetProvider(string(chatModel.Provider), providerConfig)
	if err != nil {
		strategyService.DecrementAccountTasks(account.ID)
		return nil, fmt.Errorf("get provider failed: %w", err)
	}

	effectiveStream := resolveModelChannelStream(req, modelChannel)

	chatReq := &chat.ChatRequest{
		Model:            req.Model,
		Messages:         messages,
		Temperature:      req.Temperature,
		MaxTokens:        req.MaxTokens,
		TopP:             req.TopP,
		FrequencyPenalty: req.FrequencyPenalty,
		PresencePenalty:  req.PresencePenalty,
		Stop:             req.Stop,
		Stream:           effectiveStream,
		Tools:            req.Tools,
		ToolChoice:       req.ToolChoice,
		ResponseFormat:   req.ResponseFormat,
		Seed:             req.Seed,
		User:             req.User,
	}
	if extraBody := parseExtraBody(modelChannel.ExtraConfig); len(extraBody) > 0 {
		chatReq.ExtraBody = extraBody
	}

	return &prepareResult{
		provider:        provider,
		chatReq:         chatReq,
		channel:         &channel,
		account:         &account,
		modelChannel:    modelChannel,
		conversation:    conversation,
		effectiveStream: effectiveStream,
		cleanupFunc: func() {
			strategyService.DecrementAccountTasks(account.ID)
		},
	}, nil
}

// Complete 执行对话补全
func (s *ChatService) Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error) {
	startedAt := time.Now()
	p, err := s.prepare(ctx, req)
	if err != nil {
		return nil, err
	}
	defer p.cleanupFunc()

	requestLog, err := s.createRequestLog(p.conversation, req.Model, p.channel, p.account, p.modelChannel, p.chatReq, p.effectiveStream)
	if err != nil {
		logger.Warn("create request log failed", zap.Error(err))
	}

	chatResp, err := p.provider.Complete(ctx, p.chatReq)
	latencyMs := time.Since(startedAt).Milliseconds()

	if err != nil {
		if requestLog != nil {
			s.finalizeRequestLog(requestLog, p.channel, p.modelChannel, nil, &StreamAggregationResult{ErrorMessage: err.Error()}, latencyMs, err)
		}
		if p.conversation != nil {
			s.updateConversationState(p.conversation, 0, "failed")
		}
		logger.Error("chat completion failed",
			zap.String("model", req.Model),
			zap.String("channel", p.channel.Type),
			zap.Error(err))
		return nil, fmt.Errorf("chat completion failed: %w", err)
	}

	cost, chargeErr := s.charge(req.TokenID, req.UserID, chatResp.Usage, p.modelChannel)
	if chargeErr != nil {
		logger.Warn("charge failed", zap.Error(chargeErr))
	}

	assistantMessage, saveErr := s.saveMessages(p.conversation, req.Messages, chatResp, p.modelChannel, p.account, latencyMs, cost, requestLogID(requestLog))
	if saveErr != nil {
		logger.Warn("save messages failed", zap.Error(saveErr))
	}
	if requestLog != nil {
		streamResult := &StreamAggregationResult{
			AssistantContent: assistantMessage.Content,
			ReasoningContent: assistantMessage.ReasoningContent,
			FinishReason:     assistantMessage.FinishReason,
			Usage:            chatResp.Usage,
		}
		s.finalizeRequestLog(requestLog, p.channel, p.modelChannel, chatResp, streamResult, latencyMs, nil)
	}
	if p.conversation != nil {
		s.updateConversationState(p.conversation, requestLogID(requestLog), "completed")
	}

	response := &CompletionResponse{
		ID:      chatResp.ID,
		Object:  "chat.completion",
		Created: chatResp.Created,
		Model:   req.Model,
		Choices: chatResp.Choices,
		Usage:   chatResp.Usage,
		Debug:   s.buildDebugDetail(p.conversation, requestLog, p.channel, p.modelChannel, latencyMs),
	}
	if p.conversation != nil {
		response.ConversationID = fmt.Sprintf("%d", p.conversation.ID)
	}

	logger.Info("chat completion success",
		zap.String("model", req.Model),
		zap.String("channel", p.channel.Type),
		zap.Int64("latency_ms", latencyMs),
		zap.String("cost", cost.String()))

	return response, nil
}

// StreamComplete 流式对话补全，返回带落库上下文的会话
func (s *ChatService) StreamComplete(ctx context.Context, req *CompletionRequest) (*StreamCompletionSession, error) {
	startedAt := time.Now()
	p, err := s.prepare(ctx, req)
	if err != nil {
		return nil, err
	}

	requestLog, logErr := s.createRequestLog(p.conversation, req.Model, p.channel, p.account, p.modelChannel, p.chatReq, p.effectiveStream)
	if logErr != nil {
		logger.Warn("create request log failed", zap.Error(logErr))
	}

	upstreamResp, err := p.provider.StreamComplete(ctx, p.chatReq)
	if err != nil {
		if requestLog != nil {
			s.finalizeRequestLog(requestLog, p.channel, p.modelChannel, nil, &StreamAggregationResult{ErrorMessage: err.Error()}, time.Since(startedAt).Milliseconds(), err)
		}
		if p.conversation != nil {
			s.updateConversationState(p.conversation, 0, "failed")
		}
		p.cleanupFunc()
		logger.Error("stream completion failed",
			zap.String("model", req.Model),
			zap.String("channel", p.channel.Type),
			zap.Error(err))
		return nil, fmt.Errorf("stream completion failed: %w", err)
	}

	logger.Info("stream completion started",
		zap.String("model", req.Model),
		zap.String("channel", p.channel.Type))

	return &StreamCompletionSession{
		UpstreamResp:  upstreamResp,
		Conversation:  p.conversation,
		Channel:       p.channel,
		Account:       p.account,
		ModelChannel:  p.modelChannel,
		ChatRequest:   p.chatReq,
		RequestLog:    requestLog,
		StartedAt:     startedAt,
		CleanupFunc:   p.cleanupFunc,
		OriginalModel: req.Model,
		OriginalReq:   req,
	}, nil
}

func (s *ChatService) FinalizeStream(session *StreamCompletionSession, result *StreamAggregationResult, streamErr error) (*PlaygroundDebugDetail, error) {
	if session == nil {
		return nil, nil
	}
	latencyMs := time.Since(session.StartedAt).Milliseconds()
	if result == nil {
		result = &StreamAggregationResult{}
	}

	var cost decimal.Decimal
	if result.Usage != nil {
		chargedCost, err := s.charge(session.OriginalReq.TokenID, session.OriginalReq.UserID, result.Usage, session.ModelChannel)
		if err != nil {
			logger.Warn("charge failed", zap.Error(err))
		} else {
			cost = chargedCost
		}
	}

	response := aggregatedStreamToChatResponse(session, result)
	assistantMessage, saveErr := s.saveMessages(session.Conversation, session.OriginalReq.Messages, response, session.ModelChannel, session.Account, latencyMs, cost, requestLogID(session.RequestLog))
	if saveErr != nil {
		logger.Warn("save stream messages failed", zap.Error(saveErr))
	}
	if session.RequestLog != nil {
		s.finalizeRequestLog(session.RequestLog, session.Channel, session.ModelChannel, response, result, latencyMs, streamErr)
	}
	status := "completed"
	if streamErr != nil || result.ErrorMessage != "" {
		status = "failed"
	}
	if session.Conversation != nil {
		s.updateConversationState(session.Conversation, requestLogID(session.RequestLog), status)
	}
	if assistantMessage.Content != "" && result.ResponsePreview == "" {
		result.ResponsePreview = truncateString(assistantMessage.Content, 500)
	}
	return s.buildDebugDetail(session.Conversation, session.RequestLog, session.Channel, session.ModelChannel, latencyMs), nil
}

func aggregatedStreamToChatResponse(session *StreamCompletionSession, result *StreamAggregationResult) *chat.ChatResponse {
	if session == nil || result == nil {
		return &chat.ChatResponse{}
	}
	message := chat.ChatMessage{
		Role:             model.RoleAssistant,
		Content:          result.AssistantContent,
		ReasoningContent: result.ReasoningContent,
	}
	choice := chat.ChatChoice{Index: 0, Message: message, FinishReason: result.FinishReason}
	return &chat.ChatResponse{
		ID:      fmt.Sprintf("stream-%d", time.Now().UnixNano()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   session.OriginalModel,
		Choices: []chat.ChatChoice{choice},
		Usage:   result.Usage,
	}
}

// selectModelChannel 根据令牌优先级选择模型渠道
func (s *ChatService) selectModelChannel(tokenID uint, modelCode string) (*model.ChatModelChannel, error) {
	priorityKey := "chat:" + modelCode
	db := model.DB()

	type candidate struct {
		model.ChatModelChannel
		TokenPriority *int
	}

	var candidates []candidate
	if err := db.Table("chat_model_channels AS mc").
		Select("mc.*, tcp.priority AS token_priority").
		Joins("JOIN channels c ON c.id = mc.channel_id AND c.status = 1").
		Joins("LEFT JOIN token_channel_priorities tcp ON tcp.channel_id = mc.channel_id AND tcp.token_id = ? AND tcp.capability_code = ?", tokenID, priorityKey).
		Where("mc.model_code = ? AND mc.status = 1", modelCode).
		Scan(&candidates).Error; err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no available channel for model: %s", modelCode)
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		leftPriority := candidates[i].TokenPriority
		rightPriority := candidates[j].TokenPriority
		if leftPriority != nil && rightPriority != nil && *leftPriority != *rightPriority {
			return *leftPriority < *rightPriority
		}
		if leftPriority != nil && rightPriority == nil {
			return true
		}
		if leftPriority == nil && rightPriority != nil {
			return false
		}
		if candidates[i].Priority != candidates[j].Priority {
			return candidates[i].Priority > candidates[j].Priority
		}
		return candidates[i].ID < candidates[j].ID
	})

	selected := candidates[0].ChatModelChannel
	return &selected, nil
}

func (s *ChatService) createRequestLog(
	conversation *model.Conversation,
	modelCode string,
	channel *model.Channel,
	account *model.ChannelAccount,
	mc *model.ChatModelChannel,
	req *chat.ChatRequest,
	isStream bool,
) (*model.ChannelRequestLog, error) {
	if channel == nil || account == nil || mc == nil || req == nil {
		return nil, nil
	}
	requestBodyBytes, _ := json.Marshal(req)
	headersJSON, _ := json.Marshal(maskSensitiveHeaders(parseExtraHeaders(mc.ExtraHeaders)))
	requestURL := strings.TrimSuffix(channel.BaseURL, "/") + mc.RequestPath
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
		VendorModel:    mc.VendorModel,
		RequestPath:    mc.RequestPath,
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

func (s *ChatService) finalizeRequestLog(
	requestLog *model.ChannelRequestLog,
	channel *model.Channel,
	mc *model.ChatModelChannel,
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
	if mc != nil {
		updates["vendor_model"] = mc.VendorModel
		updates["request_path"] = mc.RequestPath
	}
	if channel != nil {
		updates["url"] = strings.TrimSuffix(channel.BaseURL, "/") + requestLog.RequestPath
	}
	if err := s.requestLogService.Update(requestLog.ID, updates); err != nil {
		logger.Warn("update request log failed", zap.Error(err), zap.Uint("request_log_id", requestLog.ID))
		return
	}
	for k, v := range updates {
		switch key := k; key {
		case "duration_ms":
			requestLog.DurationMs = v.(int64)
		case "status_code":
			requestLog.StatusCode = v.(int)
		case "error_message":
			requestLog.ErrorMessage, _ = v.(string)
		case "finish_reason":
			requestLog.FinishReason, _ = v.(string)
		case "response_preview":
			requestLog.ResponsePreview, _ = v.(string)
		case "response_body":
			requestLog.ResponseBody, _ = v.(string)
		case "usage_prompt_tokens":
			requestLog.UsagePromptTokens = v.(int)
		case "usage_completion_tokens":
			requestLog.UsageCompletionTokens = v.(int)
		case "usage_total_tokens":
			requestLog.UsageTotalTokens = v.(int)
		}
	}
}

func (s *ChatService) loadConversation(conversationID string, tokenID uint) (*model.Conversation, error) {
	var conv model.Conversation
	err := model.DB().Where("id = ? AND token_id = ? AND status = 1", conversationID, tokenID).
		First(&conv).Error
	return &conv, err
}

func (s *ChatService) loadMessages(conversationID uint) ([]chat.ChatMessage, error) {
	var messages []model.Message
	model.DB().Where("conversation_id = ?", conversationID).
		Order("created_at ASC").
		Find(&messages)

	result := make([]chat.ChatMessage, 0, len(messages))
	for _, msg := range messages {
		result = append(result, chat.ChatMessage{
			Role:             msg.Role,
			Content:          msg.Content,
			ReasoningContent: msg.ReasoningContent,
		})
	}
	return result, nil
}

func (s *ChatService) createConversation(userID, tokenID uint, modelCode string, messages []chat.ChatMessage) *model.Conversation {
	title := ""
	systemPrompt := ""
	for _, msg := range messages {
		if msg.Role == model.RoleSystem {
			systemPrompt = msg.ContentText()
		} else if msg.Role == model.RoleUser && title == "" {
			title = truncateString(msg.ContentText(), 50)
		}
	}
	conv := &model.Conversation{
		UserID:       userID,
		TokenID:      tokenID,
		Title:        title,
		Model:        modelCode,
		SystemPrompt: systemPrompt,
		LastStatus:   "pending",
		Status:       1,
	}
	model.DB().Create(conv)
	return conv
}

func (s *ChatService) saveMessages(
	conv *model.Conversation,
	userMessages []chat.ChatMessage,
	resp *chat.ChatResponse,
	mc *model.ChatModelChannel,
	account *model.ChannelAccount,
	latencyMs int64,
	cost decimal.Decimal,
	requestLogID uint,
) (*model.Message, error) {
	if conv == nil || mc == nil || account == nil {
		return &model.Message{}, nil
	}
	for _, msg := range userMessages {
		message := &model.Message{
			ConversationID: conv.ID,
			RequestLogID:   requestLogID,
			Role:           msg.Role,
			Content:        msg.ContentText(),
			Model:          mc.ModelCode,
		}
		if err := model.DB().Create(message).Error; err != nil {
			return &model.Message{}, err
		}
	}

	assistantStored := &model.Message{}
	if len(resp.Choices) > 0 {
		assistantMsg := resp.Choices[0].Message
		inputTokens := 0
		outputTokens := 0
		if resp.Usage != nil {
			inputTokens = resp.Usage.PromptTokens
			outputTokens = resp.Usage.CompletionTokens
		}
		assistantStored = &model.Message{
			ConversationID:   conv.ID,
			RequestLogID:     requestLogID,
			Role:             assistantMsg.Role,
			Content:          assistantMsg.ContentText(),
			ReasoningContent: assistantMsg.ReasoningContent,
			FinishReason:     resp.Choices[0].FinishReason,
			InputTokens:      inputTokens,
			OutputTokens:     outputTokens,
			Model:            mc.ModelCode,
			ChannelID:        mc.ChannelID,
			AccountID:        account.ID,
			LatencyMs:        int(latencyMs),
			Cost:             cost,
		}
		if err := model.DB().Create(assistantStored).Error; err != nil {
			return assistantStored, err
		}

		updates := map[string]any{
			"total_tokens":  conv.TotalTokens + inputTokens + outputTokens,
			"message_count": conv.MessageCount + len(userMessages) + 1,
			"model":         mc.ModelCode,
			"last_status":   "completed",
		}
		if requestLogID > 0 {
			updates["last_request_log_id"] = requestLogID
		}
		if err := model.DB().Model(conv).Updates(updates).Error; err != nil {
			return assistantStored, err
		}
	}
	return assistantStored, nil
}

func (s *ChatService) updateConversationState(conv *model.Conversation, requestLogID uint, status string) {
	if conv == nil {
		return
	}
	updates := map[string]any{"last_status": status}
	if requestLogID > 0 {
		updates["last_request_log_id"] = requestLogID
	}
	if err := model.DB().Model(conv).Updates(updates).Error; err != nil {
		logger.Warn("update conversation state failed", zap.Error(err), zap.Uint("conversation_id", conv.ID))
	}
	conv.LastStatus = status
	if requestLogID > 0 {
		conv.LastRequestLogID = requestLogID
	}
}

func (s *ChatService) buildDebugDetail(
	conversation *model.Conversation,
	requestLog *model.ChannelRequestLog,
	channel *model.Channel,
	mc *model.ChatModelChannel,
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
	if mc != nil {
		debug.RequestPath = mc.RequestPath
		debug.VendorModel = mc.VendorModel
	}
	if requestLog.ErrorMessage != "" {
		debug.Status = "failed"
	}
	if debug.Usage != nil && debug.Usage.TotalTokens == 0 && debug.Usage.PromptTokens == 0 && debug.Usage.CompletionTokens == 0 {
		debug.Usage = nil
	}
	return debug
}

func requestLogID(log *model.ChannelRequestLog) uint {
	if log == nil {
		return 0
	}
	return log.ID
}

func (s *ChatService) charge(tokenID, userID uint, usage *chat.ChatUsage, mc *model.ChatModelChannel) (decimal.Decimal, error) {
	if usage == nil {
		return decimal.Zero, nil
	}

	var cost decimal.Decimal
	if mc.PriceMode == model.PriceModeToken {
		promptTokens := decimal.NewFromInt(int64(usage.PromptTokens))
		completionTokens := decimal.NewFromInt(int64(usage.CompletionTokens))
		million := decimal.NewFromInt(1000000)
		cost = promptTokens.Div(million).Mul(mc.InputPrice).Add(
			completionTokens.Div(million).Mul(mc.OutputPrice))
	} else {
		cost = mc.InputPrice
	}

	if cost.GreaterThan(decimal.Zero) {
		if err := s.billingService.Deduct(tokenID, userID, cost); err != nil {
			return decimal.Zero, err
		}
	}

	return cost, nil
}

// ListModels 获取可用模型列表
func (s *ChatService) ListModels(ctx context.Context) ([]model.ChatModel, error) {
	var models []model.ChatModel
	err := model.DB().Where("status = 1").Order("code ASC").Find(&models).Error
	return models, err
}

type PlaygroundModelInfo struct {
	ID                     string `json:"id"`
	Object                 string `json:"object"`
	Created                int64  `json:"created"`
	OwnedBy                string `json:"owned_by"`
	SupportsStream         *bool  `json:"supports_stream,omitempty"`
	DefaultStream          *bool  `json:"default_stream,omitempty"`
	SupportsTools          *bool  `json:"supports_tools,omitempty"`
	SupportsResponseFormat *bool  `json:"supports_response_format,omitempty"`
	SupportsMultimodal     *bool  `json:"supports_multimodal,omitempty"`
}

func (s *ChatService) ListPlaygroundModels(ctx context.Context, tokenID uint) ([]PlaygroundModelInfo, error) {
	var chatModels []model.ChatModel
	if err := model.DB().Where("status = 1").Order("code ASC").Find(&chatModels).Error; err != nil {
		return nil, err
	}

	var modelChannels []model.ChatModelChannel
	if err := model.DB().Where("status = 1").Find(&modelChannels).Error; err != nil {
		return nil, err
	}

	priorityMap := make(map[string]int)
	var priorities []model.TokenChannelPriority
	if err := model.DB().Where("token_id = ?", tokenID).Find(&priorities).Error; err == nil {
		for _, p := range priorities {
			priorityMap[fmt.Sprintf("%s:%d", p.CapabilityCode, p.ChannelID)] = p.Priority
		}
	}

	modelChannelsByCode := make(map[string][]model.ChatModelChannel)
	for _, mc := range modelChannels {
		var channel model.Channel
		if err := model.DB().Where("id = ? AND status = 1", mc.ChannelID).First(&channel).Error; err != nil {
			continue
		}
		modelChannelsByCode[mc.ModelCode] = append(modelChannelsByCode[mc.ModelCode], mc)
	}

	result := make([]PlaygroundModelInfo, 0, len(chatModels))
	for _, chatModel := range chatModels {
		channels := modelChannelsByCode[chatModel.Code]
		if len(channels) == 0 {
			continue
		}

		sort.SliceStable(channels, func(i, j int) bool {
			leftKey := fmt.Sprintf("chat:%s:%d", chatModel.Code, channels[i].ChannelID)
			rightKey := fmt.Sprintf("chat:%s:%d", chatModel.Code, channels[j].ChannelID)
			leftPriority, leftOk := priorityMap[leftKey]
			rightPriority, rightOk := priorityMap[rightKey]
			if leftOk != rightOk {
				return leftOk
			}
			if leftOk && rightOk && leftPriority != rightPriority {
				return leftPriority < rightPriority
			}
			if channels[i].Priority != channels[j].Priority {
				return channels[i].Priority > channels[j].Priority
			}
			return channels[i].ID < channels[j].ID
		})

		selected := channels[0]
		result = append(result, PlaygroundModelInfo{
			ID:             chatModel.Code,
			Object:         "model",
			Created:        chatModel.CreatedAt.Unix(),
			OwnedBy:        string(chatModel.Provider),
			SupportsStream: selected.SupportsStream,
			DefaultStream:  selected.DefaultStream,
		})
	}

	return result, nil
}

func resolveModelChannelStream(req *CompletionRequest, modelChannel *model.ChatModelChannel) bool {
	if req.StreamSpecified {
		if modelChannel.SupportsStream != nil && !*modelChannel.SupportsStream {
			return false
		}
		return req.Stream
	}
	if modelChannel.DefaultStream != nil {
		if modelChannel.SupportsStream != nil && !*modelChannel.SupportsStream && *modelChannel.DefaultStream {
			return false
		}
		return *modelChannel.DefaultStream
	}
	if modelChannel.SupportsStream != nil && !*modelChannel.SupportsStream {
		return false
	}
	return req.Stream
}

func parseExtraHeaders(raw []byte) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	var headers map[string]string
	if err := json.Unmarshal(raw, &headers); err == nil && len(headers) > 0 {
		return headers
	}
	return nil
}

func parseExtraBody(raw []byte) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var extraConfig struct {
		ExtraBody map[string]any `json:"extra_body"`
	}
	if err := json.Unmarshal(raw, &extraConfig); err == nil && len(extraConfig.ExtraBody) > 0 {
		return extraConfig.ExtraBody
	}
	return nil
}

func maskSensitiveHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	masked := make(map[string]string, len(headers))
	for key, value := range headers {
		lowerKey := strings.ToLower(key)
		switch {
		case strings.Contains(lowerKey, "authorization"), strings.Contains(lowerKey, "api-key"), strings.Contains(lowerKey, "apikey"), strings.Contains(lowerKey, "cookie"), strings.Contains(lowerKey, "token"):
			masked[key] = maskSecret(value)
		default:
			masked[key] = value
		}
	}
	return masked
}

func maskSecret(value string) string {
	if value == "" {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= 8 {
		return "***"
	}
	return string(runes[:4]) + "***" + string(runes[len(runes)-4:])
}

func truncateString(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

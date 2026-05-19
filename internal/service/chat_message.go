package service

import (
	"encoding/json"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider/chat"
	"github.com/mirainya/Prism/pkg/logger"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

func (s *UnifiedService) loadConversation(conversationID string, tokenID uint) (*model.Conversation, error) {
	var conv model.Conversation
	err := model.DB().Where("id = ? AND token_id = ? AND status = 1", conversationID, tokenID).
		First(&conv).Error
	return &conv, err
}

func (s *UnifiedService) loadMessages(conversationID uint, targetModel string) ([]chat.ChatMessage, error) {
	var messages []model.Message
	model.DB().Where("conversation_id = ?", conversationID).
		Order("created_at ASC").
		Find(&messages)

	// 判断目标模型是否支持 reasoning_content
	keepReasoning := false
	var mdl model.Model
	if model.DB().Select("provider").Where("code = ?", targetModel).First(&mdl).Error == nil {
		keepReasoning = mdl.Provider == "anthropic"
	}

	result := make([]chat.ChatMessage, 0, len(messages))
	for _, msg := range messages {
		cm := chat.ChatMessage{
			Role:    msg.Role,
			Content: restoreContent(msg.Content, msg.Attachments),
		}
		if keepReasoning {
			cm.ReasoningContent = msg.ReasoningContent
		}
		result = append(result, cm)
	}
	return result, nil
}

// restoreContent 从 text + attachments 还原为原始 content 结构
func restoreContent(text, attachments string) any {
	if attachments == "" {
		return text
	}
	var parts []any
	if err := json.Unmarshal([]byte(attachments), &parts); err != nil {
		return text
	}
	if text != "" {
		textPart := map[string]any{"type": "text", "text": text}
		parts = append([]any{textPart}, parts...)
	}
	return parts
}

func (s *UnifiedService) findOrCreateConversation(userID, tokenID uint, modelCode string, messages []chat.ChatMessage) *model.Conversation {
	// 提取第一条 user message 作为标题指纹
	title := ""
	for _, msg := range messages {
		if msg.Role == model.RoleUser {
			title = truncateString(msg.ContentText(), 50)
			break
		}
	}
	if title == "" {
		return s.createConversation(userID, tokenID, modelCode, messages)
	}

	// 在同一 token 下查找 2 小时内 title 匹配的 conversation
	// 仅当请求消息数 > 已有消息数时才归并（说明是续传历史），否则视为新对话
	var conv model.Conversation
	since := time.Now().Add(-2 * time.Hour)
	err := model.DB().Where("token_id = ? AND title = ? AND status = 1 AND updated_at > ?", tokenID, title, since).
		Order("updated_at DESC").First(&conv).Error
	if err == nil && len(messages) > conv.MessageCount {
		model.DB().Model(&conv).Update("updated_at", time.Now())
		return &conv
	}

	return s.createConversation(userID, tokenID, modelCode, messages)
}

func (s *UnifiedService) createConversation(userID, tokenID uint, modelCode string, messages []chat.ChatMessage) *model.Conversation {
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

func (s *UnifiedService) saveConversationMessages(
	conv *model.Conversation,
	userMessages []chat.ChatMessage,
	resp *chat.ChatResponse,
	endpoint *model.Endpoint,
	account *model.ChannelAccount,
	latencyMs int64,
	cost decimal.Decimal,
	reqLogID uint,
) (*model.Message, error) {
	if conv == nil || endpoint == nil || account == nil {
		return &model.Message{}, nil
	}
	for _, msg := range userMessages {
		message := &model.Message{
			ConversationID: conv.ID,
			RequestLogID:   reqLogID,
			Role:           msg.Role,
			Content:        msg.ContentText(),
			Attachments:    msg.ContentAttachments(),
			Model:          endpoint.ModelCode,
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
			RequestLogID:     reqLogID,
			Role:             assistantMsg.Role,
			Content:          assistantMsg.ContentText(),
			ReasoningContent: assistantMsg.ReasoningContent,
			FinishReason:     resp.Choices[0].FinishReason,
			InputTokens:      inputTokens,
			OutputTokens:     outputTokens,
			Model:            endpoint.ModelCode,
			ChannelID:        endpoint.ChannelID,
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
			"model":         endpoint.ModelCode,
			"last_status":   "completed",
		}
		if reqLogID > 0 {
			updates["last_request_log_id"] = reqLogID
		}
		if err := model.DB().Model(conv).Updates(updates).Error; err != nil {
			return assistantStored, err
		}
	}
	return assistantStored, nil
}

func (s *UnifiedService) updateConversationState(conv *model.Conversation, reqLogID uint, status string) {
	if conv == nil {
		return
	}
	updates := map[string]any{"last_status": status}
	if reqLogID > 0 {
		updates["last_request_log_id"] = reqLogID
	}
	if err := model.DB().Model(conv).Updates(updates).Error; err != nil {
		logger.Warn("update conversation state failed", zap.Error(err), zap.Uint("conversation_id", conv.ID))
	}
	conv.LastStatus = status
	if reqLogID > 0 {
		conv.LastRequestLogID = reqLogID
	}
}

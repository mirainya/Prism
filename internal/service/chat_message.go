package service

import (
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

func (s *UnifiedService) loadMessages(conversationID uint) ([]chat.ChatMessage, error) {
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

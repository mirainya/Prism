package service

import (
	"encoding/json"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider/chat"
)

// ConversationContext 会话上下文:playground 请求前加载,用于拼历史 + 火山 B 模式。
type ConversationContext struct {
	Conv               *model.Conversation // nil 表示新会话
	History            []chat.ChatMessage  // 已含 system prompt 的历史消息
	PreviousResponseID string              // 火山 B 模式:非空则只发新消息
}

// LoadConversationContext 按 conversationID 加载会话历史(playground 会话续聊)。
// conversationID 为空或加载失败时返回空上下文(视为新会话)。会话逻辑由调用方(handler)编排,
// gateway pipeline 保持无状态。
func LoadConversationContext(conversationID string, tokenID uint, targetModel string) *ConversationContext {
	cc := &ConversationContext{}
	if conversationID == "" {
		return cc
	}
	var conv model.Conversation
	if err := model.DB().Where("id = ? AND token_id = ? AND status = 1", conversationID, tokenID).
		First(&conv).Error; err != nil {
		return cc
	}
	cc.Conv = &conv

	history := loadConversationMessages(conv.ID, targetModel)
	if conv.SystemPrompt != "" {
		history = append([]chat.ChatMessage{{Role: model.RoleSystem, Content: conv.SystemPrompt}}, history...)
	}
	cc.History = history
	// 有状态对话(B模式):有有效 response_id 时只发新消息,历史由上游维护
	if conv.ProviderResponseID != "" {
		cc.PreviousResponseID = conv.ProviderResponseID
	}
	return cc
}

// loadConversationMessages 载入某会话的历史消息(按时间升序)。
// anthropic 模型保留 reasoning_content。
func loadConversationMessages(conversationID uint, targetModel string) []chat.ChatMessage {
	var messages []model.Message
	model.DB().Where("conversation_id = ?", conversationID).
		Order("created_at ASC").Find(&messages)

	keepReasoning := modelUsesAnthropic(targetModel)

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
	return result
}

// modelUsesAnthropic 判断某模型是否走 anthropic 协议(据 gw 路由表:任一可用渠道为 anthropic 即算)。
// anthropic 需在历史里保留 reasoning_content。chat 已全切网关,故从 gw_channels.protocol 取,
// 不再读老 models.provider。
func modelUsesAnthropic(modelName string) bool {
	var cnt int64
	model.DB().Table("gw_abilities ab").
		Joins("JOIN gw_channels gc ON gc.id = ab.channel_id AND gc.deleted_at IS NULL").
		Where("ab.model_name = ? AND gc.protocol = ?", modelName, model.ProtocolAnthropic).
		Count(&cnt)
	return cnt > 0
}

// SaveConversationTurn 保存一轮对话(playground)。cc.Conv 为 nil 时按新会话创建。
// gw 路由无 endpoint/account 概念,故 Message 的 channel_id/account_id 置空,溯源靠 request_log_id
// 关联到 channel_request_logs(那里已记录 gw channel/key id)。
// 返回落库的 conversation id + 是否回写了新的 provider_response_id。
func SaveConversationTurn(
	cc *ConversationContext,
	userID, tokenID uint,
	modelCode string,
	newMessages []chat.ChatMessage,
	assistant chat.ChatMessage,
	usage *chat.ChatUsage,
	finishReason string,
	providerResponseID string,
	reqLogID uint,
) uint {
	conv := cc.Conv
	msgs := newMessages
	if conv == nil {
		conv = findOrCreatePlaygroundConversation(userID, tokenID, modelCode, newMessages)
		// 无历史会话:只存最后一条 user 消息(增量),避免把整个 prompt 塞进去
		msgs = lastUserMessage(newMessages)
	}
	if conv == nil {
		return 0
	}

	inputTokens, outputTokens := 0, 0
	if usage != nil {
		inputTokens, outputTokens = usage.PromptTokens, usage.CompletionTokens
	}

	for _, m := range msgs {
		model.DB().Create(&model.Message{
			ConversationID: conv.ID,
			RequestLogID:   reqLogID,
			Role:           m.Role,
			Content:        m.ContentText(),
			Attachments:    m.ContentAttachments(),
			Model:          modelCode,
		})
	}
	model.DB().Create(&model.Message{
		ConversationID:   conv.ID,
		RequestLogID:     reqLogID,
		Role:             "assistant",
		Content:          assistant.ContentText(),
		ReasoningContent: assistant.ReasoningContent,
		FinishReason:     finishReason,
		InputTokens:      inputTokens,
		OutputTokens:     outputTokens,
		Model:            modelCode,
	})

	updates := map[string]any{
		"total_tokens":  conv.TotalTokens + inputTokens + outputTokens,
		"message_count": conv.MessageCount + len(msgs) + 1,
		"model":         modelCode,
		"last_status":   "completed",
	}
	if reqLogID > 0 {
		updates["last_request_log_id"] = reqLogID
	}
	// 有状态对话:回写本轮 response_id 供下一轮 B 模式复用
	if providerResponseID != "" && providerResponseID != conv.ProviderResponseID {
		updates["provider_response_id"] = providerResponseID
	}
	model.DB().Model(conv).Updates(updates)
	return conv.ID
}

// findOrCreatePlaygroundConversation 复用老逻辑:2 小时内同 token+title 且消息数增长则归并,否则新建。
func findOrCreatePlaygroundConversation(userID, tokenID uint, modelCode string, messages []chat.ChatMessage) *model.Conversation {
	title, systemPrompt := "", ""
	for _, msg := range messages {
		if msg.Role == model.RoleSystem {
			systemPrompt = msg.ContentText()
		} else if msg.Role == model.RoleUser && title == "" {
			title = truncateString(msg.ContentText(), 50)
		}
	}
	if title != "" {
		var conv model.Conversation
		since := time.Now().Add(-2 * time.Hour)
		err := model.DB().Where("token_id = ? AND title = ? AND status = 1 AND updated_at > ?", tokenID, title, since).
			Order("updated_at DESC").First(&conv).Error
		if err == nil && len(messages) > conv.MessageCount {
			model.DB().Model(&conv).Update("updated_at", time.Now())
			return &conv
		}
	}
	conv := &model.Conversation{
		UserID: userID, TokenID: tokenID, Title: title, Model: modelCode,
		SystemPrompt: systemPrompt, LastStatus: "pending", Status: 1,
	}
	model.DB().Create(conv)
	return conv
}

// restoreContent 从 text + attachments 还原为原始 content 结构。
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

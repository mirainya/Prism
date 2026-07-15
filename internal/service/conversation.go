package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider/chat"
	"gorm.io/gorm"
)

// ConversationContext 会话上下文:playground 请求前加载,用于拼历史 + 火山 B 模式。
type ConversationContext struct {
	Conv               *model.Conversation // nil 表示新会话
	History            []chat.ChatMessage  // 已含 system prompt 的历史消息
	PreviousResponseID string              // 火山 B 模式:非空则只发新消息
	ProviderKeyID      uint
	UpstreamTransport  model.UpstreamTransport
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
		cc.ProviderKeyID = conv.ProviderKeyID
		cc.UpstreamTransport = conv.UpstreamTransport
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
	callID string,
	reqLogID uint,
	provenance ...ConversationProvenance,
) (uint, error) {
	inputTokens, outputTokens := 0, 0
	if usage != nil {
		inputTokens, outputTokens = usage.PromptTokens, usage.CompletionTokens
	}

	var conversationID uint
	err := model.DB().Transaction(func(tx *gorm.DB) error {
		var conv *model.Conversation
		msgs := newMessages
		if cc != nil && cc.Conv != nil {
			var current model.Conversation
			if err := tx.First(&current, cc.Conv.ID).Error; err != nil {
				return err
			}
			conv = &current
		} else {
			var err error
			conv, err = findOrCreatePlaygroundConversationTx(tx, userID, tokenID, modelCode, newMessages)
			if err != nil {
				return err
			}
			msgs = lastUserMessage(newMessages)
		}
		if conv == nil {
			return errors.New("conversation is unavailable")
		}
		conversationID = conv.ID

		records := make([]model.Message, 0, len(msgs)+1)
		for _, message := range msgs {
			records = append(records, model.Message{
				ConversationID: conv.ID, CallID: callID, RequestLogID: reqLogID,
				Role: message.Role, Content: message.ContentText(), Attachments: message.ContentAttachments(),
				Model: modelCode,
			})
		}
		records = append(records, model.Message{
			ConversationID: conv.ID, CallID: callID, RequestLogID: reqLogID,
			Role: "assistant", Content: assistant.ContentText(), ReasoningContent: assistant.ReasoningContent,
			FinishReason: finishReason, InputTokens: inputTokens, OutputTokens: outputTokens, Model: modelCode,
		})
		if err := tx.Create(&records).Error; err != nil {
			return err
		}

		updates := map[string]any{
			"total_tokens":  gorm.Expr("total_tokens + ?", inputTokens+outputTokens),
			"message_count": gorm.Expr("message_count + ?", len(records)),
			"model":         modelCode,
			"last_status":   "completed",
		}
		if reqLogID > 0 {
			updates["last_request_log_id"] = reqLogID
		}
		if callID != "" {
			updates["call_id"] = callID
		}
		if providerResponseID != "" && providerResponseID != conv.ProviderResponseID {
			updates["provider_response_id"] = providerResponseID
		}
		if len(provenance) > 0 && (provenance[0].KeyID != 0 || provenance[0].Transport != "") {
			updates["provider_key_id"] = provenance[0].KeyID
			updates["upstream_transport"] = provenance[0].Transport
		}
		result := tx.Model(&model.Conversation{}).Where("id = ?", conv.ID).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errors.New("conversation was not updated")
		}
		if reqLogID > 0 {
			if err := linkConversationRequestLog(tx, reqLogID, conv.ID); err != nil {
				return err
			}
		}
		if callID != "" {
			if err := linkConversationCall(tx, callID, conv.ID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return conversationID, nil
}

func linkConversationRequestLog(tx *gorm.DB, requestLogID, conversationID uint) error {
	result := tx.Model(&model.ChannelRequestLog{}).
		Where("id = ? AND conversation_id = 0", requestLogID).
		Update("conversation_id", conversationID)
	if result.Error != nil || result.RowsAffected > 0 {
		return result.Error
	}
	var count int64
	if err := tx.Model(&model.ChannelRequestLog{}).
		Where("id = ? AND conversation_id = ?", requestLogID, conversationID).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("request log %d was not linked", requestLogID)
	}
	return nil
}

func linkConversationCall(tx *gorm.DB, callID string, conversationID uint) error {
	result := tx.Model(&model.APICall{}).
		Where("id = ? AND conversation_id = 0", callID).
		Update("conversation_id", conversationID)
	if result.Error != nil || result.RowsAffected > 0 {
		return result.Error
	}
	var count int64
	if err := tx.Model(&model.APICall{}).
		Where("id = ? AND conversation_id = ?", callID, conversationID).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("API call %s was not linked", callID)
	}
	return nil
}

type ConversationProvenance struct {
	KeyID     uint
	Transport model.UpstreamTransport
}

// findOrCreatePlaygroundConversationTx reuses a recent matching conversation
// while keeping creation and turn persistence in one transaction.
func findOrCreatePlaygroundConversationTx(tx *gorm.DB, userID, tokenID uint, modelCode string, messages []chat.ChatMessage) (*model.Conversation, error) {
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
		err := tx.Where("token_id = ? AND title = ? AND status = 1 AND updated_at > ?", tokenID, title, since).
			Order("updated_at DESC").First(&conv).Error
		if err == nil && len(messages) > conv.MessageCount {
			if err := tx.Model(&conv).Update("updated_at", time.Now()).Error; err != nil {
				return nil, err
			}
			return &conv, nil
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}
	conv := &model.Conversation{
		UserID: userID, TokenID: tokenID, Title: title, Model: modelCode,
		SystemPrompt: systemPrompt, LastStatus: "pending", Status: 1,
	}
	if err := tx.Create(conv).Error; err != nil {
		return nil, err
	}
	return conv, nil
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

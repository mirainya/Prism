package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/gateway/canonical"
	openai_chat "github.com/mirainya/Prism/internal/gateway/codec/openai_chat"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider/chat"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrConversationNotFound         = errors.New("conversation not found")
	ErrConversationHistoryLoad      = errors.New("conversation history load failed")
	ErrInvalidConversationTurnState = errors.New("invalid conversation turn status")
)

// ConversationContext 会话上下文:playground 请求前加载,用于拼历史 + 火山 B 模式。
type ConversationContext struct {
	Conv               *model.Conversation // nil 表示新会话
	History            []chat.ChatMessage  // 已含 system prompt 的历史消息
	PreviousResponseID string              // 火山 B 模式:非空则只发新消息
	ProviderKeyID      uint
	UpstreamTransport  model.UpstreamTransport
}

// LoadConversationContextStrict returns explicit ownership and history errors.
// An empty ID means the caller is starting a new conversation.
func LoadConversationContextStrict(conversationID string, tokenID uint, targetModel string) (*ConversationContext, error) {
	cc := &ConversationContext{}
	if conversationID == "" {
		return cc, nil
	}
	var conv model.Conversation
	if err := model.DB().Where("id = ? AND token_id = ? AND status = 1", conversationID, tokenID).
		First(&conv).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: %s", ErrConversationNotFound, conversationID)
		}
		return nil, fmt.Errorf("load conversation %s: %w", conversationID, err)
	}
	activeAt := time.Now()
	touched := model.DB().Model(&model.Conversation{}).
		Where("id = ? AND token_id = ? AND status = 1", conv.ID, tokenID).
		UpdateColumn("updated_at", activeAt)
	if touched.Error != nil {
		return nil, fmt.Errorf("mark conversation %s active: %w", conversationID, touched.Error)
	}
	if touched.RowsAffected != 1 {
		return nil, fmt.Errorf("%w: %s", ErrConversationNotFound, conversationID)
	}
	conv.UpdatedAt = activeAt
	cc.Conv = &conv

	history, err := loadConversationHistory(conv.ID, targetModel)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrConversationHistoryLoad, err)
	}
	if conv.SystemPrompt != "" {
		if len(history) == 0 || history[0].Role != model.RoleSystem || history[0].ContentText() != conv.SystemPrompt {
			history = append([]chat.ChatMessage{{Role: model.RoleSystem, Content: conv.SystemPrompt}}, history...)
		}
	}
	cc.History = history
	// 有状态对话(B模式):有有效 response_id 时只发新消息,历史由上游维护
	if conv.ProviderResponseID != "" {
		cc.PreviousResponseID = conv.ProviderResponseID
		cc.ProviderKeyID = conv.ProviderKeyID
		cc.UpstreamTransport = conv.UpstreamTransport
	}
	return cc, nil
}

func loadConversationHistory(conversationID uint, targetModel string) ([]chat.ChatMessage, error) {
	var turnCount int64
	if err := model.DB().Model(&model.ConversationTurn{}).
		Where("conversation_id = ?", conversationID).Count(&turnCount).Error; err != nil {
		return nil, err
	}
	legacy, err := loadLegacyConversationMessages(conversationID, targetModel, turnCount > 0)
	if err != nil {
		return nil, err
	}
	if turnCount == 0 {
		return legacy, nil
	}
	canonicalMessages, err := loadCanonicalConversationMessages(conversationID)
	if err != nil {
		return nil, err
	}
	return append(legacy, canonicalMessages...), nil
}

// loadLegacyConversationMessages is used only for conversations written before
// canonical turns were introduced.
func loadLegacyConversationMessages(conversationID uint, targetModel string, excludeTurnCalls bool) ([]chat.ChatMessage, error) {
	var messages []model.Message
	query := model.DB().Where("conversation_id = ?", conversationID)
	if excludeTurnCalls {
		query = query.Where("call_id = '' OR call_id NOT IN (?)",
			model.DB().Model(&model.ConversationTurn{}).Select("call_id").Where("conversation_id = ?", conversationID))
	}
	if err := query.
		Order("created_at ASC, id ASC").Find(&messages).Error; err != nil {
		return nil, err
	}

	_ = targetModel
	result := make([]chat.ChatMessage, 0, len(messages))
	for _, msg := range messages {
		cm := chat.ChatMessage{
			Role:             msg.Role,
			Content:          restoreContent(msg.Content, msg.Attachments),
			ReasoningContent: msg.ReasoningContent,
		}
		result = append(result, cm)
	}
	return result, nil
}

const (
	conversationExtraContentMode  = "openai_chat.content_mode"
	conversationExtraReasoning    = "openai_chat.reasoning_content"
	conversationExtraMessageName  = "openai_chat.message_name"
	conversationExtraRefusal      = "openai_chat.refusal"
	conversationExtraAnnotations  = "openai_chat.annotations"
	conversationExtraAudio        = "openai_chat.audio"
	conversationExtraRaw          = "openai_chat.raw"
	conversationExtraRawArguments = "prism.conversation.raw_arguments"
)

func loadCanonicalConversationMessages(conversationID uint) ([]chat.ChatMessage, error) {
	var records []model.ConversationItem
	err := model.DB().Model(&model.ConversationItem{}).
		Select("conversation_items.*").
		Joins("JOIN conversation_turns ON conversation_turns.id = conversation_items.turn_id").
		Where("conversation_items.conversation_id = ? AND conversation_turns.status = ?", conversationID, model.ConversationTurnCompleted).
		Order("conversation_items.turn_sequence ASC, conversation_items.ordinal ASC, conversation_items.id ASC").
		Find(&records).Error
	if err != nil {
		return nil, err
	}
	items := make([]canonical.Item, 0, len(records))
	for _, record := range records {
		var item canonical.Item
		if err := json.Unmarshal(record.CanonicalJSON, &item); err != nil {
			return nil, fmt.Errorf("decode conversation item %d: %w", record.ID, err)
		}
		items = append(items, item)
	}
	return canonicalItemsToChatMessages(items)
}

func chatMessagesToCanonical(messages []chat.ChatMessage) ([]canonical.Item, error) {
	request, err := openai_chat.DecodeRequest(chat.ChatRequest{Messages: messages})
	if err != nil {
		return nil, err
	}
	return request.Items, nil
}

func canonicalItemsToChatMessages(items []canonical.Item) ([]chat.ChatMessage, error) {
	messages := make([]chat.ChatMessage, 0, len(items))
	for _, item := range items {
		switch item.Type {
		case "message":
			content, err := canonicalContentToChat(item.Content, canonicalExtraString(item.Extra, conversationExtraContentMode))
			if err != nil {
				return nil, err
			}
			message := chat.ChatMessage{Role: string(item.Role), Content: content}
			message.ReasoningContent = canonicalExtraString(item.Extra, conversationExtraReasoning)
			message.Name = canonicalExtraString(item.Extra, conversationExtraMessageName)
			if raw := item.Extra[conversationExtraRefusal]; len(raw) > 0 {
				var value string
				if err := json.Unmarshal(raw, &value); err != nil {
					return nil, err
				}
				message.Refusal = &value
			}
			message.Annotations = cloneConversationRaw(item.Extra[conversationExtraAnnotations])
			message.Audio = cloneConversationRaw(item.Extra[conversationExtraAudio])
			messages = append(messages, message)
		case "function_call":
			index := len(messages) - 1
			if index < 0 || messages[index].Role != model.RoleAssistant {
				messages = append(messages, chat.ChatMessage{Role: model.RoleAssistant, Content: nil})
				index = len(messages) - 1
			}
			arguments := string(item.Arguments)
			if raw := item.Extra[conversationExtraRawArguments]; len(raw) > 0 {
				if err := json.Unmarshal(raw, &arguments); err != nil {
					return nil, err
				}
			}
			callID := item.CallID
			if callID == "" {
				callID = item.ID
			}
			messages[index].ToolCalls = append(messages[index].ToolCalls, chat.ToolCall{
				ID: callID, Type: "function",
				Function: chat.FunctionCall{Name: item.Name, Arguments: arguments},
			})
		case "function_call_output":
			var output any
			if len(item.Output) > 0 {
				if err := json.Unmarshal(item.Output, &output); err != nil {
					return nil, err
				}
			}
			messages = append(messages, chat.ChatMessage{
				Role: "tool", ToolCallID: item.CallID, Content: output,
			})
		case "reasoning":
			if len(messages) == 0 || messages[len(messages)-1].Role != model.RoleAssistant {
				messages = append(messages, chat.ChatMessage{Role: model.RoleAssistant})
			}
			var reasoning strings.Builder
			for _, part := range item.Content {
				reasoning.WriteString(part.Text)
			}
			messages[len(messages)-1].ReasoningContent += reasoning.String()
		}
	}
	return messages, nil
}

func canonicalContentToChat(parts []canonical.Content, mode string) (any, error) {
	if mode == "null" {
		return nil, nil
	}
	if mode == "string" || (mode == "" && canonicalContentIsText(parts)) {
		var text strings.Builder
		for _, part := range parts {
			text.WriteString(part.Text)
		}
		return text.String(), nil
	}
	result := make([]any, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "input_text", "output_text", "text":
			result = append(result, map[string]any{"type": "text", "text": part.Text})
		case "input_image", "image_url":
			result = append(result, map[string]any{"type": "image_url", "image_url": compactConversationMap(map[string]any{"url": part.URL, "detail": part.Detail})})
		case "input_file", "file":
			if part.URL != "" {
				result = append(result, map[string]any{"type": "file_url", "file_url": compactConversationMap(map[string]any{"url": part.URL, "filename": part.Filename, "content_type": part.MediaType})})
			} else {
				result = append(result, map[string]any{"type": "file", "file": compactConversationMap(map[string]any{"file_id": part.FileID, "file_data": part.Data, "filename": part.Filename, "content_type": part.MediaType})})
			}
		case "input_audio", "audio":
			result = append(result, map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": part.Data, "format": part.Format}})
		default:
			raw := part.Extra[conversationExtraRaw]
			if len(raw) == 0 {
				return nil, fmt.Errorf("canonical content type %q has no preserved Chat representation", part.Type)
			}
			var value any
			if err := json.Unmarshal(raw, &value); err != nil {
				return nil, err
			}
			result = append(result, value)
		}
	}
	return result, nil
}

func canonicalContentIsText(parts []canonical.Content) bool {
	for _, part := range parts {
		if part.Type != "" && part.Type != "input_text" && part.Type != "output_text" && part.Type != "text" {
			return false
		}
	}
	return true
}

func canonicalExtraString(extra map[string]json.RawMessage, key string) string {
	if len(extra[key]) == 0 {
		return ""
	}
	var value string
	_ = json.Unmarshal(extra[key], &value)
	return value
}

func compactConversationMap(values map[string]any) map[string]any {
	result := make(map[string]any, len(values))
	for key, value := range values {
		if value != nil && value != "" {
			result[key] = value
		}
	}
	return result
}

func cloneConversationRaw(value json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), value...)
}

// modelUsesAnthropic 判断某模型是否走 anthropic 协议(据 gw 路由表:任一可用渠道为 anthropic 即算)。
// anthropic 需在历史里保留 reasoning_content。chat 已全切网关,故从 gw_channels.protocol 取,
// 不再读老 models.provider。
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
	_ = usage
	var source ConversationProvenance
	if len(provenance) > 0 {
		source = provenance[0]
	}
	assistantCopy := assistant
	return RecordConversationTurn(cc, ConversationTurnRecord{
		UserID: userID, TokenID: tokenID, Model: modelCode,
		NewMessages: newMessages, Assistant: &assistantCopy,
		Status: model.ConversationTurnCompleted, FinishReason: finishReason,
		ProviderResponseID: providerResponseID, CallID: callID,
		RequestLogID: reqLogID, Provenance: source, WriteLegacyMessages: true,
	})
}

// ConversationTurnRecord is the service contract for completed, failed, and
// aborted turns. APICall remains authoritative for usage, cost, latency, and
// sanitized execution errors.
type ConversationTurnRecord struct {
	UserID              uint
	TokenID             uint
	Model               string
	NewMessages         []chat.ChatMessage
	Assistant           *chat.ChatMessage
	InputItems          []canonical.Item
	OutputItems         []canonical.Item
	ConversationID      uint
	PreviousResponseID  string
	InputPrepared       bool
	MatchCanonicalInput bool
	Status              model.ConversationTurnStatus
	FinishReason        string
	ProviderResponseID  string
	CallID              string
	RequestLogID        uint
	ErrorType           string
	ErrorCode           string
	ErrorMessage        string
	Provenance          ConversationProvenance
	WriteLegacyMessages bool
}

// RecordConversationTurn can be called by stream handlers after success,
// failure, or client abort. Failed and aborted turns are retained for audit but
// excluded from future model context.
func RecordConversationTurn(cc *ConversationContext, record ConversationTurnRecord) (uint, error) {
	if err := validateConversationTurnRecord(&record); err != nil {
		return 0, err
	}
	if conversationID, found, err := findConversationTurnByCallID(record.CallID, record.UserID, record.TokenID); err != nil {
		return 0, err
	} else if found {
		return conversationID, nil
	}

	var conversationID uint
	err := retryConversationWrite(func() error {
		return model.DB().Transaction(func(tx *gorm.DB) error {
			var call model.APICall
			if err := tx.First(&call, "id = ?", record.CallID).Error; err != nil {
				return fmt.Errorf("load API call %s: %w", record.CallID, err)
			}
			if call.UserID != record.UserID || call.TokenID != record.TokenID {
				return errors.New("conversation turn API call ownership mismatch")
			}
			expectedStatus, statusErr := conversationTurnStatusForAPICall(&call)
			if statusErr != nil {
				return statusErr
			}
			if record.Status != expectedStatus {
				return fmt.Errorf("%w: API call %s requires %s conversation status", ErrInvalidConversationTurnState, call.ID, expectedStatus)
			}
			if err := hydrateConversationTurnRecordTx(tx, &record, &call); err != nil {
				return err
			}

			var (
				conv       *model.Conversation
				messages   []chat.ChatMessage
				inputItems []canonical.Item
				err        error
			)
			if recordUsesCanonicalItems(&record) {
				conv, inputItems, err = resolveCanonicalConversationForTurnTx(tx, &record, &call)
			} else {
				conv, messages, err = resolveConversationForTurnTx(tx, cc, &record)
			}
			if err != nil {
				return err
			}
			conversationID = conv.ID

			sequence, err := allocateConversationTurnSequenceTx(tx, conv.ID)
			if err != nil {
				return err
			}
			turn := model.ConversationTurn{
				ConversationID: conv.ID, Sequence: sequence, CallID: record.CallID,
				RequestLogID: record.RequestLogID, Model: record.Model,
				ProviderResponseID: record.ProviderResponseID, Status: record.Status,
				InputTokens: call.InputTokens, OutputTokens: call.OutputTokens,
				TotalTokens: call.TotalTokens, Cost: call.FinalCost, LatencyMs: call.DurationMs,
				FinishReason: record.FinishReason,
				ErrorType:    firstNonEmpty(call.ErrorType, record.ErrorType),
				ErrorCode:    firstNonEmpty(call.ErrorCode, record.ErrorCode),
				ErrorMessage: SanitizeAPICallErrorMessage(firstNonEmpty(call.ErrorMessage, record.ErrorMessage)),
			}
			if err := tx.Create(&turn).Error; err != nil {
				return err
			}

			var canonicalBytes uint64
			if recordUsesCanonicalItems(&record) {
				canonicalBytes, err = createCanonicalConversationItemsTx(tx, &turn, inputItems, record.OutputItems)
				if err != nil {
					return err
				}
			} else {
				if err := createConversationItemsTx(tx, &turn, messages, record.Assistant); err != nil {
					return err
				}
			}

			legacyCount := 0
			if record.WriteLegacyMessages && record.Status == model.ConversationTurnCompleted {
				legacyCount, err = createLegacyConversationMessagesTx(tx, conv.ID, &record, messages, &call)
				if err != nil {
					return err
				}
			}
			messageCount := legacyCount
			if recordUsesCanonicalItems(&record) {
				messageCount = countCanonicalConversationMessages(inputItems) + countCanonicalConversationMessages(record.OutputItems)
			}

			updates := map[string]any{
				"total_tokens":  gorm.Expr("total_tokens + ?", call.TotalTokens),
				"message_count": gorm.Expr("message_count + ?", messageCount),
				"model":         record.Model,
				"last_status":   string(record.Status),
				"call_id":       record.CallID,
			}
			if recordUsesCanonicalItems(&record) && record.Status == model.ConversationTurnCompleted {
				updates["canonical_bytes"] = gorm.Expr("canonical_bytes + ?", canonicalBytes)
				stateKnown := conv.CanonicalStateVersion == 1
				if stateKnown {
					combined := append(canonical.CloneItems(inputItems), canonical.CloneItems(record.OutputItems)...)
					fingerprints, _, valid := canonicalConversationMatchFingerprints(combined)
					hashes := canonicalConversationRollingHashes(conv.CanonicalMatchHash, fingerprints)
					if valid && len(hashes) > 0 {
						updates["canonical_item_count"] = conv.CanonicalItemCount + uint64(len(fingerprints))
						updates["canonical_match_hash"] = hashes[len(hashes)-1]
						updates["canonical_state_version"] = 1
					}
				}
			} else if !recordUsesCanonicalItems(&record) && record.Status == model.ConversationTurnCompleted {
				updates["canonical_item_count"] = 0
				updates["canonical_bytes"] = 0
				updates["canonical_match_hash"] = ""
				updates["canonical_state_version"] = 0
			}
			if record.RequestLogID > 0 {
				updates["last_request_log_id"] = record.RequestLogID
			}
			if record.Status == model.ConversationTurnCompleted {
				if record.ProviderResponseID != "" && record.ProviderResponseID != conv.ProviderResponseID {
					updates["provider_response_id"] = record.ProviderResponseID
				}
				if record.Provenance.KeyID != 0 || record.Provenance.Transport != "" {
					updates["provider_key_id"] = record.Provenance.KeyID
					updates["upstream_transport"] = record.Provenance.Transport
				}
			}
			result := tx.Model(&model.Conversation{}).Where("id = ?", conv.ID).Updates(updates)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				return errors.New("conversation was not updated")
			}
			if record.RequestLogID > 0 {
				if err := linkConversationRequestLog(tx, record.RequestLogID, conv.ID, record.CallID, call.FinalAttemptID); err != nil {
					return err
				}
			}
			if err := linkConversationCall(tx, record.CallID, conv.ID); err != nil {
				return err
			}
			return linkRelatedConversationCalls(tx, &call, conv.ID)
		})
	})
	if err != nil {
		if existingID, found, lookupErr := findConversationTurnByCallID(record.CallID, record.UserID, record.TokenID); lookupErr == nil && found {
			return existingID, nil
		}
		return 0, err
	}
	return conversationID, nil
}

func findConversationTurnByCallID(callID string, userID, tokenID uint) (uint, bool, error) {
	var row struct {
		ConversationID uint `gorm:"column:conversation_id"`
	}
	err := model.DB().Table("conversation_turns AS turn").
		Select("turn.conversation_id").
		Joins("JOIN conversations AS conversation ON conversation.id = turn.conversation_id").
		Where("turn.call_id = ? AND conversation.user_id = ? AND conversation.token_id = ?", callID, userID, tokenID).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return row.ConversationID, true, nil
}

func validateConversationTurnRecord(record *ConversationTurnRecord) error {
	if record == nil || strings.TrimSpace(record.CallID) == "" {
		return errors.New("conversation turn call_id is required")
	}
	switch record.Status {
	case "":
		record.Status = model.ConversationTurnCompleted
	case model.ConversationTurnCompleted, model.ConversationTurnFailed, model.ConversationTurnAborted:
	default:
		return fmt.Errorf("%w: %s", ErrInvalidConversationTurnState, record.Status)
	}
	if record.Status == model.ConversationTurnCompleted && record.Assistant == nil && !recordUsesCanonicalItems(record) {
		return errors.New("completed conversation turn requires an assistant message")
	}
	if record.InputPrepared && record.ConversationID == 0 {
		return errors.New("prepared conversation input requires conversation_id")
	}
	return nil
}

func linkConversationRequestLog(tx *gorm.DB, requestLogID, conversationID uint, callID string, attemptID uint) error {
	query := tx.Model(&model.ChannelRequestLog{}).
		Where("id = ? AND call_id = ? AND conversation_id = 0", requestLogID, callID)
	if attemptID > 0 {
		query = query.Where("attempt_id = ?", attemptID)
	}
	result := query.
		Update("conversation_id", conversationID)
	if result.Error != nil || result.RowsAffected > 0 {
		return result.Error
	}
	var count int64
	verify := tx.Model(&model.ChannelRequestLog{}).
		Where("id = ? AND call_id = ? AND conversation_id = ?", requestLogID, callID, conversationID)
	if attemptID > 0 {
		verify = verify.Where("attempt_id = ?", attemptID)
	}
	if err := verify.Count(&count).Error; err != nil {
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

func linkRelatedConversationCalls(tx *gorm.DB, call *model.APICall, conversationID uint) error {
	if call == nil || call.ResourceType != "response" || strings.TrimSpace(call.ResourceID) == "" {
		return nil
	}
	return tx.Model(&model.APICall{}).
		Where("resource_type = ? AND resource_id = ? AND operation = ? AND conversation_id = 0", "response", call.ResourceID, "responses.replay").
		Update("conversation_id", conversationID).Error
}

type ConversationProvenance struct {
	KeyID     uint
	Transport model.UpstreamTransport
}

// RecordConversationTurnFailure is a convenience API for stream failure and
// client-abort paths. A partial assistant item may be supplied when available.
func RecordConversationTurnFailure(cc *ConversationContext, record ConversationTurnRecord) (uint, error) {
	if record.Status == "" {
		record.Status = model.ConversationTurnFailed
	}
	if record.Status != model.ConversationTurnFailed && record.Status != model.ConversationTurnAborted {
		return 0, fmt.Errorf("%w: failure API requires failed or aborted", ErrInvalidConversationTurnState)
	}
	record.WriteLegacyMessages = false
	return RecordConversationTurn(cc, record)
}

func resolveConversationForTurnTx(tx *gorm.DB, cc *ConversationContext, record *ConversationTurnRecord) (*model.Conversation, []chat.ChatMessage, error) {
	if cc != nil && cc.Conv != nil {
		var current model.Conversation
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND user_id = ? AND token_id = ? AND status = 1", cc.Conv.ID, record.UserID, record.TokenID).
			First(&current).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, nil, ErrConversationNotFound
			}
			return nil, nil, err
		}
		return &current, record.NewMessages, nil
	}
	conv, err := createPlaygroundConversationTx(tx, record.UserID, record.TokenID, record.Model, record.NewMessages)
	if err != nil {
		return nil, nil, err
	}
	return conv, lastUserMessage(record.NewMessages), nil
}

// createPlaygroundConversationTx always creates a fresh session. Callers must
// pass an existing ConversationContext to append to an earlier conversation.
func createPlaygroundConversationTx(tx *gorm.DB, userID, tokenID uint, modelCode string, messages []chat.ChatMessage) (*model.Conversation, error) {
	title, systemPrompt := "", ""
	for _, msg := range messages {
		if msg.Role == model.RoleSystem {
			systemPrompt = msg.ContentText()
		} else if msg.Role == model.RoleUser && title == "" {
			title = truncateString(msg.ContentText(), 50)
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

func allocateConversationTurnSequenceTx(tx *gorm.DB, conversationID uint) (uint64, error) {
	result := tx.Model(&model.Conversation{}).Where("id = ?", conversationID).
		UpdateColumn("turn_sequence", gorm.Expr("turn_sequence + 1"))
	if result.Error != nil {
		return 0, result.Error
	}
	if result.RowsAffected != 1 {
		return 0, ErrConversationNotFound
	}
	var current struct {
		Sequence uint64 `gorm:"column:turn_sequence"`
	}
	if err := tx.Model(&model.Conversation{}).Select("turn_sequence").Where("id = ?", conversationID).Scan(&current).Error; err != nil {
		return 0, err
	}
	if current.Sequence == 0 {
		return 0, errors.New("conversation turn sequence was not allocated")
	}
	return current.Sequence, nil
}

func createConversationItemsTx(tx *gorm.DB, turn *model.ConversationTurn, input []chat.ChatMessage, assistant *chat.ChatMessage) error {
	inputItems, err := chatMessagesToCanonical(input)
	if err != nil {
		return fmt.Errorf("canonicalize conversation input: %w", err)
	}
	outputItems := []canonical.Item(nil)
	if assistant != nil {
		outputItems, err = chatMessagesToCanonical([]chat.ChatMessage{*assistant})
		if err != nil {
			return fmt.Errorf("canonicalize conversation output: %w", err)
		}
	}
	records := make([]model.ConversationItem, 0, len(inputItems)+len(outputItems))
	appendItems := func(direction string, items []canonical.Item) error {
		for _, item := range items {
			encoded, err := marshalConversationCanonicalItem(item)
			if err != nil {
				return err
			}
			records = append(records, model.ConversationItem{
				ConversationID: turn.ConversationID, TurnID: turn.ID,
				TurnSequence: turn.Sequence, Direction: direction,
				Ordinal: len(records), CanonicalJSON: encoded,
			})
		}
		return nil
	}
	if err := appendItems(model.ConversationItemInput, inputItems); err != nil {
		return err
	}
	if err := appendItems(model.ConversationItemOutput, outputItems); err != nil {
		return err
	}
	if len(records) == 0 {
		return nil
	}
	return tx.Create(&records).Error
}

func marshalConversationCanonicalItem(item canonical.Item) ([]byte, error) {
	if len(item.Arguments) > 0 {
		if item.Extra == nil {
			item.Extra = make(map[string]json.RawMessage)
		} else {
			clone := make(map[string]json.RawMessage, len(item.Extra)+1)
			for key, value := range item.Extra {
				clone[key] = cloneConversationRaw(value)
			}
			item.Extra = clone
		}
		rawArguments, _ := json.Marshal(string(item.Arguments))
		item.Extra[conversationExtraRawArguments] = rawArguments
		if !json.Valid(item.Arguments) {
			item.Arguments = rawArguments
		}
	}
	encoded, err := json.Marshal(item)
	if err != nil {
		return nil, fmt.Errorf("encode canonical conversation item: %w", err)
	}
	return encoded, nil
}

func createLegacyConversationMessagesTx(tx *gorm.DB, conversationID uint, record *ConversationTurnRecord, messages []chat.ChatMessage, call *model.APICall) (int, error) {
	records := make([]model.Message, 0, len(messages)+1)
	for _, message := range messages {
		records = append(records, model.Message{
			ConversationID: conversationID, CallID: record.CallID, RequestLogID: record.RequestLogID,
			Role: message.Role, Content: message.ContentText(), Attachments: message.ContentAttachments(),
			Model: record.Model,
		})
	}
	if record.Assistant != nil {
		records = append(records, model.Message{
			ConversationID: conversationID, CallID: record.CallID, RequestLogID: record.RequestLogID,
			Role: model.RoleAssistant, Content: record.Assistant.ContentText(),
			Attachments: record.Assistant.ContentAttachments(), ReasoningContent: record.Assistant.ReasoningContent,
			FinishReason: record.FinishReason, InputTokens: call.InputTokens, OutputTokens: call.OutputTokens,
			Model: record.Model, LatencyMs: int(call.DurationMs), Cost: call.FinalCost,
		})
	}
	if len(records) == 0 {
		return 0, nil
	}
	if err := tx.Create(&records).Error; err != nil {
		return 0, err
	}
	return len(records), nil
}

func retryConversationWrite(write func() error) error {
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		if err = write(); err == nil {
			return nil
		}
		message := strings.ToLower(err.Error())
		if !strings.Contains(message, "database is locked") &&
			!strings.Contains(message, "database table is locked") &&
			!strings.Contains(message, "deadlock found") {
			return err
		}
		time.Sleep(time.Duration(attempt+1) * 10 * time.Millisecond)
	}
	return err
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
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

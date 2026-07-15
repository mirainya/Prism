package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider/chat"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

func TestSaveConversationTurnBindsRequestLog(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(
		&model.Conversation{},
		&model.Message{},
		&model.ConversationTurn{},
		&model.ConversationItem{},
		&model.ChannelRequestLog{},
		&model.APICall{},
	); err != nil {
		t.Fatalf("migrate conversation tables: %v", err)
	}

	requestLog := &model.ChannelRequestLog{CallID: "call_conversation", RequestType: model.RequestTypeChat}
	if err := db.Create(requestLog).Error; err != nil {
		t.Fatalf("create request log: %v", err)
	}
	call := &model.APICall{
		ID: "call_conversation", RequestID: "request-conversation", UserID: 1, TokenID: 2,
		Status: model.APICallStatusCompleted, InputTokens: 11, OutputTokens: 7, TotalTokens: 18,
		FinalCost: decimal.RequireFromString("0.125"), DurationMs: 321,
	}
	if err := db.Create(call).Error; err != nil {
		t.Fatalf("create API call: %v", err)
	}

	conversationID, err := SaveConversationTurn(
		&ConversationContext{},
		1,
		2,
		"test-model",
		[]chat.ChatMessage{{Role: model.RoleUser, Content: "hello"}},
		chat.ChatMessage{Role: model.RoleAssistant, Content: "world"},
		nil,
		"stop",
		"",
		call.ID,
		requestLog.ID,
	)
	if err != nil {
		t.Fatalf("SaveConversationTurn failed: %v", err)
	}
	if conversationID == 0 {
		t.Fatal("SaveConversationTurn returned no conversation ID")
	}

	var savedLog model.ChannelRequestLog
	if err := db.First(&savedLog, requestLog.ID).Error; err != nil {
		t.Fatalf("reload request log: %v", err)
	}
	if savedLog.ConversationID != conversationID {
		t.Fatalf("conversation_id = %d, want %d", savedLog.ConversationID, conversationID)
	}

	var conversation model.Conversation
	if err := db.First(&conversation, conversationID).Error; err != nil {
		t.Fatalf("reload conversation: %v", err)
	}
	if conversation.CallID != call.ID {
		t.Fatalf("conversation call_id = %q, want %q", conversation.CallID, call.ID)
	}
	var messages []model.Message
	if err := db.Where("conversation_id = ?", conversationID).Find(&messages).Error; err != nil {
		t.Fatalf("reload messages: %v", err)
	}
	if len(messages) != 2 || messages[0].CallID != call.ID || messages[1].CallID != call.ID {
		t.Fatalf("messages are not linked to call: %#v", messages)
	}
	if !messages[1].Cost.Equal(call.FinalCost) || messages[1].LatencyMs != int(call.DurationMs) ||
		messages[1].InputTokens != call.InputTokens || messages[1].OutputTokens != call.OutputTokens {
		t.Fatalf("legacy assistant metrics = %#v, call = %#v", messages[1], call)
	}
	var turn model.ConversationTurn
	if err := db.Where("conversation_id = ?", conversationID).First(&turn).Error; err != nil {
		t.Fatalf("reload conversation turn: %v", err)
	}
	if turn.Sequence != 1 || turn.CallID != call.ID || turn.TotalTokens != call.TotalTokens ||
		!turn.Cost.Equal(call.FinalCost) || turn.LatencyMs != call.DurationMs {
		t.Fatalf("conversation turn = %#v", turn)
	}
	if err := db.First(call, "id = ?", call.ID).Error; err != nil {
		t.Fatalf("reload API call: %v", err)
	}
	if call.ConversationID != conversationID {
		t.Fatalf("API call conversation_id = %d, want %d", call.ConversationID, conversationID)
	}
}

func TestSaveConversationTurnRollsBackWhenAssociationFails(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(
		&model.Conversation{},
		&model.Message{},
		&model.ConversationTurn{},
		&model.ConversationItem{},
		&model.ChannelRequestLog{},
		&model.APICall{},
	); err != nil {
		t.Fatal(err)
	}

	conversationID, err := SaveConversationTurn(
		&ConversationContext{}, 1, 2, "test-model",
		[]chat.ChatMessage{{Role: model.RoleUser, Content: "hello"}},
		chat.ChatMessage{Role: model.RoleAssistant, Content: "world"},
		nil, "stop", "", "missing-call", 999,
	)
	if err == nil || conversationID != 0 {
		t.Fatalf("save result: conversation=%d err=%v", conversationID, err)
	}
	var conversations, messages, turns, items int64
	if err := db.Model(&model.Conversation{}).Count(&conversations).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Message{}).Count(&messages).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.ConversationTurn{}).Count(&turns).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.ConversationItem{}).Count(&items).Error; err != nil {
		t.Fatal(err)
	}
	if conversations != 0 || messages != 0 || turns != 0 || items != 0 {
		t.Fatalf("partial conversation persisted: conversations=%d messages=%d turns=%d items=%d", conversations, messages, turns, items)
	}
}

func TestConversationCanonicalHistoryPreservesToolsMultimodalAndBranches(t *testing.T) {
	db := setupConversationDomainTestDB(t)
	callOne := createConversationTestCall(t, db, "call_canonical_1", 1, 2, decimal.RequireFromString("0.25"))
	refusal := "policy refusal"
	input := []chat.ChatMessage{{
		Role: model.RoleUser,
		Content: []any{
			map[string]any{"type": "text", "text": "inspect"},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.test/image.png", "detail": "high"}},
			map[string]any{"type": "file_url", "file_url": map[string]any{"url": "https://example.test/file.pdf", "content_type": "application/pdf"}},
		},
	}}
	assistant := chat.ChatMessage{
		Role: model.RoleAssistant, Content: nil, Name: "worker", ReasoningContent: "checking",
		Refusal: &refusal, Annotations: json.RawMessage(`[{"type":"citation"}]`), Audio: json.RawMessage(`{"id":"audio_1"}`),
		ToolCalls: []chat.ToolCall{{ID: "tool_1", Type: "function", Function: chat.FunctionCall{Name: "lookup", Arguments: `{"q":"x"}`}}},
	}
	conversationID, err := SaveConversationTurn(&ConversationContext{}, 1, 2, "model-a", input, assistant, nil, "tool_calls", "provider-1", callOne.ID, 0)
	if err != nil {
		t.Fatal(err)
	}

	contextOne, err := LoadConversationContextStrict(fmt.Sprint(conversationID), 2, "model-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(contextOne.History) != 2 || len(contextOne.History[1].ToolCalls) != 1 {
		t.Fatalf("canonical history = %#v", contextOne.History)
	}
	parts, ok := contextOne.History[0].Content.([]any)
	if !ok || len(parts) != 3 || contextOne.History[1].Name != "worker" || contextOne.History[1].Refusal == nil ||
		string(contextOne.History[1].Annotations) != `[{"type":"citation"}]` || string(contextOne.History[1].Audio) != `{"id":"audio_1"}` {
		t.Fatalf("canonical fields were not preserved: %#v", contextOne.History)
	}

	callTwo := createConversationTestCall(t, db, "call_canonical_2", 1, 2, decimal.RequireFromString("0.5"))
	toolResult := []chat.ChatMessage{{Role: "tool", ToolCallID: "tool_1", Content: map[string]any{"value": 42}}}
	finalAssistant := chat.ChatMessage{Role: model.RoleAssistant, Content: "done"}
	if _, err := SaveConversationTurn(contextOne, 1, 2, "model-a", toolResult, finalAssistant, nil, "stop", "provider-2", callTwo.ID, 0); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConversationContextStrict(fmt.Sprint(conversationID), 2, "model-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.History) != 4 || loaded.History[2].Role != "tool" || loaded.History[2].ToolCallID != "tool_1" || loaded.History[3].Content != "done" {
		t.Fatalf("tool branch history = %#v", loaded.History)
	}

	branchCall := createConversationTestCall(t, db, "call_canonical_branch", 1, 2, decimal.Zero)
	branchID, err := SaveConversationTurn(&ConversationContext{}, 1, 2, "model-a", input, chat.ChatMessage{Role: model.RoleAssistant, Content: "branch"}, nil, "stop", "", branchCall.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if branchID == conversationID {
		t.Fatal("a new conversation reused a title-matching session")
	}
}

func TestConversationFailureTurnIsRecordedButExcludedFromContext(t *testing.T) {
	db := setupConversationDomainTestDB(t)
	completedCall := createConversationTestCall(t, db, "call_failure_base", 1, 2, decimal.RequireFromString("0.1"))
	conversationID, err := SaveConversationTurn(&ConversationContext{}, 1, 2, "model-a",
		[]chat.ChatMessage{{Role: model.RoleUser, Content: "start"}}, chat.ChatMessage{Role: model.RoleAssistant, Content: "ok"},
		nil, "stop", "", completedCall.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	cc, err := LoadConversationContextStrict(fmt.Sprint(conversationID), 2, "model-a")
	if err != nil {
		t.Fatal(err)
	}
	failedCall := createConversationTestCall(t, db, "call_failure_abort", 1, 2, decimal.RequireFromString("0.2"))
	if err := db.Model(failedCall).Updates(map[string]any{"status": model.APICallStatusCancelled, "error_type": "cancelled", "error_code": "client_cancelled", "error_message": "client disconnected"}).Error; err != nil {
		t.Fatal(err)
	}
	partial := chat.ChatMessage{Role: model.RoleAssistant, Content: "partial"}
	if _, err := RecordConversationTurnFailure(cc, ConversationTurnRecord{
		UserID: 1, TokenID: 2, Model: "model-a", NewMessages: []chat.ChatMessage{{Role: model.RoleUser, Content: "abort me"}},
		Assistant: &partial, Status: model.ConversationTurnAborted, CallID: failedCall.ID,
	}); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConversationContextStrict(fmt.Sprint(conversationID), 2, "model-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.History) != 2 {
		t.Fatalf("failed turn leaked into context: %#v", loaded.History)
	}
	var turn model.ConversationTurn
	if err := db.First(&turn, "call_id = ?", failedCall.ID).Error; err != nil {
		t.Fatal(err)
	}
	if turn.Status != model.ConversationTurnAborted || turn.ErrorCode != "client_cancelled" || !turn.Cost.Equal(failedCall.FinalCost) {
		t.Fatalf("failed turn = %#v", turn)
	}
	list, err := NewConversationService().ListConversations(&ListConversationsRequest{UserID: 1})
	if err != nil || len(list.Items) != 1 || !list.Items[0].TotalCost.Equal(decimal.RequireFromString("0.3")) {
		t.Fatalf("turn cost aggregate = %#v, err = %v", list, err)
	}
	if _, err := LoadConversationContextStrict("999999", 2, "model-a"); !errors.Is(err, ErrConversationNotFound) {
		t.Fatalf("missing conversation error = %v", err)
	}
}

func TestConversationHistoryMergesLegacyAndCanonicalWithoutDuplicateWrites(t *testing.T) {
	db := setupConversationDomainTestDB(t)
	conv := &model.Conversation{UserID: 1, TokenID: 2, Title: "legacy", Model: "model-a", Status: 1}
	if err := db.Create(conv).Error; err != nil {
		t.Fatal(err)
	}
	legacy := []model.Message{
		{ConversationID: conv.ID, Role: model.RoleUser, Content: "old user"},
		{ConversationID: conv.ID, Role: model.RoleAssistant, Content: "old assistant"},
	}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	call := createConversationTestCall(t, db, "call_mixed_history", 1, 2, decimal.Zero)
	if _, err := SaveConversationTurn(&ConversationContext{Conv: conv}, 1, 2, "model-a",
		[]chat.ChatMessage{{Role: model.RoleUser, Content: "new user"}}, chat.ChatMessage{Role: model.RoleAssistant, Content: "new assistant"},
		nil, "stop", "", call.ID, 0); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConversationContextStrict(fmt.Sprint(conv.ID), 2, "model-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.History) != 4 || loaded.History[0].Content != "old user" || loaded.History[1].Content != "old assistant" ||
		loaded.History[2].Content != "new user" || loaded.History[3].Content != "new assistant" {
		t.Fatalf("mixed history = %#v", loaded.History)
	}
}

func TestLoadConversationContextMarksConversationActive(t *testing.T) {
	db := setupConversationDomainTestDB(t)
	conversation := &model.Conversation{UserID: 1, TokenID: 2, Title: "old", Model: "model-a", Status: 1}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatal(err)
	}
	old := time.Now().AddDate(0, 0, -100)
	if err := db.Model(&model.Conversation{}).Where("id = ?", conversation.ID).UpdateColumn("updated_at", old).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConversationContextStrict(fmt.Sprint(conversation.ID), conversation.TokenID, conversation.Model); err != nil {
		t.Fatal(err)
	}
	var current model.Conversation
	if err := db.First(&current, conversation.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !current.UpdatedAt.After(old) {
		t.Fatalf("updated_at = %v, want after %v", current.UpdatedAt, old)
	}
}

func TestConversationTurnSequenceAndCallIdempotency(t *testing.T) {
	db := setupConversationDomainTestDB(t)
	baseCall := createConversationTestCall(t, db, "call_sequence_base", 1, 2, decimal.Zero)
	conversationID, err := SaveConversationTurn(&ConversationContext{}, 1, 2, "model-a",
		[]chat.ChatMessage{{Role: model.RoleUser, Content: "base"}}, chat.ChatMessage{Role: model.RoleAssistant, Content: "ok"}, nil, "stop", "", baseCall.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	cc, err := LoadConversationContextStrict(fmt.Sprint(conversationID), 2, "model-a")
	if err != nil {
		t.Fatal(err)
	}

	const writers = 4
	calls := make([]*model.APICall, writers)
	for index := 0; index < writers; index++ {
		calls[index] = createConversationTestCall(t, db, fmt.Sprintf("call_sequence_%d", index), 1, 2, decimal.Zero)
	}
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for index := 0; index < writers; index++ {
		wg.Add(1)
		go func(index int, callID string) {
			defer wg.Done()
			_, saveErr := RecordConversationTurn(cc, ConversationTurnRecord{
				UserID: 1, TokenID: 2, Model: "model-a", CallID: callID,
				NewMessages: []chat.ChatMessage{{Role: model.RoleUser, Content: fmt.Sprintf("input-%d", index)}},
				Assistant:   &chat.ChatMessage{Role: model.RoleAssistant, Content: "ok"}, Status: model.ConversationTurnCompleted,
			})
			errs <- saveErr
		}(index, calls[index].ID)
	}
	wg.Wait()
	close(errs)
	for saveErr := range errs {
		if saveErr != nil {
			t.Fatal(saveErr)
		}
	}
	var turns []model.ConversationTurn
	if err := db.Where("conversation_id = ?", conversationID).Order("turn_sequence ASC").Find(&turns).Error; err != nil {
		t.Fatal(err)
	}
	if len(turns) != writers+1 {
		t.Fatalf("turn count = %d", len(turns))
	}
	for index, turn := range turns {
		if turn.Sequence != uint64(index+1) {
			t.Fatalf("turn sequences = %#v", turns)
		}
	}
	duplicate := turns[len(turns)-1]
	returnedID, err := RecordConversationTurn(cc, ConversationTurnRecord{
		UserID: 1, TokenID: 2, Model: "model-a", CallID: duplicate.CallID,
		NewMessages: []chat.ChatMessage{{Role: model.RoleUser, Content: "duplicate"}},
		Assistant:   &chat.ChatMessage{Role: model.RoleAssistant, Content: "duplicate"}, Status: model.ConversationTurnCompleted,
	})
	if err != nil || returnedID != conversationID {
		t.Fatalf("idempotent result = %d, %v", returnedID, err)
	}
	var count int64
	if err := db.Model(&model.ConversationTurn{}).Where("call_id = ?", duplicate.CallID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("duplicate turn count = %d, err = %v", count, err)
	}
}

func setupConversationDomainTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupTestDB(t)
	if err := db.AutoMigrate(
		&model.Conversation{}, &model.Message{}, &model.ConversationTurn{}, &model.ConversationItem{},
		&model.ChannelRequestLog{}, &model.APICall{}, &model.AIResponse{},
	); err != nil {
		t.Fatal(err)
	}
	return db
}

func createConversationTestCall(t *testing.T, db *gorm.DB, id string, userID, tokenID uint, cost decimal.Decimal) *model.APICall {
	t.Helper()
	call := &model.APICall{
		ID: id, RequestID: id, UserID: userID, TokenID: tokenID, Status: model.APICallStatusCompleted,
		ProjectConversation: true,
		InputTokens:         3, OutputTokens: 2, TotalTokens: 5, FinalCost: cost, DurationMs: 25,
	}
	if err := db.Create(call).Error; err != nil {
		t.Fatal(err)
	}
	return call
}

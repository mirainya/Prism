package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider/chat"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

func TestProjectAPIConversationPreservesCanonicalMultimodalToolsAndLinksLedger(t *testing.T) {
	db := setupConversationDomainTestDB(t)
	call := createConversationTestCall(t, db, "call_projection_canonical", 11, 22, decimal.RequireFromString("0.125"))
	requestLog := &model.ChannelRequestLog{CallID: call.ID, RequestType: model.RequestTypeChat}
	if err := db.Create(requestLog).Error; err != nil {
		t.Fatal(err)
	}
	input := []canonical.Item{
		canonicalMessage(canonical.RoleSystem, "rules", "input_text"),
		{Type: "message", Role: canonical.RoleUser, Content: []canonical.Content{
			{Type: "input_text", Text: "inspect"},
			{Type: "input_image", URL: "https://example.test/image.png", Detail: "high"},
			{Type: "input_file", FileID: "file_1", Filename: "report.pdf", MediaType: "application/pdf"},
		}},
		{Type: "function_call_output", CallID: "tool_previous", Output: json.RawMessage(`{"ok":true}`)},
	}
	output := []canonical.Item{
		{ID: "reasoning_1", Type: "reasoning", Role: canonical.RoleAssistant, Content: []canonical.Content{{Type: "reasoning_text", Text: "checking"}}},
		canonicalMessage(canonical.RoleAssistant, "working", "output_text"),
		{ID: "tool_current", Type: "function_call", Role: canonical.RoleAssistant, CallID: "tool_current", Name: "lookup", Arguments: json.RawMessage(`{"q":"x"}`)},
	}

	conversationID, err := ProjectAPIConversation(ConversationProjectionRequest{
		UserID: 11, TokenID: 22, Model: "model-a", CallID: call.ID,
		InputItems: input, OutputItems: output, Status: model.ConversationTurnCompleted,
		ProviderResponseID: "provider-response", FinishReason: "tool_calls",
	})
	if err != nil {
		t.Fatal(err)
	}
	var conversation model.Conversation
	if err := db.First(&conversation, conversationID).Error; err != nil {
		t.Fatal(err)
	}
	if conversation.Title != "inspect" || conversation.SystemPrompt != "rules" || conversation.CallID != call.ID || conversation.ProviderResponseID != "provider-response" || conversation.MessageCount != 4 {
		t.Fatalf("conversation = %#v", conversation)
	}
	var turn model.ConversationTurn
	if err := db.First(&turn, "call_id = ?", call.ID).Error; err != nil {
		t.Fatal(err)
	}
	if turn.RequestLogID != requestLog.ID || turn.TotalTokens != call.TotalTokens || !turn.Cost.Equal(call.FinalCost) || turn.FinishReason != "tool_calls" {
		t.Fatalf("turn = %#v", turn)
	}
	var items []model.ConversationItem
	if err := db.Where("turn_id = ?", turn.ID).Order("ordinal ASC").Find(&items).Error; err != nil {
		t.Fatal(err)
	}
	if len(items) != len(input)+len(output) || items[0].Direction != model.ConversationItemInput || items[len(items)-1].Direction != model.ConversationItemOutput {
		t.Fatalf("items = %#v", items)
	}
	var storedImage canonical.Item
	if err := json.Unmarshal(items[1].CanonicalJSON, &storedImage); err != nil {
		t.Fatal(err)
	}
	if len(storedImage.Content) != 3 || storedImage.Content[1].URL != "https://example.test/image.png" || storedImage.Content[2].FileID != "file_1" {
		t.Fatalf("stored multimodal item = %#v", storedImage)
	}
	var storedTool canonical.Item
	if err := json.Unmarshal(items[len(items)-1].CanonicalJSON, &storedTool); err != nil {
		t.Fatal(err)
	}
	if storedTool.CallID != "tool_current" || storedTool.Name != "lookup" || string(storedTool.Arguments) != `{"q":"x"}` {
		t.Fatalf("stored tool item = %#v", storedTool)
	}
	if err := db.First(call, "id = ?", call.ID).Error; err != nil {
		t.Fatal(err)
	}
	if call.ConversationID != conversationID {
		t.Fatalf("call conversation = %d, want %d", call.ConversationID, conversationID)
	}
	if err := db.First(requestLog, requestLog.ID).Error; err != nil {
		t.Fatal(err)
	}
	if requestLog.ConversationID != conversationID {
		t.Fatalf("request log conversation = %d, want %d", requestLog.ConversationID, conversationID)
	}
	context, err := LoadConversationContextStrict(fmt.Sprint(conversationID), 22, "model-a")
	if err != nil {
		t.Fatal(err)
	}
	systemMessages := 0
	for _, message := range context.History {
		if message.Role == model.RoleSystem {
			systemMessages++
		}
	}
	if systemMessages != 1 {
		t.Fatalf("canonical system prompt was duplicated: %#v", context.History)
	}
}

func TestProjectAPIConversationMatchesUniqueLongestCanonicalHistoryPrefix(t *testing.T) {
	db := setupConversationDomainTestDB(t)
	firstCall := createConversationTestCall(t, db, "call_projection_prefix_1", 1, 2, decimal.Zero)
	firstInput := []canonical.Item{
		canonicalMessage(canonical.RoleSystem, "rules", "input_text"),
		canonicalMessage(canonical.RoleUser, "hello", "input_text"),
	}
	firstOutput := []canonical.Item{canonicalMessage(canonical.RoleAssistant, "hi", "output_text")}
	conversationID, err := ProjectAPIConversation(ConversationProjectionRequest{
		UserID: 1, TokenID: 2, Model: "model-a", CallID: firstCall.ID,
		InputItems: firstInput, OutputItems: firstOutput, Status: model.ConversationTurnCompleted,
	})
	if err != nil {
		t.Fatal(err)
	}

	secondCall := createConversationTestCall(t, db, "call_projection_prefix_2", 1, 2, decimal.Zero)
	fullHistory := append(canonical.CloneItems(firstInput), canonicalMessage(canonical.RoleAssistant, "hi", "input_text"))
	fullHistory = append(fullHistory, canonicalMessage(canonical.RoleUser, "again", "input_text"))
	returnedID, err := ProjectAPIConversation(ConversationProjectionRequest{
		UserID: 1, TokenID: 2, Model: "model-b", CallID: secondCall.ID,
		InputItems:  fullHistory,
		OutputItems: []canonical.Item{canonicalMessage(canonical.RoleAssistant, "done", "output_text")},
		Status:      model.ConversationTurnCompleted,
	})
	if err != nil {
		t.Fatal(err)
	}
	if returnedID != conversationID {
		t.Fatalf("matched conversation = %d, want %d", returnedID, conversationID)
	}
	var secondTurn model.ConversationTurn
	if err := db.First(&secondTurn, "call_id = ?", secondCall.ID).Error; err != nil {
		t.Fatal(err)
	}
	var secondItems []model.ConversationItem
	if err := db.Where("turn_id = ?", secondTurn.ID).Order("ordinal ASC").Find(&secondItems).Error; err != nil {
		t.Fatal(err)
	}
	if len(secondItems) != 2 || secondItems[0].Direction != model.ConversationItemInput || secondItems[1].Direction != model.ConversationItemOutput {
		t.Fatalf("second turn items = %#v", secondItems)
	}
	var newInput canonical.Item
	if err := json.Unmarshal(secondItems[0].CanonicalJSON, &newInput); err != nil {
		t.Fatal(err)
	}
	if canonicalConversationItemText(newInput) != "again" {
		t.Fatalf("stored suffix = %#v", newInput)
	}
}

func TestProjectAPIConversationMatchesHistoryWithoutEchoedReasoningOrToolShell(t *testing.T) {
	db := setupConversationDomainTestDB(t)
	firstCall := createConversationTestCall(t, db, "call_projection_reasoning_base", 1, 2, decimal.Zero)
	firstInput := []canonical.Item{canonicalMessage(canonical.RoleUser, "lookup", "input_text")}
	firstOutput := []canonical.Item{
		{Type: "reasoning", Role: canonical.RoleAssistant, Content: []canonical.Content{{Type: "reasoning_text", Text: "checking"}}},
		{Type: "function_call", Role: canonical.RoleAssistant, CallID: "tool_1", Name: "lookup", Arguments: json.RawMessage(`{"q":"x"}`)},
	}
	conversationID, err := ProjectAPIConversation(ConversationProjectionRequest{
		UserID: 1, TokenID: 2, Model: "model-a", CallID: firstCall.ID,
		InputItems: firstInput, OutputItems: firstOutput,
	})
	if err != nil {
		t.Fatal(err)
	}

	secondCall := createConversationTestCall(t, db, "call_projection_reasoning_next", 1, 2, decimal.Zero)
	secondInput := []canonical.Item{
		canonicalMessage(canonical.RoleUser, "lookup", "input_text"),
		{Type: "message", Role: canonical.RoleAssistant},
		{Type: "function_call", Role: canonical.RoleAssistant, CallID: "tool_1", Name: "lookup", Arguments: json.RawMessage(`{"q":"x"}`)},
		{Type: "function_call_output", Role: canonical.RoleTool, CallID: "tool_1", Output: json.RawMessage(`{"value":42}`)},
	}
	returnedID, err := ProjectAPIConversation(ConversationProjectionRequest{
		UserID: 1, TokenID: 2, Model: "model-a", CallID: secondCall.ID,
		InputItems:  secondInput,
		OutputItems: []canonical.Item{canonicalMessage(canonical.RoleAssistant, "done", "output_text")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if returnedID != conversationID {
		t.Fatalf("reasoning/tool continuation conversation = %d, want %d", returnedID, conversationID)
	}
	var turn model.ConversationTurn
	if err := db.First(&turn, "call_id = ?", secondCall.ID).Error; err != nil {
		t.Fatal(err)
	}
	var items []model.ConversationItem
	if err := db.Where("turn_id = ?", turn.ID).Order("ordinal ASC").Find(&items).Error; err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("continuation stored %d items, want tool result and output: %#v", len(items), items)
	}
	var newInput canonical.Item
	if err := json.Unmarshal(items[0].CanonicalJSON, &newInput); err != nil {
		t.Fatal(err)
	}
	if newInput.Type != "function_call_output" || newInput.CallID != "tool_1" {
		t.Fatalf("trimmed continuation input = %#v", newInput)
	}
}

func TestCanonicalConversationMatchingKeepsSemanticEmptyAssistantItems(t *testing.T) {
	refusal := canonical.Item{
		Type: "message", Role: canonical.RoleAssistant,
		Extra: map[string]json.RawMessage{"openai_chat.refusal": json.RawMessage(`"blocked"`)},
	}
	if canonicalConversationMatchIgnorable([]canonical.Item{refusal}, 0) {
		t.Fatal("refusal-only assistant item was ignored")
	}

	prefix := []canonical.Item{
		canonicalMessage(canonical.RoleUser, "question", "input_text"),
		canonicalMessage(canonical.RoleAssistant, "answer", "output_text"),
		{Type: "reasoning", Role: canonical.RoleAssistant, Content: []canonical.Content{{Type: "reasoning_text", Text: "trailing"}}},
	}
	input := append(canonical.CloneItems(prefix), canonicalMessage(canonical.RoleUser, "next", "input_text"))
	consumed, matched, ok := canonicalConversationPrefixConsumed(prefix, input)
	if !ok || matched != 2 || consumed != 3 {
		t.Fatalf("trailing reasoning match = consumed:%d matched:%d ok:%v", consumed, matched, ok)
	}
}

func TestCanonicalConversationFingerprintPreservesLargeJSONIntegers(t *testing.T) {
	left, err := canonicalConversationFingerprint(canonical.Item{
		Type: "function_call", Role: canonical.RoleAssistant,
		Name: "lookup", Arguments: json.RawMessage(`{"id":9007199254740992}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	right, err := canonicalConversationFingerprint(canonical.Item{
		Type: "function_call", Role: canonical.RoleAssistant,
		Name: "lookup", Arguments: json.RawMessage(`{"id":9007199254740993}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(left, right) {
		t.Fatal("distinct integers above 2^53 produced the same conversation fingerprint")
	}
}

func TestCanonicalConversationStateRebuildsAfterLegacyTurn(t *testing.T) {
	db := setupConversationDomainTestDB(t)
	firstCall := createConversationTestCall(t, db, "call_projection_before_legacy", 1, 2, decimal.Zero)
	conversationID, err := ProjectAPIConversation(ConversationProjectionRequest{
		UserID: 1, TokenID: 2, Model: "model-a", CallID: firstCall.ID,
		InputItems:  []canonical.Item{canonicalMessage(canonical.RoleUser, "api first", "input_text")},
		OutputItems: []canonical.Item{canonicalMessage(canonical.RoleAssistant, "api answer", "output_text")},
	})
	if err != nil {
		t.Fatal(err)
	}
	var conversation model.Conversation
	if err := db.First(&conversation, conversationID).Error; err != nil {
		t.Fatal(err)
	}
	if conversation.CanonicalStateVersion != 1 || conversation.CanonicalItemCount != 2 {
		t.Fatalf("initial canonical state = %#v", conversation)
	}

	legacyCall := createConversationTestCall(t, db, "call_projection_legacy_middle", 1, 2, decimal.Zero)
	if _, err := SaveConversationTurn(
		&ConversationContext{Conv: &conversation}, 1, 2, "model-a",
		[]chat.ChatMessage{{Role: model.RoleUser, Content: "legacy next"}},
		chat.ChatMessage{Role: model.RoleAssistant, Content: "legacy answer"},
		nil, "stop", "", legacyCall.ID, 0,
	); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&conversation, conversationID).Error; err != nil {
		t.Fatal(err)
	}
	if conversation.CanonicalStateVersion != 0 || conversation.CanonicalItemCount != 0 || conversation.CanonicalMatchHash != "" {
		t.Fatalf("legacy turn did not invalidate canonical state: %#v", conversation)
	}

	nextCall := createConversationTestCall(t, db, "call_projection_after_legacy", 1, 2, decimal.Zero)
	returnedID, err := ProjectAPIConversation(ConversationProjectionRequest{
		UserID: 1, TokenID: 2, Model: "model-a", CallID: nextCall.ID, ConversationID: conversationID,
		InputItems: []canonical.Item{
			canonicalMessage(canonical.RoleUser, "api first", "input_text"),
			canonicalMessage(canonical.RoleAssistant, "api answer", "input_text"),
			canonicalMessage(canonical.RoleUser, "legacy next", "input_text"),
			canonicalMessage(canonical.RoleAssistant, "legacy answer", "input_text"),
			canonicalMessage(canonical.RoleUser, "api final", "input_text"),
		},
		OutputItems: []canonical.Item{canonicalMessage(canonical.RoleAssistant, "final answer", "output_text")},
	})
	if err != nil || returnedID != conversationID {
		t.Fatalf("continuation after legacy turn = %d, %v", returnedID, err)
	}
	var turn model.ConversationTurn
	if err := db.First(&turn, "call_id = ?", nextCall.ID).Error; err != nil {
		t.Fatal(err)
	}
	var itemCount int64
	if err := db.Model(&model.ConversationItem{}).Where("turn_id = ?", turn.ID).Count(&itemCount).Error; err != nil {
		t.Fatal(err)
	}
	if itemCount != 2 {
		t.Fatalf("continuation stored %d items, want only the new input and output", itemCount)
	}
}

func TestProjectAPIConversationUsesMatchStateWithoutScanningOversizedHistory(t *testing.T) {
	db := setupConversationDomainTestDB(t)
	baseCall := createConversationTestCall(t, db, "call_projection_oversized_base", 1, 2, decimal.Zero)
	baseID, err := ProjectAPIConversation(ConversationProjectionRequest{
		UserID: 1, TokenID: 2, Model: "model-a", CallID: baseCall.ID,
		InputItems:  []canonical.Item{canonicalMessage(canonical.RoleUser, "base", "input_text")},
		OutputItems: []canonical.Item{canonicalMessage(canonical.RoleAssistant, "answer", "output_text")},
	})
	if err != nil {
		t.Fatal(err)
	}
	oversized := json.RawMessage(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"` +
		strings.Repeat("x", 2<<20) + `"}]}`)
	if err := db.Model(&model.ConversationItem{}).
		Where("conversation_id = ? AND direction = ?", baseID, model.ConversationItemOutput).
		Update("canonical_json", oversized).Error; err != nil {
		t.Fatal(err)
	}

	nextCall := createConversationTestCall(t, db, "call_projection_oversized_next", 1, 2, decimal.Zero)
	nextID, err := ProjectAPIConversation(ConversationProjectionRequest{
		UserID: 1, TokenID: 2, Model: "model-a", CallID: nextCall.ID,
		InputItems: []canonical.Item{
			canonicalMessage(canonical.RoleUser, "base", "input_text"),
			canonicalMessage(canonical.RoleAssistant, "answer", "input_text"),
			canonicalMessage(canonical.RoleUser, "next", "input_text"),
		},
		OutputItems: []canonical.Item{canonicalMessage(canonical.RoleAssistant, "done", "output_text")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if nextID != baseID {
		t.Fatal("persisted match state did not reuse oversized conversation")
	}
}

func TestProjectAPIConversationCreatesNewConversationForAmbiguousPrefix(t *testing.T) {
	db := setupConversationDomainTestDB(t)
	input := []canonical.Item{canonicalMessage(canonical.RoleUser, "same", "input_text")}
	output := []canonical.Item{canonicalMessage(canonical.RoleAssistant, "same reply", "output_text")}
	firstID := projectConversationTestTurn(t, db, "call_projection_ambiguous_1", input, output)
	secondID := projectConversationTestTurn(t, db, "call_projection_ambiguous_2", input, output)
	if firstID == secondID {
		t.Fatal("identical initial requests should remain separate conversations")
	}
	continuation := append(canonical.CloneItems(input), canonicalMessage(canonical.RoleAssistant, "same reply", "input_text"))
	continuation = append(continuation, canonicalMessage(canonical.RoleUser, "continue", "input_text"))
	thirdID := projectConversationTestTurn(t, db, "call_projection_ambiguous_3", continuation,
		[]canonical.Item{canonicalMessage(canonical.RoleAssistant, "third", "output_text")})
	if thirdID == firstID || thirdID == secondID {
		t.Fatalf("ambiguous prefix reused conversation %d", thirdID)
	}
	var turn model.ConversationTurn
	if err := db.First(&turn, "call_id = ?", "call_projection_ambiguous_3").Error; err != nil {
		t.Fatal(err)
	}
	var inputCount int64
	if err := db.Model(&model.ConversationItem{}).Where("turn_id = ? AND direction = ?", turn.ID, model.ConversationItemInput).Count(&inputCount).Error; err != nil {
		t.Fatal(err)
	}
	if inputCount != int64(len(continuation)) {
		t.Fatalf("new conversation input count = %d, want %d", inputCount, len(continuation))
	}
}

func TestProjectAPIConversationRecordsFailureWithoutAddingItToMatchableHistory(t *testing.T) {
	db := setupConversationDomainTestDB(t)
	baseInput := []canonical.Item{canonicalMessage(canonical.RoleUser, "start", "input_text")}
	baseOutput := []canonical.Item{canonicalMessage(canonical.RoleAssistant, "ok", "output_text")}
	conversationID := projectConversationTestTurn(t, db, "call_projection_failure_base", baseInput, baseOutput)

	failedCall := createConversationTestCall(t, db, "call_projection_failure", 1, 2, decimal.RequireFromString("0.2"))
	if err := db.Model(failedCall).Updates(map[string]any{
		"status": model.APICallStatusFailed, "error_type": "upstream_error",
		"error_code": "bad_gateway", "error_message": "upstream failed",
	}).Error; err != nil {
		t.Fatal(err)
	}
	failedInput := append(canonical.CloneItems(baseInput), canonicalMessage(canonical.RoleAssistant, "ok", "input_text"))
	failedInput = append(failedInput, canonicalMessage(canonical.RoleUser, "fail", "input_text"))
	returnedID, err := ProjectAPIConversation(ConversationProjectionRequest{
		UserID: 1, TokenID: 2, Model: "model-a", CallID: failedCall.ID,
		InputItems: failedInput,
	})
	if err != nil {
		t.Fatal(err)
	}
	if returnedID != conversationID {
		t.Fatalf("failed turn conversation = %d, want %d", returnedID, conversationID)
	}
	var turn model.ConversationTurn
	if err := db.First(&turn, "call_id = ?", failedCall.ID).Error; err != nil {
		t.Fatal(err)
	}
	if turn.Status != model.ConversationTurnFailed || turn.ErrorType != "upstream_error" || turn.ErrorCode != "bad_gateway" || turn.ErrorMessage != "upstream failed" {
		t.Fatalf("failed turn = %#v", turn)
	}
	histories, err := loadCompletedCanonicalConversationItemsTx(db, []uint{conversationID})
	if err != nil {
		t.Fatal(err)
	}
	if len(histories[conversationID]) != len(baseInput)+len(baseOutput) {
		t.Fatalf("completed history includes failed turn: %#v", histories[conversationID])
	}
}

func TestProjectAPIConversationIsIdempotentByCallID(t *testing.T) {
	db := setupConversationDomainTestDB(t)
	call := createConversationTestCall(t, db, "call_projection_idempotent", 1, 2, decimal.Zero)
	request := ConversationProjectionRequest{
		UserID: 1, TokenID: 2, Model: "model-a", CallID: call.ID,
		InputItems:  []canonical.Item{canonicalMessage(canonical.RoleUser, "hello", "input_text")},
		OutputItems: []canonical.Item{canonicalMessage(canonical.RoleAssistant, "first", "output_text")},
		Status:      model.ConversationTurnCompleted,
	}
	firstID, err := ProjectAPIConversation(request)
	if err != nil {
		t.Fatal(err)
	}
	request.OutputItems = []canonical.Item{canonicalMessage(canonical.RoleAssistant, "duplicate", "output_text")}
	secondID, err := ProjectAPIConversation(request)
	if err != nil {
		t.Fatal(err)
	}
	if firstID != secondID {
		t.Fatalf("idempotent IDs = %d, %d", firstID, secondID)
	}
	var turns, items int64
	if err := db.Model(&model.ConversationTurn{}).Where("call_id = ?", call.ID).Count(&turns).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.ConversationItem{}).Where("conversation_id = ?", firstID).Count(&items).Error; err != nil {
		t.Fatal(err)
	}
	if turns != 1 || items != 2 {
		t.Fatalf("idempotent rows: turns=%d items=%d", turns, items)
	}
	var conversation model.Conversation
	if err := db.First(&conversation, firstID).Error; err != nil {
		t.Fatal(err)
	}
	if conversation.TotalTokens != call.TotalTokens {
		t.Fatalf("total tokens = %d, want %d", conversation.TotalTokens, call.TotalTokens)
	}
}

func TestProjectAPIConversationRecordsCompletedTurnWithoutOutput(t *testing.T) {
	db := setupConversationDomainTestDB(t)
	call := createConversationTestCall(t, db, "call_projection_empty_output", 1, 2, decimal.Zero)
	conversationID, err := ProjectAPIConversation(ConversationProjectionRequest{
		UserID: 1, TokenID: 2, Model: "model-a", CallID: call.ID,
		InputItems: []canonical.Item{canonicalMessage(canonical.RoleUser, "hello", "input_text")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if conversationID == 0 {
		t.Fatal("completed empty-output call was not projected")
	}
	var turn model.ConversationTurn
	if err := db.First(&turn, "call_id = ?", call.ID).Error; err != nil {
		t.Fatal(err)
	}
	if turn.Status != model.ConversationTurnCompleted {
		t.Fatalf("turn status = %s", turn.Status)
	}
}

func TestProjectAPIConversationKeepsOnlyPrimaryChoiceInTimeline(t *testing.T) {
	db := setupConversationDomainTestDB(t)
	call := createConversationTestCall(t, db, "call_projection_choices", 1, 2, decimal.Zero)
	choice := func(index int, text string) canonical.Item {
		return canonical.Item{
			Type: "message", Role: canonical.RoleAssistant,
			Content: []canonical.Content{{Type: "output_text", Text: text}},
			Extra:   map[string]json.RawMessage{"openai_chat.choice_index": json.RawMessage(fmt.Sprintf("%d", index))},
		}
	}
	conversationID, err := ProjectAPIConversation(ConversationProjectionRequest{
		UserID: 1, TokenID: 2, Model: "model-a", CallID: call.ID,
		InputItems: []canonical.Item{canonicalMessage(canonical.RoleUser, "hello", "input_text")},
		OutputItems: []canonical.Item{
			choice(0, "primary"), choice(1, "alternative"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var items []model.ConversationItem
	if err := db.Where("conversation_id = ? AND direction = ?", conversationID, model.ConversationItemOutput).
		Order("ordinal ASC").Find(&items).Error; err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("stored output choices = %d, want 1", len(items))
	}
	var stored canonical.Item
	if err := json.Unmarshal(items[0].CanonicalJSON, &stored); err != nil {
		t.Fatal(err)
	}
	if canonicalConversationItemText(stored) != "primary" {
		t.Fatalf("primary choice = %#v", stored)
	}
}

func TestProjectAPIConversationRejectsNonTerminalOrMismatchedStatus(t *testing.T) {
	db := setupConversationDomainTestDB(t)
	call := createConversationTestCall(t, db, "call_projection_status", 1, 2, decimal.Zero)
	if err := db.Model(call).Update("status", model.APICallStatusInProgress).Error; err != nil {
		t.Fatal(err)
	}
	request := ConversationProjectionRequest{
		UserID: 1, TokenID: 2, Model: "model-a", CallID: call.ID,
		InputItems:  []canonical.Item{canonicalMessage(canonical.RoleUser, "hello", "input_text")},
		OutputItems: []canonical.Item{canonicalMessage(canonical.RoleAssistant, "answer", "output_text")},
	}
	if _, err := ProjectAPIConversation(request); err == nil || !strings.Contains(err.Error(), "not terminal") {
		t.Fatalf("non-terminal projection error = %v", err)
	}

	if err := db.Model(call).Update("status", model.APICallStatusFailed).Error; err != nil {
		t.Fatal(err)
	}
	request.Status = model.ConversationTurnCompleted
	if _, err := ProjectAPIConversation(request); !errors.Is(err, ErrInvalidConversationTurnState) {
		t.Fatalf("mismatched projection error = %v", err)
	}
}

func TestProjectAPIConversationUsesExplicitHintsAndFinalAttemptMetadata(t *testing.T) {
	db := setupConversationDomainTestDB(t)
	if err := db.AutoMigrate(&model.APICallAttempt{}); err != nil {
		t.Fatal(err)
	}
	baseCall := createConversationTestCall(t, db, "call_projection_hint_base", 1, 2, decimal.Zero)
	baseID, err := ProjectAPIConversation(ConversationProjectionRequest{
		UserID: 1, TokenID: 2, Model: "model-a", CallID: baseCall.ID,
		InputItems:  []canonical.Item{canonicalMessage(canonical.RoleUser, "base", "input_text")},
		OutputItems: []canonical.Item{canonicalMessage(canonical.RoleAssistant, "ok", "output_text")},
		Status:      model.ConversationTurnCompleted, ProviderResponseID: "provider-base",
	})
	if err != nil {
		t.Fatal(err)
	}

	hintCall := createConversationTestCall(t, db, "call_projection_hint", 1, 2, decimal.Zero)
	attempt := &model.APICallAttempt{
		CallID: hintCall.ID, AttemptNo: 1, Status: model.APICallAttemptStatusCompleted,
		KeyID: 77, Transport: model.UpstreamTransport("openai_responses"), ProviderResponseID: "provider-next",
	}
	if err := db.Create(attempt).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(hintCall).Update("final_attempt_id", attempt.ID).Error; err != nil {
		t.Fatal(err)
	}
	requestLog := &model.ChannelRequestLog{CallID: hintCall.ID, AttemptID: attempt.ID, FinishReason: "stop", RequestType: model.RequestTypeResponses}
	if err := db.Create(requestLog).Error; err != nil {
		t.Fatal(err)
	}
	returnedID, err := ProjectAPIConversation(ConversationProjectionRequest{
		UserID: 1, TokenID: 2, Model: "model-a", CallID: hintCall.ID,
		PreviousResponseID: "provider-base",
		InputItems:         []canonical.Item{canonicalMessage(canonical.RoleUser, "delta only", "input_text")},
		OutputItems:        []canonical.Item{canonicalMessage(canonical.RoleAssistant, "next", "output_text")},
		Status:             model.ConversationTurnCompleted,
	})
	if err != nil {
		t.Fatal(err)
	}
	if returnedID != baseID {
		t.Fatalf("hint conversation = %d, want %d", returnedID, baseID)
	}
	if err := ValidateAPIConversationID(baseID, 1, 2); err != nil {
		t.Fatalf("validate owned conversation: %v", err)
	}
	if err := ValidateAPIConversationID(baseID, 99, 2); !errors.Is(err, ErrConversationNotFound) {
		t.Fatalf("foreign conversation validation = %v", err)
	}
	var turn model.ConversationTurn
	if err := db.First(&turn, "call_id = ?", hintCall.ID).Error; err != nil {
		t.Fatal(err)
	}
	if turn.RequestLogID != requestLog.ID || turn.ProviderResponseID != "provider-next" || turn.FinishReason != "stop" {
		t.Fatalf("hydrated turn = %#v", turn)
	}
	var conversation model.Conversation
	if err := db.First(&conversation, baseID).Error; err != nil {
		t.Fatal(err)
	}
	if conversation.ProviderKeyID != 77 || conversation.UpstreamTransport != attempt.Transport {
		t.Fatalf("hydrated provenance = %#v", conversation)
	}
}

func TestProjectAPIConversationResolvesOlderPublicResponseAndPreservesCompletedProviderState(t *testing.T) {
	db := setupConversationDomainTestDB(t)
	baseCall := createConversationTestCall(t, db, "call_projection_public_base", 1, 2, decimal.Zero)
	if err := db.Model(baseCall).Updates(map[string]any{
		"resource_type": "response", "resource_id": "resp_projection_base",
	}).Error; err != nil {
		t.Fatal(err)
	}
	conversationID, err := ProjectAPIConversation(ConversationProjectionRequest{
		UserID: 1, TokenID: 2, Model: "model-a", CallID: baseCall.ID,
		InputItems:         []canonical.Item{canonicalMessage(canonical.RoleUser, "base", "input_text")},
		OutputItems:        []canonical.Item{canonicalMessage(canonical.RoleAssistant, "ok", "output_text")},
		Status:             model.ConversationTurnCompleted,
		ProviderResponseID: "provider-base",
		Provenance: ConversationProvenance{
			KeyID: 11, Transport: model.UpstreamTransportOpenAIResponses,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.AIResponse{
		ID: "resp_projection_base", UserID: 1, TokenID: 2, CallID: baseCall.ID,
		Model: "model-a", Status: "completed", Store: true,
		IdempotencyKey: "internal:resp_projection_base",
	}).Error; err != nil {
		t.Fatal(err)
	}

	failedCall := createConversationTestCall(t, db, "call_projection_public_failed", 1, 2, decimal.Zero)
	if err := db.Model(failedCall).Updates(map[string]any{
		"status": model.APICallStatusFailed, "resource_type": "response", "resource_id": "resp_projection_failed",
		"error_type": "upstream_error", "error_code": "stream_failed",
	}).Error; err != nil {
		t.Fatal(err)
	}
	failedConversationID, err := ProjectAPIConversation(ConversationProjectionRequest{
		UserID: 1, TokenID: 2, Model: "model-a", CallID: failedCall.ID,
		PreviousResponseID: "resp_projection_base",
		InputItems:         []canonical.Item{canonicalMessage(canonical.RoleUser, "failed delta", "input_text")},
		OutputItems:        []canonical.Item{canonicalMessage(canonical.RoleAssistant, "partial", "output_text")},
		Status:             model.ConversationTurnFailed,
		ProviderResponseID: "provider-partial",
		Provenance: ConversationProvenance{
			KeyID: 99, Transport: model.UpstreamTransportAnthropic,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if failedConversationID != conversationID {
		t.Fatalf("failed conversation = %d, want %d", failedConversationID, conversationID)
	}
	var afterFailure model.Conversation
	if err := db.First(&afterFailure, conversationID).Error; err != nil {
		t.Fatal(err)
	}
	if afterFailure.ProviderResponseID != "provider-base" || afterFailure.ProviderKeyID != 11 ||
		afterFailure.UpstreamTransport != model.UpstreamTransportOpenAIResponses {
		t.Fatalf("failed turn replaced completed provider state: %#v", afterFailure)
	}
	var failedTurn model.ConversationTurn
	if err := db.First(&failedTurn, "call_id = ?", failedCall.ID).Error; err != nil {
		t.Fatal(err)
	}
	if failedTurn.ProviderResponseID != "provider-partial" || failedTurn.Status != model.ConversationTurnFailed {
		t.Fatalf("failed turn lost its partial provider state: %#v", failedTurn)
	}

	// Call metadata is independently retained; public response resolution must
	// continue to work from the durable response-to-turn relationship.
	if err := db.Delete(&model.APICall{}, "id = ?", baseCall.ID).Error; err != nil {
		t.Fatal(err)
	}
	retryCall := createConversationTestCall(t, db, "call_projection_public_retry", 1, 2, decimal.Zero)
	retryConversationID, err := ProjectAPIConversation(ConversationProjectionRequest{
		UserID: 1, TokenID: 2, Model: "model-a", CallID: retryCall.ID,
		PreviousResponseID: "resp_projection_base",
		InputItems:         []canonical.Item{canonicalMessage(canonical.RoleUser, "retry delta", "input_text")},
		OutputItems:        []canonical.Item{canonicalMessage(canonical.RoleAssistant, "done", "output_text")},
		Status:             model.ConversationTurnCompleted,
	})
	if err != nil {
		t.Fatal(err)
	}
	if retryConversationID != conversationID {
		t.Fatalf("retry conversation = %d, want %d", retryConversationID, conversationID)
	}
}

func TestProjectAPIConversationRejectsForeignRequestLogAndSanitizesFailure(t *testing.T) {
	db := setupConversationDomainTestDB(t)
	first := createConversationTestCall(t, db, "call_projection_log_first", 1, 2, decimal.Zero)
	second := createConversationTestCall(t, db, "call_projection_log_second", 1, 2, decimal.Zero)
	foreignLog := &model.ChannelRequestLog{CallID: second.ID, RequestType: model.RequestTypeChat}
	if err := db.Create(foreignLog).Error; err != nil {
		t.Fatal(err)
	}
	_, err := ProjectAPIConversation(ConversationProjectionRequest{
		UserID: 1, TokenID: 2, Model: "model-a", CallID: first.ID, RequestLogID: foreignLog.ID,
		InputItems:  []canonical.Item{canonicalMessage(canonical.RoleUser, "hello", "input_text")},
		OutputItems: []canonical.Item{canonicalMessage(canonical.RoleAssistant, "answer", "output_text")},
	})
	if err == nil || !strings.Contains(err.Error(), "request log") {
		t.Fatalf("foreign request log error = %v", err)
	}

	if err := db.Model(first).Updates(map[string]any{
		"status": model.APICallStatusFailed, "error_message": "Bearer secret-call-token",
	}).Error; err != nil {
		t.Fatal(err)
	}
	_, err = ProjectAPIConversation(ConversationProjectionRequest{
		UserID: 1, TokenID: 2, Model: "model-a", CallID: first.ID,
		InputItems:   []canonical.Item{canonicalMessage(canonical.RoleUser, "hello", "input_text")},
		ErrorMessage: "api_key=secret-projection-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	var turn model.ConversationTurn
	if err := db.First(&turn, "call_id = ?", first.ID).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(turn.ErrorMessage, "secret-call-token") || strings.Contains(turn.ErrorMessage, "secret-projection-token") {
		t.Fatalf("unsanitized conversation error = %q", turn.ErrorMessage)
	}
}

func TestProjectAPIConversationFallsBackWhenRequestLogWasDeleted(t *testing.T) {
	testCases := []struct {
		name         string
		createLatest bool
		expectLatest bool
	}{
		{name: "no remaining request log"},
		{name: "uses latest request log", createLatest: true, expectLatest: true},
	}
	for index, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			db := setupConversationDomainTestDB(t)
			call := createConversationTestCall(
				t, db, fmt.Sprintf("call_projection_missing_log_%d", index), 1, 2, decimal.Zero,
			)
			var latest model.ChannelRequestLog
			if testCase.createLatest {
				latest = model.ChannelRequestLog{CallID: call.ID, RequestType: model.RequestTypeChat, FinishReason: "stop"}
				if err := db.Create(&latest).Error; err != nil {
					t.Fatal(err)
				}
			}
			_, err := ProjectAPIConversation(ConversationProjectionRequest{
				UserID: 1, TokenID: 2, Model: "model-a", CallID: call.ID, RequestLogID: 999999,
				InputItems:  []canonical.Item{canonicalMessage(canonical.RoleUser, "hello", "input_text")},
				OutputItems: []canonical.Item{canonicalMessage(canonical.RoleAssistant, "answer", "output_text")},
			})
			if err != nil {
				t.Fatal(err)
			}
			var turn model.ConversationTurn
			if err := db.First(&turn, "call_id = ?", call.ID).Error; err != nil {
				t.Fatal(err)
			}
			expected := uint(0)
			if testCase.expectLatest {
				expected = latest.ID
			}
			if turn.RequestLogID != expected {
				t.Fatalf("request log id = %d, want %d", turn.RequestLogID, expected)
			}
		})
	}
}

func TestConversationProjectionPreparesConcurrentExplicitHistoryAsDeltas(t *testing.T) {
	testCases := []struct {
		name     string
		callTag  string
		messages [2]string
	}{
		{name: "same new message", callTag: "same", messages: [2]string{"same next request", "same next request"}},
		{name: "different new messages", callTag: "different", messages: [2]string{"first next request", "second next request"}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			db := setupConversationProjectionOutboxTestDB(t)
			baseInput := []canonical.Item{canonicalMessage(canonical.RoleUser, "base", "input_text")}
			baseOutput := []canonical.Item{canonicalMessage(canonical.RoleAssistant, "base answer", "output_text")}
			conversationID := projectConversationTestTurn(t, db, "call_projection_prepared_base", baseInput, baseOutput)
			calls := []*model.APICall{
				createConversationTestCall(t, db, "call_projection_prepared_"+testCase.callTag+"_1", 1, 2, decimal.Zero),
				createConversationTestCall(t, db, "call_projection_prepared_"+testCase.callTag+"_2", 1, 2, decimal.Zero),
			}
			for _, call := range calls {
				setConversationProjectionTestCallID(t, db, call, conversationID)
			}

			start := make(chan struct{})
			stageErrors := make(chan error, len(calls))
			var wait sync.WaitGroup
			for index, call := range calls {
				wait.Add(1)
				go func(index int, callID string) {
					defer wait.Done()
					<-start
					input := append(canonical.CloneItems(baseInput), canonical.CloneItems(baseOutput)...)
					input = append(input, canonicalMessage(canonical.RoleUser, testCase.messages[index], "input_text"))
					stageErrors <- StageAPIConversationProjectionInput(ConversationProjectionInputRequest{
						CallID: callID, ConversationID: conversationID, InputItems: input,
					})
				}(index, call.ID)
			}
			close(start)
			wait.Wait()
			close(stageErrors)
			for err := range stageErrors {
				if err != nil {
					t.Fatal(err)
				}
			}

			for index, call := range calls {
				var entry model.ConversationProjectionOutbox
				if err := db.First(&entry, "call_id = ?", call.ID).Error; err != nil {
					t.Fatal(err)
				}
				input, err := unmarshalConversationProjectionItems(entry.CanonicalInput, "input")
				if err != nil {
					t.Fatal(err)
				}
				if !entry.InputPrepared || len(input) != 1 || canonicalConversationItemText(input[0]) != testCase.messages[index] {
					t.Fatalf("prepared input %d = prepared:%v items:%#v", index, entry.InputPrepared, input)
				}
				updated, err := StageAPIConversationProjectionOutputIfPresent(ConversationProjectionOutputRequest{
					CallID: call.ID,
					OutputItems: []canonical.Item{
						canonicalMessage(canonical.RoleAssistant, fmt.Sprintf("answer-%d", index), "output_text"),
					},
				})
				if err != nil || !updated {
					t.Fatalf("stage output %d: updated=%v err=%v", index, updated, err)
				}
			}

			for _, call := range calls {
				returnedID, err := ProjectPendingAPIConversation(call.ID)
				if err != nil || returnedID != conversationID {
					t.Fatalf("projected conversation = %d, want %d: %v", returnedID, conversationID, err)
				}
			}

			var turnCount int64
			if err := db.Model(&model.ConversationTurn{}).Where("conversation_id = ?", conversationID).Count(&turnCount).Error; err != nil {
				t.Fatal(err)
			}
			if turnCount != 3 {
				t.Fatalf("conversation turn count = %d, want base plus two continuations", turnCount)
			}
			histories, err := loadCompletedCanonicalConversationItemsTx(db, []uint{conversationID})
			if err != nil {
				t.Fatal(err)
			}
			if len(histories[conversationID]) != 6 {
				t.Fatalf("completed history item count = %d, want 6", len(histories[conversationID]))
			}
			textCounts := make(map[string]int)
			for _, item := range histories[conversationID] {
				textCounts[canonicalConversationItemText(item)]++
			}
			if textCounts["base"] != 1 || textCounts["base answer"] != 1 {
				t.Fatalf("base history was duplicated: %#v", textCounts)
			}
			for _, message := range testCase.messages {
				textCounts[message]--
			}
			if textCounts[testCase.messages[0]] != 0 || textCounts[testCase.messages[1]] != 0 {
				t.Fatalf("prepared messages were not recorded once per call: %#v", textCounts)
			}
		})
	}
}

func TestConversationProjectionPreparesDelayedExplicitHistoryAsDeltas(t *testing.T) {
	testCases := []struct {
		name        string
		callTag     string
		firstInput  string
		secondInput string
	}{
		{name: "same new message", callTag: "same", firstInput: "same next request", secondInput: "same next request"},
		{name: "different new messages", callTag: "different", firstInput: "first next request", secondInput: "second next request"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			db := setupConversationProjectionOutboxTestDB(t)
			baseInput := []canonical.Item{canonicalMessage(canonical.RoleUser, "base", "input_text")}
			baseOutput := []canonical.Item{canonicalMessage(canonical.RoleAssistant, "base answer", "output_text")}
			conversationID := projectConversationTestTurn(t, db, "call_projection_delayed_base", baseInput, baseOutput)

			stageAndProject := func(callID, inputText, outputText string) {
				t.Helper()
				call := createConversationTestCall(t, db, callID, 1, 2, decimal.Zero)
				setConversationProjectionTestCallID(t, db, call, conversationID)
				input := append(canonical.CloneItems(baseInput), canonical.CloneItems(baseOutput)...)
				input = append(input, canonicalMessage(canonical.RoleUser, inputText, "input_text"))
				if err := StageAPIConversationProjectionInput(ConversationProjectionInputRequest{
					CallID: call.ID, ConversationID: conversationID, InputItems: input,
				}); err != nil {
					t.Fatal(err)
				}
				var entry model.ConversationProjectionOutbox
				if err := db.First(&entry, "call_id = ?", call.ID).Error; err != nil {
					t.Fatal(err)
				}
				prepared, err := unmarshalConversationProjectionItems(entry.CanonicalInput, "input")
				if err != nil {
					t.Fatal(err)
				}
				if !entry.InputPrepared || len(prepared) != 1 || canonicalConversationItemText(prepared[0]) != inputText {
					t.Fatalf("delayed prepared input = prepared:%v items:%#v", entry.InputPrepared, prepared)
				}
				updated, err := StageAPIConversationProjectionOutputIfPresent(ConversationProjectionOutputRequest{
					CallID: call.ID,
					OutputItems: []canonical.Item{
						canonicalMessage(canonical.RoleAssistant, outputText, "output_text"),
					},
				})
				if err != nil || !updated {
					t.Fatalf("stage delayed output: updated=%v err=%v", updated, err)
				}
				if returnedID, err := ProjectPendingAPIConversation(call.ID); err != nil || returnedID != conversationID {
					t.Fatalf("project delayed call = %d, %v", returnedID, err)
				}
			}

			stageAndProject("call_projection_delayed_"+testCase.callTag+"_1", testCase.firstInput, "first answer")
			stageAndProject("call_projection_delayed_"+testCase.callTag+"_2", testCase.secondInput, "second answer")

			histories, err := loadCompletedCanonicalConversationItemsTx(db, []uint{conversationID})
			if err != nil {
				t.Fatal(err)
			}
			if len(histories[conversationID]) != 6 {
				t.Fatalf("delayed completed history item count = %d, want 6", len(histories[conversationID]))
			}
			textCounts := make(map[string]int)
			for _, item := range histories[conversationID] {
				textCounts[canonicalConversationItemText(item)]++
			}
			if textCounts["base"] != 1 || textCounts["base answer"] != 1 {
				t.Fatalf("delayed staging duplicated base history: %#v", textCounts)
			}
			if testCase.firstInput == testCase.secondInput {
				if textCounts[testCase.firstInput] != 2 {
					t.Fatalf("same input count = %d, want 2", textCounts[testCase.firstInput])
				}
			} else if textCounts[testCase.firstInput] != 1 || textCounts[testCase.secondInput] != 1 {
				t.Fatalf("different input counts = %#v", textCounts)
			}
		})
	}
}

func TestConversationProjectionPreservesCompleteDelayedInputSegment(t *testing.T) {
	testCases := []struct {
		name        string
		callTag     string
		firstOutput []canonical.Item
	}{
		{
			name: "multi-item input before completed output", callTag: "multi_output",
			firstOutput: []canonical.Item{canonicalMessage(canonical.RoleAssistant, "first answer", "output_text")},
		},
		{name: "multi-item input after empty output", callTag: "empty_output"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			db := setupConversationProjectionOutboxTestDB(t)
			baseInput := []canonical.Item{canonicalMessage(canonical.RoleUser, "base", "input_text")}
			baseOutput := []canonical.Item{canonicalMessage(canonical.RoleAssistant, "base answer", "output_text")}
			conversationID := projectConversationTestTurn(
				t, db, "call_projection_segment_base_"+testCase.callTag, baseInput, baseOutput,
			)
			delta := []canonical.Item{
				{
					Type: "function_call_output", CallID: "tool_1",
					Output: json.RawMessage(`{"temperature":21}`),
				},
				canonicalMessage(canonical.RoleUser, "continue after tool", "input_text"),
			}
			fullInput := append(canonical.CloneItems(baseInput), canonical.CloneItems(baseOutput)...)
			fullInput = append(fullInput, canonical.CloneItems(delta)...)

			first := createConversationTestCall(
				t, db, "call_projection_segment_first_"+testCase.callTag, 1, 2, decimal.Zero,
			)
			setConversationProjectionTestCallID(t, db, first, conversationID)
			if err := StageAPIConversationProjectionInput(ConversationProjectionInputRequest{
				CallID: first.ID, ConversationID: conversationID, InputItems: fullInput,
			}); err != nil {
				t.Fatal(err)
			}
			updated, err := StageAPIConversationProjectionOutputIfPresent(ConversationProjectionOutputRequest{
				CallID: first.ID, OutputItems: testCase.firstOutput,
			})
			if err != nil || !updated {
				t.Fatalf("stage first output: updated=%v err=%v", updated, err)
			}
			if returnedID, err := ProjectPendingAPIConversation(first.ID); err != nil || returnedID != conversationID {
				t.Fatalf("project first call = %d, %v", returnedID, err)
			}

			second := createConversationTestCall(
				t, db, "call_projection_segment_second_"+testCase.callTag, 1, 2, decimal.Zero,
			)
			setConversationProjectionTestCallID(t, db, second, conversationID)
			if err := StageAPIConversationProjectionInput(ConversationProjectionInputRequest{
				CallID: second.ID, ConversationID: conversationID, InputItems: fullInput,
			}); err != nil {
				t.Fatal(err)
			}
			var entry model.ConversationProjectionOutbox
			if err := db.First(&entry, "call_id = ?", second.ID).Error; err != nil {
				t.Fatal(err)
			}
			prepared, err := unmarshalConversationProjectionItems(entry.CanonicalInput, "input")
			if err != nil {
				t.Fatal(err)
			}
			equal, err := canonicalConversationItemSlicesEqual(delta, prepared)
			if err != nil {
				t.Fatal(err)
			}
			if !entry.InputPrepared || !equal {
				t.Fatalf("prepared delayed segment = prepared:%v items:%#v", entry.InputPrepared, prepared)
			}
		})
	}
}

func TestCanonicalConversationLegacyBackfillScansMultiplePages(t *testing.T) {
	db := setupConversationProjectionOutboxTestDB(t)
	conversation := &model.Conversation{
		UserID: 1, TokenID: 2, Model: "model-a", Title: "legacy paged",
		CanonicalStateVersion: 0, TurnSequence: 1, Status: 1,
	}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatal(err)
	}
	turn := &model.ConversationTurn{
		ConversationID: conversation.ID, Sequence: 1, CallID: "call_projection_legacy_paged_seed",
		Model: "model-a", Status: model.ConversationTurnCompleted,
	}
	if err := db.Create(turn).Error; err != nil {
		t.Fatal(err)
	}

	history := make([]canonical.Item, canonicalConversationScanPageSize*3+17)
	for index := range history {
		history[index] = canonicalMessage(
			canonical.RoleUser,
			fmt.Sprintf("legacy-%04d-%s", index, strings.Repeat("x", 256)),
			"input_text",
		)
	}
	// The ignored assistant shell and reasoning cross a page boundary.
	history[canonicalConversationScanPageSize-1] = canonical.Item{Type: "message", Role: canonical.RoleAssistant}
	history[canonicalConversationScanPageSize] = canonical.Item{
		Type: "reasoning", Role: canonical.RoleAssistant,
		Content: []canonical.Content{{Type: "reasoning_text", Text: "legacy reasoning"}},
	}
	history[canonicalConversationScanPageSize+1] = canonical.Item{
		Type: "function_call", Role: canonical.RoleAssistant, CallID: "legacy_tool",
		Name: "lookup", Arguments: json.RawMessage(`{"page":2}`),
	}

	records := make([]model.ConversationItem, 0, len(history))
	var historyBytes uint64
	for index, item := range history {
		encoded, err := marshalConversationCanonicalItem(item)
		if err != nil {
			t.Fatal(err)
		}
		historyBytes += uint64(len(encoded))
		records = append(records, model.ConversationItem{
			ConversationID: conversation.ID, TurnID: turn.ID, TurnSequence: turn.Sequence,
			Direction: model.ConversationItemInput, Ordinal: index, CanonicalJSON: encoded,
		})
	}
	if err := db.CreateInBatches(&records, canonicalConversationScanPageSize).Error; err != nil {
		t.Fatal(err)
	}

	newInput := canonicalMessage(canonical.RoleUser, "after legacy", "input_text")
	newOutput := canonicalMessage(canonical.RoleAssistant, "after legacy answer", "output_text")
	call := createConversationTestCall(t, db, "call_projection_legacy_paged_next", 1, 2, decimal.Zero)
	setConversationProjectionTestCallID(t, db, call, conversation.ID)
	input := append(canonical.CloneItems(history), newInput)
	if err := StageAPIConversationProjectionInput(ConversationProjectionInputRequest{
		CallID: call.ID, ConversationID: conversation.ID, InputItems: input,
	}); err != nil {
		t.Fatal(err)
	}
	var entry model.ConversationProjectionOutbox
	if err := db.First(&entry, "call_id = ?", call.ID).Error; err != nil {
		t.Fatal(err)
	}
	preparedInput, err := unmarshalConversationProjectionItems(entry.CanonicalInput, "input")
	if err != nil {
		t.Fatal(err)
	}
	if !entry.InputPrepared || len(preparedInput) != 1 || canonicalConversationItemText(preparedInput[0]) != "after legacy" {
		t.Fatalf("paged legacy prepared input = prepared:%v items:%#v", entry.InputPrepared, preparedInput)
	}
	updated, err := StageAPIConversationProjectionOutputIfPresent(ConversationProjectionOutputRequest{
		CallID: call.ID, OutputItems: []canonical.Item{newOutput},
	})
	if err != nil || !updated {
		t.Fatalf("stage paged legacy output: updated=%v err=%v", updated, err)
	}
	returnedID, err := ProjectPendingAPIConversation(call.ID)
	if err != nil || returnedID != conversation.ID {
		t.Fatalf("paged legacy continuation = %d, %v", returnedID, err)
	}

	for _, item := range []canonical.Item{newInput, newOutput} {
		encoded, err := marshalConversationCanonicalItem(item)
		if err != nil {
			t.Fatal(err)
		}
		historyBytes += uint64(len(encoded))
	}
	allItems := append(canonical.CloneItems(history), newInput, newOutput)
	fingerprints, _, valid := canonicalConversationMatchFingerprints(allItems)
	if !valid {
		t.Fatal("expected legacy history to be fingerprintable")
	}
	hashes := canonicalConversationRollingHashes("", fingerprints)
	if err := db.First(conversation, conversation.ID).Error; err != nil {
		t.Fatal(err)
	}
	if conversation.CanonicalStateVersion != 1 || conversation.CanonicalItemCount != uint64(len(fingerprints)) {
		t.Fatalf("backfilled canonical state = %#v", conversation)
	}
	if conversation.CanonicalBytes != historyBytes {
		t.Fatalf("backfilled canonical bytes = %d, want %d", conversation.CanonicalBytes, historyBytes)
	}
	if len(hashes) == 0 || conversation.CanonicalMatchHash != hashes[len(hashes)-1] {
		t.Fatalf("backfilled canonical hash = %q", conversation.CanonicalMatchHash)
	}

	staleInput := canonicalMessage(canonical.RoleUser, "stale branch after legacy", "input_text")
	staleOutput := canonicalMessage(canonical.RoleAssistant, "stale branch answer", "output_text")
	staleCall := createConversationTestCall(t, db, "call_projection_legacy_paged_stale", 1, 2, decimal.Zero)
	setConversationProjectionTestCallID(t, db, staleCall, conversation.ID)
	staleHistory := append(canonical.CloneItems(history), staleInput)
	if err := StageAPIConversationProjectionInput(ConversationProjectionInputRequest{
		CallID: staleCall.ID, ConversationID: conversation.ID, InputItems: staleHistory,
	}); err != nil {
		t.Fatal(err)
	}
	entry = model.ConversationProjectionOutbox{}
	if err := db.First(&entry, "call_id = ?", staleCall.ID).Error; err != nil {
		t.Fatal(err)
	}
	preparedInput, err = unmarshalConversationProjectionItems(entry.CanonicalInput, "input")
	if err != nil {
		t.Fatal(err)
	}
	if !entry.InputPrepared || len(preparedInput) != 1 || canonicalConversationItemText(preparedInput[0]) != "stale branch after legacy" {
		t.Fatalf("paged stale prepared input = prepared:%v items:%#v", entry.InputPrepared, preparedInput)
	}
	updated, err = StageAPIConversationProjectionOutputIfPresent(ConversationProjectionOutputRequest{
		CallID: staleCall.ID, OutputItems: []canonical.Item{staleOutput},
	})
	if err != nil || !updated {
		t.Fatalf("stage paged stale output: updated=%v err=%v", updated, err)
	}
	if returnedID, err := ProjectPendingAPIConversation(staleCall.ID); err != nil || returnedID != conversation.ID {
		t.Fatalf("project paged stale call = %d, %v", returnedID, err)
	}
	completed, err := loadCompletedCanonicalConversationItemsTx(db, []uint{conversation.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(completed[conversation.ID]) != len(history)+4 {
		t.Fatalf("paged stale history item count = %d, want %d", len(completed[conversation.ID]), len(history)+4)
	}
}

func canonicalMessage(role canonical.Role, text, contentType string) canonical.Item {
	return canonical.Item{Type: "message", Role: role, Content: []canonical.Content{{Type: contentType, Text: text}}}
}

func setConversationProjectionTestCallID(t *testing.T, db *gorm.DB, call *model.APICall, conversationID uint) {
	t.Helper()
	if err := db.Model(call).Update("conversation_id", conversationID).Error; err != nil {
		t.Fatal(err)
	}
	call.ConversationID = conversationID
}

func projectConversationTestTurn(t *testing.T, db *gorm.DB, callID string, input, output []canonical.Item) uint {
	t.Helper()
	call := createConversationTestCall(t, db, callID, 1, 2, decimal.Zero)
	conversationID, err := ProjectAPIConversation(ConversationProjectionRequest{
		UserID: 1, TokenID: 2, Model: "model-a", CallID: call.ID,
		InputItems: input, OutputItems: output, Status: model.ConversationTurnCompleted,
	})
	if err != nil {
		t.Fatal(err)
	}
	return conversationID
}

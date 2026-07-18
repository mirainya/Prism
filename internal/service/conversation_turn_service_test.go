package service

import (
	"encoding/json"
	"testing"

	"github.com/mirainya/Prism/internal/model"
	"github.com/shopspring/decimal"
	"gorm.io/datatypes"
)

func TestListConversationTurnsPaginatesSortsAndScopesItems(t *testing.T) {
	db := setupConversationDomainTestDB(t)
	conversation := &model.Conversation{UserID: 1, TokenID: 2, Title: "turns", Model: "model-a", Status: 1}
	otherConversation := &model.Conversation{UserID: 1, TokenID: 2, Title: "other", Model: "model-a", Status: 1}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(otherConversation).Error; err != nil {
		t.Fatal(err)
	}

	turns := []model.ConversationTurn{
		{ConversationID: conversation.ID, Sequence: 3, CallID: "call_turn_3", Model: "model-a", Status: model.ConversationTurnFailed, Cost: decimal.RequireFromString("0.30000000")},
		{ConversationID: conversation.ID, Sequence: 1, CallID: "call_turn_1", Model: "model-a", Status: model.ConversationTurnCompleted, Cost: decimal.RequireFromString("0.10000000")},
		{ConversationID: conversation.ID, Sequence: 2, CallID: "call_turn_2", Model: "model-a", Status: model.ConversationTurnAborted, Cost: decimal.RequireFromString("0.20000000")},
		{ConversationID: otherConversation.ID, Sequence: 1, CallID: "call_turn_other", Model: "model-a", Status: model.ConversationTurnCompleted},
	}
	for index := range turns {
		if err := db.Create(&turns[index]).Error; err != nil {
			t.Fatal(err)
		}
	}

	items := []model.ConversationItem{
		{ConversationID: conversation.ID, TurnID: turns[1].ID, TurnSequence: 1, Direction: model.ConversationItemOutput, Ordinal: 1, CanonicalJSON: datatypes.JSON(`{"type":"message","role":"assistant"}`)},
		{ConversationID: conversation.ID, TurnID: turns[1].ID, TurnSequence: 1, Direction: model.ConversationItemInput, Ordinal: 0, CanonicalJSON: datatypes.JSON(`{"type":"message","role":"user"}`)},
		{ConversationID: conversation.ID, TurnID: turns[2].ID, TurnSequence: 2, Direction: model.ConversationItemInput, Ordinal: 0, CanonicalJSON: datatypes.JSON(`{"type":"message","role":"user"}`)},
		{ConversationID: otherConversation.ID, TurnID: turns[3].ID, TurnSequence: 1, Direction: model.ConversationItemInput, Ordinal: 0, CanonicalJSON: datatypes.JSON(`{"type":"message","role":"user"}`)},
		// Corrupt ownership must not leak merely because turn_id belongs to the requested page.
		{ConversationID: otherConversation.ID, TurnID: turns[1].ID, TurnSequence: 1, Direction: model.ConversationItemInput, Ordinal: 9, CanonicalJSON: datatypes.JSON(`{"type":"message","role":"user","name":"foreign"}`)},
	}
	for index := range items {
		if err := db.Create(&items[index]).Error; err != nil {
			t.Fatal(err)
		}
	}

	pageOne, err := NewConversationService().ListTurns(conversation.ID, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if pageOne.Total != 3 || pageOne.Page != 1 || pageOne.PageSize != 2 || len(pageOne.Items) != 2 {
		t.Fatalf("page one metadata = %#v", pageOne)
	}
	if pageOne.Items[0].Sequence != 1 || pageOne.Items[1].Sequence != 2 {
		t.Fatalf("turn order = %#v", pageOne.Items)
	}
	if len(pageOne.Items[0].Items) != 2 || pageOne.Items[0].Items[0].Ordinal != 0 || pageOne.Items[0].Items[1].Ordinal != 1 {
		t.Fatalf("first turn items = %#v", pageOne.Items[0].Items)
	}
	if len(pageOne.Items[1].Items) != 1 || pageOne.Items[1].Items[0].TurnID != pageOne.Items[1].ID {
		t.Fatalf("second turn items = %#v", pageOne.Items[1].Items)
	}

	pageTwo, err := NewConversationService().ListTurns(conversation.ID, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(pageTwo.Items) != 1 || pageTwo.Items[0].Sequence != 3 || pageTwo.Items[0].Items == nil {
		t.Fatalf("page two = %#v", pageTwo)
	}
}

func TestListConversationTurnsEmptyResultAndJSONContract(t *testing.T) {
	db := setupConversationDomainTestDB(t)
	conversation := &model.Conversation{UserID: 1, TokenID: 2, Title: "empty", Model: "model-a", Status: 1}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatal(err)
	}

	empty, err := NewConversationService().ListTurns(conversation.ID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Total != 0 || empty.Page != 1 || empty.PageSize != 50 || empty.Items == nil || len(empty.Items) != 0 {
		t.Fatalf("empty response = %#v", empty)
	}

	turn := &model.ConversationTurn{
		ConversationID: conversation.ID, Sequence: 1, CallID: "call_contract", RequestLogID: 7,
		Model: "model-a", ProviderResponseID: "provider-1", Status: model.ConversationTurnCompleted,
		InputTokens: 3, OutputTokens: 2, TotalTokens: 5, Cost: decimal.RequireFromString("0.12345678"),
		LatencyMs: 25, FinishReason: "stop",
	}
	if err := db.Create(turn).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ConversationItem{
		ConversationID: conversation.ID, TurnID: turn.ID, TurnSequence: turn.Sequence,
		Direction: model.ConversationItemInput, Ordinal: 0,
		CanonicalJSON: datatypes.JSON(`{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}`),
	}).Error; err != nil {
		t.Fatal(err)
	}

	response, err := NewConversationService().ListTurns(conversation.ID, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(response.Items[0])
	if err != nil {
		t.Fatal(err)
	}
	var contract map[string]any
	if err := json.Unmarshal(encoded, &contract); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"id", "conversation_id", "sequence", "call_id", "request_log_id", "model", "provider_response_id", "status", "context_mode", "input_tokens", "output_tokens", "total_tokens", "cost", "latency_ms", "finish_reason", "created_at", "items"} {
		if _, ok := contract[key]; !ok {
			t.Fatalf("turn JSON is missing %q: %s", key, encoded)
		}
	}
	if _, exists := contract["turn_sequence"]; exists {
		t.Fatalf("turn JSON exposed storage name instead of sequence: %s", encoded)
	}
	encodedItems, ok := contract["items"].([]any)
	if !ok || len(encodedItems) != 1 {
		t.Fatalf("items contract = %#v", contract["items"])
	}
	itemContract := encodedItems[0].(map[string]any)
	if _, ok := itemContract["canonical"].(map[string]any); !ok {
		t.Fatalf("canonical JSON is not an object: %#v", itemContract["canonical"])
	}
}

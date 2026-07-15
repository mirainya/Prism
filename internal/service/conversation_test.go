package service

import (
	"testing"

	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider/chat"
)

func TestSaveConversationTurnBindsRequestLog(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(
		&model.Conversation{},
		&model.Message{},
		&model.ChannelRequestLog{},
		&model.APICall{},
	); err != nil {
		t.Fatalf("migrate conversation tables: %v", err)
	}

	requestLog := &model.ChannelRequestLog{RequestType: model.RequestTypeChat}
	if err := db.Create(requestLog).Error; err != nil {
		t.Fatalf("create request log: %v", err)
	}
	call := &model.APICall{ID: "call_conversation", RequestID: "request-conversation", UserID: 1, TokenID: 2}
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
	var conversations, messages int64
	if err := db.Model(&model.Conversation{}).Count(&conversations).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Message{}).Count(&messages).Error; err != nil {
		t.Fatal(err)
	}
	if conversations != 0 || messages != 0 {
		t.Fatalf("partial conversation persisted: conversations=%d messages=%d", conversations, messages)
	}
}

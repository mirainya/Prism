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
	); err != nil {
		t.Fatalf("migrate conversation tables: %v", err)
	}

	requestLog := &model.ChannelRequestLog{RequestType: model.RequestTypeChat}
	if err := db.Create(requestLog).Error; err != nil {
		t.Fatalf("create request log: %v", err)
	}

	conversationID := SaveConversationTurn(
		&ConversationContext{},
		1,
		2,
		"test-model",
		[]chat.ChatMessage{{Role: model.RoleUser, Content: "hello"}},
		chat.ChatMessage{Role: model.RoleAssistant, Content: "world"},
		nil,
		"stop",
		"",
		requestLog.ID,
	)
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
}

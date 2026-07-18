package model

import (
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestDBReturnsUnpollutedSession(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file:model_session?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&GwChannelKey{}, &AIResponse{}); err != nil {
		t.Fatal(err)
	}
	SetDB(database.Model(&GwChannelKey{}))

	var responses []AIResponse
	if err := DB().Where("status = ?", "queued").Find(&responses).Error; err != nil {
		t.Fatalf("fresh session inherited another model: %v", err)
	}
}

func TestAutoMigrateCreatesConversationProjectionSchema(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file:model_auto_migrate_index?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	SetDB(database)
	if err := AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	if !database.Migrator().HasIndex(&Conversation{}, conversationCanonicalMatchIndexName) {
		t.Fatalf("missing index %s", conversationCanonicalMatchIndexName)
	}
	if !database.Migrator().HasIndex(&ConversationProjectionOutbox{}, "idx_conversation_projection_request_log_id") {
		t.Fatal("missing conversation projection request log index")
	}
	if !database.Migrator().HasIndex(&ConversationProjectionOutbox{}, "idx_conversation_projection_previous_response_id") {
		t.Fatal("missing conversation projection previous response index")
	}
	if !database.Migrator().HasIndex(&Conversation{}, "idx_conversations_provider_response_id") {
		t.Fatal("missing conversation provider response index")
	}
	columnTypes, err := database.Migrator().ColumnTypes(&Conversation{})
	if err != nil {
		t.Fatal(err)
	}
	modelLengthOK := false
	systemPromptLongText := false
	for _, columnType := range columnTypes {
		switch columnType.Name() {
		case "model":
			length, ok := columnType.Length()
			modelLengthOK = ok && length >= 80
		case "system_prompt":
			systemPromptLongText = strings.EqualFold(columnType.DatabaseTypeName(), "longtext")
		}
	}
	if !modelLengthOK {
		t.Fatal("conversation model column is shorter than 80 characters")
	}
	if !systemPromptLongText {
		t.Fatal("conversation system prompt column is not LONGTEXT")
	}
	for _, column := range []struct {
		model any
		name  string
	}{
		{model: &APICall{}, name: "project_conversation"},
		{model: &Conversation{}, name: "canonical_item_count"},
		{model: &Conversation{}, name: "canonical_bytes"},
		{model: &Conversation{}, name: "canonical_match_hash"},
		{model: &Conversation{}, name: "canonical_state_version"},
		{model: &ConversationProjectionOutbox{}, name: "input_prepared"},
		{model: &ConversationProjectionOutbox{}, name: "context_mode"},
		{model: &ConversationTurn{}, name: "context_mode"},
	} {
		if !database.Migrator().HasColumn(column.model, column.name) {
			t.Fatalf("missing column %T.%s", column.model, column.name)
		}
	}
}

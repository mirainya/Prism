package handler

import (
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider/chat"
	"gorm.io/gorm"
)

func TestResolveOwnedChatFiles(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:handler_chat_files?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.AIFile{}); err != nil {
		t.Fatal(err)
	}
	model.SetDB(db)
	record := model.AIFile{ID: "file_owned", UserID: 1, TokenID: 10, Filename: "notes.txt", MimeType: "text/plain", Purpose: "user_data", Bytes: 3, Content: []byte("abc"), Status: "processed"}
	if err := db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}

	messages := []chat.ChatMessage{{Role: "user", Content: []any{
		map[string]any{"type": "file", "file": map[string]any{"file_id": record.ID}},
	}}}
	resolved, err := resolveOwnedChatFiles(10, messages)
	if err != nil {
		t.Fatalf("resolve owned file: %v", err)
	}
	part := resolved[0].Content.([]any)[0].(map[string]any)
	file := part["file"].(map[string]any)
	if _, exists := file["file_id"]; exists {
		t.Fatalf("resolved file still contains file_id: %#v", file)
	}
	if file["filename"] != record.Filename || !strings.HasPrefix(file["file_data"].(string), "data:text/plain;base64,") {
		t.Fatalf("unexpected resolved file: %#v", file)
	}
	if _, err := resolveOwnedChatFiles(11, messages); err == nil {
		t.Fatal("cross-token file_id was accepted")
	}
	if _, err := resolveOwnedChatFiles(10, []chat.ChatMessage{{Role: "user", Content: []any{
		map[string]any{"type": "file", "file": map[string]any{"file_id": "file-upstream"}},
	}}}); err == nil {
		t.Fatal("unknown upstream file_id was accepted")
	}
}

package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider/chat"
	"github.com/mirainya/Prism/pkg/config"
	"gorm.io/gorm"
)

func TestResolveOwnedChatFiles(t *testing.T) {
	dsn := fmt.Sprintf("file:handler_chat_files_%d?mode=memory&cache=shared", filesTestDBSequence.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&model.AIFile{}, &model.MediaAsset{}, &model.MediaAssetRef{}); err != nil {
		t.Fatal(err)
	}
	model.SetDB(db)
	const locator = "https://cdn.example/files/owned"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/file/download" || r.URL.Query().Get("url") != locator {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte("abc"))
	}))
	defer server.Close()
	previousConfig := config.C
	config.C = &config.Config{FileStorage: config.FileStorageConfig{BaseURL: server.URL, APIKey: "test"}}
	t.Cleanup(func() { config.C = previousConfig })
	record := model.AIFile{ID: "file_owned", UserID: 1, TokenID: 10, Filename: "notes.txt", MimeType: "text/plain", Purpose: "user_data", Bytes: 3, Status: "processed"}
	if err := db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("abc"))
	asset := model.MediaAsset{UserID: 1, TokenID: 10, Purpose: "file", ObjectKey: "xfs:test:file_owned", StorageLocator: locator, ContentType: "text/plain", ContentLength: 3, SHA256: hex.EncodeToString(digest[:]), State: "active", StateVersion: 1}
	if err := db.Create(&asset).Error; err != nil {
		t.Fatal(err)
	}
	fileID := record.ID
	if err := db.Create(&model.MediaAssetRef{MediaAssetID: asset.ID, UserID: 1, TokenID: 10, Role: "file", AIFileID: &fileID}).Error; err != nil {
		t.Fatal(err)
	}

	messages := []chat.ChatMessage{{Role: "user", Content: []any{
		map[string]any{"type": "file", "file": map[string]any{"file_id": record.ID}},
	}}}
	resolved, err := resolveOwnedChatFiles(context.Background(), 10, messages)
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
	if _, err := resolveOwnedChatFiles(context.Background(), 11, messages); err == nil {
		t.Fatal("cross-token file_id was accepted")
	}
	if _, err := resolveOwnedChatFiles(context.Background(), 10, []chat.ChatMessage{{Role: "user", Content: []any{
		map[string]any{"type": "file", "file": map[string]any{"file_id": "file-upstream"}},
	}}}); err == nil {
		t.Fatal("unknown upstream file_id was accepted")
	}
}

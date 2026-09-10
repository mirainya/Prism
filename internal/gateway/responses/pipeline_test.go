package responses

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mirainya/Prism/internal/model"
	protocol "github.com/mirainya/Prism/internal/provider/responses"
	"github.com/mirainya/Prism/pkg/config"
	"gorm.io/gorm"
)

func storeResponseTestFiles(t *testing.T, db *gorm.DB, files ...model.AIFile) {
	t.Helper()
	if err := db.AutoMigrate(&model.AIFile{}, &model.MediaAsset{}, &model.MediaAssetRef{}); err != nil {
		t.Fatal(err)
	}
	objects := make(map[string][]byte, len(files))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		locator := r.URL.Query().Get("url")
		content, ok := objects[locator]
		if r.URL.Path != "/api/v1/file/download" || !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_, _ = w.Write(content)
	}))
	t.Cleanup(server.Close)
	previousConfig := config.C
	config.C = &config.Config{FileStorage: config.FileStorageConfig{BaseURL: server.URL, APIKey: "test-key"}}
	t.Cleanup(func() { config.C = previousConfig })
	for index := range files {
		file := files[index]
		content := append([]byte(nil), file.Content...)
		file.Content = nil
		if file.CreatedAt.IsZero() {
			file.CreatedAt = time.Now().UTC()
		}
		file.UpdatedAt = file.CreatedAt
		if err := db.Create(&file).Error; err != nil {
			t.Fatal(err)
		}
		locator := fmt.Sprintf("https://cdn.example/files/%s", file.ID)
		objects[locator] = content
		digest := sha256.Sum256(content)
		asset := model.MediaAsset{UserID: file.UserID, TokenID: file.TokenID, Purpose: "file", ObjectKey: "xfs:test:" + file.ID, StorageLocator: locator, ContentType: file.MimeType, ContentLength: uint64(len(content)), SHA256: hex.EncodeToString(digest[:]), State: "active", StateVersion: 1, CreatedAt: file.CreatedAt, UpdatedAt: file.CreatedAt}
		if err := db.Create(&asset).Error; err != nil {
			t.Fatal(err)
		}
		fileID := file.ID
		if err := db.Create(&model.MediaAssetRef{MediaAssetID: asset.ID, UserID: file.UserID, TokenID: file.TokenID, Role: "file", AIFileID: &fileID, CreatedAt: file.CreatedAt}).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func TestResolveInputFilesChecksOwnership(t *testing.T) {
	db := openResponsesTestDB(t)
	model.SetDB(db)
	file := model.AIFile{ID: "file_test", UserID: 1, TokenID: 10, Filename: "a.txt", Purpose: "user_data", Bytes: 3, MimeType: "text/plain", Content: []byte("abc"), Status: "processed"}
	storeResponseTestFiles(t, db, file)
	req := &protocol.Request{Input: json.RawMessage(`[{"type":"message","content":[{"type":"input_file","file_id":"file_test"}]}]`)}
	if err := resolveInputFiles(context.Background(), 10, req); err != nil {
		t.Fatalf("owned file: %v", err)
	}
	if string(req.Input) == "" || !json.Valid(req.Input) {
		t.Fatalf("invalid input: %s", req.Input)
	}
	bad := &protocol.Request{Input: json.RawMessage(`[{"type":"message","content":[{"type":"input_file","file_id":"file_test"}]}]`)}
	if err := resolveInputFiles(context.Background(), 11, bad); err == nil {
		t.Fatal("cross-token file access was accepted")
	}
}

func TestResolveInputFilesUsesModalitySpecificDataURLs(t *testing.T) {
	db := openResponsesTestDB(t)
	model.SetDB(db)
	files := []model.AIFile{
		{ID: "file_audio", UserID: 1, TokenID: 10, Filename: "sound.wav", Purpose: "user_data", Bytes: 3, MimeType: "audio/wav", Content: []byte("abc"), Status: "processed"},
		{ID: "file_video", UserID: 1, TokenID: 10, Filename: "clip.mp4", Purpose: "user_data", Bytes: 3, MimeType: "video/mp4", Content: []byte("xyz"), Status: "processed"},
	}
	storeResponseTestFiles(t, db, files...)
	req := &protocol.Request{Input: json.RawMessage(`[{"role":"user","content":[{"type":"input_audio","file_id":"file_audio"},{"type":"input_video","file_id":"file_video"}]}]`)}
	if err := resolveInputFiles(context.Background(), 10, req); err != nil {
		t.Fatal(err)
	}
	text := string(req.Input)
	for _, expected := range []string{`"audio_url":"data:audio/wav;base64,YWJj"`, `"video_url":"data:video/mp4;base64,eHl6"`} {
		if !strings.Contains(text, expected) {
			t.Fatalf("missing %s in %s", expected, text)
		}
	}
	if strings.Contains(text, `"file_id"`) || strings.Contains(text, `"file_data"`) {
		t.Fatalf("modality files used the wrong field: %s", text)
	}
}

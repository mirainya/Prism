//go:build integration

package migrate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mirainya/Prism/internal/tokenauth"
	"github.com/mirainya/Prism/pkg/config"
)

func verifyLegacyAIFileImport(t *testing.T, db *sql.DB) {
	t.Helper()
	content := []byte("legacy file bytes spanning object storage")
	var uploaded []byte
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/file/download" {
			if r.Header.Get("X-Api-Key") != "integration-key" || r.URL.Query().Get("url") != "https://storage.example/private/stored-file" {
				http.Error(w, "unexpected download", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Length", fmt.Sprint(len(uploaded)))
			_, _ = w.Write(uploaded)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/upload" || r.Header.Get("X-Api-Key") != "integration-key" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer file.Close()
		uploaded, err = io.ReadAll(file)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		data, _ := json.Marshal(map[string]any{
			"id": "stored-file", "url": "storage.example/private/stored-file",
			"size": len(uploaded), "filename": "stored-file.bin", "path": r.FormValue("path"),
			"contentType": "application/octet-stream", "platform": "integration", "objectId": "object-1",
		})
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "ok", "data": json.RawMessage(data)})
	}))
	defer storage.Close()
	previousConfig := config.C
	config.C = &config.Config{FileStorage: config.FileStorageConfig{
		BaseURL: storage.URL, APIKey: "integration-key", UploadPath: "prism/", MaxFileSizeMB: 64,
	}}
	t.Cleanup(func() { config.C = previousConfig })

	now := time.Now().UTC().Truncate(time.Millisecond)
	_, selector, secretDigest, err := tokenauth.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO tokens (user_id,selector,secret_digest,secret_digest_version,auth_version,key_hint,name,balance,total_used,rate_limit,status,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, 4343, selector, secretDigest, tokenauth.DigestVersion, 1, "test", "file import fixture", 0, 0, 60, 1, now, now); err != nil {
		t.Fatal(err)
	}
	var tokenID int64
	if err := db.QueryRow(`SELECT id FROM tokens WHERE selector=?`, selector).Scan(&tokenID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO ai_files (id,user_id,token_id,filename,purpose,bytes,mime_type,content,status,created_at) VALUES (?,?,?,?,?,?,?,?,?,?)`, "file-integration-1", 4343, tokenID, "fixture.bin", "assistants", len(content), "application/octet-stream", content, "processed", now); err != nil {
		t.Fatal(err)
	}

	options := ImportOptions{HMACKey: bytes.Repeat([]byte{91}, 32)}
	report, err := ImportLegacyAIFiles(context.Background(), db, options)
	if err != nil || report.Imported != 1 || report.Issues != 0 {
		t.Fatalf("AI file import report=%+v err=%v", report, err)
	}
	if !bytes.Equal(uploaded, content) {
		t.Fatalf("uploaded bytes=%q, want %q", uploaded, content)
	}
	digest := sha256.Sum256(content)
	var targetCount int
	if err := db.QueryRow(`
		SELECT COUNT(*)
		FROM gw_file_resources f
		JOIN gw_media_asset_refs r ON r.ai_file_id=f.id AND r.role='file' AND r.ordinal=0
		JOIN gw_media_assets a ON a.id=r.media_asset_id
		WHERE f.id=? AND f.user_id=? AND f.token_id=? AND f.status='processed'
		  AND a.state='active' AND a.content_length=? AND a.sha256=?`,
		"file-integration-1", 4343, tokenID, len(content), hex.EncodeToString(digest[:])).Scan(&targetCount); err != nil {
		t.Fatal(err)
	}
	if targetCount != 1 {
		t.Fatalf("target file relation count=%d", targetCount)
	}
	audit, err := auditAIFileHistory(context.Background(), db)
	if err != nil || !audit {
		t.Fatalf("AI file audit=%t err=%v", audit, err)
	}
	second, err := ImportLegacyAIFiles(context.Background(), db, options)
	if err != nil || second.RunID != report.RunID || second.Imported != 0 || second.Skipped != 1 {
		t.Fatalf("duplicate AI file import report=%+v err=%v", second, err)
	}
}

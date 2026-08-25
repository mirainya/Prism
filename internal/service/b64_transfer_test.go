package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mirainya/Prism/pkg/config"
)

func TestResolveB64ToURLsContinuesAfterParentCancellation(t *testing.T) {
	payload := []byte("generated-image")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Errorf("form file: %v", err)
			http.Error(w, "invalid multipart", http.StatusBadRequest)
			return
		}
		defer file.Close()
		got, err := io.ReadAll(file)
		if err != nil {
			t.Errorf("read form file: %v", err)
			http.Error(w, "read failed", http.StatusInternalServerError)
			return
		}
		if string(got) != string(payload) {
			t.Errorf("payload = %q, want %q", got, payload)
			http.Error(w, "invalid payload", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200,
			"data": map[string]any{"url": "https://cdn.example/image.png"},
		})
	}))
	defer server.Close()

	previous := config.C
	config.C = &config.Config{FileStorage: config.FileStorageConfig{
		BaseURL: server.URL, APIKey: "test-key", UploadPath: "prism/",
	}}
	t.Cleanup(func() { config.C = previous })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	urls, err := resolveB64ToURLs(ctx, []string{base64.StdEncoding.EncodeToString(payload)}, "image-test")
	if err != nil {
		t.Fatal(err)
	}
	if len(urls) != 1 || urls[0] != "https://cdn.example/image.png" {
		t.Fatalf("URLs = %#v", urls)
	}
}

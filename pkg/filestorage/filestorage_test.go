package filestorage

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mirainya/Prism/pkg/config"
)

func TestNormalizePublicURL(t *testing.T) {
	tests := map[string]string{
		"http://storage.example/image.png":  "https://storage.example/image.png",
		"https://storage.example/image.png": "https://storage.example/image.png",
		"storage.example/image.png":         "https://storage.example/image.png",
		"":                                  "",
	}
	for input, expected := range tests {
		if actual := normalizePublicURL(input); actual != expected {
			t.Fatalf("normalizePublicURL(%q) = %q, want %q", input, actual, expected)
		}
	}
}

func TestTransferReaderStreamsMultipartUpload(t *testing.T) {
	payload := []byte("streamed-video-content")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/upload" || r.Header.Get("X-Api-Key") != "test-key" {
			t.Errorf("request = %s %s key=%q", r.Method, r.URL.Path, r.Header.Get("X-Api-Key"))
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
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
		if !bytes.Equal(got, payload) || r.FormValue("path") == "" {
			t.Errorf("payload=%q path=%q", got, r.FormValue("path"))
			http.Error(w, "invalid payload", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200, "message": "ok", "data": map[string]any{"url": "https://cdn.example/video.mp4"},
		})
	}))
	defer server.Close()
	previous := config.C
	config.C = &config.Config{FileStorage: config.FileStorageConfig{BaseURL: server.URL, APIKey: "test-key", UploadPath: "prism/"}}
	t.Cleanup(func() { config.C = previous })

	got, err := TransferReader(context.Background(), bytes.NewReader(payload), "video/mp4", "video-assets")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://cdn.example/video.mp4" {
		t.Fatalf("URL = %q", got)
	}
}

func TestDeleteURLUsesDocumentedEndpoint(t *testing.T) {
	const assetURL = "https://cdn.example/video assets/file.mp4"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v1/file/delete" || r.URL.Query().Get("url") != assetURL {
			t.Errorf("request = %s %s url=%q", r.Method, r.URL.Path, r.URL.Query().Get("url"))
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "ok", "data": true})
	}))
	defer server.Close()
	previous := config.C
	config.C = &config.Config{FileStorage: config.FileStorageConfig{BaseURL: server.URL, APIKey: "test-key"}}
	t.Cleanup(func() { config.C = previous })

	if err := DeleteURL(context.Background(), assetURL); err != nil {
		t.Fatal(err)
	}
}

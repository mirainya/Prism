package filestorage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
			"code": 200, "message": "ok", "data": map[string]any{"url": "https://temporary.example/video.mp4?sig=short", "rawUrl": "https://cdn.example/video.mp4"},
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
	if got != "https://temporary.example/video.mp4?sig=short" {
		t.Fatalf("URL = %q", got)
	}
}

func TestUploadReaderReturnsStableStorageIdentity(t *testing.T) {
	payload := []byte("stored-content")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/upload" || r.FormValue("path") != "prism/files/file-1/" {
			t.Fatalf("unexpected upload request %s path=%q", r.URL.Path, r.FormValue("path"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200,
			"data": map[string]any{
				"id": "storage-row-1", "objectId": "object-1", "platform": "r2-main",
				"url": "https://temporary.example/stored.bin?sig=short", "rawUrl": serverURL(r), "path": "prism/files/file-1/", "filename": "stored.bin",
				"size": len(payload), "contentType": "application/octet-stream",
			},
		})
	}))
	defer server.Close()
	previous := config.C
	config.C = &config.Config{FileStorage: config.FileStorageConfig{BaseURL: server.URL, APIKey: "test-key"}}
	t.Cleanup(func() { config.C = previous })

	result, err := UploadReaderAtPath(context.Background(), bytes.NewReader(payload), "application/octet-stream", "prism/files/file-1/")
	if err != nil {
		t.Fatal(err)
	}
	if result.StorageKey() != "xfs:r2-main:object-1" || result.Size != int64(len(payload)) {
		t.Fatalf("unexpected upload result: %+v key=%q", result, result.StorageKey())
	}
}

func TestUploadReaderAtPathWithFilenameSendsRecoveryIdentity(t *testing.T) {
	const filename = "8c7dd922ad47494fc02c388e12c00eac67da1f5cd03cc320e384d10cf4cf2ad7.mp4"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("form file: %v", err)
		}
		defer file.Close()
		if header.Filename != filename || r.FormValue("path") != "prism/gateway-results/17/" {
			t.Fatalf("filename=%q path=%q", header.Filename, r.FormValue("path"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200,
			"data": map[string]any{"url": "https://temporary.example/result.mp4?sig=short", "rawUrl": "prism/gateway-results/17/generated.mp4", "filename": "generated.mp4", "originalFilename": filename, "path": "prism/gateway-results/17/"},
		})
	}))
	defer server.Close()
	previous := config.C
	config.C = &config.Config{FileStorage: config.FileStorageConfig{BaseURL: server.URL, APIKey: "test-key"}}
	t.Cleanup(func() { config.C = previous })

	result, err := UploadReaderAtPathWithFilename(context.Background(), bytes.NewReader([]byte("video")), "video/mp4", "prism/gateway-results/17/", filename)
	if err != nil {
		t.Fatal(err)
	}
	if result.Filename != "generated.mp4" || result.OriginalName != filename || result.URL != "https://temporary.example/result.mp4?sig=short" || result.RawURL != "prism/gateway-results/17/generated.mp4" || result.StorageLocator() != result.RawURL {
		t.Fatalf("result=%+v", result)
	}
	for _, invalid := range []string{"", "../result.mp4", `nested\\result.mp4`, "result.mp4\nignored"} {
		if _, err := UploadReaderAtPathWithFilename(context.Background(), bytes.NewReader([]byte("video")), "video/mp4", "prism/gateway-results/17/", invalid); err == nil {
			t.Fatalf("invalid filename %q accepted", invalid)
		}
	}
}

func TestOpenDownloadUsesAuthenticatedStorageProxy(t *testing.T) {
	const locator = "https://cdn.example/private/file.bin"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/file/download" || r.URL.Query().Get("url") != locator || r.Header.Get("X-Api-Key") != "test-key" {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte("private-content"))
	}))
	defer server.Close()
	previous := config.C
	config.C = &config.Config{FileStorage: config.FileStorageConfig{BaseURL: server.URL, APIKey: "test-key"}}
	t.Cleanup(func() { config.C = previous })

	data, err := ReadURL(context.Background(), locator, 64)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "private-content" {
		t.Fatalf("downloaded %q", data)
	}
}

func TestVerifyURLRejectsStoredObjectCorruption(t *testing.T) {
	const locator = "https://cdn.example/private/file.bin"
	payload := []byte("stored-content")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "14")
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	previous := config.C
	config.C = &config.Config{FileStorage: config.FileStorageConfig{BaseURL: server.URL, APIKey: "test-key"}}
	t.Cleanup(func() { config.C = previous })
	digest := sha256.Sum256(payload)
	if err := VerifyURL(context.Background(), locator, int64(len(payload)), hex.EncodeToString(digest[:])); err != nil {
		t.Fatal(err)
	}
	badDigest := sha256.Sum256([]byte("different"))
	if err := VerifyURL(context.Background(), locator, int64(len(payload)), hex.EncodeToString(badDigest[:])); err == nil {
		t.Fatal("expected checksum mismatch")
	}
	if err := VerifyURL(context.Background(), locator, int64(len(payload))+1, hex.EncodeToString(digest[:])); err == nil {
		t.Fatal("expected length mismatch")
	}
}

func serverURL(r *http.Request) string {
	return "https://cdn.example" + r.URL.Path + "/stored.bin"
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

func TestBoundClientProbeSupportsRelativeLocator(t *testing.T) {
	payload := []byte("prism-xfs-binding-probe")
	const locator = "Prism/system-check/2026-09-17/probe.txt"
	calls := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "bound-key" {
			http.Error(w, "wrong key", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v1/upload":
			calls["upload"]++
			file, _, err := r.FormFile("file")
			if err != nil {
				http.Error(w, "missing file", http.StatusBadRequest)
				return
			}
			defer file.Close()
			body, _ := io.ReadAll(file)
			if !bytes.Equal(body, payload) {
				http.Error(w, "wrong body", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"url": "https://temporary.example/probe?sig=short", "rawUrl": locator, "size": len(body)}})
		case "/api/v1/file/download":
			calls["download"]++
			if r.URL.Query().Get("url") != locator {
				http.Error(w, "wrong locator", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Length", "23")
			_, _ = w.Write(payload)
		case "/api/v1/file/presigned-url":
			calls["presign"]++
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": "https://cdn.example/probe.txt?signature=test"})
		case "/api/v1/file/delete":
			calls["delete"]++
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	previous := config.C
	config.C = &config.Config{FileStorage: config.FileStorageConfig{BaseURL: server.URL, APIKey: "global-key", UploadPath: "Prism/"}}
	t.Cleanup(func() { config.C = previous })

	if err := WithAPIKey("bound-key").Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"upload", "download", "presign", "delete"} {
		if calls[operation] != 1 {
			t.Fatalf("%s calls = %d, want 1", operation, calls[operation])
		}
	}
}

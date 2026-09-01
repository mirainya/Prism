package video

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPresignedUploadResolverSingleUpload(t *testing.T) {
	data := []byte("reference-image")
	var completed bool
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/video-generations/uploads":
			if r.Header.Get("Authorization") != "Bearer secret" || r.Header.Get("Idempotency-Key") == "" {
				t.Error("upload application headers are missing")
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["idempotency_key"] == "" {
				t.Error("upload application body idempotency key is missing")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"code":0,"data":{"disposition":"owner","upload":{"session_token":"session-1","generation":1,"mode":"single","upload_url":"`+serverURL+`/r2/single"}}}`)
		case "/r2/single":
			body, _ := io.ReadAll(r.Body)
			if string(body) != string(data) || r.Header.Get("Authorization") != "" || r.Header.Get("If-None-Match") != "*" {
				t.Error("single upload did not use the signed URL contract")
			}
			w.WriteHeader(http.StatusCreated)
		case "/api/v1/video-generations/uploads/complete":
			completed = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"code":0,"data":{"storage_object_id":"object-1"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	resolver := &presignedUploadResolver{
		baseURL: server.URL + "/api", apiKey: "secret", controlClient: server.Client(), uploadClient: server.Client(),
		requestID: "task-1", config: testPresignedUploadConfig(), openSource: testAssetSource(data, "image/png"),
	}
	resolver.config.IdempotencyBodyField = "idempotency_key"
	asset := &VideoAsset{ID: "asset-1", Kind: "image", ContentType: "image/png", StoragePath: "https://cdn.example/image.png", Status: VideoAssetStatusReady, ExpiresAt: time.Now().Add(time.Hour)}
	resolved, err := resolver.Prepare(context.Background(), asset)
	if err != nil || resolved.RefType != ResolvedAssetRefProviderObject || resolved.RefValue != "object-1" || !completed {
		t.Fatalf("resolved=%#v err=%v completed=%t", resolved, err, completed)
	}
}

func TestPresignedUploadResolverMultipartAndWaiting(t *testing.T) {
	data := []byte("abcde")
	var completedParts []presignedCompletedPart
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/video-generations/uploads":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"code":0,"data":{"disposition":"waiting","ticket":"ticket-1","retry_after_seconds":0}}`)
		case "/api/v1/video-generations/uploads/wait":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"code":0,"data":{"disposition":"owner","upload":{"session_token":"session-2","generation":2,"mode":"multipart","parts":[{"part_number":1,"size_bytes":3,"upload_url":"`+serverURL+`/r2/1"},{"part_number":2,"size_bytes":2,"upload_url":"`+serverURL+`/r2/2"}]}}}`)
		case "/r2/1", "/r2/2":
			body, _ := io.ReadAll(r.Body)
			if r.URL.Path == "/r2/1" && string(body) != "abc" || r.URL.Path == "/r2/2" && string(body) != "de" {
				t.Errorf("unexpected part %q: %q", r.URL.Path, body)
			}
			w.Header().Set("ETag", `"etag-`+strings.TrimPrefix(r.URL.Path, "/r2/")+`"`)
			w.WriteHeader(http.StatusCreated)
		case "/api/v1/video-generations/uploads/complete":
			var body struct {
				Parts []presignedCompletedPart `json:"parts"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			completedParts = body.Parts
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"code":0,"data":{"storage_object_id":"object-2"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL
	resolver := &presignedUploadResolver{
		baseURL: server.URL + "/api", apiKey: "secret", controlClient: server.Client(), uploadClient: server.Client(),
		config: testPresignedUploadConfig(), openSource: testAssetSource(data, "image/png"),
	}
	asset := &VideoAsset{ID: "asset-2", Kind: "image", ContentType: "image/png", StoragePath: "https://cdn.example/image.png", Status: VideoAssetStatusReady, ExpiresAt: time.Now().Add(time.Hour)}
	resolved, err := resolver.Prepare(context.Background(), asset)
	if err != nil || resolved.RefValue != "object-2" || len(completedParts) != 2 || completedParts[0].ETag != `"etag-1"` {
		t.Fatalf("resolved=%#v err=%v parts=%#v", resolved, err, completedParts)
	}
}

func TestParsePresignedUploadConfigRejectsMissingAndInvalidPaths(t *testing.T) {
	tests := []struct {
		name        string
		extraConfig []byte
		wantError   string
	}{
		{name: "missing config", wantError: "requires extra_config.asset_resolver"},
		{
			name:        "missing required path",
			extraConfig: []byte(`{"asset_resolver":{"profile":"disposition_v1"}}`),
			wantError:   "apply_path must be an absolute URL path",
		},
		{
			name:        "absolute URL",
			extraConfig: []byte(`{"asset_resolver":{"profile":"disposition_v1","apply_path":"https://upload.example/apply","wait_path":"/wait","complete_path":"/complete","abort_path":"/abort"}}`),
			wantError:   "apply_path must be an absolute URL path",
		},
		{
			name:        "protocol relative path",
			extraConfig: []byte(`{"asset_resolver":{"profile":"disposition_v1","apply_path":"//upload.example/apply","wait_path":"/wait","complete_path":"/complete","abort_path":"/abort"}}`),
			wantError:   "apply_path must be an absolute URL path",
		},
		{
			name:        "invalid idempotency body field",
			extraConfig: []byte(`{"asset_resolver":{"profile":"disposition_v1","apply_path":"/apply","wait_path":"/wait","complete_path":"/complete","abort_path":"/abort","idempotency_body_field":"metadata.idempotency_key"}}`),
			wantError:   "idempotency_body_field must be a JSON field name",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parsePresignedUploadConfig(&VideoChannel{ExtraConfig: test.extraConfig})
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("err=%v, want error containing %q", err, test.wantError)
			}
		})
	}
}

func TestPresignedUploadResolverUsesCachedReference(t *testing.T) {
	expiresAt := time.Now().Add(time.Hour)
	encoded, err := json.Marshal(map[string]cachedAssetReference{
		"presigned_upload:12:34:disposition_v1": {
			RefType: ResolvedAssetRefProviderObject, RefValue: "cached-object", ExpiresAt: expiresAt,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	opened := false
	resolver := &presignedUploadResolver{
		baseURL: "https://provider.example", apiKey: "secret", cacheScope: "presigned_upload:12:34:disposition_v1",
		controlClient: http.DefaultClient, uploadClient: http.DefaultClient,
		openSource: func(context.Context, *VideoAsset, int64) (*preparedAssetSource, error) {
			opened = true
			return nil, nil
		},
	}
	asset := &VideoAsset{
		ID: "asset-cached", Kind: "image", ContentType: "image/png", StoragePath: "https://cdn.example/image.png",
		Status: VideoAssetStatusReady, ExpiresAt: time.Now().Add(time.Hour), UpstreamRefs: encoded,
	}
	resolved, err := resolver.Prepare(context.Background(), asset)
	if err != nil || resolved.RefType != ResolvedAssetRefProviderObject || resolved.RefValue != "cached-object" || opened {
		t.Fatalf("resolved=%#v err=%v opened=%t", resolved, err, opened)
	}
}

func TestPresignedUploadResolverRestartsExpiredSession(t *testing.T) {
	data := []byte("reference-image")
	var applyCount, abortCount, completeCount int
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/video-generations/uploads":
			applyCount++
			uploadPath := "/r2/valid"
			if applyCount == 1 {
				uploadPath = "/r2/expired"
			}
			_, _ = io.WriteString(w, `{"code":0,"data":{"disposition":"owner","upload":{"session_token":"session-`+string(rune('0'+applyCount))+`","generation":1,"mode":"single","upload_url":"`+serverURL+uploadPath+`"}}}`)
		case "/r2/expired":
			w.WriteHeader(http.StatusForbidden)
		case "/r2/valid":
			w.WriteHeader(http.StatusCreated)
		case "/api/v1/video-generations/uploads/abort":
			abortCount++
			_, _ = io.WriteString(w, `{"code":0,"data":{}}`)
		case "/api/v1/video-generations/uploads/complete":
			completeCount++
			_, _ = io.WriteString(w, `{"code":0,"data":{"storage_object_id":"object-restarted"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	config := testPresignedUploadConfig()
	config.MaxSessionRestarts = 1
	resolver := &presignedUploadResolver{
		baseURL: server.URL + "/api", apiKey: "secret", controlClient: server.Client(), uploadClient: server.Client(),
		config: config, openSource: testAssetSource(data, "image/png"),
	}
	asset := &VideoAsset{ID: "asset-restart", Kind: "image", ContentType: "image/png", StoragePath: "https://cdn.example/image.png", Status: VideoAssetStatusReady, ExpiresAt: time.Now().Add(time.Hour)}
	resolved, err := resolver.Prepare(context.Background(), asset)
	if err != nil || resolved.RefValue != "object-restarted" || applyCount != 2 || abortCount != 1 || completeCount != 1 {
		t.Fatalf("resolved=%#v err=%v apply=%d abort=%d complete=%d", resolved, err, applyCount, abortCount, completeCount)
	}
}

func TestPresignedUploadResolverRetriesControlAndPart(t *testing.T) {
	data := []byte("part-data")
	var applyCount, partCount int
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/video-generations/uploads":
			applyCount++
			if applyCount == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_, _ = io.WriteString(w, `{"code":0,"data":{"disposition":"owner","upload":{"session_token":"session-retry","generation":1,"mode":"multipart","parts":[{"part_number":1,"size_bytes":9,"upload_url":"`+serverURL+`/r2/part"}]}}}`)
		case "/r2/part":
			partCount++
			if partCount == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("ETag", `"etag-retry"`)
			w.WriteHeader(http.StatusCreated)
		case "/api/v1/video-generations/uploads/complete":
			_, _ = io.WriteString(w, `{"code":0,"data":{"storage_object_id":"object-retry"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	config := testPresignedUploadConfig()
	config.ControlRetries = 1
	config.PartRetries = 1
	resolver := &presignedUploadResolver{
		baseURL: server.URL + "/api", apiKey: "secret", controlClient: server.Client(), uploadClient: server.Client(),
		config: config, openSource: testAssetSource(data, "image/png"),
	}
	asset := &VideoAsset{ID: "asset-retry", Kind: "image", ContentType: "image/png", StoragePath: "https://cdn.example/image.png", Status: VideoAssetStatusReady, ExpiresAt: time.Now().Add(time.Hour)}
	resolved, err := resolver.Prepare(context.Background(), asset)
	if err != nil || resolved.RefValue != "object-retry" || applyCount != 2 || partCount != 2 {
		t.Fatalf("resolved=%#v err=%v apply=%d part=%d", resolved, err, applyCount, partCount)
	}
}

func testPresignedUploadConfig() presignedUploadConfig {
	config := presignedUploadConfig{
		Profile:   presignedDispositionProfile,
		ApplyPath: "/v1/video-generations/uploads", WaitPath: "/v1/video-generations/uploads/wait",
		CompletePath: "/v1/video-generations/uploads/complete", AbortPath: "/v1/video-generations/uploads/abort",
	}
	config.defaults()
	return config
}

func testAssetSource(data []byte, contentType string) func(context.Context, *VideoAsset, int64) (*preparedAssetSource, error) {
	return func(context.Context, *VideoAsset, int64) (*preparedAssetSource, error) {
		sum := sha256.Sum256(data)
		return &preparedAssetSource{
			ReaderAt: bytes.NewReader(data), SizeBytes: int64(len(data)),
			SHA256: hex.EncodeToString(sum[:]), ContentType: contentType,
		}, nil
	}
}

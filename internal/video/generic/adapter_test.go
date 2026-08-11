package generic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/mirainya/Prism/internal/video"
	"github.com/mirainya/Prism/internal/video/taskhttp"
)

func newTestAdapter(baseURL, apiKey string, client *http.Client, config adapterConfig) *Adapter {
	return &Adapter{
		client: taskhttp.NewClient(taskhttp.Config{
			BaseURL: baseURL, APIKey: apiKey,
			AuthLocation: config.AuthLocation, AuthHeader: config.AuthKey,
			AuthPrefix: valueOrEmpty(config.AuthPrefix), HTTPClient: client,
		}),
		config: config, apiKey: apiKey,
	}
}

func TestGenericAdapterBuildsConfiguredRequest(t *testing.T) {
	config := testAdapterConfig()
	config.Request.Fields["prompt"] = "input.prompt"
	emptyPrefix := ""
	config.AuthPrefix = &emptyPrefix
	adapter := newTestAdapter("https://provider.example", "secret", http.DefaultClient, config)
	request, err := adapter.BuildRequest(context.Background(), &video.GenerateRequest{
		Model: "seedance-2.0", Prompt: "make a clip", Resolution: "720p", Duration: 5, Audio: false,
		TaskMode: "references", TaskID: "task-1", Params: map[string]any{"model": "do-not-overwrite", "priority": 4},
		Content: []video.ContentItem{{Type: "image_url", Role: "reference_image", StorageObjectID: "object-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]any{
		"model": "seedance-2.0", "input": map[string]any{"prompt": "make a clip"},
		"resolution": "720p", "duration": 5, "generate_audio": false, "task_mode": "references",
		"content":  []map[string]any{{"type": "image_url", "role": "reference_image", "storage_object_id": "object-1"}},
		"priority": 4,
	}
	if !reflect.DeepEqual(request.Body, expected) {
		t.Fatalf("body=%#v, want %#v", request.Body, expected)
	}
	if request.Headers["Authorization"] != "" || request.Headers["X-Request-ID"] != "task-1" {
		t.Fatalf("headers=%#v", request.Headers)
	}
}

func TestGenericAdapterProjectsSelectedContentIntoRequestFields(t *testing.T) {
	config := testAdapterConfig()
	config.Request.IncludeContent = boolPointer(false)
	config.Request.ContentProjections = []contentProjection{
		{Source: "url", Target: "image_url", Types: []string{"image_url"}, Roles: []string{"first_frame"}, Required: true},
		{Source: "url", Target: "input.backup_image_url", Roles: []string{"reference_image"}, Index: 1},
	}
	adapter := newTestAdapter("https://provider.example", "secret", http.DefaultClient, config)

	request, err := adapter.BuildRequest(context.Background(), &video.GenerateRequest{
		Model: "grok-imagine-video-1.5",
		Content: []video.ContentItem{
			{Type: "image_url", Role: "reference_image", URL: "https://cdn.example/reference-1.png"},
			{Type: "image_url", Role: "first_frame", URL: "https://cdn.example/first.png"},
			{Type: "image_url", Role: "reference_image", URL: "https://cdn.example/reference-2.png"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]any{
		"model":          "grok-imagine-video-1.5",
		"generate_audio": false,
		"image_url":      "https://cdn.example/first.png",
		"input": map[string]any{
			"backup_image_url": "https://cdn.example/reference-2.png",
		},
	}
	if !reflect.DeepEqual(request.Body, expected) {
		t.Fatalf("body=%#v, want %#v", request.Body, expected)
	}
}

func TestGenericAdapterRejectsUnresolvedProjectedAsset(t *testing.T) {
	config := testAdapterConfig()
	config.Request.IncludeContent = boolPointer(false)
	config.Request.ContentProjections = []contentProjection{{Source: "url", Target: "image_url"}}
	adapter := newTestAdapter("https://provider.example", "secret", http.DefaultClient, config)

	_, err := adapter.BuildRequest(context.Background(), &video.GenerateRequest{
		Content: []video.ContentItem{{AssetID: "asset-unresolved"}},
	})
	if err == nil || !strings.Contains(err.Error(), "was not resolved") {
		t.Fatalf("unresolved asset error=%v", err)
	}
}

func TestGenericAdapterProjectsContentCollections(t *testing.T) {
	config := testAdapterConfig()
	config.Request.IncludeContent = boolPointer(false)
	config.Request.ContentProjections = []contentProjection{
		{Source: "url", Target: "images", Output: "array", Models: []string{"sora2"}, Types: []string{"image_url"}},
		{Source: "url", Target: "images_text", Output: "join", Separator: "\n", Models: []string{"sora2"}, Types: []string{"image_url"}},
		{Source: "url", Target: "ignored", Models: []string{"another-model"}, Required: true},
	}
	adapter := newTestAdapter("https://provider.example", "secret", http.DefaultClient, config)

	request, err := adapter.BuildRequest(context.Background(), &video.GenerateRequest{
		Model: "sora2",
		Content: []video.ContentItem{
			{Type: "image_url", Role: "reference_image", URL: "https://cdn.example/one.png"},
			{Type: "image_url", Role: "reference_image", URL: "https://cdn.example/two.png"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(request.Body["images"], []any{"https://cdn.example/one.png", "https://cdn.example/two.png"}) {
		t.Fatalf("images=%#v", request.Body["images"])
	}
	if request.Body["images_text"] != "https://cdn.example/one.png\nhttps://cdn.example/two.png" {
		t.Fatalf("images_text=%#v", request.Body["images_text"])
	}
	if _, exists := request.Body["ignored"]; exists {
		t.Fatalf("model-filtered projection was included")
	}
}

func TestGenericAdapterRequiresProjectedContent(t *testing.T) {
	config := testAdapterConfig()
	config.Request.ContentProjections = []contentProjection{{
		Source: "url", Target: "image_url", Roles: []string{"first_frame"}, Required: true,
	}}
	adapter := newTestAdapter("https://provider.example", "secret", http.DefaultClient, config)

	_, err := adapter.BuildRequest(context.Background(), &video.GenerateRequest{
		Model:   "grok-imagine-video-1.5",
		Content: []video.ContentItem{{Type: "image_url", Role: "reference_image", URL: "https://cdn.example/reference.png"}},
	})
	if err == nil || !strings.Contains(err.Error(), "requires a matching item") {
		t.Fatalf("required projection error=%v", err)
	}
}

func TestGenericAdapterSubmitPollAndCancel(t *testing.T) {
	var seenBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer secret" {
			http.Error(writer, "missing auth", http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/tasks":
			_ = json.NewDecoder(request.Body).Decode(&seenBody)
			_, _ = writer.Write([]byte(`{"code":0,"data":{"id":123,"status":"queued"}}`))
		case request.Method == http.MethodGet && request.URL.Path == "/tasks/123":
			_, _ = writer.Write([]byte(`{"code":0,"data":{"status":"succeeded","progress":75,"result":{"video_url":"https://cdn.example/video.mp4","thumbnail_url":"https://cdn.example/thumb.png","duration":4.5}}}`))
		case request.Method == http.MethodDelete && request.URL.Path == "/tasks/123":
			writer.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	config := testAdapterConfig()
	config.Submit.Path = "/tasks"
	config.Poll.Path = "/tasks/{task_id}"
	config.Cancel = operationConfig{Enabled: true, Method: http.MethodDelete, Path: "/tasks/{task_id}", AllowedStatuses: []string{"submitted", "tracking"}}
	adapter := newTestAdapter(server.URL, "secret", server.Client(), config)
	request, err := adapter.BuildRequest(context.Background(), &video.GenerateRequest{Model: "seedance-2.0", Prompt: "test", TaskID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	submitted, err := adapter.Submit(context.Background(), request)
	if err != nil || submitted.ProviderTaskID != "123" || submitted.Status != video.VideoTaskStatusSubmitted {
		t.Fatalf("submitted=%#v err=%v", submitted, err)
	}
	progress, err := adapter.Poll(context.Background(), submitted.ProviderTaskID)
	if err != nil || progress.Status != video.VideoTaskStatusCompleted || progress.Percent != 100 || progress.Result == nil || progress.Result.VideoURL != "https://cdn.example/video.mp4" || progress.Result.Duration != 4.5 {
		t.Fatalf("progress=%#v err=%v", progress, err)
	}
	if err := adapter.Cancel(context.Background(), submitted.ProviderTaskID); err != nil {
		t.Fatal(err)
	}
	if seenBody["model"] != "seedance-2.0" {
		t.Fatalf("submit body=%#v", seenBody)
	}
}

func TestGenericAdapterPostsTaskIDAndAuthenticationInBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/tasks/status" {
			t.Fatalf("request=%s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "" || request.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("headers=%#v", request.Header)
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["task_id"] != "task-123" || body["token"] != "Bearer secret" {
			t.Fatalf("body=%#v", body)
		}
		_, _ = writer.Write([]byte(`{"code":0,"data":{"id":"task-123","status":"running"}}`))
	}))
	defer server.Close()

	config := testAdapterConfig()
	config.AuthLocation = "body"
	config.AuthKey = "token"
	config.Poll = operationConfig{Enabled: true, Method: http.MethodPost, Path: "/tasks/status", TaskIDBodyPath: "task_id"}
	adapter := newTestAdapter(server.URL, "secret", server.Client(), config)

	progress, err := adapter.Poll(context.Background(), "task-123")
	if err != nil || progress.Status != video.VideoTaskStatusTracking {
		t.Fatalf("progress=%#v err=%v", progress, err)
	}
}

func TestGenericAdapterEstimatesWithSubmitRequestMapping(t *testing.T) {
	var seenBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/tasks/estimate" {
			http.NotFound(writer, request)
			return
		}
		if request.Header.Get("Authorization") != "Bearer secret" || request.Header.Get("X-Request-ID") != "estimate-1" {
			http.Error(writer, "missing request headers", http.StatusUnauthorized)
			return
		}
		_ = json.NewDecoder(request.Body).Decode(&seenBody)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"code":0,"data":{"estimated_cost":"1.25"}}`))
	}))
	defer server.Close()

	config := testAdapterConfig()
	config.Estimate = operationConfig{Enabled: true, Method: http.MethodPost, Path: "/tasks/estimate"}
	config.Response.EstimatedCostPaths = []string{"estimated_cost"}
	adapter := newTestAdapter(server.URL, "secret", server.Client(), config)
	cost, err := adapter.Estimate(context.Background(), &video.GenerateRequest{
		Model: "seedance-2.5", Prompt: "test", Resolution: "720p", Duration: 5,
		TaskID: "estimate-1", Params: map[string]any{"priority": 4},
		Content: []video.ContentItem{{Type: "video_url", Role: "reference_video", StorageObjectID: "object-1"}},
	})
	if err != nil || cost != 1.25 {
		t.Fatalf("cost=%v err=%v", cost, err)
	}
	if seenBody["priority"] != float64(4) || seenBody["model"] != "seedance-2.5" {
		t.Fatalf("estimate body=%#v", seenBody)
	}
	content, ok := seenBody["content"].([]any)
	if !ok || len(content) != 1 || content[0].(map[string]any)["storage_object_id"] != "object-1" {
		t.Fatalf("estimate content=%#v", seenBody["content"])
	}
}

func TestGenericAdapterValidatesConfigAndRequestLimits(t *testing.T) {
	missing := &video.VideoChannel{BaseURL: "https://provider.example", ExtraConfig: []byte(`{}`)}
	if err := ValidateChannelConfig(missing); err == nil || !strings.Contains(err.Error(), "extra_config.adapter") {
		t.Fatalf("missing config error=%v", err)
	}
	invalid := &video.VideoChannel{BaseURL: "https://provider.example", ExtraConfig: []byte(`{"adapter":{"profile":"json_task_v1","submit":{"path":"https://provider.example/tasks"},"poll":{"path":"/tasks/{task_id}"},"response":{"task_id_paths":["id"],"status_paths":["status"]}}}`)}
	if err := ValidateChannelConfig(invalid); err == nil || !strings.Contains(err.Error(), "submit path") {
		t.Fatalf("invalid config error=%v", err)
	}
	missingEstimate := &video.VideoChannel{
		BaseURL: "https://provider.example", Pricing: []byte(`{"mode":"upstream_estimate"}`),
		ExtraConfig: []byte(`{"adapter":{"profile":"json_task_v1","submit":{"path":"/tasks"},"poll":{"path":"/tasks/{task_id}"},"response":{"task_id_paths":["id"],"status_paths":["status"]}}}`),
	}
	if err := ValidateChannelConfig(missingEstimate); err == nil || !strings.Contains(err.Error(), "estimate operation") {
		t.Fatalf("missing estimate error=%v", err)
	}
	invalidProjection := &video.VideoChannel{
		BaseURL:     "https://provider.example",
		ExtraConfig: []byte(`{"adapter":{"profile":"json_task_v1","submit":{"path":"/tasks"},"poll":{"path":"/tasks/{task_id}"},"request":{"content_projections":[{"source":"asset","target":"image_url"}]},"response":{"task_id_paths":["id"],"status_paths":["status"]}}}`),
	}
	if err := ValidateChannelConfig(invalidProjection); err == nil || !strings.Contains(err.Error(), "source \"asset\" is not supported") {
		t.Fatalf("invalid projection error=%v", err)
	}
	config := testAdapterConfig()
	config.Validation.Models = map[string]validationRule{
		"seedance-2.5": {
			DurationMin: 4, DurationMax: 30, TaskModes: []string{"references"}, RequireMedia: true,
			AllowGeneratedAudio: boolPointer(false), MaxImages: 30, MaxMedia: 50,
			MediaDurationMin: 2, MediaDurationMax: 30,
		},
	}
	adapter := newTestAdapter("https://provider.example", "secret", http.DefaultClient, config)
	err := adapter.ValidateRequest(context.Background(), &video.GenerateRequest{Model: "seedance-2.5", Duration: 4, TaskMode: "references", Audio: true})
	if err == nil || !strings.Contains(err.Error(), "generated audio") {
		t.Fatalf("expected generated audio validation error, got %v", err)
	}
	if err := adapter.ValidateRequest(context.Background(), &video.GenerateRequest{Model: "seedance-2.5", Duration: 4, TaskMode: "references", Content: []video.ContentItem{{Type: "video_url", DurationSeconds: 1}}}); err == nil || !strings.Contains(err.Error(), "at least 2") {
		t.Fatalf("expected media duration validation error, got %v", err)
	}
}

func TestGenericAdapterSupportsOptionalResponseEnvelope(t *testing.T) {
	config := testAdapterConfig()
	config.Response.Root = ""
	config.Response.SuccessCodeOptional = true
	config.Response.TaskIDPaths = []string{"data.id", "id"}
	config.Response.StatusPaths = []string{"data.status", "status"}
	config.Response.VideoURLPaths = []string{"data.result.video_url", "result.video_url"}
	adapter := &Adapter{config: config}

	for _, body := range [][]byte{
		[]byte(`{"id":"direct-task","status":"queued"}`),
		[]byte(`{"code":0,"data":{"id":"wrapped-task","status":"queued"}}`),
	} {
		parsed, err := adapter.parseResponse(body, string(video.VideoTaskStatusSubmitted))
		if err != nil || parsed.ProviderTaskID == "" || parsed.Status != video.VideoTaskStatusSubmitted {
			t.Fatalf("parsed=%#v err=%v", parsed, err)
		}
	}
	if _, err := adapter.parseResponse([]byte(`{"code":400,"message":"invalid"}`), string(video.VideoTaskStatusSubmitted)); err == nil {
		t.Fatal("non-success response code should fail")
	}
}

func TestGenericAdapterConfiguresLocalCancellation(t *testing.T) {
	config := testAdapterConfig()
	config.LocalCancel.DisabledModels = []string{"seedance-2.5"}
	adapter := &Adapter{config: config}

	if !adapter.CanCancelLocal(&video.VideoTask{Model: "seedance-2.0", Status: video.VideoTaskStatusQueued}) {
		t.Fatal("seedance-2.0 queued task should support local cancellation")
	}
	if adapter.CanCancelLocal(&video.VideoTask{Model: "seedance-2.5", Status: video.VideoTaskStatusQueued}) {
		t.Fatal("seedance-2.5 queued task should reject local cancellation")
	}
}

func TestGenericAdapterClassifiesRetryablePollFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()
	config := testAdapterConfig()
	config.Poll.Path = "/tasks/{task_id}"
	adapter := newTestAdapter(server.URL, "secret", server.Client(), config)
	_, err := adapter.Poll(context.Background(), "task-1")
	if err == nil || !video.IsRetryableProviderError(err) {
		t.Fatalf("err=%v, want retryable provider error", err)
	}
}

func testAdapterConfig() adapterConfig {
	config := adapterConfig{
		Profile: ProfileJSONTaskV1,
		Submit:  operationConfig{Method: http.MethodPost, Path: "/tasks"},
		Poll:    operationConfig{Method: http.MethodGet, Path: "/tasks/{task_id}"},
		Response: responseConfig{
			Root: "data", SuccessCodePath: "code", SuccessCodeValues: []string{"0"},
			TaskIDPaths: []string{"id"}, StatusPaths: []string{"status"}, ProgressPaths: []string{"progress"},
			VideoURLPaths: []string{"result.video_url", "video_url"}, ThumbnailURLPaths: []string{"result.thumbnail_url"},
			DurationPaths: []string{"result.duration", "duration"}, ErrorPaths: []string{"error.message", "error"},
		},
	}
	config.defaults()
	return config
}

func boolPointer(value bool) *bool { return &value }

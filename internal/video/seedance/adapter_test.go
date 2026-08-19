package seedance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/mirainya/Prism/internal/video"
)

func TestBuildRequestUsesOfficialContentStructure(t *testing.T) {
	adapter := Codec{}
	request, err := adapter.BuildRequest(context.Background(), &video.GenerateRequest{
		Model: "seedance-2.0", Prompt: "使用@图片1和@视频1", TaskMode: "references",
		Resolution: "1080p", Ratio: "16:9", Duration: 5, Audio: true,
		Content: []video.ContentItem{
			{Type: "text", Text: "结合@音频1"},
			{Type: "image_url", Role: "reference_image", URL: "https://cdn.example/image.png"},
			{Type: "video_url", Role: "reference_video", URL: "https://cdn.example/video.mp4", DurationSeconds: 3},
		},
		Params: map[string]any{"camera_fixed": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]any{
		"model": "seedance-2.0", "resolution": "1080p", "ratio": "16:9", "duration": 5,
		"generate_audio": true, "camera_fixed": true,
		"content": []map[string]any{
			{"type": "text", "text": "使用@图片1和@视频1"},
			{"type": "text", "text": "结合@音频1"},
			{"type": "image_url", "role": "reference_image", "image_url": map[string]any{"url": "https://cdn.example/image.png"}},
			{"type": "video_url", "role": "reference_video", "video_url": map[string]any{"url": "https://cdn.example/video.mp4"}},
		},
	}
	if !reflect.DeepEqual(request.Body, expected) {
		t.Fatalf("body = %#v, want %#v", request.Body, expected)
	}
}

func TestBuildRequestRejectsUnresolvedAsset(t *testing.T) {
	adapter := Codec{}
	_, err := adapter.BuildRequest(context.Background(), &video.GenerateRequest{
		Model: "seedance-2.0", Prompt: "test",
		Content: []video.ContentItem{{Type: "image_url", AssetID: "asset-1"}},
	})
	if err == nil {
		t.Fatal("expected unresolved asset to fail")
	}
}

func TestBuildRequestRejectsUnknownOrInvalidParameters(t *testing.T) {
	adapter := Codec{}
	for _, params := range []map[string]any{
		{"unexpected": true},
		{"camera_fixed": "yes"},
	} {
		_, err := adapter.BuildRequest(context.Background(), &video.GenerateRequest{
			Model: "seedance-2.0", Prompt: "test", Params: params,
		})
		if err == nil {
			t.Fatalf("parameters %#v should fail", params)
		}
	}
}

func TestAdapterSupportsDirectAndEnvelopeResponses(t *testing.T) {
	var seenSubmit bool
	var seenCancel bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "missing auth", http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == defaultTaskPath:
			seenSubmit = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":123}`))
		case r.Method == http.MethodGet && r.URL.Path == defaultTaskPath+"/123":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"data":{"status":"succeeded","duration":4.5,"content":{"video_url":"https://cdn.example/video.mp4","last_frame_url":"https://cdn.example/last.png"}}}`))
		case r.Method == http.MethodDelete && r.URL.Path == defaultTaskPath+"/123":
			seenCancel = true
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := NewAdapter(&video.VideoChannel{BaseURL: server.URL}, &video.VideoChannelKey{APIKey: "secret"})

	request, err := adapter.BuildRequest(context.Background(), &video.GenerateRequest{Model: "seedance-2.0", Prompt: "test", TaskID: "request-1"})
	if err != nil {
		t.Fatal(err)
	}
	submitted, err := adapter.Submit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if submitted.ProviderTaskID != "123" || submitted.Status != video.VideoTaskStatusSubmitted {
		t.Fatalf("submit result = %#v", submitted)
	}

	progress, err := adapter.Poll(context.Background(), submitted.ProviderTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if progress.Status != video.VideoTaskStatusCompleted || progress.Percent != 100 || progress.Result == nil ||
		progress.Result.VideoURL != "https://cdn.example/video.mp4" || progress.Result.ThumbnailURL != "https://cdn.example/last.png" || progress.Result.Duration != 4.5 {
		t.Fatalf("poll result = %#v", progress)
	}
	if err := adapter.(video.Canceller).Cancel(context.Background(), submitted.ProviderTaskID); err != nil {
		t.Fatal(err)
	}
	if !seenSubmit || !seenCancel {
		t.Fatalf("submit seen=%t cancel seen=%t", seenSubmit, seenCancel)
	}
}

func TestAdapterOnlyCancelsSubmittedUpstreamTasks(t *testing.T) {
	adapter := NewAdapter(
		&video.VideoChannel{BaseURL: "https://provider.example"},
		&video.VideoChannelKey{APIKey: "secret"},
	).(video.Canceller)
	if !adapter.CanCancel(video.VideoTaskStatusSubmitted) {
		t.Fatal("submitted task should be cancellable")
	}
	for _, status := range []video.VideoTaskStatus{video.VideoTaskStatusQueued, video.VideoTaskStatusTracking} {
		if adapter.CanCancel(status) {
			t.Fatalf("status %q should not be cancellable", status)
		}
	}
}

func TestAdapterAcceptsDirectStringTaskID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"task-abc","status":"processing"}`))
	}))
	defer server.Close()
	adapter := NewAdapter(&video.VideoChannel{BaseURL: server.URL}, &video.VideoChannelKey{APIKey: "secret"})
	request := &video.ProviderRequest{Body: map[string]any{"prompt": "test"}}
	result, err := adapter.Submit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.ProviderTaskID != "task-abc" || result.Status != video.VideoTaskStatusTracking {
		t.Fatalf("result = %#v", result)
	}
}

func TestAdapterRejectsHTTPStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"rate limited"}`, http.StatusTooManyRequests)
	}))
	defer server.Close()
	adapter := NewAdapter(&video.VideoChannel{BaseURL: server.URL}, &video.VideoChannelKey{APIKey: "secret"})
	if _, err := adapter.Submit(context.Background(), &video.ProviderRequest{Body: map[string]any{}}); err == nil || !strings.Contains(err.Error(), "upstream HTTP 429") {
		t.Fatalf("HTTP status error = %v", err)
	}
}

func TestSubmitClassifiesHTTPFailures(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		retryable bool
		ambiguous bool
	}{
		{name: "server error", status: http.StatusInternalServerError, retryable: true, ambiguous: true},
		{name: "rate limited", status: http.StatusTooManyRequests, retryable: true},
		{name: "invalid request", status: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, http.StatusText(test.status), test.status)
			}))
			defer server.Close()
			adapter := NewAdapter(
				&video.VideoChannel{BaseURL: server.URL},
				&video.VideoChannelKey{APIKey: "secret"},
			)
			_, err := adapter.Submit(context.Background(), &video.ProviderRequest{Body: map[string]any{}})
			if err == nil {
				t.Fatal("expected submit to fail")
			}
			if got := video.IsRetryableProviderError(err); got != test.retryable {
				t.Fatalf("retryable=%t, want %t: %v", got, test.retryable, err)
			}
			if got := video.IsAmbiguousProviderError(err); got != test.ambiguous {
				t.Fatalf("ambiguous=%t, want %t: %v", got, test.ambiguous, err)
			}
		})
	}
}

func TestParseProviderTaskIDRejectsObjects(t *testing.T) {
	if _, err := parseProviderTaskID(json.RawMessage(`{"id":"nested"}`)); err == nil {
		t.Fatal("expected object task id to fail")
	}
}

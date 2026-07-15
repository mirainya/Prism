package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mirainya/Prism/internal/domain"
)

func TestBaseProviderSubmitReturnsRequestMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		if r.URL.Path != "/prefix/jobs/model-a" {
			t.Errorf("path = %s, want /prefix/jobs/model-a", r.URL.Path)
		}
		if r.URL.Query().Get("key") != "secret" {
			t.Errorf("query auth was not applied")
		}
		time.Sleep(5 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"job-1","status":"submitted"}`))
	}))
	defer server.Close()
	setProviderHTTPClient(t, server.Client())

	provider := &BaseProvider{
		BaseURL:         server.URL + "/prefix",
		APIKey:          "secret",
		AuthLocation:    "query",
		AuthKey:         "key",
		ContentType:     "application/json",
		RequestMethod:   http.MethodPut,
		SubmitPath:      "/jobs/{model}",
		Parser:          NewDefaultParser(),
		ResponseMapping: &ResponseMapping{TaskID: "id", Status: "status"},
	}

	result, err := provider.Submit(context.Background(), SubmitRequest{
		Params: map[string]any{"model": "model-a"},
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if result.ProviderTaskID != "job-1" || result.Status != StatusSubmitted {
		t.Fatalf("Submit() result = %#v", result)
	}
	assertRequestMetadata(t, result.RequestMetadata, http.MethodPut, "/prefix/jobs/model-a", http.StatusCreated)
}

func TestBaseProviderSubmitRetainsMetadataOnHTTPAndParseErrors(t *testing.T) {
	t.Run("http error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(5 * time.Millisecond)
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"limited"}}`))
		}))
		defer server.Close()
		setProviderHTTPClient(t, server.Client())

		provider := &BaseProvider{
			BaseURL:       server.URL,
			AuthLocation:  "none",
			ContentType:   "application/json",
			RequestMethod: http.MethodPost,
			SubmitPath:    "/submit",
			Parser:        NewDefaultParser(),
		}
		result, err := provider.Submit(context.Background(), SubmitRequest{})
		if domain.UpstreamStatusCode(err) != http.StatusTooManyRequests {
			t.Fatalf("Submit() error = %v, want status 429", err)
		}
		assertRequestMetadata(t, result.RequestMetadata, http.MethodPost, "/submit", http.StatusTooManyRequests)
	})

	t.Run("parse error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"job-1"}`))
		}))
		defer server.Close()
		setProviderHTTPClient(t, server.Client())

		provider := &BaseProvider{
			BaseURL:       server.URL,
			AuthLocation:  "none",
			ContentType:   "application/json",
			RequestMethod: http.MethodPost,
			SubmitPath:    "/submit",
			Parser:        failingResponseParser{err: errors.New("invalid response")},
		}
		result, err := provider.Submit(context.Background(), SubmitRequest{})
		if err == nil || err.Error() != "invalid response" {
			t.Fatalf("Submit() error = %v, want invalid response", err)
		}
		assertRequestMetadata(t, result.RequestMetadata, http.MethodPost, "/submit", http.StatusOK)
	})
}

func TestBaseProviderGetProgressReturnsMetadataOnSuccessAndError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantError  bool
	}{
		{name: "success", statusCode: http.StatusOK},
		{name: "upstream error", statusCode: http.StatusServiceUnavailable, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/prefix/tasks/task-1" {
					t.Errorf("request = %s %s", r.Method, r.URL.Path)
				}
				time.Sleep(5 * time.Millisecond)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.statusCode)
				if test.wantError {
					_, _ = w.Write([]byte(`{"message":"unavailable"}`))
					return
				}
				_, _ = w.Write([]byte(`{"status":"processing","progress":25}`))
			}))
			defer server.Close()
			setProviderHTTPClient(t, server.Client())

			provider := &BaseProvider{
				BaseURL:             server.URL + "/prefix",
				AuthLocation:        "none",
				PollMethod:          http.MethodGet,
				ProgressPath:        "/tasks/{task_id}",
				Parser:              NewDefaultParser(),
				PollResponseMapping: &ResponseMapping{Status: "status", Progress: "progress"},
			}
			result, err := provider.GetProgress(context.Background(), "task-1")
			if test.wantError {
				if domain.UpstreamStatusCode(err) != test.statusCode {
					t.Fatalf("GetProgress() error = %v, want status %d", err, test.statusCode)
				}
			} else {
				if err != nil {
					t.Fatalf("GetProgress() error = %v", err)
				}
				if result.Status != StatusProcessing || result.Progress != 25 {
					t.Fatalf("GetProgress() result = %#v", result)
				}
			}
			assertRequestMetadata(t, result.RequestMetadata, http.MethodGet, "/prefix/tasks/task-1", test.statusCode)
		})
	}
}

func TestBaseProviderReturnsMetadataWithoutHTTPResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	client := server.Client()
	baseURL := server.URL
	server.Close()
	setProviderHTTPClient(t, client)

	provider := &BaseProvider{
		BaseURL:       baseURL,
		AuthLocation:  "none",
		ContentType:   "application/json",
		RequestMethod: http.MethodPost,
		SubmitPath:    "/submit",
		Parser:        NewDefaultParser(),
	}
	result, err := provider.Submit(context.Background(), SubmitRequest{})
	if err == nil {
		t.Fatal("Submit() error = nil, want transport error")
	}
	assertRequestMetadata(t, result.RequestMetadata, http.MethodPost, "/submit", 0)
}

func setProviderHTTPClient(t *testing.T, client *http.Client) {
	t.Helper()
	previous := sharedHTTPClient
	sharedHTTPClient = client
	t.Cleanup(func() { sharedHTTPClient = previous })
}

func assertRequestMetadata(t *testing.T, metadata RequestMetadata, method, path string, statusCode int) {
	t.Helper()
	if metadata.Method != method || metadata.RequestPath != path || metadata.StatusCode != statusCode {
		t.Fatalf("request metadata = %#v, want method=%s path=%s status=%d", metadata, method, path, statusCode)
	}
	if metadata.RequestAt.IsZero() {
		t.Fatal("request metadata has zero RequestAt")
	}
	if metadata.DurationMs < 0 {
		t.Fatalf("request metadata duration = %d", metadata.DurationMs)
	}
}

type failingResponseParser struct {
	err error
}

func (p failingResponseParser) ParseSubmitResponse([]byte, *ResponseMapping) (SubmitResult, error) {
	return SubmitResult{}, p.err
}

func (p failingResponseParser) ParseProgressResponse([]byte, *ResponseMapping) (ProgressResult, error) {
	return ProgressResult{}, p.err
}

func (p failingResponseParser) ParseCallbackResponse([]byte, *ResponseMapping) (ProgressResult, string, error) {
	return ProgressResult{}, "", p.err
}

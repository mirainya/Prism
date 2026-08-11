package taskhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mirainya/Prism/internal/video"
)

func TestClientInjectsAuthenticationAndEscapesTaskID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.EscapedPath() != "/tasks/task%2Fone" {
			t.Fatalf("request = %s %s", request.Method, request.URL.EscapedPath())
		}
		if request.Header.Get("X-API-Key") != "Token secret" {
			t.Fatalf("authentication = %q", request.Header.Get("X-API-Key"))
		}
		_, _ = writer.Write([]byte(`{"status":"running"}`))
	}))
	defer server.Close()

	client := NewClient(Config{
		BaseURL: server.URL, APIKey: "secret", AuthHeader: "X-API-Key", AuthPrefix: "Token ", HTTPClient: server.Client(),
	})
	if _, err := client.Do(context.Background(), "poll", Operation{Method: http.MethodGet, Path: "/tasks/{task_id}"}, nil, nil, "task/one"); err != nil {
		t.Fatal(err)
	}
}

func TestClientInjectsQueryAuthentication(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("api_key") != "Token secret" {
			t.Fatalf("query authentication = %q", request.URL.RawQuery)
		}
		if request.Header.Get("api_key") != "" {
			t.Fatalf("query authentication leaked into header")
		}
		_, _ = writer.Write([]byte(`{"status":"running"}`))
	}))
	defer server.Close()

	client := NewClient(Config{
		BaseURL: server.URL, APIKey: "secret", AuthLocation: "query",
		AuthHeader: "api_key", AuthPrefix: "Token ", HTTPClient: server.Client(),
	})
	if _, err := client.Do(context.Background(), "poll", Operation{Method: http.MethodGet, Path: "/tasks"}, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestClientClassifiesSubmitAndPollFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Error(writer, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	client := NewClient(Config{BaseURL: server.URL, APIKey: "secret", HTTPClient: server.Client()})

	_, submitErr := client.Do(context.Background(), "submit", Operation{Method: http.MethodPost, Path: "/tasks"}, nil, nil)
	if !video.IsAmbiguousProviderError(submitErr) {
		t.Fatalf("submit error = %v, want ambiguous", submitErr)
	}
	_, pollErr := client.Do(context.Background(), "poll", Operation{Method: http.MethodGet, Path: "/tasks/1"}, nil, nil)
	if !video.IsRetryableProviderError(pollErr) || video.IsAmbiguousProviderError(pollErr) {
		t.Fatalf("poll error = %v, want retryable only", pollErr)
	}
}

func TestClientLimitsResponseSize(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte("12345"))
	}))
	defer server.Close()
	client := NewClient(Config{BaseURL: server.URL, APIKey: "secret", HTTPClient: server.Client(), MaxResponseBytes: 4})

	if _, err := client.Do(context.Background(), "poll", Operation{Method: http.MethodGet, Path: "/tasks/1"}, nil, nil); err == nil {
		t.Fatal("expected oversized response to fail")
	}
}

package engine

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/mirainya/Prism/internal/gateway/canonical"
)

func TestLogURLRemovesUserInfoAndSensitiveQueryValues(t *testing.T) {
	path, logged := logURL("https://upstream-user:secret@example.test/v1/models/vendor?apiKey=camel-secret&key=secret&keep=yes")
	if path != "/v1/models/vendor" {
		t.Fatalf("path=%q", path)
	}
	if strings.Contains(logged, "upstream-user") || strings.Contains(logged, "camel-secret") || strings.Contains(logged, "secret") {
		t.Fatalf("sensitive URL data leaked: %s", logged)
	}
	if !strings.Contains(logged, "keep=yes") || !strings.Contains(logged, "%5BREDACTED%5D") {
		t.Fatalf("unexpected logged URL: %s", logged)
	}
}

func TestRequestLoggingRedactsHeadersAndNestedJSON(t *testing.T) {
	headers := redactedHeaders(http.Header{
		"Authorization": []string{"Bearer secret"},
		"X-Api-Key":     []string{"secret"},
		"X-Test":        []string{"kept"},
	})
	if headers["Authorization"] != "[REDACTED]" || headers["X-Api-Key"] != "[REDACTED]" || headers["X-Test"] != "kept" {
		t.Fatalf("headers=%#v", headers)
	}
	raw := []byte(`{"api_key":"secret","nested":{"accessToken":"camel-secret","input":"ok"},"image":"data:image/png;base64,` + strings.Repeat("A", 1200) + `"}`)
	redacted := redactedJSON(raw)
	var value map[string]any
	if err := json.Unmarshal(redacted, &value); err != nil {
		t.Fatal(err)
	}
	nested, ok := value["nested"].(map[string]any)
	if !ok || value["api_key"] != "[REDACTED]" || nested["accessToken"] != "[REDACTED]" || nested["input"] != "ok" || value["image"] != "[OMITTED]" {
		t.Fatalf("redacted=%s", redacted)
	}
}

func TestResponseFromEventsUsesTerminalStateAndUsage(t *testing.T) {
	response := responseFromEvents([]canonical.Event{
		{Type: canonical.EventTextDelta, Delta: "part"},
		{Type: canonical.EventCompleted, Usage: &canonical.Usage{TotalTokens: 7}},
	})
	if response == nil || response.Status != "completed" || response.Usage == nil || response.Usage.TotalTokens != 7 {
		t.Fatalf("response=%#v", response)
	}
	if responseFromEvents([]canonical.Event{{Type: canonical.EventTextDelta, Delta: "part"}}) != nil {
		t.Fatal("non-terminal deltas manufactured a response")
	}
}

func TestResponsePreviewIsBounded(t *testing.T) {
	text := strings.Repeat("x", 1200)
	preview := responsePreview(&canonical.Response{Output: []canonical.Item{{Content: []canonical.Content{{Text: text}}}}})
	if len(preview) != 1000 || preview != text[:1000] {
		t.Fatalf("preview length=%d", len(preview))
	}
}

type statusError struct{}

func (statusError) Error() string   { return "limited" }
func (statusError) HTTPStatus() int { return http.StatusTooManyRequests }

func TestErrorStatusUsesProviderStatus(t *testing.T) {
	if got := errorStatus(statusError{}); got != http.StatusTooManyRequests {
		t.Fatalf("status=%d", got)
	}
	if got := errorStatus(errors.New("network")); got != http.StatusBadGateway {
		t.Fatalf("fallback status=%d", got)
	}
}

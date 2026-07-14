package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/gateway/transport"
	volcenginetransport "github.com/mirainya/Prism/internal/gateway/transport/volcengine"
)

func TestNormalizeAnthropicEventKeepsOnlyNativeRaw(t *testing.T) {
	foreign := canonical.Event{Type: canonical.EventRaw, RawType: "response.vendor.trace", Raw: json.RawMessage(`{"type":"response.vendor.trace"}`)}
	if normalizeAnthropicEvent(&foreign, "public-model", transport.OpenAIResponses) {
		t.Fatal("foreign Responses raw event was forwarded to Messages")
	}

	native := canonical.Event{Type: canonical.EventRaw, RawType: "ping", Raw: json.RawMessage(`{"type":"ping"}`)}
	if !normalizeAnthropicEvent(&native, "public-model", transport.AnthropicMessages) {
		t.Fatal("native Anthropic raw event was dropped")
	}

	known := canonical.Event{Type: canonical.EventTextDelta, RawType: "openai.chat.chunk", Raw: json.RawMessage(`{"vendor":true}`), Response: &canonical.Response{Model: "vendor-model"}}
	if !normalizeAnthropicEvent(&known, "public-model", transport.OpenAIChat) {
		t.Fatal("known converted event was dropped")
	}
	if len(known.Raw) != 0 || known.RawType != "" || known.Response.Model != "public-model" {
		t.Fatalf("known event was not normalized: %#v", known)
	}
}

func TestWriteAnthropicExecutionErrorPreservesTransportDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	writeAnthropicExecutionError(context, &volcenginetransport.HTTPError{
		Status:  http.StatusBadRequest,
		Details: &canonical.Error{Status: http.StatusBadRequest, Message: "max_tokens exceeds model limit", Type: "invalid_request_error"},
	})
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"message":"max_tokens exceeds model limit"`) {
		t.Fatalf("unexpected response: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestWriteAnthropicExecutionErrorClassifiesIncompatibleTransport(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	writeAnthropicExecutionError(context, routing.ErrNoCompatibleTransport)

	body := recorder.Body.String()
	if recorder.Code != http.StatusBadRequest ||
		!strings.Contains(body, `"type":"invalid_request_error"`) ||
		!strings.Contains(body, `"message":"The requested model does not support this Anthropic request"`) {
		t.Fatalf("unexpected response: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

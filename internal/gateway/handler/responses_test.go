package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/gateway/engine"
	responsepipeline "github.com/mirainya/Prism/internal/gateway/responses"
	"github.com/mirainya/Prism/internal/gateway/routing"
	volcenginetransport "github.com/mirainya/Prism/internal/gateway/transport/volcengine"
	"github.com/mirainya/Prism/internal/model"
	protocol "github.com/mirainya/Prism/internal/provider/responses"
	"github.com/mirainya/Prism/pkg/httputil"
)

func TestValidateResponseRequest(t *testing.T) {
	tests := []struct {
		name  string
		req   protocol.Request
		valid bool
	}{
		{"text input", protocol.Request{Model: "m", Input: json.RawMessage(`"hello"`)}, true},
		{"message input", protocol.Request{Model: "m", Input: json.RawMessage(`[{"role":"user","content":[{"type":"input_text","text":"hi"}]}]`)}, true},
		{"extended native fields", protocol.Request{Model: "m", Input: json.RawMessage(`"hello"`), Conversation: json.RawMessage(`"conv_1"`), Prompt: json.RawMessage(`{"id":"pmpt_1"}`), StreamOptions: json.RawMessage(`{"include_obfuscation":false}`), ContextManagement: json.RawMessage(`[]`), Thinking: json.RawMessage(`{"type":"enabled"}`), Caching: json.RawMessage(`{"type":"enabled"}`), Session: json.RawMessage(`{"id":"session_1"}`)}, true},
		{"missing model", protocol.Request{Input: json.RawMessage(`"hello"`)}, false},
		{"empty input", protocol.Request{Model: "m", Input: json.RawMessage(`[]`)}, false},
		{"background stream", protocol.Request{Model: "m", Input: json.RawMessage(`"x"`), Background: true, Stream: true}, false},
		{"invalid top logprobs", protocol.Request{Model: "m", Input: json.RawMessage(`"x"`), TopLogprobs: intPointer(21)}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message, _, _ := validateResponseRequest(&tt.req)
			if (message == "") != tt.valid {
				t.Fatalf("message=%q valid=%v", message, tt.valid)
			}
		})
	}
}

func TestSetResponsesRecordHeadersIncludesCallID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	setResponsesRecordHeaders(context, &responsepipeline.Result{
		CallID: "call_response", Record: &model.AIResponse{CallID: "call_response", RequestLogID: 42},
	})

	if got := recorder.Header().Get("X-Prism-Call-ID"); got != "call_response" {
		t.Fatalf("X-Prism-Call-ID = %q", got)
	}
	if got := recorder.Header().Get("X-Prism-Request-Log-ID"); got != "42" {
		t.Fatalf("X-Prism-Request-Log-ID = %q", got)
	}
}

func TestRespondResponsesErrorPreservesTransportDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	respondResponsesError(context, &volcenginetransport.HTTPError{
		Status:  http.StatusBadRequest,
		Details: &canonical.Error{Status: http.StatusBadRequest, Message: "max_output_tokens exceeds model limit", Type: "invalid_request_error", Code: "invalid_parameter"},
	})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	body := recorder.Body.String()
	for _, expected := range []string{`"message":"max_output_tokens exceeds model limit"`, `"type":"invalid_request_error"`, `"code":"invalid_parameter"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("response missing %s: %s", expected, body)
		}
	}
}

func TestRespondResponsesErrorClassifiesIncompatibleTransport(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)

	respondResponsesError(context, routing.ErrNoCompatibleTransport)

	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"code":"unsupported_model_capability"`) {
		t.Fatalf("unexpected response: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestRespondResponsesErrorClassifiesMissingTransportPlan(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)

	respondResponsesError(context, engine.ErrNoTransportPlan)

	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"code":"unsupported_model_capability"`) {
		t.Fatalf("unexpected response: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func intPointer(value int) *int { return &value }

func TestRespondResponsesErrorPreservesUpstreamHTTPError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	param := "model"
	respondResponsesError(context, &httputil.HTTPError{Status: http.StatusTooManyRequests, Message: "rate limited", Type: "rate_limit_error", Code: "rate_limit_exceeded", Param: &param})
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusTooManyRequests)
	}
	body := recorder.Body.String()
	for _, expected := range []string{`"message":"rate limited"`, `"type":"rate_limit_error"`, `"code":"rate_limit_exceeded"`, `"param":"model"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("response missing %s: %s", expected, body)
		}
	}
}

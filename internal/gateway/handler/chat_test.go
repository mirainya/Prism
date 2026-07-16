package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/openaierror"
	"github.com/mirainya/Prism/internal/gateway/engine"
	"github.com/mirainya/Prism/internal/gateway/routing"
)

func TestCompletionsRejectsInvalidRequestsWithOpenAIError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name      string
		body      string
		wantParam string
		wantCode  string
	}{
		{"unknown field", `{"model":"m","messages":[{"role":"user","content":"hi"}],"unknown":true}`, "unknown", "unsupported_parameter"},
		{"missing model", `{"messages":[{"role":"user","content":"hi"}]}`, "model", "missing_required_parameter"},
		{"missing messages", `{"model":"m","messages":[]}`, "messages", "missing_required_parameter"},
		{"unsupported content", `{"model":"m","messages":[{"role":"user","content":[{"type":"video_url"}]}]}`, "messages[0].content[0].type", "unsupported_multimodal_input"},
		{"invalid content", `{"model":"m","messages":[{"role":"user","content":42}]}`, "messages[0].content", "invalid_type"},
		{"null user content", `{"model":"m","messages":[{"role":"user","content":null}]}`, "messages[0].content", "missing_required_parameter"},
		{"invalid audio", `{"model":"m","messages":[{"role":"user","content":[{"type":"input_audio","input_audio":{"format":"wav"}}]}]}`, "messages[0].content[0].input_audio.data", "missing_required_parameter"},
		{"stream options without stream", `{"model":"m","messages":[{"role":"user","content":"hi"}],"stream_options":{"include_usage":true}}`, "stream_options", "invalid_value"},
		{"streaming multiple choices", `{"model":"m","messages":[{"role":"user","content":"hi"}],"stream":true,"n":2}`, "n", "unsupported_value"},
		{"too many choices", `{"model":"m","messages":[{"role":"user","content":"hi"}],"n":17}`, "n", "invalid_value"},
		{"conflicting token limits", `{"model":"m","messages":[{"role":"user","content":"hi"}],"max_tokens":10,"max_completion_tokens":10}`, "max_completion_tokens", "invalid_value"},
		{"top logprobs without logprobs", `{"model":"m","messages":[{"role":"user","content":"hi"}],"top_logprobs":2}`, "top_logprobs", "invalid_value"},
		{"unsupported modality", `{"model":"m","messages":[{"role":"user","content":"hi"}],"modalities":["video"]}`, "modalities[0]", "unsupported_value"},
		{"audio config without modality", `{"model":"m","messages":[{"role":"user","content":"hi"}],"audio":{"format":"wav","voice":"alloy"}}`, "audio", "invalid_value"},
		{"unknown content part field", `{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"hi","extra":true}]}]}`, "messages[0].content[0].extra", "unsupported_parameter"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := gin.New()
			router.POST("/v1/chat/completions", NewChatHandler(nil).Completions)
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(tt.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", response.Code, response.Body.String())
			}
			var body openaierror.Body
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body.Error.Type != "invalid_request_error" || body.Error.Param == nil || *body.Error.Param != tt.wantParam || body.Error.Code != tt.wantCode {
				t.Fatalf("unexpected error body: %+v", body.Error)
			}
		})
	}
}

func TestStopSequencesAcceptsStringAndArray(t *testing.T) {
	for _, raw := range []string{`"stop"`, `["one","two"]`} {
		var got stopSequences
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Fatalf("UnmarshalJSON(%s): %v", raw, err)
		}
		if len(got) == 0 {
			t.Fatalf("UnmarshalJSON(%s) returned no stop sequences", raw)
		}
	}
}

func TestStopSequencesAcceptsNull(t *testing.T) {
	got := stopSequences{"existing"}
	if err := json.Unmarshal([]byte(`null`), &got); err != nil {
		t.Fatalf("UnmarshalJSON(null): %v", err)
	}
	if got != nil {
		t.Fatalf("UnmarshalJSON(null) = %#v, want nil", got)
	}
}

func TestRespondChatPipelineErrorClassifiesRoutingErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		err        error
		wantStatus int
		wantCode   string
	}{
		{routing.ErrModelNotFound, http.StatusNotFound, "model_not_found"},
		{routing.ErrCapabilityUnavailable, http.StatusBadRequest, "unsupported_model_capability"},
		{routing.ErrNoCompatibleTransport, http.StatusBadRequest, "unsupported_model_capability"},
		{engine.ErrNoTransportPlan, http.StatusBadRequest, "unsupported_model_capability"},
		{routing.ErrNoRoute, http.StatusServiceUnavailable, "model_unavailable"},
	}
	for _, tt := range tests {
		response := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(response)
		respondChatPipelineError(ctx, tt.err)
		if response.Code != tt.wantStatus || !strings.Contains(response.Body.String(), tt.wantCode) {
			t.Fatalf("error %v: status=%d body=%s", tt.err, response.Code, response.Body.String())
		}
	}
}

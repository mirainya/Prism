package openai_responses

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mirainya/Prism/internal/gateway/canonical"
	protocol "github.com/mirainya/Prism/internal/provider/responses"
)

func TestDecodeRequestPreservesExtensionsAndVideo(t *testing.T) {
	var request protocol.Request
	if err := json.Unmarshal([]byte(`{
		"model":"doubao","input":[{"role":"user","content":[
			{"type":"input_text","text":"describe"},
			{"type":"input_video","video_url":"https://example.test/v.mp4","fps":2}
		]}],"thinking":{"type":"enabled"},"future_control":{"enabled":true}
	}`), &request); err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Endpoint != canonical.EndpointOpenAIResponses || len(decoded.Items) != 1 || len(decoded.Items[0].Content) != 2 {
		t.Fatalf("unexpected request: %#v", decoded)
	}
	video := decoded.Items[0].Content[1]
	if video.Type != "input_video" || video.URL != "https://example.test/v.mp4" || string(video.Extra["fps"]) != "2" {
		t.Fatalf("video was not preserved: %#v", video)
	}
	if decoded.ProviderOptions.Volcengine == nil || len(decoded.ProviderOptions.Volcengine.Thinking) == 0 || len(decoded.ProviderOptions.Volcengine.Unknown["future_control"]) == 0 {
		t.Fatalf("Volcengine extensions missing: %#v", decoded.ProviderOptions)
	}
	if len(decoded.ClientExtensions[extraRequest]) == 0 {
		t.Fatal("client extension copy missing")
	}
}

func TestDecodeRequestNormalizesCallsAudioFilesAndKnownExtras(t *testing.T) {
	var request protocol.Request
	if err := json.Unmarshal([]byte(`{
		"model":"gpt","input":[
			{"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup","arguments":"{\"q\":\"x\"}"},
			{"type":"function_call_output","call_id":"call_1","output":"ok"},
			{"role":"user","content":[
				{"type":"input_audio","input_audio":{"data":"YWJj","format":"wav"}},
				{"type":"input_file","file_data":"ZmlsZQ==","filename":"notes.txt","content_type":"text/plain"}
			]}
		],"conversation":"conv_1","max_tool_calls":3,"text":{"format":{"type":"text"},"verbosity":"low"}
	}`), &request); err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if got := decoded.Items[0]; got.CallID != "call_1" || string(got.Arguments) != `{"q":"x"}` {
		t.Fatalf("function call = %#v", got)
	}
	if got := decoded.Items[2].Content[0]; got.Data != "YWJj" || got.Format != "wav" {
		t.Fatalf("audio = %#v", got)
	}
	if got := decoded.Items[2].Content[1]; got.Filename != "notes.txt" || got.MediaType != "text/plain" {
		t.Fatalf("file = %#v", got)
	}
	if extras := string(decoded.ClientExtensions[extraRequest]); !strings.Contains(extras, `"conversation":"conv_1"`) || !strings.Contains(extras, `"max_tool_calls":3`) || !strings.Contains(extras, `"verbosity":"low"`) {
		t.Fatalf("known extensions were lost: %s", extras)
	}
}

func TestEncodeResponsePreservesProviderAndUsageExtensions(t *testing.T) {
	encoded, err := EncodeResponseJSON(canonical.Response{
		ID: "resp_1", Model: "public", Status: "completed", CreatedAt: 7,
		Output:             []canonical.Item{{ID: "msg_1", Type: "message", Role: canonical.RoleAssistant, Content: []canonical.Content{{Type: "output_text", Text: "done"}}}},
		Usage:              &canonical.Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5, Extra: map[string]json.RawMessage{"tool_usage": json.RawMessage(`{"web_search":1}`)}},
		ProviderExtensions: map[string]json.RawMessage{"service_status": json.RawMessage(`{"tier":"fast"}`), "future_response": json.RawMessage(`true`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	if string(result["future_response"]) != "true" || !strings.Contains(string(result["service_status"]), `"tier":"fast"`) || !strings.Contains(string(result["usage"]), "tool_usage") || !strings.Contains(string(result["output"]), "output_text") {
		t.Fatalf("extensions were lost: %s", encoded)
	}
}

func TestEncodeResponseUsesWireStringsForCalls(t *testing.T) {
	encoded, err := EncodeResponseJSON(canonical.Response{
		ID: "resp_1", Model: "gpt", Output: []canonical.Item{
			{ID: "fc_1", Type: "function_call", CallID: "call_1", Name: "lookup", Arguments: json.RawMessage(`{"q":"x"}`)},
			{Type: "function_call_output", CallID: "call_1", Output: json.RawMessage(`"ok"`)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Output []struct {
			CallID    string `json:"call_id"`
			Arguments string `json:"arguments"`
			Output    string `json:"output"`
		} `json:"output"`
	}
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	if result.Output[0].CallID != "call_1" || result.Output[0].Arguments != `{"q":"x"}` || result.Output[1].Output != "ok" {
		t.Fatalf("response = %s", encoded)
	}
}

func TestEncodeSSEFrameUsesResponsesWireFormat(t *testing.T) {
	frame, err := EncodeSSEFrame(canonical.Event{Type: canonical.EventTextDelta, SequenceNumber: 3, OutputIndex: 1, ContentIndex: 0, Item: &canonical.Item{ID: "msg_1"}, Delta: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	text := string(frame)
	if !strings.HasPrefix(text, "event: response.output_text.delta\ndata: ") || !strings.Contains(text, `"sequence_number":3`) || !strings.Contains(text, `"item_id":"msg_1"`) || !strings.Contains(text, `"content_index":0`) {
		t.Fatalf("invalid Responses SSE frame: %s", text)
	}

	errorFrame, err := EncodeSSEFrame(canonical.Event{Type: canonical.EventError, Error: &canonical.Error{Code: "rate_limit", Message: "slow down", Param: "model"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(errorFrame), "event: error") || !strings.Contains(string(errorFrame), `"code":"rate_limit"`) || strings.Contains(string(errorFrame), `"error":{`) {
		t.Fatalf("invalid error frame: %s", errorFrame)
	}
}

func TestEncodeSSEFrameIgnoresStandardRawAndBuildsTerminalResponse(t *testing.T) {
	frame, err := EncodeSSEFrame(canonical.Event{
		Type: canonical.EventToolArgumentsDelta, SequenceNumber: 0, OutputIndex: 0,
		Item: &canonical.Item{ID: "fc_1", CallID: "call_1"}, Delta: `{"q":`,
		Raw: json.RawMessage(`{"choices":[{"delta":{"content":"wrong protocol"}}]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(frame)
	if !strings.Contains(text, `"sequence_number":0`) || !strings.Contains(text, `"output_index":0`) || !strings.Contains(text, `"item_id":"fc_1"`) || strings.Contains(text, "choices") {
		t.Fatalf("tool delta = %s", frame)
	}

	terminal, err := EncodeSSEFrame(canonical.Event{Type: canonical.EventFailed, SequenceNumber: 4, ProviderResponseID: "resp_1", Error: &canonical.Error{Type: "server_error", Code: "upstream", Message: "failed"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(terminal), "event: response.failed") || !strings.Contains(string(terminal), `"status":"failed"`) || !strings.Contains(string(terminal), `"response":{`) {
		t.Fatalf("terminal = %s", terminal)
	}

	errorFrame, err := EncodeSSEFrame(canonical.Event{Type: canonical.EventError, Error: &canonical.Error{Type: "server_error", Code: "upstream", Message: "failed"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(errorFrame), `"type":"error"`) || strings.Contains(string(errorFrame), `"type":"server_error"`) {
		t.Fatalf("error = %s", errorFrame)
	}
}

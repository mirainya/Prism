package openai_chat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/provider/chat"
)

func TestDecodeRequestMapsMultimodalToolsAndExtensions(t *testing.T) {
	max := 128
	strict := true
	serviceTier := "auto"
	request := chat.ChatRequest{Model: "public", MaxCompletionTokens: &max, ServiceTier: &serviceTier, Messages: []chat.ChatMessage{{Role: "user", Content: []any{map[string]any{"type": "text", "text": "hi"}, map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.test/a.png", "detail": "low"}}}}}, Tools: []chat.ToolDefinition{{Type: "function", Function: chat.FunctionDef{Name: "weather", Parameters: json.RawMessage(`{"type":"object"}`), Strict: &strict}}}, ToolChoice: map[string]any{"type": "function", "function": map[string]any{"name": "weather"}}, ExtraBody: map[string]any{"frequency_penalty": 0.2, "vendor_flag": true}}
	decoded, err := DecodeRequest(request)
	if err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if decoded.Endpoint != canonical.EndpointOpenAIChat || decoded.MaxOutputTokens == nil || *decoded.MaxOutputTokens != 128 {
		t.Fatalf("bad request mapping: %#v", decoded)
	}
	if decoded.ServiceTier != "auto" {
		t.Fatalf("service tier = %q", decoded.ServiceTier)
	}
	if len(decoded.Items) != 1 || len(decoded.Items[0].Content) != 2 || decoded.Items[0].Content[1].URL == "" {
		t.Fatalf("multimodal mapping failed: %#v", decoded.Items)
	}
	if len(decoded.Tools) != 1 || decoded.Tools[0].Name != "weather" || decoded.ToolChoice.Name != "weather" {
		t.Fatalf("tool mapping failed: %#v %#v", decoded.Tools, decoded.ToolChoice)
	}
	if len(decoded.ClientExtensions[extraRequest]) == 0 {
		t.Fatal("unmodeled request fields were lost")
	} else if strings.Contains(string(decoded.ClientExtensions[extraRequest]), `"service_tier"`) {
		t.Fatalf("service tier remained an extension: %s", decoded.ClientExtensions[extraRequest])
	}
}

func TestDecodeRequestSplitsToolHistoryAndKeepsMediaFields(t *testing.T) {
	request := chat.ChatRequest{
		Model: "public", Stop: []string{"END"}, User: "user_1", Modalities: []string{"text", "audio"},
		Messages: []chat.ChatMessage{
			{Role: "assistant", Content: nil, ToolCalls: []chat.ToolCall{{ID: "call_1", Type: "function", Function: chat.FunctionCall{Name: "lookup", Arguments: `{"q":"x"}`}}}},
			{Role: "tool", ToolCallID: "call_1", Content: "ok"},
			{Role: "user", Content: []any{
				map[string]any{"type": "file", "file": map[string]any{"file_data": "ZmlsZQ==", "filename": "notes.txt"}},
				map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": "YWJj", "format": "wav"}},
			}},
		},
	}
	decoded, err := DecodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Items) != 3 || decoded.Items[0].Type != "function_call" || decoded.Items[0].CallID != "call_1" || string(decoded.Items[0].Arguments) != `{"q":"x"}` {
		t.Fatalf("tool call history = %#v", decoded.Items)
	}
	if decoded.Items[1].Type != "function_call_output" || decoded.Items[1].CallID != "call_1" || string(decoded.Items[1].Output) != `"ok"` {
		t.Fatalf("tool output = %#v", decoded.Items[1])
	}
	if file := decoded.Items[2].Content[0]; file.Filename != "notes.txt" || file.Data != "ZmlsZQ==" {
		t.Fatalf("file = %#v", file)
	}
	if audio := decoded.Items[2].Content[1]; audio.Format != "wav" || audio.Data != "YWJj" {
		t.Fatalf("audio = %#v", audio)
	}
	if len(decoded.Stop) != 1 || decoded.User != "user_1" || len(decoded.Modalities) != 2 {
		t.Fatalf("request controls = %#v", decoded)
	}
}

func TestEncodeResponseJSONPreservesChoicesToolsAndExtensions(t *testing.T) {
	response := canonical.Response{ID: "chatcmpl_1", Model: "public", CreatedAt: 9, FinishReason: "stop", Usage: &canonical.Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}, ProviderExtensions: map[string]json.RawMessage{extraResponse: json.RawMessage(`{"custom_response_field":{"enabled":true}}`), extraSystemFP: json.RawMessage(`"fp_1"`)}, Output: []canonical.Item{{Type: "message", Role: canonical.RoleAssistant, Content: []canonical.Content{{Type: "input_text", Text: "done"}}, Extra: map[string]json.RawMessage{extraChoiceIndex: json.RawMessage(`1`), extraFinishReason: json.RawMessage(`"tool_calls"`)}}, {Type: "function_call", Name: "weather", CallID: "call_1", Arguments: json.RawMessage(`{}`), Extra: map[string]json.RawMessage{extraChoiceIndex: json.RawMessage(`1`)}}}}
	encoded, err := EncodeResponseJSON(response)
	if err != nil {
		t.Fatalf("encode response: %v", err)
	}
	for _, want := range []string{`"index":1`, `"tool_calls"`, `"custom_response_field":{"enabled":true}`, `"system_fingerprint":"fp_1"`} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("missing %s in %s", want, encoded)
		}
	}
}

func TestEncodeResponseNormalizesCrossProtocolReasoningAndFinishReason(t *testing.T) {
	response := canonical.Response{ID: "chatcmpl_1", Model: "public", FinishReason: "tool_use", Output: []canonical.Item{
		{Type: "reasoning", Content: []canonical.Content{{Type: "reasoning_text", Text: "think"}}},
		{Type: "function_call", ID: "fc_1", Name: "lookup", Arguments: json.RawMessage(`"{\"q\":\"x\"}"`)},
	}}
	encoded, err := EncodeResponseJSON(response)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"reasoning_content":"think"`) || !strings.Contains(string(encoded), `"finish_reason":"tool_calls"`) || !strings.Contains(string(encoded), `"arguments":"{\"q\":\"x\"}"`) || !strings.Contains(string(encoded), `"id":"fc_1"`) {
		t.Fatalf("response = %s", encoded)
	}
}

func TestSSEFrameDecodesAndReencodesCanonicalDeltaAndDone(t *testing.T) {
	frame := []byte("data: {\"id\":\"chatcmpl_1\",\"choices\":[{\"index\":2,\"delta\":{\"role\":\"assistant\",\"content\":\"hi\"},\"finish_reason\":null}]}\n\n")
	event, done, err := DecodeSSEFrame(frame)
	if err != nil || done {
		t.Fatalf("decode: %#v done=%v err=%v", event, done, err)
	}
	if event.Type != canonical.EventTextDelta || event.ChoiceIndex != 2 || event.Delta != "hi" {
		t.Fatalf("bad event: %#v", event)
	}
	encoded, err := EncodeSSEFrame(event)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !strings.Contains(string(encoded), `"object":"chat.completion.chunk"`) || !strings.Contains(string(encoded), `"content":"hi"`) || !strings.Contains(string(encoded), `"id":"chatcmpl_1"`) {
		t.Fatalf("canonical frame = %s", encoded)
	}
	_, done, err = DecodeSSEFrame([]byte("data: [DONE]\n\n"))
	if err != nil || !done {
		t.Fatalf("done parse failed: %v %v", done, err)
	}
}

func TestEncodeSSEToolStartDeltaAndTerminal(t *testing.T) {
	start, err := EncodeSSEFrame(canonical.Event{
		Type: canonical.EventOutputItemAdded, ToolIndex: 0,
		Item: &canonical.Item{ID: "fc_1", CallID: "call_1", Type: "function_call", Name: "lookup"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(start), `"tool_calls"`) || !strings.Contains(string(start), `"id":"call_1"`) || !strings.Contains(string(start), `"name":"lookup"`) || !strings.Contains(string(start), `"type":"function"`) {
		t.Fatalf("tool start frame = %s", start)
	}

	frame, err := EncodeSSEFrame(canonical.Event{
		Type: canonical.EventToolArgumentsDelta, ToolIndex: 0,
		Item: &canonical.Item{ID: "fc_1", CallID: "call_1", Type: "function_call", Name: "lookup"}, Delta: `{"q":`,
		Raw: json.RawMessage(`{"type":"content_block_delta","delta":{"text":"wrong protocol"}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(frame), `"tool_calls"`) || !strings.Contains(string(frame), `"arguments":"{\"q\":"`) || strings.Contains(string(frame), `"id":"call_1"`) || strings.Contains(string(frame), `"id":"fc_1"`) || strings.Contains(string(frame), `"name":"lookup"`) || strings.Contains(string(frame), `"type":"function"`) || strings.Contains(string(frame), "content_block_delta") {
		t.Fatalf("tool frame = %s", frame)
	}

	terminal, err := EncodeSSEFrame(canonical.Event{Type: canonical.EventIncomplete, Usage: &canonical.Usage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(terminal), `"finish_reason":"length"`) || !strings.Contains(string(terminal), `"usage":`) || !strings.HasSuffix(string(terminal), "data: [DONE]\n\n") {
		t.Fatalf("terminal = %s", terminal)
	}
}

func TestDecodeSSEToolCallUsageAndError(t *testing.T) {
	event, _, err := DecodeSSEFrame([]byte("data: {\"id\":\"chatcmpl_1\",\"model\":\"gpt\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":2,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"lookup\",\"arguments\":\"{\\\"q\\\":\"}}]},\"finish_reason\":null}]}\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	if event.Type != canonical.EventToolArgumentsDelta || event.ToolIndex != 2 || event.Item == nil || event.Item.CallID != "call_1" {
		t.Fatalf("tool event = %#v", event)
	}

	usage, _, err := DecodeSSEFrame([]byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3,\"total_tokens\":5,\"prompt_tokens_details\":{\"cached_tokens\":1},\"completion_tokens_details\":{\"reasoning_tokens\":2}}}\n\n"))
	if err != nil || usage.Usage == nil || usage.Usage.CachedInputTokens != 1 || usage.Usage.ReasoningOutputTokens != 2 {
		t.Fatalf("usage event = %#v err=%v", usage, err)
	}

	errorEvent, _, err := DecodeSSEFrame([]byte("data: {\"error\":{\"message\":\"bad\",\"type\":\"server_error\",\"code\":null}}\n\n"))
	if err != nil || errorEvent.Error == nil || errorEvent.Error.Code != "" {
		t.Fatalf("error event = %#v err=%v", errorEvent, err)
	}
}

func TestEncodeSyntheticSSEError(t *testing.T) {
	frame, err := EncodeSSEFrame(canonical.Event{Type: canonical.EventError, Error: &canonical.Error{Message: "bad", Type: "invalid_request_error", Code: "bad_request"}})
	if err != nil {
		t.Fatalf("encode error: %v", err)
	}
	if !strings.Contains(string(frame), `"error"`) || !strings.Contains(string(frame), `"bad_request"`) {
		t.Fatalf("bad error frame: %s", frame)
	}
}

func TestEncodeResponseKeepsAssistantTextAlongsideToolCalls(t *testing.T) {
	response := canonical.Response{ID: "chatcmpl_1", Model: "public", FinishReason: "tool_use", Output: []canonical.Item{
		{Type: "message", Role: canonical.RoleAssistant, Content: []canonical.Content{{Type: "output_text", Text: "让我查一下天气"}}},
		{Type: "function_call", CallID: "call_1", Name: "weather", Arguments: json.RawMessage(`{"city":"tokyo"}`)},
	}}
	encoded, err := EncodeResponseJSON(response)
	if err != nil {
		t.Fatalf("encode response: %v", err)
	}
	if !strings.Contains(string(encoded), `"content":"让我查一下天气"`) {
		t.Fatalf("assistant text dropped: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"name":"weather"`) || !strings.Contains(string(encoded), `"finish_reason":"tool_calls"`) {
		t.Fatalf("tool call missing: %s", encoded)
	}
}

func TestEncodeResponseLiftsRefusalAndAudioParts(t *testing.T) {
	response := canonical.Response{ID: "chatcmpl_1", Model: "public", FinishReason: "stop", Output: []canonical.Item{
		{Type: "message", Role: canonical.RoleAssistant, Content: []canonical.Content{
			{Type: "output_text", Text: "hi"},
			{Type: "refusal", Text: "I cannot help"},
			{Type: "output_audio", Data: "YWJj", Transcript: "spoken"},
		}},
	}}
	encoded, err := EncodeResponseJSON(response)
	if err != nil {
		t.Fatalf("encode must not fail on refusal/audio parts: %v", err)
	}
	for _, want := range []string{`"refusal":"I cannot help"`, `"data":"YWJj"`, `"transcript":"spoken"`, `"content":"hi"`} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("missing %s in %s", want, encoded)
		}
	}
}

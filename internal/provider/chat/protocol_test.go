package chat

import (
	"encoding/json"
	"testing"
)

func TestChatResponsePreservesOpenAIFields(t *testing.T) {
	raw := []byte(`{"id":"x","object":"chat.completion","created":1,"model":"m","system_fingerprint":"fp","service_tier":"flex","choices":[{"index":0,"message":{"role":"assistant","content":null,"refusal":"no","annotations":[{"type":"url_citation"}],"audio":{"id":"a"}},"finish_reason":"stop","logprobs":{"content":[]}}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3,"prompt_tokens_details":{"cached_tokens":1},"completion_tokens_details":{"reasoning_tokens":2}}}`)
	var response ChatResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"system_fingerprint", "service_tier"} {
		if got[field] == nil {
			t.Fatalf("response lost %s: %s", field, encoded)
		}
	}
	choice := got["choices"].([]any)[0].(map[string]any)
	message := choice["message"].(map[string]any)
	for _, field := range []string{"refusal", "annotations", "audio"} {
		if message[field] == nil {
			t.Fatalf("message lost %s: %s", field, encoded)
		}
	}
	if choice["logprobs"] == nil {
		t.Fatalf("choice lost logprobs: %s", encoded)
	}
}

func TestChatRequestPreservesNewOpenAIFields(t *testing.T) {
	strict := true
	request := ChatRequest{
		Model: "m", Messages: []ChatMessage{{Role: "user", Content: "hi"}},
		MaxCompletionTokens: intPointer(256), N: intPointer(2), Logprobs: boolPointer(true), TopLogprobs: intPointer(3),
		ParallelToolCalls: boolPointer(false), Modalities: []string{"text"}, Store: boolPointer(true),
		Metadata: map[string]string{"tenant": "a"}, ServiceTier: stringPointer("flex"),
		Tools: []ToolDefinition{{Type: "function", Function: FunctionDef{Name: "f", Strict: &strict}}},
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"max_completion_tokens", "n", "logprobs", "top_logprobs", "parallel_tool_calls", "modalities", "store", "metadata", "service_tier"} {
		if got[field] == nil {
			t.Fatalf("request lost %s: %s", field, encoded)
		}
	}
	tools := got["tools"].([]any)
	function := tools[0].(map[string]any)["function"].(map[string]any)
	if function["strict"] != true {
		t.Fatalf("function strict lost: %s", encoded)
	}
}

func intPointer(value int) *int          { return &value }
func boolPointer(value bool) *bool       { return &value }
func stringPointer(value string) *string { return &value }

func TestAnthropicToolConversationConversion(t *testing.T) {
	request := &ChatRequest{Model: "claude", Messages: []ChatMessage{
		{Role: "user", Content: "weather"},
		{Role: "assistant", Content: nil, ToolCalls: []ToolCall{{ID: "call_1", Type: "function", Function: FunctionCall{Name: "weather", Arguments: `{"city":"Shanghai"}`}}}},
		{Role: "tool", ToolCallID: "call_1", Content: `{"temperature":30}`},
	}}
	body := ConvertRequestToAnthropic(request)
	messages := body["messages"].([]map[string]any)
	assistant := messages[1]["content"].([]map[string]any)
	if assistant[0]["type"] != "tool_use" || assistant[0]["id"] != "call_1" {
		t.Fatalf("assistant tool call not converted: %#v", messages[1])
	}
	result := messages[2]["content"].([]map[string]any)
	if result[0]["type"] != "tool_result" || result[0]["tool_use_id"] != "call_1" {
		t.Fatalf("tool result not converted: %#v", messages[2])
	}
}

func TestGoogleToolConversationConversion(t *testing.T) {
	provider := NewGoogleProvider(ProviderConfig{VendorModel: "gemini"})
	request := &ChatRequest{Messages: []ChatMessage{
		{Role: "assistant", Content: nil, ToolCalls: []ToolCall{
			{ID: "call_1", Type: "function", Function: FunctionCall{Name: "weather", Arguments: `{"city":"Shanghai"}`}},
			{ID: "call_2", Type: "function", Function: FunctionCall{Name: "time", Arguments: `{"zone":"Asia/Shanghai"}`}},
		}},
		{Role: "tool", ToolCallID: "call_1", Content: `{"temperature":30}`},
		{Role: "tool", ToolCallID: "call_2", Content: `{"hour":13}`},
	}}
	body := provider.convertRequest(request)
	contents := body["contents"].([]map[string]any)
	assistantParts := contents[0]["parts"].([]map[string]any)
	if assistantParts[len(assistantParts)-1]["functionCall"] == nil {
		t.Fatalf("assistant tool call not converted: %#v", contents[0])
	}
	toolParts := contents[1]["parts"].([]map[string]any)
	response := toolParts[0]["functionResponse"].(map[string]any)
	if response["name"] != "weather" {
		t.Fatalf("tool result name = %#v", response["name"])
	}
	if len(contents) != 2 || len(toolParts) != 2 {
		t.Fatalf("parallel tool results were not grouped: %#v", contents)
	}
}

func TestStandardFileContentConversion(t *testing.T) {
	content := []any{map[string]any{
		"type": "file",
		"file": map[string]any{
			"filename":  "notes.pdf",
			"file_data": "data:application/pdf;base64,YWJj",
		},
	}}

	anthropic := ConvertToAnthropicContent(content)
	if len(anthropic) != 1 || anthropic[0]["type"] != "document" {
		t.Fatalf("Anthropic file content was dropped: %#v", anthropic)
	}
	anthropicSource := anthropic[0]["source"].(map[string]any)
	if anthropicSource["media_type"] != "application/pdf" || anthropicSource["data"] != "YWJj" {
		t.Fatalf("unexpected Anthropic file source: %#v", anthropicSource)
	}

	google := convertToGeminiParts(content)
	if len(google) != 1 || google[0]["inlineData"] == nil {
		t.Fatalf("Google file content was dropped: %#v", google)
	}
	googleData := google[0]["inlineData"].(map[string]any)
	if googleData["mimeType"] != "application/pdf" || googleData["data"] != "YWJj" {
		t.Fatalf("unexpected Google file data: %#v", googleData)
	}
}

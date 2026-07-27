package anthropic

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mirainya/Prism/internal/gateway/canonical"
)

func TestDecodeResponseMapsStopReasonAndToolUse(t *testing.T) {
	response, err := DecodeResponse([]byte(`{
		"id":"msg_1","model":"claude","stop_reason":"tool_use",
		"content":[{"type":"text","text":"checking"},{"type":"tool_use","id":"tool_1","name":"lookup","input":{"q":"x"}}],
		"usage":{"input_tokens":3,"output_tokens":4}
	}`), "public")
	if err != nil {
		t.Fatal(err)
	}
	if response.FinishReason != "tool_use" || len(response.Output) != 2 {
		t.Fatalf("response = %#v", response)
	}
	if response.Output[1].Name != "lookup" || string(response.Output[1].Arguments) != `{"q":"x"}` {
		t.Fatalf("tool = %#v", response.Output[1])
	}
}

func TestDecodeRequestMapsToolHistoryMediaAndExtensions(t *testing.T) {
	request, err := DecodeRequestJSON([]byte(`{
		"model":"claude","max_tokens":64,"stream":true,
		"metadata":{"user_id":"user_1","trace":"abc"},"service_tier":"auto",
		"tool_choice":{"type":"tool","name":"lookup","disable_parallel_tool_use":true},
		"messages":[
			{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"lookup","input":{"q":"x"}}]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"call_1","content":"ok"},
				{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aW1n"}},
				{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"cGRm"},"title":"doc.pdf"}
			]}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Items) != 3 || request.Items[0].Type != "function_call" || request.Items[0].CallID != "call_1" || string(request.Items[0].Arguments) != `{"q":"x"}` {
		t.Fatalf("tool call = %#v", request.Items)
	}
	if request.Items[1].Type != "function_call_output" || request.Items[1].CallID != "call_1" || string(request.Items[1].Output) != `"ok"` {
		t.Fatalf("tool result = %#v", request.Items[1])
	}
	if image := request.Items[2].Content[0]; image.MediaType != "image/png" || image.Data != "aW1n" {
		t.Fatalf("image = %#v", image)
	}
	if file := request.Items[2].Content[1]; file.MediaType != "application/pdf" || file.Filename != "doc.pdf" {
		t.Fatalf("file = %#v", file)
	}
	if request.ParallelToolCalls == nil || *request.ParallelToolCalls || request.ToolChoice == nil || request.ToolChoice.Name != "lookup" {
		t.Fatalf("tool choice = %#v parallel=%v", request.ToolChoice, request.ParallelToolCalls)
	}
	if request.User != "user_1" || !strings.Contains(string(request.ClientExtensions[extraRequest]), `"service_tier":"auto"`) {
		t.Fatalf("extensions = %s", request.ClientExtensions[extraRequest])
	}
}

func TestAnthropicReasoningProofRequestRoundTrip(t *testing.T) {
	request, err := DecodeRequestJSON([]byte(`{
		"model":"claude","max_tokens":64,"messages":[
			{"role":"assistant","content":[
				{"type":"thinking","thinking":"first","signature":"sig-thinking"},
				{"type":"redacted_thinking","data":"opaque"}
			]},
			{"role":"user","content":[{"type":"text","text":"continue"}]}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Items) != 3 {
		t.Fatalf("items = %#v", request.Items)
	}
	proof := request.Items[0].Proof
	if proof == nil || proof.Provider != canonical.ProofProviderAnthropic || proof.Value != "sig-thinking" {
		t.Fatalf("thinking proof = %#v", proof)
	}
	redactedProof := request.Items[1].Proof
	if redactedProof == nil || redactedProof.Provider != canonical.ProofProviderAnthropic || redactedProof.Subject != canonical.ProofSubjectAnthropicRedacted || redactedProof.Value != "opaque" {
		t.Fatalf("redacted thinking proof = %#v", redactedProof)
	}

	body, err := EncodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, expected := range []string{
		`"thinking":"first"`, `"signature":"sig-thinking"`,
		`"type":"redacted_thinking"`, `"data":"opaque"`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("missing %s in %s", expected, text)
		}
	}
	if strings.Count(text, `"signature"`) != 1 {
		t.Fatalf("unexpected redacted signature in %s", text)
	}
}

func TestAnthropicEncoderRejectsForeignReasoningProof(t *testing.T) {
	rawBlock := map[string]json.RawMessage{extraRawBlock: json.RawMessage(`{"type":"thinking","thinking":"stale","signature":"must-not-leak"}`)}
	reasoning := canonical.Item{
		Type: "reasoning", Role: canonical.RoleAssistant,
		Content: []canonical.Content{{Type: "reasoning_text", Text: "safe", Extra: rawBlock}},
		Proof:   &canonical.ProviderProof{Provider: canonical.ProofProviderGoogle, Value: "foreign-proof"},
		Extra:   rawBlock,
	}
	requestBody, err := EncodeRequest(canonical.Request{
		Model: "claude", Items: []canonical.Item{
			reasoning,
			{Type: "message", Role: canonical.RoleUser, Content: []canonical.Content{{Type: "input_text", Text: "continue"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	requestJSON, _ := json.Marshal(requestBody)
	if strings.Contains(string(requestJSON), "signature") || strings.Contains(string(requestJSON), "foreign-proof") {
		t.Fatalf("foreign proof leaked into request: %s", requestJSON)
	}

	responseJSON, err := EncodeResponseJSON(canonical.Response{Model: "claude", Output: []canonical.Item{reasoning}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(responseJSON), "signature") || strings.Contains(string(responseJSON), "foreign-proof") {
		t.Fatalf("foreign proof leaked into response: %s", responseJSON)
	}
}

func TestEncodeRequestPreservesProofOnlyReasoning(t *testing.T) {
	request := canonical.Request{Model: "claude", Items: []canonical.Item{{
		Type: "reasoning", Role: canonical.RoleAssistant,
		Proof: &canonical.ProviderProof{Provider: canonical.ProofProviderAnthropic, Value: "sig-only"},
	}}}
	body, err := EncodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"type":"thinking"`) || !strings.Contains(string(encoded), `"thinking":""`) || !strings.Contains(string(encoded), `"signature":"sig-only"`) {
		t.Fatalf("proof-only reasoning was lost: %s", encoded)
	}
}

func TestEncodeRequestPreservesRedactedReasoningWithoutContent(t *testing.T) {
	request := canonical.Request{Model: "claude", Items: []canonical.Item{{
		Type: "reasoning",
		Extra: map[string]json.RawMessage{
			extraRawBlock: json.RawMessage(`{"type":"redacted_thinking","data":"opaque"}`),
		},
	}}}
	body, err := EncodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"type":"redacted_thinking"`) || !strings.Contains(string(encoded), `"data":"opaque"`) {
		t.Fatalf("redacted reasoning was lost: %s", encoded)
	}
}

func TestDecodePublicRequestAndEncodeResponse(t *testing.T) {
	request, err := DecodeRequestJSON([]byte(`{"model":"claude","max_tokens":32,"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if request.Endpoint != canonical.EndpointAnthropic || request.Items[0].Content[0].Text != "hello" {
		t.Fatalf("request = %#v", request)
	}
	encoded, err := EncodeResponseJSON(canonical.Response{ID: "msg_1", Model: "claude", FinishReason: "stop", Output: []canonical.Item{{Type: "message", Content: []canonical.Content{{Text: "hi"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"stop_reason":"end_turn"`) {
		t.Fatalf("response = %s", encoded)
	}
}

func TestEncodeRequestUsesAnthropicToolAndMediaShapes(t *testing.T) {
	parallel := false
	request := canonical.Request{
		Model: "claude", MaxOutputTokens: intPointer(64), ParallelToolCalls: &parallel,
		ToolChoice: &canonical.ToolChoice{Mode: "function", Name: "lookup"},
		Items: []canonical.Item{
			{Type: "message", Role: canonical.RoleAssistant, Content: []canonical.Content{{Type: "input_text", Text: "checking"}}},
			{ID: "fc_1", Type: "function_call", Role: canonical.RoleAssistant, CallID: "call_1", Name: "lookup", Arguments: json.RawMessage(`"{\"q\":\"x\"}"`)},
			{Type: "function_call_output", Role: canonical.RoleTool, CallID: "call_1", Output: json.RawMessage(`"ok"`)},
			{Type: "message", Role: canonical.RoleUser, Content: []canonical.Content{
				{Type: "input_image", Data: "aW1n", MediaType: "image/png"},
				{Type: "input_file", Data: "cGRm", Filename: "doc.pdf", MediaType: "application/pdf"},
			}},
		},
	}
	body, err := EncodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(body)
	text := string(encoded)
	for _, expected := range []string{`"type":"tool_use"`, `"id":"call_1"`, `"input":{"q":"x"}`, `"tool_use_id":"call_1"`, `"content":"ok"`, `"media_type":"image/png"`, `"media_type":"application/pdf"`, `"disable_parallel_tool_use":true`} {
		if !strings.Contains(text, expected) {
			t.Fatalf("missing %s in %s", expected, text)
		}
	}
}

func TestDecodeResponseMapsErrorAndExtendedUsage(t *testing.T) {
	response, err := DecodeResponse([]byte(`{
		"id":"msg_1","type":"message","role":"assistant","model":"claude","stop_reason":"max_tokens","stop_sequence":null,
		"content":[{"type":"thinking","thinking":"hmm","signature":"sig"},{"type":"text","text":"done"}],
		"usage":{"input_tokens":3,"output_tokens":4,"cache_read_input_tokens":2,"cache_creation_input_tokens":1,"server_tool_use":{"web_search_requests":1}}
	}`), "public")
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != "incomplete" || response.Usage == nil || response.Usage.CachedInputTokens != 2 || len(response.Usage.Extra["cache_creation_input_tokens"]) == 0 || len(response.Output) != 2 {
		t.Fatalf("response = %#v", response)
	}
	if proof := response.Output[0].Proof; proof == nil || proof.Provider != canonical.ProofProviderAnthropic || proof.Value != "sig" {
		t.Fatalf("thinking proof = %#v", proof)
	}
	encoded, err := EncodeResponseJSON(response)
	if err != nil || !strings.Contains(string(encoded), `"signature":"sig"`) {
		t.Fatalf("encoded response = %s err=%v", encoded, err)
	}
	errorResponse, err := DecodeResponse([]byte(`{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`), "public")
	if err != nil || errorResponse.Error == nil || errorResponse.Error.Type != "overloaded_error" || !errorResponse.Error.Retryable {
		t.Fatalf("error response = %#v err=%v", errorResponse, err)
	}
}

func TestAnthropicSSEEncodesToolStartTerminalAndError(t *testing.T) {
	start, err := EncodeSSEFrame(canonical.Event{Type: canonical.EventOutputItemAdded, ToolIndex: 2, Item: &canonical.Item{ID: "fc_1", Type: "function_call", CallID: "call_1", Name: "lookup", Arguments: json.RawMessage(`{}`)}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(start), "event: content_block_start") || !strings.Contains(string(start), `"index":2`) || !strings.Contains(string(start), `"id":"call_1"`) {
		t.Fatalf("start = %s", start)
	}

	terminal, err := EncodeSSEFrame(canonical.Event{Type: canonical.EventIncomplete, Usage: &canonical.Usage{OutputTokens: 7}, Raw: json.RawMessage(`{"type":"response.incomplete"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(terminal), "event: message_delta") || !strings.Contains(string(terminal), `"stop_reason":"max_tokens"`) || !strings.Contains(string(terminal), `"output_tokens":7`) || !strings.HasSuffix(string(terminal), "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n") {
		t.Fatalf("terminal = %s", terminal)
	}

	errorFrame, err := EncodeSSEFrame(canonical.Event{Type: canonical.EventError, Error: &canonical.Error{Type: "overloaded_error", Message: "busy"}})
	if err != nil || !strings.Contains(string(errorFrame), `"type":"overloaded_error"`) {
		t.Fatalf("error = %s err=%v", errorFrame, err)
	}
}

func TestDecodeEventUsesTaggedDeltaFields(t *testing.T) {
	event, err := DecodeEvent("content_block_delta", []byte(`{"type":"content_block_delta","index":3,"delta":{"type":"input_json_delta","partial_json":"{\"q\":"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if event.Type != canonical.EventToolArgumentsDelta || event.ToolIndex != 3 || event.Delta != `{"q":` {
		t.Fatalf("event = %#v", event)
	}
}

func TestEncodeSSEFrameUsesProviderProofFragment(t *testing.T) {
	event := canonical.Event{
		Type: canonical.EventProviderProof, ContentIndex: 0, Delta: "native",
		Item: &canonical.Item{Type: "reasoning", Proof: &canonical.ProviderProof{Provider: canonical.ProofProviderAnthropic, Value: "sig-native"}},
	}
	frame, err := EncodeSSEFrame(event)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(frame), `"signature":"native"`) || strings.Contains(string(frame), `"signature":"sig-native"`) {
		t.Fatalf("signature fragment frame = %s", frame)
	}
}

func TestAnthropicSignatureDeltaRoundTrip(t *testing.T) {
	start, err := DecodeEvent("content_block_start", []byte(`{"type":"content_block_start","index":3,"content_block":{"type":"thinking","thinking":""}}`))
	if err != nil || start.Item == nil || start.Item.Type != "reasoning" {
		t.Fatalf("start = %#v err=%v", start, err)
	}
	reasoningDelta, err := DecodeEvent("content_block_delta", []byte(`{"type":"content_block_delta","index":3,"delta":{"type":"thinking_delta","thinking":"considering"}}`))
	if err != nil {
		t.Fatal(err)
	}
	event, err := DecodeEvent("content_block_delta", []byte(`{"type":"content_block_delta","index":3,"delta":{"type":"signature_delta","signature":"sig-stream"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if event.Type != canonical.EventProviderProof || event.ContentIndex != 3 || event.Item == nil || event.Item.Type != "reasoning" {
		t.Fatalf("event = %#v", event)
	}
	proof := event.Item.Proof
	if proof == nil || proof.Provider != canonical.ProofProviderAnthropic || proof.Value != "sig-stream" {
		t.Fatalf("proof = %#v", proof)
	}
	accumulator := canonical.NewEventAccumulator()
	accumulator.Observe(start)
	accumulator.Observe(reasoningDelta)
	accumulator.Observe(event)
	output := accumulator.Snapshot().Output
	if len(output) != 1 || output[0].Type != "reasoning" || output[0].Proof == nil || output[0].Proof.Value != "sig-stream" {
		t.Fatalf("accumulated output = %#v", output)
	}
	var reasoningText string
	for _, part := range output[0].Content {
		reasoningText += part.Text
	}
	if reasoningText != "considering" {
		t.Fatalf("reasoning text = %q; output=%#v", reasoningText, output)
	}
	frame, err := EncodeSSEFrame(event)
	if err != nil || !strings.Contains(string(frame), `"index":3`) || !strings.Contains(string(frame), `"signature":"sig-stream","type":"signature_delta"`) {
		t.Fatalf("frame = %s err=%v", frame, err)
	}

	foreign := event
	foreign.Item = &canonical.Item{Type: "reasoning", Proof: &canonical.ProviderProof{Provider: canonical.ProofProviderOpenAI, Value: "foreign"}}
	frame, err = EncodeSSEFrame(foreign)
	if err != nil || len(frame) != 0 {
		t.Fatalf("foreign proof frame = %s err=%v", frame, err)
	}
	encoded := encodeSSEEvents(t, NewSSEEncoder("public-model"), []canonical.Event{foreign})
	if encoded != "" {
		t.Fatalf("foreign proof opened an Anthropic stream block: %s", encoded)
	}
}

func TestSSEEncoderPreservesRedactedThinkingStart(t *testing.T) {
	event, err := DecodeEvent("content_block_start", []byte(`{"type":"content_block_start","index":2,"content_block":{"type":"redacted_thinking","data":"opaque"}}`))
	if err != nil {
		t.Fatal(err)
	}
	encoded := encodeSSEEvents(t, NewSSEEncoder("public-model"), []canonical.Event{event})
	if !strings.Contains(encoded, `"type":"redacted_thinking"`) || !strings.Contains(encoded, `"data":"opaque"`) {
		t.Fatalf("redacted thinking start was not preserved: %s", encoded)
	}
	if strings.Contains(encoded, `"type":"thinking","thinking":""`) {
		t.Fatalf("redacted thinking was rewritten as ordinary thinking: %s", encoded)
	}
}

func TestSSEEncoderRebuildsRedactedThinkingFromProof(t *testing.T) {
	item := canonical.Item{Type: "reasoning", Role: canonical.RoleAssistant, Proof: &canonical.ProviderProof{
		Provider: canonical.ProofProviderAnthropic, Subject: canonical.ProofSubjectAnthropicRedacted, Value: "opaque-proof",
	}}
	encoded := encodeSSEEvents(t, NewSSEEncoder("public-model"), []canonical.Event{
		{Type: canonical.EventProviderProof, Item: &item},
		{Type: canonical.EventCompleted},
	})
	if !strings.Contains(encoded, `"type":"redacted_thinking"`) || !strings.Contains(encoded, `"data":"opaque-proof"`) || strings.Contains(encoded, `"signature":"opaque-proof"`) {
		t.Fatalf("redacted proof stream = %s", encoded)
	}
}

func TestSSEEncoderSynthesizesGoogleTextLifecycle(t *testing.T) {
	encoded := encodeSSEEvents(t, NewSSEEncoder("public-model"), []canonical.Event{
		{Type: canonical.EventTextDelta, ProviderResponseID: "gemini_1", Delta: "hello"},
		{Type: canonical.EventCompleted, ProviderResponseID: "gemini_1", Usage: &canonical.Usage{OutputTokens: 1}},
	})

	assertSSEEventCount(t, encoded, "message_start", 1)
	assertSSEEventCount(t, encoded, "content_block_start", 1)
	assertSSEEventCount(t, encoded, "content_block_delta", 1)
	assertSSEEventCount(t, encoded, "content_block_stop", 1)
	assertSSEEventCount(t, encoded, "message_stop", 1)
	assertSSEOrder(t, encoded, "event: message_start", "event: content_block_start", "event: content_block_delta", "event: content_block_stop", "event: message_stop")
	if !strings.Contains(encoded, `"model":"public-model"`) || !strings.Contains(encoded, `"text":"hello","type":"text_delta"`) {
		t.Fatalf("unexpected stream: %s", encoded)
	}
}

func TestSSEEncoderDeduplicatesResponsesTextLifecycle(t *testing.T) {
	message := &canonical.Item{Type: "message", Role: canonical.RoleAssistant, Content: []canonical.Content{{Type: "output_text"}}}
	encoded := encodeSSEEvents(t, NewSSEEncoder("public-model"), []canonical.Event{
		{Type: canonical.EventResponseCreated, Response: &canonical.Response{ID: "resp_1", Model: "public-model"}},
		{Type: canonical.EventOutputItemAdded, OutputIndex: 0, Item: &canonical.Item{Type: "message", Role: canonical.RoleAssistant}},
		{Type: canonical.EventContentPartAdded, OutputIndex: 0, ContentIndex: 0, Item: message},
		{Type: canonical.EventTextDelta, OutputIndex: 0, ContentIndex: 0, Delta: "answer"},
		{Type: canonical.EventTextDone, OutputIndex: 0, ContentIndex: 0},
		{Type: canonical.EventContentPartDone, OutputIndex: 0, ContentIndex: 0, Item: message},
		{Type: canonical.EventOutputItemDone, OutputIndex: 0, Item: message},
		{Type: canonical.EventCompleted, Response: &canonical.Response{ID: "resp_1", FinishReason: "stop"}},
	})

	assertSSEEventCount(t, encoded, "message_start", 1)
	assertSSEEventCount(t, encoded, "content_block_start", 1)
	assertSSEEventCount(t, encoded, "content_block_stop", 1)
	assertSSEEventCount(t, encoded, "message_stop", 1)
}

func TestSSEEncoderDeduplicatesChatReasoningAndToolLifecycle(t *testing.T) {
	tool := &canonical.Item{ID: "call_1", Type: "function_call", CallID: "call_1", Name: "lookup"}
	encoded := encodeSSEEvents(t, NewSSEEncoder("public-model"), []canonical.Event{
		{Type: canonical.EventOutputItemAdded, Item: &canonical.Item{Type: "message", Role: canonical.RoleAssistant}},
		{Type: canonical.EventReasoningDelta, Delta: "think"},
		{Type: canonical.EventOutputItemAdded, ToolIndex: 0, Item: tool},
		{Type: canonical.EventOutputItemAdded, ToolIndex: 0, Item: tool},
		{Type: canonical.EventToolArgumentsDelta, ToolIndex: 0, Item: tool, Delta: `{"q":"x"}`},
		{Type: canonical.EventOutputItemDone, ToolIndex: 0, Item: tool},
		{Type: canonical.EventOutputItemDone, ToolIndex: 0, Item: tool},
		{Type: canonical.EventCompleted},
	})

	assertSSEEventCount(t, encoded, "message_start", 1)
	assertSSEEventCount(t, encoded, "content_block_start", 2)
	assertSSEEventCount(t, encoded, "content_block_stop", 2)
	assertSSEEventCount(t, encoded, "message_stop", 1)
	for _, expected := range []string{
		`"delta":{"thinking":"think","type":"thinking_delta"},"index":0`,
		`"content_block":{"id":"call_1","input":{},"name":"lookup","type":"tool_use"},"index":1`,
		`"delta":{"partial_json":"{\"q\":\"x\"}","type":"input_json_delta"},"index":1`,
		`"stop_reason":"tool_use"`,
	} {
		if !strings.Contains(encoded, expected) {
			t.Fatalf("missing %s in %s", expected, encoded)
		}
	}
}

func TestDecodeResponseNormalizesCachedUsageRoundTrip(t *testing.T) {
	response, err := DecodeResponse([]byte(`{
		"id":"msg_1","model":"claude","stop_reason":"end_turn","content":[{"type":"text","text":"ok"}],
		"usage":{"input_tokens":3,"output_tokens":4,"cache_read_input_tokens":2,"cache_creation_input_tokens":1}
	}`), "public")
	if err != nil {
		t.Fatal(err)
	}
	usage := response.Usage
	if usage == nil || usage.InputTokens != 6 || usage.CachedInputTokens != 2 || usage.TotalTokens != 10 {
		t.Fatalf("usage = %#v", usage)
	}
	encoded, err := EncodeResponseJSON(response)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"input_tokens":3`, `"cache_read_input_tokens":2`, `"cache_creation_input_tokens":1`} {
		if !strings.Contains(string(encoded), expected) {
			t.Fatalf("missing %s in %s", expected, encoded)
		}
	}
}

func TestAnthropicFinishReasonMapsOpenAIVocabulary(t *testing.T) {
	for _, test := range []struct{ source, want string }{
		{"stop", "end_turn"}, {"length", "max_tokens"}, {"tool_calls", "tool_use"}, {"content_filter", "refusal"},
		{"end_turn", "end_turn"}, {"tool_use", "tool_use"}, {"max_tokens", "max_tokens"}, {"refusal", "refusal"},
		{"stop_sequence", "stop_sequence"}, {"", "end_turn"},
	} {
		if got := anthropicFinishReason(test.source); got != test.want {
			t.Fatalf("anthropicFinishReason(%q) = %q, want %q", test.source, got, test.want)
		}
	}
}

func TestSSEEncoderFallsBackToChatFinishReasonExtra(t *testing.T) {
	item := &canonical.Item{Type: "message", Role: canonical.RoleAssistant, Extra: map[string]json.RawMessage{
		"openai_chat.finish_reason": json.RawMessage(`"tool_calls"`),
	}}
	encoded := encodeSSEEvents(t, NewSSEEncoder("public-model"), []canonical.Event{
		{Type: canonical.EventTextDelta, Delta: "checking"},
		{Type: canonical.EventOutputItemDone, Item: item},
		{Type: canonical.EventCompleted},
	})
	if !strings.Contains(encoded, `"stop_reason":"tool_use"`) {
		t.Fatalf("chat finish_reason extra was ignored: %s", encoded)
	}

	// 终态 Response.FinishReason 优先于 Item.Extra 兜底。
	explicit := encodeSSEEvents(t, NewSSEEncoder("public-model"), []canonical.Event{
		{Type: canonical.EventTextDelta, Delta: "checking"},
		{Type: canonical.EventOutputItemDone, Item: item},
		{Type: canonical.EventCompleted, Response: &canonical.Response{FinishReason: "stop"}},
	})
	if !strings.Contains(explicit, `"stop_reason":"end_turn"`) || strings.Contains(explicit, `"stop_reason":"tool_use"`) {
		t.Fatalf("explicit finish_reason was overridden: %s", explicit)
	}
}

func TestSSEEncoderMergesUsageIntoTerminalDelta(t *testing.T) {
	encoded := encodeSSEEvents(t, NewSSEEncoder("public-model"), []canonical.Event{
		{Type: canonical.EventTextDelta, Delta: "answer"},
		{Type: canonical.EventUsage, Usage: &canonical.Usage{InputTokens: 2, OutputTokens: 7, TotalTokens: 9}},
		{Type: canonical.EventCompleted, Response: &canonical.Response{FinishReason: "stop"}},
	})
	assertSSEEventCount(t, encoded, "message_delta", 1)
	assertSSEEventCount(t, encoded, "message_stop", 1)
	if !strings.Contains(encoded, `"output_tokens":7`) || !strings.Contains(encoded, `"stop_reason":"end_turn"`) {
		t.Fatalf("terminal delta lost usage or stop_reason: %s", encoded)
	}
}

func encodeSSEEvents(t *testing.T, encoder *SSEEncoder, events []canonical.Event) string {
	t.Helper()
	var encoded strings.Builder
	for _, event := range events {
		frame, err := encoder.Encode(event)
		if err != nil {
			t.Fatal(err)
		}
		encoded.Write(frame)
	}
	return encoded.String()
}

func assertSSEEventCount(t *testing.T, encoded, name string, expected int) {
	t.Helper()
	if count := strings.Count(encoded, "event: "+name+"\n"); count != expected {
		t.Fatalf("%s count = %d, want %d; stream=%s", name, count, expected, encoded)
	}
}

func assertSSEOrder(t *testing.T, encoded string, names ...string) {
	t.Helper()
	position := -1
	for _, name := range names {
		next := strings.Index(encoded[position+1:], name)
		if next < 0 {
			t.Fatalf("missing %s in %s", name, encoded)
		}
		position += next + 1
	}
}

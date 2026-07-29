package openai

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/gateway/transport"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAITransportsConvertAnthropicMessages(t *testing.T) {
	request := canonical.Request{Endpoint: canonical.EndpointAnthropic}
	features := canonical.NewFeatureSet(canonical.FeatureTools)
	if plan := NewChat(nil).Plan(transport.OperationMessages, request, features); !plan.Supported() || plan.Kind != transport.PlanConverted {
		t.Fatalf("chat plan=%#v", plan)
	}
	if plan := NewResponses(nil).Plan(transport.OperationMessages, request, features); !plan.Supported() || plan.Kind != transport.PlanConverted {
		t.Fatalf("responses plan=%#v", plan)
	}
}

func TestOpenAIPlanKindsAndExtensionBoundaries(t *testing.T) {
	tests := []struct {
		name      string
		item      transport.Transport
		operation transport.Operation
		kind      transport.PlanKind
		upstream  transport.Operation
	}{
		{name: "chat native", item: NewChat(nil), operation: transport.OperationChat, kind: transport.PlanExact, upstream: transport.OperationChat},
		{name: "chat from responses", item: NewChat(nil), operation: transport.OperationResponses, kind: transport.PlanConverted, upstream: transport.OperationChat},
		{name: "chat from messages", item: NewChat(nil), operation: transport.OperationMessages, kind: transport.PlanConverted, upstream: transport.OperationChat},
		{name: "responses native", item: NewResponses(nil), operation: transport.OperationResponses, kind: transport.PlanExact, upstream: transport.OperationResponses},
		{name: "responses from chat", item: NewResponses(nil), operation: transport.OperationChat, kind: transport.PlanConverted, upstream: transport.OperationResponses},
		{name: "responses from messages", item: NewResponses(nil), operation: transport.OperationMessages, kind: transport.PlanConverted, upstream: transport.OperationResponses},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := test.item.Plan(test.operation, canonical.Request{}, canonical.FeatureSet{})
			if plan.Kind != test.kind || plan.Upstream != test.upstream {
				t.Fatalf("plan = %#v", plan)
			}
		})
	}

	request := canonical.Request{Items: []canonical.Item{{Type: "message", Extra: map[string]json.RawMessage{"future_item": json.RawMessage(`true`)}}}}
	if plan := NewChat(nil).Plan(transport.OperationChat, request, canonical.FeatureSet{}); plan.Supported() {
		t.Fatalf("Chat accepted an item extension it does not encode: %#v", plan)
	}
	if plan := NewResponses(nil).Plan(transport.OperationResponses, request, canonical.FeatureSet{}); !plan.Supported() {
		t.Fatalf("Responses rejected a generic item extension it preserves: %#v", plan)
	}
}

func TestOpenAIPlansRejectIncompatibleProviderProofs(t *testing.T) {
	foreign := canonical.Request{Items: []canonical.Item{{Type: "reasoning", Proof: &canonical.ProviderProof{Provider: canonical.ProofProviderAnthropic, Value: "sig"}}}}
	if plan := NewResponses(nil).Plan(transport.OperationResponses, foreign, canonical.NewFeatureSet(canonical.FeatureReasoning)); plan.Supported() {
		t.Fatalf("Responses accepted foreign proof: %#v", plan)
	}
	if plan := NewChat(nil).Plan(transport.OperationChat, foreign, canonical.NewFeatureSet(canonical.FeatureReasoning)); plan.Supported() {
		t.Fatalf("Chat accepted provider proof: %#v", plan)
	}

	native := canonical.Request{Items: []canonical.Item{{Type: "reasoning", Proof: &canonical.ProviderProof{Provider: canonical.ProofProviderOpenAI, Value: "enc"}}}}
	if plan := NewResponses(nil).Plan(transport.OperationResponses, native, canonical.NewFeatureSet(canonical.FeatureReasoning)); !plan.Supported() {
		t.Fatalf("Responses rejected native proof: %#v", plan)
	}
	wrongItem := native.Clone()
	wrongItem.Items[0].Type = "function_call"
	if plan := NewResponses(nil).Plan(transport.OperationResponses, wrongItem, canonical.NewFeatureSet(canonical.FeatureReasoning, canonical.FeatureTools)); plan.Supported() {
		t.Fatalf("Responses accepted proof on a non-reasoning item: %#v", plan)
	}

	namespaced := canonical.Request{Items: []canonical.Item{{Type: "function_call", Namespace: "tools"}}}
	if plan := NewChat(nil).Plan(transport.OperationChat, namespaced, canonical.FeatureSet{}); plan.Supported() {
		t.Fatalf("Chat accepted namespace: %#v", plan)
	}
	if plan := NewResponses(nil).Plan(transport.OperationResponses, namespaced, canonical.FeatureSet{}); !plan.Supported() {
		t.Fatalf("Responses rejected namespace: %#v", plan)
	}
}

func TestOpenAIResponsesPlanRejectsReasoningToolsForForeignDownstreams(t *testing.T) {
	request := canonical.Request{
		Reasoning: &canonical.Reasoning{Effort: "high"},
		Tools:     []canonical.Tool{{Type: "function", Name: "lookup"}},
	}
	features := canonical.NewFeatureSet(canonical.FeatureReasoning, canonical.FeatureTools)
	item := NewResponses(nil)
	if plan := item.Plan(transport.OperationResponses, request, features); !plan.Supported() {
		t.Fatalf("native Responses reasoning tools were rejected: %#v", plan)
	}
	for _, operation := range []transport.Operation{transport.OperationChat, transport.OperationMessages} {
		if plan := item.Plan(operation, request, features); plan.Supported() {
			t.Fatalf("%s route could lose OpenAI reasoning proof: %#v", operation, plan)
		}
	}
}

func TestOpenAIResponsesReasoningProofRoundTrip(t *testing.T) {
	item, err := decodeResponseItem(json.RawMessage(`{"id":"rs_1","type":"reasoning","encrypted_content":"enc_1","summary":[{"type":"summary_text","text":"analysis"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if item.Proof == nil || item.Proof.Provider != canonical.ProofProviderOpenAI || item.Proof.Value != "enc_1" || len(item.Content) != 1 || item.Content[0].Text != "analysis" {
		t.Fatalf("decoded item = %#v", item)
	}
	items, err := responseItems([]canonical.Item{item})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(items[0])
	if err != nil {
		t.Fatal(err)
	}
	if text := string(encoded); !strings.Contains(text, `"encrypted_content":"enc_1"`) || !strings.Contains(text, `"summary":[{"text":"analysis","type":"summary_text"}]`) {
		t.Fatalf("encoded item = %s", text)
	}
}

func TestOpenAIResponsesTreatsTaggedLookingNativeProofAsOpaque(t *testing.T) {
	item, err := decodeResponseItem(json.RawMessage(`{"id":"rs_1","type":"reasoning","encrypted_content":"anthropic#opaque-native","summary":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if item.Proof == nil || item.Proof.Provider != canonical.ProofProviderOpenAI || item.Proof.Value != "anthropic#opaque-native" {
		t.Fatalf("native OpenAI proof was reclassified: %#v", item.Proof)
	}
}

func TestChatPlanDropsResponsesEncryptedReasoningInclude(t *testing.T) {
	request := canonical.Request{
		Model:   "m",
		Include: []string{includeReasoningEncryptedContent},
		ClientExtensions: map[string]json.RawMessage{
			chatRequestExtras: json.RawMessage(`{"include":["reasoning.encrypted_content"]}`),
		},
		Items: []canonical.Item{{
			Type: "message", Role: canonical.RoleUser,
			Content: []canonical.Content{{Type: "input_text", Text: "hi"}},
		}},
	}
	item := NewChat(nil)
	plan := item.Plan(transport.OperationResponses, request, canonical.FeatureSet{})
	if !plan.Supported() || plan.Kind != transport.PlanConverted || plan.Upstream != transport.OperationChat {
		t.Fatalf("chat plan = %#v", plan)
	}

	prepared, err := item.Prepare(context.Background(), transport.Invocation{
		Route: transport.Route{BaseURL: "https://example.test"}, Operation: transport.OperationResponses, Request: request,
	})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(prepared.Body, &body); err != nil {
		t.Fatal(err)
	}
	if _, exists := body["include"]; exists {
		t.Fatalf("Chat upstream body contains Responses include: %s", prepared.Body)
	}
}

func TestOpenAIServiceTierSurvivesResponsesRouting(t *testing.T) {
	request := canonical.Request{
		Model: "m", ServiceTier: "auto",
		Items: []canonical.Item{{Type: "message", Role: canonical.RoleUser, Content: []canonical.Content{{Type: "input_text", Text: "hi"}}}},
	}
	tests := []struct {
		name string
		item transport.Transport
	}{
		{name: "converted chat", item: NewChat(nil)},
		{name: "native responses", item: NewResponses(nil)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := test.item.Plan(transport.OperationResponses, request, canonical.FeatureSet{})
			if !plan.Supported() {
				t.Fatalf("plan = %#v", plan)
			}
			prepared, err := test.item.Prepare(context.Background(), transport.Invocation{
				Route:     transport.Route{BaseURL: "https://example.test", VendorModel: "m"},
				Operation: transport.OperationResponses, Request: request,
			})
			if err != nil {
				t.Fatal(err)
			}
			var body map[string]json.RawMessage
			if err := json.Unmarshal(prepared.Body, &body); err != nil {
				t.Fatal(err)
			}
			if got := string(body["service_tier"]); got != `"auto"` {
				t.Fatalf("service_tier = %s body=%s", got, prepared.Body)
			}
		})
	}
}

func TestChatPlanRejectsUnsupportedResponsesInclude(t *testing.T) {
	tests := []struct {
		name      string
		operation transport.Operation
		include   []string
	}{
		{name: "unsupported value", operation: transport.OperationResponses, include: []string{"file_search_call.results"}},
		{name: "mixed values", operation: transport.OperationResponses, include: []string{includeReasoningEncryptedContent, "file_search_call.results"}},
		{name: "non Responses operation", operation: transport.OperationChat, include: []string{includeReasoningEncryptedContent}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := canonical.Request{Include: test.include}
			if plan := NewChat(nil).Plan(test.operation, request, canonical.FeatureSet{}); plan.Supported() {
				t.Fatalf("Chat accepted unsupported include: %#v", plan)
			}
		})
	}
}

func TestConvertedOpenAIResponsesNormalizesAnthropicToolChoice(t *testing.T) {
	request := canonical.Request{
		Model: "m",
		Items: []canonical.Item{{Type: "message", Role: canonical.RoleUser, Content: []canonical.Content{{Type: "input_text", Text: "hi"}}}},
		ToolChoice: &canonical.ToolChoice{
			Mode: "tool", Type: "tool", Name: "lookup",
			Raw: json.RawMessage(`{"type":"tool","name":"lookup","disable_parallel_tool_use":false}`),
		},
	}
	item := NewResponses(nil)
	if plan := item.Plan(transport.OperationMessages, request, canonical.FeatureSet{}); !plan.Supported() {
		t.Fatalf("standard Anthropic tool choice was rejected: %#v", plan)
	}
	prepared, err := item.Prepare(context.Background(), transport.Invocation{Route: transport.Route{BaseURL: "https://example.test"}, Operation: transport.OperationMessages, Request: request})
	if err != nil {
		t.Fatal(err)
	}
	body := string(prepared.Body)
	if !strings.Contains(body, `"tool_choice":{"name":"lookup","type":"function"}`) || strings.Contains(body, "disable_parallel_tool_use") {
		t.Fatalf("tool choice was not normalized: %s", body)
	}

	request.ToolChoice.Raw = json.RawMessage(`{"type":"tool","name":"lookup","future":true}`)
	if plan := item.Plan(transport.OperationMessages, request, canonical.FeatureSet{}); plan.Supported() {
		t.Fatalf("tool choice extension must be rejected: %#v", plan)
	}
}

func TestChatExecuteAndError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test-Error") == "true" {
			w.WriteHeader(429)
			w.Write([]byte(`{"error":{"message":"slow"}}`))
			return
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer key" {
			t.Error("auth")
		}
		w.Write([]byte(`{"id":"up","created":1,"model":"vendor","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`))
	}))
	defer srv.Close()
	in := transport.Invocation{Route: transport.Route{BaseURL: srv.URL, APIKey: "key", VendorModel: "vendor", PublicModel: "public"}, Operation: transport.OperationChat, Request: canonical.Request{Model: "public", Items: []canonical.Item{{Type: "message", Role: canonical.RoleUser, Content: []canonical.Content{{Type: "input_text", Text: "hi"}}}}}}
	res, _, err := transport.Execute(context.Background(), NewChat(nil), in)
	if err != nil || res.Model != "public" || res.Usage.TotalTokens != 3 {
		t.Fatalf("response %#v err %v", res, err)
	}
	in.Route.ExtraHeaders = map[string]string{"X-Test-Error": "true"}
	_, _, err = transport.Execute(context.Background(), NewChat(nil), in)
	var e *HTTPError
	if !errors.As(err, &e) || e.Status != 429 || string(e.Body) == "" {
		t.Fatalf("error %v", err)
	}
}
func TestResponsesStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n"))
		w.Write([]byte("event: response.completed\ndata: {\"type\":\"response.completed\"}\n\n"))
	}))
	defer srv.Close()
	in := transport.Invocation{Route: transport.Route{BaseURL: srv.URL}, Operation: transport.OperationResponses, Request: canonical.Request{Model: "m", Stream: true, Items: []canonical.Item{{Type: "message", Role: canonical.RoleUser, Content: []canonical.Content{{Type: "input_text", Text: "hi"}}}}}}
	stream, _, err := transport.Stream(context.Background(), NewResponses(nil), in)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	e, err := stream.Next(context.Background())
	if err != nil || e.Type != canonical.EventTextDelta {
		t.Fatalf("event %#v err %v", e, err)
	}
	_, err = stream.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = stream.Next(context.Background())
	if !errors.Is(err, io.EOF) {
		t.Fatalf("want eof %v", err)
	}
}

func TestResponsesStreamMapsErrorEvent(t *testing.T) {
	event, err := decodeResponseEvent("error", []byte(`{"type":"error","code":"rate_limit","message":"slow"}`), transport.Route{})
	if err != nil {
		t.Fatal(err)
	}
	if event.Type != canonical.EventError || event.Error == nil || len(event.Error.Raw) == 0 {
		t.Fatalf("event = %#v", event)
	}
}

func TestResponsesStreamDecodesReasoningSummaryLifecycle(t *testing.T) {
	tests := []struct {
		name     string
		payload  string
		wantType canonical.EventType
		wantText string
		wantPart bool
	}{
		{
			name:     "part added",
			payload:  `{"type":"response.reasoning_summary_part.added","sequence_number":1,"output_index":3,"summary_index":2,"item_id":"rs_1","part":{"type":"summary_text","text":"","future":true}}`,
			wantType: canonical.EventReasoningPartAdded,
			wantPart: true,
		},
		{
			name:     "text delta",
			payload:  `{"type":"response.reasoning_summary_text.delta","sequence_number":2,"output_index":3,"summary_index":2,"item_id":"rs_1","delta":"ana"}`,
			wantType: canonical.EventReasoningDelta,
			wantText: "ana",
		},
		{
			name:     "text done",
			payload:  `{"type":"response.reasoning_summary_text.done","sequence_number":3,"output_index":3,"summary_index":2,"item_id":"rs_1","text":"analysis"}`,
			wantType: canonical.EventReasoningTextDone,
			wantText: "analysis",
			wantPart: true,
		},
		{
			name:     "part done",
			payload:  `{"type":"response.reasoning_summary_part.done","sequence_number":4,"output_index":3,"summary_index":2,"item_id":"rs_1","part":{"type":"summary_text","text":"analysis","future":true}}`,
			wantType: canonical.EventReasoningPartDone,
			wantText: "analysis",
			wantPart: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event, err := decodeResponseEvent("", []byte(test.payload), transport.Route{})
			if err != nil {
				t.Fatal(err)
			}
			if event.Type != test.wantType || event.OutputIndex != 3 || event.ContentIndex != 2 || event.Item == nil || event.Item.ID != "rs_1" || event.Item.Type != "reasoning" || event.Item.Role != canonical.RoleAssistant {
				t.Fatalf("event = %#v", event)
			}
			if event.Delta != test.wantText {
				t.Fatalf("delta = %q, want %q", event.Delta, test.wantText)
			}
			if !test.wantPart {
				if len(event.Item.Content) != 0 {
					t.Fatalf("unexpected reasoning content = %#v", event.Item.Content)
				}
				return
			}
			if len(event.Item.Content) != 1 || event.Item.Content[0].Type != "reasoning_text" || event.Item.Content[0].Text != test.wantText {
				t.Fatalf("reasoning content = %#v", event.Item.Content)
			}
			if strings.Contains(test.payload, `"future"`) && string(event.Item.Content[0].Extra["future"]) != "true" {
				t.Fatalf("reasoning extras = %#v", event.Item.Content[0].Extra)
			}
		})
	}
}

func TestDecodeChatPreservesReasoningMessageToolOrder(t *testing.T) {
	response, err := decodeChat([]byte(`{"id":"chat_1","choices":[{"index":0,"message":{"role":"assistant","reasoning_content":"think","content":"answer","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`), transport.Route{PublicModel: "public"})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Output) != 3 || response.Output[0].Type != "reasoning" || response.Output[1].Type != "message" || response.Output[2].Type != "function_call" {
		t.Fatalf("chat output order = %#v", response.Output)
	}
}

func TestChatStreamMapsTerminalUsage(t *testing.T) {
	event, err := decodeChatEvent("", []byte(`{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`), transport.Route{})
	if err != nil {
		t.Fatal(err)
	}
	if event.Type != canonical.EventCompleted || event.Usage == nil || event.Usage.TotalTokens != 5 {
		t.Fatalf("event = %#v", event)
	}
}

func TestResponsesTerminalCarriesProviderState(t *testing.T) {
	event, err := decodeResponseEvent("response.completed", []byte(`{"type":"response.completed","response":{"id":"resp_up","status":"completed","model":"m","usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}}`), transport.Route{PublicModel: "public"})
	if err != nil {
		t.Fatal(err)
	}
	if event.ProviderResponseID != "resp_up" || event.Usage == nil || event.Usage.TotalTokens != 5 {
		t.Fatalf("event = %#v", event)
	}
}

func TestPreparePreservesChatToolsAndMultimodalFields(t *testing.T) {
	request := canonical.Request{Model: "public", Stream: true, Items: []canonical.Item{
		{Type: "message", Role: canonical.RoleUser, Extra: map[string]json.RawMessage{
			transport.ExtensionChatContentMode: json.RawMessage(`"array"`), chatMessageName: json.RawMessage(`"operator"`),
		}, Content: []canonical.Content{
			{Type: "input_image", Data: "aW1n", MediaType: "image/png", Detail: "high"},
			{Type: "input_file", FileID: "file_1", Filename: "report.pdf"},
			{Type: "input_audio", Data: "YXVkaW8=", MediaType: "audio/wav", Format: "wav"},
		}},
		{Type: "function_call", CallID: "call_1", Name: "weather", Arguments: json.RawMessage(`{"city":"Shanghai"}`)},
		{Type: "function_call_output", CallID: "call_1", Output: json.RawMessage(`"sunny"`)},
	}}
	prepared, err := NewChat(nil).Prepare(context.Background(), transport.Invocation{Route: transport.Route{BaseURL: "https://example.test", VendorModel: "vendor"}, Operation: transport.OperationChat, Request: request})
	if err != nil {
		t.Fatal(err)
	}
	body := string(prepared.Body)
	for _, expected := range []string{`"id":"call_1"`, `"tool_call_id":"call_1"`, `"filename":"report.pdf"`, `"format":"wav"`, `"name":"operator"`, `data:image/png;base64,aW1n`, `"include_usage":true`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("prepared body missing %s: %s", expected, body)
		}
	}
}

func TestPreparePreservesResponsesNativeToolsAndCallID(t *testing.T) {
	request := canonical.Request{Model: "m", Items: []canonical.Item{
		{Type: "function_call", CallID: "call_2", Name: "lookup", Arguments: json.RawMessage(`{"q":"x"}`)},
		{Type: "function_call_output", CallID: "call_2", Output: json.RawMessage(`"ok"`)},
		{Type: "message", Role: canonical.RoleUser, Content: []canonical.Content{
			{Type: "input_file", FileID: "file_2", Filename: "a.txt", MediaType: "text/plain"},
			{Type: "input_audio", Data: "YQ==", Format: "wav", Extra: map[string]json.RawMessage{transport.ExtensionResponsesAudioOptions: json.RawMessage(`{"language":"en"}`)}},
		}},
	}, Tools: []canonical.Tool{{Type: "web_search", Options: json.RawMessage(`{"search_context_size":"high"}`)}, {Type: "function", Name: "lookup", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
	prepared, err := NewResponses(nil).Prepare(context.Background(), transport.Invocation{Route: transport.Route{BaseURL: "https://example.test"}, Operation: transport.OperationResponses, Request: request})
	if err != nil {
		t.Fatal(err)
	}
	body := string(prepared.Body)
	for _, expected := range []string{`"call_id":"call_2"`, `"type":"web_search"`, `"search_context_size":"high"`, `"filename":"a.txt"`, `"content_type":"text/plain"`, `"format":"wav"`, `"language":"en"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("prepared body missing %s: %s", expected, body)
		}
	}
}

func TestDecodeResponsesNormalizesToolArguments(t *testing.T) {
	response, err := decodeResponses([]byte(`{"id":"resp_1","model":"vendor","status":"completed","output":[{"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup","arguments":"{\"q\":\"x\"}"}]}`), transport.Route{PublicModel: "public"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Model != "public" || len(response.Output) != 1 || response.Output[0].CallID != "call_1" || string(response.Output[0].Arguments) != `{"q":"x"}` {
		t.Fatalf("response = %#v", response)
	}
}

func TestDecodeChatToolChunkStartsBeforeInitialArguments(t *testing.T) {
	events, err := decodeChatEvents("", []byte(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"x\"}"}}]}}]}`), transport.Route{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Type != canonical.EventOutputItemAdded || events[1].Type != canonical.EventToolArgumentsDelta {
		t.Fatalf("initial tool events = %#v", events)
	}
	if events[0].Item == nil || events[0].Item.CallID != "call_1" || events[0].Item.Name != "lookup" || events[1].Delta != `{"q":"x"}` {
		t.Fatalf("initial tool events = %#v", events)
	}

	continuation, err := decodeChatEvents("", []byte(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"arguments":"more"}}]}}]}`), transport.Route{})
	if err != nil {
		t.Fatal(err)
	}
	if len(continuation) != 1 || continuation[0].Type != canonical.EventToolArgumentsDelta || continuation[0].Delta != "more" {
		t.Fatalf("continuation events = %#v", continuation)
	}
}

func TestChatMessagesPreserveAssistantContentBeforeToolCalls(t *testing.T) {
	request := canonical.Request{Items: []canonical.Item{
		{Type: "message", Role: canonical.RoleAssistant, Content: []canonical.Content{{Type: "output_text", Text: "let me check"}}},
		{Type: "function_call", CallID: "call_1", Name: "lookup", Arguments: json.RawMessage(`{"q":"x"}`)},
	}}
	messages, err := chatMessages(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("messages = %#v", messages)
	}
	if content, _ := messages[0]["content"].(string); content != "let me check" {
		t.Fatalf("assistant content was dropped: %#v", messages[0])
	}
	if calls, _ := messages[0]["tool_calls"].([]any); len(calls) != 1 {
		t.Fatalf("tool_calls = %#v", messages[0])
	}

	empty := canonical.Request{Items: []canonical.Item{
		{Type: "message", Role: canonical.RoleAssistant},
		{Type: "function_call", CallID: "call_2", Name: "lookup", Arguments: json.RawMessage(`{}`)},
	}}
	messages, err = chatMessages(empty)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0]["content"] != nil {
		t.Fatalf("empty assistant content must stay null: %#v", messages)
	}
}

func TestChatStreamFlushesTerminalsForAllChoices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"a\"}}]}\n\n"+
			"data: {\"choices\":[{\"index\":1,\"delta\":{},\"finish_reason\":\"length\"}]}\n\n"+
			"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"+
			"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3,\"total_tokens\":5}}\n\n"+
			"data: [DONE]\n\n")
	}))
	defer server.Close()
	stream, _, err := transport.Stream(context.Background(), NewChat(server.Client()), transport.Invocation{Route: transport.Route{BaseURL: server.URL}, Operation: transport.OperationChat, Request: canonical.Request{Model: "m", Stream: true, Items: []canonical.Item{{Type: "message", Role: canonical.RoleUser, Content: []canonical.Content{{Type: "input_text", Text: "hi"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	var terminals []canonical.Event
	for {
		event, err := stream.Next(context.Background())
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if event.Type == canonical.EventCompleted || event.Type == canonical.EventIncomplete {
			terminals = append(terminals, event)
		}
	}
	if len(terminals) != 2 {
		t.Fatalf("terminals = %#v", terminals)
	}
	first, second := terminals[0], terminals[1]
	if first.ChoiceIndex != 0 || first.Type != canonical.EventCompleted || first.Response == nil || first.Response.FinishReason != "stop" {
		t.Fatalf("choice 0 terminal = %#v", first)
	}
	if second.ChoiceIndex != 1 || second.Type != canonical.EventIncomplete || second.Response == nil || second.Response.FinishReason != "length" {
		t.Fatalf("choice 1 terminal = %#v", second)
	}
	// Engine 在第一个终态事件上结算,usage 必须已附着。
	if first.Usage == nil || first.Usage.TotalTokens != 5 || second.Usage == nil || second.Usage.TotalTokens != 5 {
		t.Fatalf("terminal usage = %#v / %#v", first.Usage, second.Usage)
	}
	if first.Item == nil || string(first.Item.Extra[chatFinishReason]) != `"stop"` {
		t.Fatalf("choice 0 finish extra = %#v", first.Item)
	}
}

func TestResponsesPrepareDisablesStoreForStatelessRequests(t *testing.T) {
	item := NewResponses(nil)
	prepare := func(request canonical.Request) map[string]json.RawMessage {
		t.Helper()
		prepared, err := item.Prepare(context.Background(), transport.Invocation{Route: transport.Route{BaseURL: "https://example.test"}, Operation: transport.OperationChat, Request: request})
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]json.RawMessage
		if err := json.Unmarshal(prepared.Body, &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	base := canonical.Request{Model: "m", Items: []canonical.Item{{Type: "message", Role: canonical.RoleUser, Content: []canonical.Content{{Type: "input_text", Text: "hi"}}}}}

	if body := prepare(base); string(body["store"]) != "false" {
		t.Fatalf("stateless request must disable store: %s", body["store"])
	}

	storeEnabled := true
	enabled := base
	enabled.Store = &storeEnabled
	if body := prepare(enabled); string(body["store"]) != "true" {
		t.Fatalf("explicit store must be preserved: %s", body["store"])
	}

	continuation := base
	continuation.PreviousResponseID = "resp_prev"
	if body := prepare(continuation); body["store"] != nil {
		t.Fatalf("native continuation must not force store: %s", body["store"])
	}
}

func TestChatStreamSeparatesTextRefusalAndAudioSlots(t *testing.T) {
	frames := []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":"hi"}}]}`,
		`{"choices":[{"index":0,"delta":{"refusal":"no "}}]}`,
		`{"choices":[{"index":0,"delta":{"content":" there"}}]}`,
		`{"choices":[{"index":0,"delta":{"refusal":"way"}}]}`,
		`{"choices":[{"index":0,"delta":{"audio":{"id":"au_1","data":"AAA","transcript":"he"}}}]}`,
		`{"choices":[{"index":0,"delta":{"audio":{"data":"BBB","transcript":"llo"}}}]}`,
	}
	accumulator := canonical.NewEventAccumulator()
	for _, frame := range frames {
		events, err := decodeChatEvents("", []byte(frame), transport.Route{})
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range events {
			if event.Item != nil && len(event.Item.Content) == 1 {
				switch event.Item.Content[0].Type {
				case "refusal":
					if event.ContentIndex != 1 {
						t.Fatalf("refusal content index = %d", event.ContentIndex)
					}
				case "output_audio":
					if event.ContentIndex != 2 {
						t.Fatalf("audio content index = %d", event.ContentIndex)
					}
				}
			}
			accumulator.Observe(event)
		}
	}
	snapshot := accumulator.Snapshot()
	if len(snapshot.Output) != 1 {
		t.Fatalf("output = %#v", snapshot.Output)
	}
	content := snapshot.Output[0].Content
	if len(content) != 3 {
		t.Fatalf("content = %#v", content)
	}
	if content[0].Type != "output_text" || content[0].Text != "hi there" {
		t.Fatalf("text part = %#v", content[0])
	}
	if content[1].Type != "refusal" || content[1].Text != "no way" {
		t.Fatalf("refusal part = %#v", content[1])
	}
	if content[2].Type != "output_audio" || content[2].Data != "AAABBB" || content[2].Transcript != "hello" {
		t.Fatalf("audio part = %#v", content[2])
	}
}

func TestChatStreamTerminalCarriesResponseFinishReason(t *testing.T) {
	event, err := decodeChatEvent("", []byte(`{"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`), transport.Route{})
	if err != nil {
		t.Fatal(err)
	}
	if event.Type != canonical.EventCompleted || event.Response == nil || event.Response.FinishReason != "tool_calls" {
		t.Fatalf("event = %#v", event)
	}
	if event.Item == nil || string(event.Item.Extra[chatFinishReason]) != `"tool_calls"` {
		t.Fatalf("finish extra = %#v", event.Item)
	}
}

func TestDecodeChatContentParsesImageURL(t *testing.T) {
	response, err := decodeChat([]byte(`{"id":"chat_1","choices":[{"index":0,"message":{"role":"assistant","content":[{"type":"text","text":"look"},{"type":"image_url","image_url":{"url":"https://example.test/img.png","detail":"high"}}]},"finish_reason":"stop"}]}`), transport.Route{})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Output) != 1 || len(response.Output[0].Content) != 2 {
		t.Fatalf("output = %#v", response.Output)
	}
	image := response.Output[0].Content[1]
	if image.Type != "image_url" || image.URL != "https://example.test/img.png" || image.Detail != "high" {
		t.Fatalf("image part = %#v", image)
	}
}

func TestChatStreamAttachesTrailingUsageToTerminal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3,\"total_tokens\":5}}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	stream, _, err := transport.Stream(context.Background(), NewChat(server.Client()), transport.Invocation{Route: transport.Route{BaseURL: server.URL}, Operation: transport.OperationChat, Request: canonical.Request{Model: "m", Stream: true, Items: []canonical.Item{{Type: "message", Role: canonical.RoleUser, Content: []canonical.Content{{Type: "input_text", Text: "hi"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	event, err := stream.Next(context.Background())
	if err != nil || event.Type != canonical.EventCompleted || event.Usage == nil || event.Usage.TotalTokens != 5 {
		t.Fatalf("terminal=%#v err=%v", event, err)
	}
}

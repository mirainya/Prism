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

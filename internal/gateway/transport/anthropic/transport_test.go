package anthropic

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

func TestExecuteUsesAnthropicHeaders(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "key" || r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Error("headers")
		}
		w.Write([]byte(`{"id":"msg_1","model":"claude","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":2},"stop_reason":"end_turn"}`))
	}))
	defer s.Close()
	in := transport.Invocation{Route: transport.Route{BaseURL: s.URL, APIKey: "key", PublicModel: "public"}, Operation: transport.OperationChat, Request: canonical.Request{Model: "public", Items: []canonical.Item{{Type: "message", Role: canonical.RoleUser, Content: []canonical.Content{{Type: "input_text", Text: "hi"}}}}}}
	r, _, e := transport.Execute(context.Background(), New(nil), in)
	if e != nil || r.Model != "public" || r.Usage.TotalTokens != 3 {
		t.Fatalf("%#v %v", r, e)
	}
}
func TestPlanRejectsExtensions(t *testing.T) {
	p := New(nil).Plan(transport.OperationResponses, canonical.Request{ClientExtensions: map[string]json.RawMessage{"x": json.RawMessage(`1`)}}, canonical.FeatureSet{})
	if p.Supported() {
		t.Fatal("extensions must be rejected")
	}
}

func TestPlanAcceptsOnlyAnthropicProviderProof(t *testing.T) {
	item := New(nil)
	native := canonical.Request{Items: []canonical.Item{{
		Type:    "reasoning",
		Content: []canonical.Content{{Type: "reasoning_text", Text: "analysis"}},
		Proof:   &canonical.ProviderProof{Provider: canonical.ProofProviderAnthropic, Value: "sig"},
	}}}
	if plan := item.Plan(transport.OperationMessages, native, canonical.NewFeatureSet(canonical.FeatureReasoning)); !plan.Supported() {
		t.Fatalf("native proof was rejected: %#v", plan)
	}
	proofOnly := native.Clone()
	proofOnly.Items[0].Content = nil
	if plan := item.Plan(transport.OperationMessages, proofOnly, canonical.NewFeatureSet(canonical.FeatureReasoning)); plan.Supported() {
		t.Fatalf("proof without its thinking block was accepted: %#v", plan)
	}
	multipart := native.Clone()
	multipart.Items[0].Content = append(multipart.Items[0].Content, canonical.Content{Type: "reasoning_text", Text: "second"})
	if plan := item.Plan(transport.OperationMessages, multipart, canonical.NewFeatureSet(canonical.FeatureReasoning)); plan.Supported() {
		t.Fatalf("one proof was accepted for multiple thinking blocks: %#v", plan)
	}
	wrongContent := native.Clone()
	wrongContent.Items[0].Content[0].Type = "output_text"
	if plan := item.Plan(transport.OperationMessages, wrongContent, canonical.NewFeatureSet(canonical.FeatureReasoning)); plan.Supported() {
		t.Fatalf("proof on non-reasoning content was accepted: %#v", plan)
	}
	foreign := native.Clone()
	foreign.Items[0].Proof.Provider = canonical.ProofProviderGoogle
	if plan := item.Plan(transport.OperationMessages, foreign, canonical.NewFeatureSet(canonical.FeatureReasoning)); plan.Supported() {
		t.Fatalf("foreign proof was accepted: %#v", plan)
	}
	wrongItem := native.Clone()
	wrongItem.Items[0].Type = "function_call"
	if plan := item.Plan(transport.OperationMessages, wrongItem, canonical.NewFeatureSet(canonical.FeatureReasoning, canonical.FeatureTools)); plan.Supported() {
		t.Fatalf("proof on a non-reasoning item was accepted: %#v", plan)
	}
	unsigned := canonical.Request{Items: []canonical.Item{{Type: "reasoning", Content: []canonical.Content{{Type: "reasoning_text", Text: "unsigned"}}}}}
	if plan := item.Plan(transport.OperationMessages, unsigned, canonical.NewFeatureSet(canonical.FeatureReasoning)); plan.Supported() {
		t.Fatalf("unsigned thinking was accepted: %#v", plan)
	}
	redacted := canonical.Request{Items: []canonical.Item{{
		Type:  "reasoning",
		Extra: map[string]json.RawMessage{transport.ExtensionAnthropicRawBlock: json.RawMessage(`{"type":"redacted_thinking","data":"opaque"}`)},
	}}}
	if plan := item.Plan(transport.OperationMessages, redacted, canonical.NewFeatureSet(canonical.FeatureReasoning)); !plan.Supported() {
		t.Fatalf("native redacted thinking was rejected: %#v", plan)
	}
	redactedWithProof := redacted.Clone()
	redactedWithProof.Items[0].Proof = &canonical.ProviderProof{Provider: canonical.ProofProviderAnthropic, Value: "invalid"}
	if plan := item.Plan(transport.OperationMessages, redactedWithProof, canonical.NewFeatureSet(canonical.FeatureReasoning)); plan.Supported() {
		t.Fatalf("redacted thinking with a signature was accepted: %#v", plan)
	}
	redactedOverride := redacted.Clone()
	redactedOverride.Items[0].Content = []canonical.Content{{
		Type: "reasoning_text",
		Extra: map[string]json.RawMessage{
			transport.ExtensionAnthropicRawBlock: json.RawMessage(`{"type":"thinking","thinking":""}`),
		},
	}}
	if plan := item.Plan(transport.OperationMessages, redactedOverride, canonical.NewFeatureSet(canonical.FeatureReasoning)); plan.Supported() {
		t.Fatalf("redacted thinking with a conflicting content block was accepted: %#v", plan)
	}
	redacted.Items[0].Proof = &canonical.ProviderProof{Provider: canonical.ProofProviderGoogle, Value: "foreign"}
	if plan := item.Plan(transport.OperationMessages, redacted, canonical.NewFeatureSet(canonical.FeatureReasoning)); plan.Supported() {
		t.Fatalf("redacted thinking hid a foreign proof: %#v", plan)
	}
	namespaced := canonical.Request{Items: []canonical.Item{{Type: "function_call", Namespace: "tools"}}}
	if plan := item.Plan(transport.OperationMessages, namespaced, canonical.FeatureSet{}); plan.Supported() {
		t.Fatalf("namespace was accepted: %#v", plan)
	}
}

func TestPlanKindsAndNativeExtensionBoundary(t *testing.T) {
	item := New(nil)
	for _, test := range []struct {
		name      string
		operation transport.Operation
		kind      transport.PlanKind
	}{
		{name: "messages native", operation: transport.OperationMessages, kind: transport.PlanExact},
		{name: "chat converted", operation: transport.OperationChat, kind: transport.PlanConverted},
		{name: "responses converted", operation: transport.OperationResponses, kind: transport.PlanConverted},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := item.Plan(test.operation, canonical.Request{}, canonical.FeatureSet{})
			if plan.Kind != test.kind || plan.Upstream != transport.OperationMessages {
				t.Fatalf("plan = %#v", plan)
			}
		})
	}

	native := canonical.Request{Items: []canonical.Item{{Type: "message", Role: canonical.RoleUser, Content: []canonical.Content{{
		Type: "input_text", Text: "hi", Extra: map[string]json.RawMessage{
			transport.ExtensionAnthropicRawBlock: json.RawMessage(`{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}}`),
		},
	}}}}}
	if plan := item.Plan(transport.OperationMessages, native, canonical.FeatureSet{}); !plan.Supported() {
		t.Fatalf("native Anthropic raw block was rejected: %#v", plan)
	}
	if plan := item.Plan(transport.OperationChat, canonical.Request{Items: []canonical.Item{{Type: "message", Extra: map[string]json.RawMessage{"future": json.RawMessage(`true`)}}}}, canonical.FeatureSet{}); plan.Supported() {
		t.Fatalf("foreign item extension must be rejected: %#v", plan)
	}
}

func TestPlanRejectsChatToolsWithAnthropicThinking(t *testing.T) {
	request := canonical.Request{
		Reasoning: &canonical.Reasoning{Raw: json.RawMessage(`{"type":"enabled","budget_tokens":4096}`)},
		Tools:     []canonical.Tool{{Type: "function", Name: "lookup"}},
	}
	features := canonical.NewFeatureSet(canonical.FeatureReasoning, canonical.FeatureTools)
	item := New(nil)
	if plan := item.Plan(transport.OperationChat, request, features); plan.Supported() {
		t.Fatalf("Chat tool route could lose Anthropic thinking proof: %#v", plan)
	}
	if plan := item.Plan(transport.OperationResponses, request, features); !plan.Supported() {
		t.Fatalf("Responses can preserve Anthropic thinking proof: %#v", plan)
	}
}

func TestResponsesStoreIsLocalForAnthropicPlan(t *testing.T) {
	store, noStore := true, false
	item := New(nil)
	if plan := item.Plan(transport.OperationResponses, canonical.Request{Store: &store}, canonical.FeatureSet{}); !plan.Supported() {
		t.Fatalf("Prism-owned Responses storage was rejected: %#v", plan)
	}
	if plan := item.Plan(transport.OperationChat, canonical.Request{Store: &store}, canonical.FeatureSet{}); plan.Supported() {
		t.Fatalf("upstream Chat storage was accepted: %#v", plan)
	}
	if plan := item.Plan(transport.OperationChat, canonical.Request{Store: &noStore}, canonical.FeatureSet{}); !plan.Supported() {
		t.Fatalf("store=false should be safe for a stateless upstream: %#v", plan)
	}
	include := canonical.Request{Include: []string{transport.ResponsesReasoningProofInclude}}
	if plan := item.Plan(transport.OperationResponses, include, canonical.FeatureSet{}); !plan.Supported() {
		t.Fatalf("Prism-owned Responses include was rejected: %#v", plan)
	}
	if plan := item.Plan(transport.OperationChat, include, canonical.FeatureSet{}); plan.Supported() {
		t.Fatalf("foreign include was accepted: %#v", plan)
	}
}

func TestPreparePreservesMediaTypeAndFilename(t *testing.T) {
	request := canonical.Request{Model: "claude", Items: []canonical.Item{{Type: "message", Role: canonical.RoleUser, Content: []canonical.Content{
		{Type: "input_image", Data: "aW1n", MediaType: "image/png"},
		{Type: "input_file", Data: "cGRm", MediaType: "application/pdf", Filename: "report.pdf"},
	}}}}
	prepared, err := New(nil).Prepare(context.Background(), transport.Invocation{Route: transport.Route{BaseURL: "https://example.test"}, Operation: transport.OperationMessages, Request: request})
	if err != nil {
		t.Fatal(err)
	}
	body := string(prepared.Body)
	for _, expected := range []string{`"media_type":"image/png"`, `"media_type":"application/pdf"`, `"type":"document"`, `"title":"report.pdf"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("prepared body missing %s: %s", expected, body)
		}
	}
}

func TestStreamMapsToolUseUsageAndTerminal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		frames := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"claude\",\"usage\":{\"input_tokens\":2}}}\n\n" +
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"tool_1\",\"name\":\"lookup\",\"input\":{}}}\n\n" +
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"q\\\":\"}}\n\n" +
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"\\\"x\\\"}\"}}\n\n" +
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":3}}\n\n" +
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
		_, _ = io.WriteString(writer, frames)
	}))
	defer server.Close()
	stream, _, err := transport.Stream(context.Background(), New(server.Client()), transport.Invocation{Route: transport.Route{BaseURL: server.URL, PublicModel: "public"}, Operation: transport.OperationMessages, Request: canonical.Request{Model: "public", Stream: true, Items: []canonical.Item{{Type: "message", Role: canonical.RoleUser, Content: []canonical.Content{{Type: "input_text", Text: "hi"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	want := []canonical.EventType{canonical.EventResponseCreated, canonical.EventOutputItemAdded, canonical.EventToolArgumentsDelta, canonical.EventToolArgumentsDelta, canonical.EventOutputItemDone, canonical.EventUsage, canonical.EventCompleted}
	accumulator := canonical.NewEventAccumulator()
	var events []canonical.Event
	for range want {
		event, nextErr := stream.Next(context.Background())
		if nextErr != nil {
			t.Fatal(nextErr)
		}
		accumulator.Observe(event)
		events = append(events, event)
	}
	for index, event := range events {
		if event.Type != want[index] {
			t.Fatalf("event %d = %#v", index, event)
		}
	}
	if events[1].Item == nil || events[1].Item.CallID != "tool_1" || events[2].Item == nil || events[2].Item.Name != "lookup" {
		t.Fatalf("tool events = %#v %#v", events[1], events[2])
	}
	if events[4].Item == nil || string(events[4].Item.Arguments) != `{"q":"x"}` {
		t.Fatalf("tool done event = %#v", events[4])
	}
	output := accumulator.Snapshot().Output
	if len(output) != 1 || string(output[0].Arguments) != `{"q":"x"}` {
		t.Fatalf("accumulated tool output = %#v", output)
	}
	terminal := events[len(events)-1]
	if terminal.ProviderResponseID != "msg_1" || terminal.Usage == nil || terminal.Usage.TotalTokens != 5 {
		t.Fatalf("terminal = %#v", terminal)
	}
}

func TestStreamPreservesThinkingSignature(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		frames := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_thinking\",\"model\":\"claude\",\"usage\":{\"input_tokens\":2}}}\n\n" +
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\n" +
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"consider\"}}\n\n" +
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"sig-\"}}\n\n" +
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"native\"}}\n\n" +
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
		_, _ = io.WriteString(writer, frames)
	}))
	defer server.Close()
	stream, _, err := transport.Stream(context.Background(), New(server.Client()), transport.Invocation{
		Route: transport.Route{BaseURL: server.URL, PublicModel: "public"}, Operation: transport.OperationResponses,
		Request: canonical.Request{Model: "public", Stream: true, Items: []canonical.Item{{Type: "message", Role: canonical.RoleUser, Content: []canonical.Content{{Type: "input_text", Text: "hi"}}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	want := []canonical.EventType{
		canonical.EventResponseCreated, canonical.EventContentPartAdded, canonical.EventReasoningDelta,
		canonical.EventProviderProof, canonical.EventProviderProof, canonical.EventContentPartDone, canonical.EventCompleted,
	}
	accumulator := canonical.NewEventAccumulator()
	for index, eventType := range want {
		event, nextErr := stream.Next(context.Background())
		if nextErr != nil {
			t.Fatal(nextErr)
		}
		if event.Type != eventType {
			t.Fatalf("event %d type = %q, want %q", index, event.Type, eventType)
		}
		if event.Type == canonical.EventProviderProof && (event.Item == nil || event.Item.Proof == nil) {
			t.Fatalf("signature event = %#v", event)
		}
		if index == 3 && event.Item.Proof.Value != "sig-" {
			t.Fatalf("first signature fragment = %#v", event.Item.Proof)
		}
		if index == 4 && event.Item.Proof.Value != "sig-native" {
			t.Fatalf("accumulated signature = %#v", event.Item.Proof)
		}
		accumulator.Observe(event)
	}
	output := accumulator.Snapshot().Output
	if len(output) != 1 || len(output[0].Content) != 1 || output[0].Content[0].Text != "consider" || output[0].Proof == nil || output[0].Proof.Provider != canonical.ProofProviderAnthropic || output[0].Proof.Value != "sig-native" {
		t.Fatalf("thinking output = %#v", output)
	}
}

func TestStreamPreservesRedactedThinkingBlock(t *testing.T) {
	decoder := &stream{blocks: map[int]canonical.Item{}, toolDeltas: map[int]bool{}}
	event, emit, err := decoder.decode("content_block_start", []byte(`{"type":"content_block_start","index":2,"content_block":{"type":"redacted_thinking","data":"opaque"}}`))
	if err != nil || !emit {
		t.Fatalf("decode redacted thinking: emit=%v err=%v", emit, err)
	}
	if event.Type != canonical.EventContentPartAdded || event.OutputIndex != 2 || event.Item == nil || event.Item.Type != "reasoning" || !strings.Contains(string(event.Item.Extra[transport.ExtensionAnthropicRawBlock]), `"data":"opaque"`) {
		t.Fatalf("redacted event = %#v", event)
	}
	accumulator := canonical.NewEventAccumulator()
	accumulator.Observe(event)
	output := accumulator.Snapshot().Output
	if event.ContentIndex != 0 || len(output) != 1 || len(output[0].Content) != 1 || output[0].Content[0].Type != "reasoning_text" {
		t.Fatalf("redacted content indexes = event %#v output %#v", event, output)
	}
	request := canonical.Request{Items: []canonical.Item{*event.Item}}
	if plan := New(nil).Plan(transport.OperationMessages, request, canonical.NewFeatureSet(canonical.FeatureReasoning)); !plan.Supported() {
		t.Fatalf("redacted stream block was not replayable: %#v", plan)
	}
}

func TestStreamContentBlockStopKeepsToolIndex(t *testing.T) {
	decoder := &stream{blocks: map[int]canonical.Item{}, toolDeltas: map[int]bool{}}
	frames := []struct{ name, data string }{
		{"message_start", `{"type":"message_start","message":{"id":"msg_1","model":"claude","usage":{"input_tokens":1}}}`},
		{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
		{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"checking"}}`},
		{"content_block_stop", `{"type":"content_block_stop","index":0}`},
		{"content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"tool_1","name":"lookup","input":{}}}`},
		{"content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{}"}}`},
		{"content_block_stop", `{"type":"content_block_stop","index":1}`},
	}
	var events []canonical.Event
	for index, frame := range frames {
		event, emit, err := decoder.decode(frame.name, []byte(frame.data))
		if err != nil || !emit {
			t.Fatalf("frame %d emit=%v err=%v", index, emit, err)
		}
		events = append(events, event)
	}
	if textStop := events[3]; textStop.Type != canonical.EventContentPartDone || textStop.OutputIndex != 0 || textStop.ToolIndex != 0 {
		t.Fatalf("text stop = %#v", textStop)
	}
	toolStop := events[6]
	if toolStop.Type != canonical.EventOutputItemDone || toolStop.OutputIndex != 1 || toolStop.ToolIndex != 1 {
		t.Fatalf("tool stop = %#v", toolStop)
	}
	if events[4].ToolIndex != toolStop.ToolIndex || events[5].ToolIndex != toolStop.ToolIndex {
		t.Fatalf("tool index drifted: start=%d delta=%d stop=%d", events[4].ToolIndex, events[5].ToolIndex, toolStop.ToolIndex)
	}
}

func TestStreamTerminalCarriesFinishReason(t *testing.T) {
	decoder := &stream{blocks: map[int]canonical.Item{}, toolDeltas: map[int]bool{}}
	if _, _, err := decoder.decode("message_start", []byte(`{"type":"message_start","message":{"id":"msg_1","model":"claude","usage":{"input_tokens":2}}}`)); err != nil {
		t.Fatal(err)
	}
	usageEvent, _, err := decoder.decode("message_delta", []byte(`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":3}}`))
	if err != nil {
		t.Fatal(err)
	}
	if usageEvent.Response == nil || usageEvent.Response.FinishReason != "tool_use" || usageEvent.Response.Status != "completed" || usageEvent.Response.Usage == nil {
		t.Fatalf("usage event response = %#v", usageEvent.Response)
	}
	terminal, _, err := decoder.decode("message_stop", []byte(`{"type":"message_stop"}`))
	if err != nil {
		t.Fatal(err)
	}
	if terminal.Type != canonical.EventCompleted || terminal.Response == nil || terminal.Response.FinishReason != "tool_use" {
		t.Fatalf("terminal = %#v response=%#v", terminal, terminal.Response)
	}

	truncated := &stream{blocks: map[int]canonical.Item{}, toolDeltas: map[int]bool{}}
	if _, _, err := truncated.decode("message_delta", []byte(`{"type":"message_delta","delta":{"stop_reason":"max_tokens"},"usage":{"output_tokens":9}}`)); err != nil {
		t.Fatal(err)
	}
	incomplete, _, err := truncated.decode("message_stop", []byte(`{"type":"message_stop"}`))
	if err != nil {
		t.Fatal(err)
	}
	if incomplete.Type != canonical.EventIncomplete || incomplete.Response == nil || incomplete.Response.FinishReason != "max_tokens" || incomplete.Response.Status != "incomplete" {
		t.Fatalf("incomplete terminal = %#v response=%#v", incomplete, incomplete.Response)
	}
}

func TestStreamNormalizesCachedUsage(t *testing.T) {
	decoder := &stream{blocks: map[int]canonical.Item{}, toolDeltas: map[int]bool{}}
	start, _, err := decoder.decode("message_start", []byte(`{"type":"message_start","message":{"id":"msg_1","model":"claude","usage":{"input_tokens":2,"cache_read_input_tokens":8,"cache_creation_input_tokens":5}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if start.Usage == nil || start.Usage.InputTokens != 15 || start.Usage.CachedInputTokens != 8 || !strings.Contains(string(start.Usage.Extra["cache_creation_input_tokens"]), "5") {
		t.Fatalf("start usage = %#v", start.Usage)
	}
	final, _, err := decoder.decode("message_delta", []byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`))
	if err != nil {
		t.Fatal(err)
	}
	if final.Usage == nil || final.Usage.InputTokens != 15 || final.Usage.CachedInputTokens != 8 || final.Usage.OutputTokens != 3 || final.Usage.TotalTokens != 18 {
		t.Fatalf("final usage = %#v", final.Usage)
	}
}

func TestExecuteNormalizesCachedUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Write([]byte(`{"id":"msg_1","model":"claude","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn",
			"usage":{"input_tokens":1,"output_tokens":2,"cache_read_input_tokens":4,"cache_creation_input_tokens":3,"server_tool_use":{"web_search_requests":1}}}`))
	}))
	defer server.Close()
	invocation := transport.Invocation{Route: transport.Route{BaseURL: server.URL, PublicModel: "public"}, Operation: transport.OperationMessages, Request: canonical.Request{Model: "public", Items: []canonical.Item{{Type: "message", Role: canonical.RoleUser, Content: []canonical.Content{{Type: "input_text", Text: "hi"}}}}}}
	response, _, err := transport.Execute(context.Background(), New(nil), invocation)
	if err != nil {
		t.Fatal(err)
	}
	usage := response.Usage
	if usage == nil || usage.InputTokens != 8 || usage.CachedInputTokens != 4 || usage.TotalTokens != 10 {
		t.Fatalf("usage = %#v", usage)
	}
	if !strings.Contains(string(usage.Extra["cache_creation_input_tokens"]), "3") || len(usage.Extra["server_tool_use"]) == 0 {
		t.Fatalf("usage extras were dropped: %#v", usage.Extra)
	}
}

func TestPlanAllowsTextOnlyModalities(t *testing.T) {
	item := New(nil)
	if plan := item.Plan(transport.OperationResponses, canonical.Request{Modalities: []string{"text"}}, canonical.FeatureSet{}); !plan.Supported() {
		t.Fatalf("modalities [text] was rejected: %#v", plan)
	}
	if plan := item.Plan(transport.OperationResponses, canonical.Request{Modalities: []string{"audio"}}, canonical.FeatureSet{}); plan.Supported() {
		t.Fatalf("modalities [audio] was accepted: %#v", plan)
	}
	if plan := item.Plan(transport.OperationResponses, canonical.Request{Modalities: []string{"text", "audio"}}, canonical.FeatureSet{}); plan.Supported() {
		t.Fatalf("modalities [text audio] was accepted: %#v", plan)
	}
}

func TestHTTPErrorIncludesAnthropicDetails(t *testing.T) {
	err := newHTTPError(http.StatusTooManyRequests, []byte(`{"type":"error","error":{"type":"rate_limit_error","message":"slow"}}`))
	var upstream *HTTPError
	if !errors.As(err, &upstream) || upstream.Details == nil || upstream.Details.Status != http.StatusTooManyRequests || upstream.Details.Code != "rate_limit_error" || upstream.Details.Message != "slow" || !upstream.Details.Retryable {
		t.Fatalf("error = %#v", err)
	}
}

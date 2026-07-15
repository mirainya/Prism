package responses

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mirainya/Prism/internal/gateway/canonical"
	openairesponses "github.com/mirainya/Prism/internal/gateway/codec/openai_responses"
	"github.com/mirainya/Prism/internal/model"
	protocol "github.com/mirainya/Prism/internal/provider/responses"
)

type v2StreamEvents struct {
	events []canonical.Event
	errors []error
	index  int
	closed int
}

type v2FailingWriter struct {
	bytes.Buffer
	match string
	err   error
}

func (w *v2FailingWriter) Write(data []byte) (int, error) {
	if strings.Contains(string(data), w.match) {
		return 0, w.err
	}
	return w.Buffer.Write(data)
}

func (s *v2StreamEvents) Next(context.Context) (canonical.Event, error) {
	if s.index >= len(s.events) {
		return canonical.Event{}, io.EOF
	}
	event := s.events[s.index]
	var err error
	if s.index < len(s.errors) {
		err = s.errors[s.index]
	}
	s.index++
	return event, err
}

func (s *v2StreamEvents) Close() error {
	s.closed++
	return nil
}

func TestConsumeV2StreamAggregatesAndReleasesOnce(t *testing.T) {
	events := &v2StreamEvents{events: []canonical.Event{
		{
			Type: canonical.EventResponseCreated,
			Response: &canonical.Response{
				ID: "provider_resp", ProviderResponseID: "provider_resp", Model: "vendor", Status: "in_progress",
			},
		},
		{Type: canonical.EventOutputTextDelta, OutputIndex: 0, ContentIndex: 0, Delta: "hello"},
		{Type: canonical.EventUsage, Usage: &canonical.Usage{InputTokens: 3, OutputTokens: 2, TotalTokens: 5}},
		{Type: canonical.EventCompleted, Response: &canonical.Response{ID: "provider_resp", Status: "completed"}},
	}}
	var output bytes.Buffer
	summary, err := consumeV2Stream(context.Background(), &output, events)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Terminal != canonical.EventCompleted || summary.EventCount != 4 {
		t.Fatalf("terminal summary = %#v", summary)
	}
	if summary.ProviderResponseID != "provider_resp" || summary.Response == nil || summary.Response.Status != "completed" {
		t.Fatalf("response summary = %#v", summary)
	}
	if summary.Usage == nil || summary.Usage.TotalTokens != 5 || summary.Response.Usage == nil {
		t.Fatalf("usage summary = %#v", summary)
	}
	if len(summary.Response.Output) != 1 || len(summary.Response.Output[0].Content) != 1 || summary.Response.Output[0].Content[0].Text != "hello" {
		t.Fatalf("aggregated output = %#v", summary.Response.Output)
	}
	for _, expected := range []string{"event: response.created", "event: response.output_text.delta", "event: response.completed"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("SSE output missing %q: %s", expected, output.String())
		}
	}
	if events.closed != 1 {
		t.Fatalf("close count = %d", events.closed)
	}
}

func TestConsumeV2StreamAggregatesRawTerminalEvent(t *testing.T) {
	raw := json.RawMessage(`{"type":"response.completed","sequence_number":9,"response":{"id":"provider_raw","object":"response","created_at":123,"status":"completed","model":"vendor","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"done"}]}],"usage":{"input_tokens":4,"output_tokens":6,"total_tokens":10,"vendor_cached":2},"vendor_response":{"trace":"ok"}}}`)
	events := &v2StreamEvents{events: []canonical.Event{{Type: canonical.EventRaw, Raw: raw}}}
	var output bytes.Buffer

	summary, err := consumeV2Stream(context.Background(), &output, events)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Terminal != canonical.EventCompleted || summary.ProviderResponseID != "provider_raw" {
		t.Fatalf("raw terminal summary = %#v", summary)
	}
	if summary.Response == nil || len(summary.Response.Output) != 1 || summary.Response.Output[0].Content[0].Text != "done" {
		t.Fatalf("raw response was not aggregated: %#v", summary.Response)
	}
	if summary.Usage == nil || summary.Usage.TotalTokens != 10 || string(summary.Usage.Extra["vendor_cached"]) != "2" {
		t.Fatalf("raw usage was not preserved: %#v", summary.Usage)
	}
	if !strings.HasPrefix(output.String(), "event: response.completed\ndata: ") || !strings.Contains(output.String(), `"vendor_response":{"trace":"ok"}`) {
		t.Fatalf("raw frame was not preserved: %s", output.String())
	}
	if events.closed != 1 {
		t.Fatalf("stream close count = %d", events.closed)
	}
}

func TestConsumeV2StreamRewritesNativePublicResponseIdentity(t *testing.T) {
	raw := json.RawMessage(`{"type":"response.completed","response_id":"provider_raw","response":{"id":"provider_raw","object":"response","status":"completed","model":"vendor","output":[]}}`)
	events := &v2StreamEvents{events: []canonical.Event{{Type: canonical.EventCompleted, RawType: "response.completed", Raw: raw}}}
	var output bytes.Buffer
	_, err := consumeV2Stream(context.Background(), &output, events, V2StreamPublicOptions{
		ResponseID: "resp_public", Model: "public", PreviousResponseID: "resp_previous", Store: true, PreserveNativeRaw: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"response_id":"resp_public"`, `"id":"resp_public"`, `"model":"public"`, `"previous_response_id":"resp_previous"`} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("output missing %s: %s", expected, output.String())
		}
	}
}

func TestConsumeV2StreamBuildsCompleteConvertedLifecycle(t *testing.T) {
	events := &v2StreamEvents{events: []canonical.Event{
		{Type: canonical.EventOutputTextDelta, OutputIndex: 0, ContentIndex: 0, Delta: "hel"},
		{Type: canonical.EventOutputTextDelta, OutputIndex: 0, ContentIndex: 0, Delta: "lo"},
		{Type: canonical.EventCompleted, ProviderResponseID: "provider_resp", Usage: &canonical.Usage{InputTokens: 2, OutputTokens: 1, TotalTokens: 3}},
	}}
	var output bytes.Buffer
	summary, err := consumeV2Stream(context.Background(), &output, events, V2StreamPublicOptions{
		ResponseID: "resp_public", Model: "public-model", CreatedAt: 123, Store: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"response.created",
		"response.output_item.added",
		"response.content_part.added",
		"response.output_text.delta",
		"response.output_text.delta",
		"response.output_text.done",
		"response.content_part.done",
		"response.output_item.done",
		"response.completed",
	}
	if got := v2SSEEventNames(output.String()); !equalV2Strings(got, want) {
		t.Fatalf("event lifecycle = %#v, want %#v\n%s", got, want, output.String())
	}
	for _, expected := range []string{`"id":"resp_public"`, `"model":"public-model"`, `"created_at":123`, `"text":"hello"`} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("converted stream missing %s: %s", expected, output.String())
		}
	}
	if summary == nil || summary.Terminal != canonical.EventCompleted || summary.ProviderResponseID != "provider_resp" {
		t.Fatalf("summary = %#v", summary)
	}
	if summary.Response == nil || len(summary.Response.Output) != 1 || summary.Response.Output[0].Content[0].Text != "hello" {
		t.Fatalf("aggregated response = %#v", summary.Response)
	}
}

func TestConsumeV2StreamDoesNotDuplicateExistingConvertedLifecycle(t *testing.T) {
	message := canonical.Item{ID: "msg_provider", Type: "message", Role: canonical.RoleAssistant, Status: "in_progress"}
	content := canonical.Item{ID: message.ID, Type: message.Type, Role: message.Role, Content: []canonical.Content{{Type: "output_text"}}}
	done := canonical.Item{ID: message.ID, Type: message.Type, Role: message.Role, Status: "completed", Content: []canonical.Content{{Type: "output_text", Text: "hello"}}}
	events := &v2StreamEvents{events: []canonical.Event{
		{Type: canonical.EventResponseCreated, Response: &canonical.Response{ID: "provider_resp", Model: "vendor", Status: "in_progress"}},
		{Type: canonical.EventOutputItemAdded, OutputIndex: 0, Item: &message},
		{Type: canonical.EventContentPartAdded, OutputIndex: 0, ContentIndex: 0, Item: &content},
		{Type: canonical.EventOutputTextDelta, OutputIndex: 0, ContentIndex: 0, Item: &message, Delta: "hello"},
		{Type: canonical.EventTextDone, OutputIndex: 0, ContentIndex: 0, Item: &message, Delta: "hello"},
		{Type: canonical.EventContentPartDone, OutputIndex: 0, ContentIndex: 0, Item: &content},
		{Type: canonical.EventOutputItemDone, OutputIndex: 0, Item: &done},
		{Type: canonical.EventCompleted, Response: &canonical.Response{ID: "provider_resp", Model: "vendor", Status: "completed"}},
	}}
	var output bytes.Buffer
	_, err := consumeV2Stream(context.Background(), &output, events, V2StreamPublicOptions{ResponseID: "resp_public", Model: "public"})
	if err != nil {
		t.Fatal(err)
	}
	names := v2SSEEventNames(output.String())
	for _, name := range []string{
		"response.created", "response.output_item.added", "response.content_part.added",
		"response.output_text.done", "response.content_part.done", "response.output_item.done", "response.completed",
	} {
		if countV2String(names, name) != 1 {
			t.Fatalf("%s count = %d, events = %#v", name, countV2String(names, name), names)
		}
	}
}

func TestConsumeV2StreamBuildsConvertedToolLifecycle(t *testing.T) {
	tool := canonical.Item{
		ID: "fc_provider", Type: "function_call", CallID: "call_provider", Name: "lookup",
		Arguments: json.RawMessage(`{}`),
	}
	events := &v2StreamEvents{events: []canonical.Event{
		{Type: canonical.EventToolArgumentsDelta, ToolIndex: 0, Item: &tool, Delta: `{"query":"hi"}`},
		{Type: canonical.EventCompleted, ProviderResponseID: "provider_resp"},
	}}
	var output bytes.Buffer
	summary, err := consumeV2Stream(context.Background(), &output, events, V2StreamPublicOptions{ResponseID: "resp_public", Model: "public"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"response.created",
		"response.output_item.added",
		"response.function_call_arguments.delta",
		"response.function_call_arguments.done",
		"response.output_item.done",
		"response.completed",
	}
	if got := v2SSEEventNames(output.String()); !equalV2Strings(got, want) {
		t.Fatalf("tool lifecycle = %#v, want %#v\n%s", got, want, output.String())
	}
	if strings.Contains(output.String(), `{}{"query"`) || !strings.Contains(output.String(), `"arguments":"{\"query\":\"hi\"}"`) {
		t.Fatalf("tool arguments were not assembled correctly: %s", output.String())
	}
	if summary == nil || summary.Response == nil || len(summary.Response.Output) != 1 || string(summary.Response.Output[0].Arguments) != `{"query":"hi"}` {
		t.Fatalf("aggregated tool output = %#v", summary)
	}
}

func TestConsumeV2StreamPreservesAnthropicReasoningTextAndParallelTools(t *testing.T) {
	reasoning := canonical.Item{
		ID: "thinking_0", Type: "reasoning", Role: canonical.RoleAssistant,
		Content: []canonical.Content{{Type: "reasoning_text"}},
	}
	message := canonical.Item{
		ID: "message_0", Type: "message", Role: canonical.RoleAssistant,
		Content: []canonical.Content{{Type: "output_text"}},
	}
	firstTool := canonical.Item{
		ID: "tool_1", Type: "function_call", CallID: "tool_1", Name: "search",
		Arguments: json.RawMessage(`{}`), Status: "in_progress",
	}
	secondTool := canonical.Item{
		ID: "tool_3", Type: "function_call", CallID: "tool_3", Name: "lookup",
		Arguments: json.RawMessage(`{}`), Status: "in_progress",
	}
	completedFirstTool := firstTool
	completedFirstTool.Arguments = json.RawMessage(`{"query":"alpha"}`)
	completedFirstTool.Status = "completed"
	completedSecondTool := secondTool
	completedSecondTool.Arguments = json.RawMessage(`{"id":42}`)
	completedSecondTool.Status = "completed"

	events := &v2StreamEvents{events: []canonical.Event{
		{Type: canonical.EventContentPartAdded, OutputIndex: 0, ContentIndex: 0, Item: &reasoning},
		{Type: canonical.EventReasoningDelta, OutputIndex: 0, ContentIndex: 0, Item: &reasoning, Delta: "think"},
		{Type: canonical.EventContentPartDone, OutputIndex: 0, ContentIndex: 0, Item: &reasoning},
		{Type: canonical.EventOutputItemAdded, OutputIndex: 0, ToolIndex: 1, Item: &firstTool},
		{Type: canonical.EventToolArgumentsDelta, OutputIndex: 0, ToolIndex: 1, Item: &firstTool, Delta: `{"query":"alpha"}`},
		{Type: canonical.EventOutputItemDone, OutputIndex: 0, ToolIndex: 1, Item: &completedFirstTool},
		{Type: canonical.EventContentPartAdded, OutputIndex: 0, ContentIndex: 2, Item: &message},
		{Type: canonical.EventTextDelta, OutputIndex: 0, ContentIndex: 2, Item: &message, Delta: "answer"},
		{Type: canonical.EventContentPartDone, OutputIndex: 0, ContentIndex: 2, Item: &message},
		{Type: canonical.EventOutputItemAdded, OutputIndex: 0, ToolIndex: 3, Item: &secondTool},
		{Type: canonical.EventToolArgumentsDelta, OutputIndex: 0, ToolIndex: 3, Item: &secondTool, Delta: `{"id":42}`},
		{Type: canonical.EventOutputItemDone, OutputIndex: 0, ToolIndex: 3, Item: &completedSecondTool},
		{Type: canonical.EventCompleted, ProviderResponseID: "provider_resp"},
	}}
	var output bytes.Buffer
	summary, err := consumeV2Stream(context.Background(), &output, events, V2StreamPublicOptions{
		ResponseID: "resp_public", Model: "public-model", CreatedAt: 123,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertAnthropicCompositeOutput(t, summary.Response.Output)
	wire := output.String()
	for _, index := range []string{`"output_index":0`, `"output_index":1`, `"output_index":2`, `"output_index":3`} {
		if !strings.Contains(wire, index) {
			t.Fatalf("converted stream is missing %s: %s", index, wire)
		}
	}
	if strings.Contains(wire, `"content_index":2`) {
		t.Fatalf("converted stream leaked the Anthropic block index as a content index: %s", wire)
	}

	publicResponse, err := publicV2StreamResponse(summary, &model.AIResponse{
		ID: "resp_public", Model: "public-model", CreatedAt: time.Unix(123, 0),
	}, &protocol.Request{Model: "public-model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := openairesponses.DecodeItems(publicResponse.Output)
	if err != nil {
		t.Fatal(err)
	}
	assertAnthropicCompositeOutput(t, persisted)
}

func TestConsumeV2StreamPreservesNormalizedPartialAnthropicOutput(t *testing.T) {
	reasoning := canonical.Item{
		ID: "thinking_0", Type: "reasoning", Role: canonical.RoleAssistant,
		Content: []canonical.Content{{Type: "reasoning_text"}},
	}
	message := canonical.Item{
		ID: "message_2", Type: "message", Role: canonical.RoleAssistant,
		Content: []canonical.Content{{Type: "output_text"}},
	}
	events := &v2StreamEvents{events: []canonical.Event{
		{Type: canonical.EventContentPartAdded, OutputIndex: 0, ContentIndex: 0, Item: &reasoning},
		{Type: canonical.EventReasoningDelta, OutputIndex: 0, ContentIndex: 0, Item: &reasoning, Delta: "partial thought"},
		{Type: canonical.EventContentPartDone, OutputIndex: 0, ContentIndex: 0, Item: &reasoning},
		{Type: canonical.EventContentPartAdded, OutputIndex: 0, ContentIndex: 2, Item: &message},
		{Type: canonical.EventTextDelta, OutputIndex: 0, ContentIndex: 2, Item: &message, Delta: "partial answer"},
	}}
	writeErr := errors.New("client disconnected")
	writer := &v2FailingWriter{match: "response.output_text.delta", err: writeErr}
	summary, err := consumeV2Stream(context.Background(), writer, events, V2StreamPublicOptions{
		ResponseID: "resp_public", Model: "public-model", CreatedAt: 123,
	})
	if !errors.Is(err, writeErr) {
		t.Fatalf("stream error = %v", err)
	}
	if summary == nil || summary.Response == nil || summary.Terminal != "" {
		t.Fatalf("partial summary = %#v", summary)
	}
	if len(summary.Response.Output) != 2 {
		t.Fatalf("partial output = %#v", summary.Response.Output)
	}
	for _, item := range summary.Response.Output {
		for _, content := range item.Content {
			if content.Type == "" {
				t.Fatalf("partial output contains an empty content type: %#v", summary.Response.Output)
			}
		}
	}
	if canonicalItemText(summary.Response.Output[0]) != "partial thought" || canonicalItemText(summary.Response.Output[1]) != "partial answer" {
		t.Fatalf("partial output text = %#v", summary.Response.Output)
	}
}

func assertAnthropicCompositeOutput(t *testing.T, items []canonical.Item) {
	t.Helper()
	if len(items) != 4 {
		t.Fatalf("output count = %d, want 4: %#v", len(items), items)
	}
	byType := make(map[string][]canonical.Item)
	for _, item := range items {
		for _, content := range item.Content {
			if content.Type == "" {
				t.Fatalf("output contains an empty content type: %#v", items)
			}
		}
		byType[item.Type] = append(byType[item.Type], item)
	}
	if len(byType["reasoning"]) != 1 || canonicalItemText(byType["reasoning"][0]) != "think" {
		t.Fatalf("reasoning output = %#v", byType["reasoning"])
	}
	if len(byType["message"]) != 1 || canonicalItemText(byType["message"][0]) != "answer" {
		t.Fatalf("message output = %#v", byType["message"])
	}
	if len(byType["function_call"]) != 2 {
		t.Fatalf("tool output = %#v", byType["function_call"])
	}
	arguments := map[string]string{}
	for _, item := range byType["function_call"] {
		arguments[item.CallID] = string(item.Arguments)
	}
	if arguments["tool_1"] != `{"query":"alpha"}` || arguments["tool_3"] != `{"id":42}` {
		t.Fatalf("tool arguments = %#v", arguments)
	}
}

func canonicalItemText(item canonical.Item) string {
	var text strings.Builder
	for _, content := range item.Content {
		text.WriteString(content.Text)
	}
	return text.String()
}

func TestConsumeV2StreamPreservesUnknownNativeEventWithoutNumericLoss(t *testing.T) {
	unknown := json.RawMessage(`{"type":"response.vendor.trace","response_id":"provider_resp","vendor":{"request_number":9007199254740993,"enabled":true}}`)
	terminal := json.RawMessage(`{"type":"response.completed","response":{"id":"provider_resp","object":"response","status":"completed","model":"vendor","output":[]}}`)
	events := &v2StreamEvents{events: []canonical.Event{
		{Type: canonical.EventRaw, RawType: "response.vendor.trace", Raw: unknown, ProviderResponseID: "provider_resp"},
		{Type: canonical.EventCompleted, RawType: "response.completed", Raw: terminal, ProviderResponseID: "provider_resp"},
	}}
	var output bytes.Buffer
	_, err := consumeV2Stream(context.Background(), &output, events, V2StreamPublicOptions{
		ResponseID: "resp_public", Model: "public", PreserveNativeRaw: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, expected := range []string{
		"event: response.vendor.trace", `"response_id":"resp_public"`, `"request_number":9007199254740993`,
		`"enabled":true`, `"id":"resp_public"`, `"model":"public"`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("native stream missing %s: %s", expected, text)
		}
	}
}

func TestConsumeV2StreamRequiresTerminalEvent(t *testing.T) {
	events := &v2StreamEvents{events: []canonical.Event{{Type: canonical.EventOutputTextDelta, Delta: "partial"}}}
	var output bytes.Buffer
	summary, err := consumeV2Stream(context.Background(), &output, events)
	if !errors.Is(err, ErrV2StreamMissingTerminal) {
		t.Fatalf("error = %v", err)
	}
	if summary == nil || summary.EventCount != 1 || summary.Terminal != "" {
		t.Fatalf("partial summary = %#v", summary)
	}
	if !strings.Contains(output.String(), "partial") || events.closed != 1 {
		t.Fatalf("partial output=%q close=%d", output.String(), events.closed)
	}
}

func TestConsumeV2StreamAggregatesErrorTerminal(t *testing.T) {
	raw := json.RawMessage(`{"type":"error","code":"rate_limit_exceeded","message":"too many requests","param":"model"}`)
	events := &v2StreamEvents{events: []canonical.Event{{Type: canonical.EventRaw, Raw: raw}}}
	var output bytes.Buffer
	summary, err := consumeV2Stream(context.Background(), &output, events)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Terminal != canonical.EventError || summary.Error == nil || summary.Error.Code != "rate_limit_exceeded" || summary.Error.Message != "too many requests" {
		t.Fatalf("error summary = %#v", summary)
	}
	if summary.Response == nil || summary.Response.Status != "failed" || summary.Response.Error == nil {
		t.Fatalf("failed response = %#v", summary.Response)
	}
	if !strings.HasPrefix(output.String(), "event: error\ndata: ") {
		t.Fatalf("error frame = %s", output.String())
	}
}

func TestConsumeV2StreamWritesTerminalEventReturnedWithError(t *testing.T) {
	settleErr := errors.New("settlement failed")
	events := &v2StreamEvents{
		events: []canonical.Event{{Type: canonical.EventCompleted, Response: &canonical.Response{ID: "provider", Status: "completed"}}},
		errors: []error{settleErr},
	}
	var output bytes.Buffer
	summary, err := consumeV2Stream(context.Background(), &output, events)
	if !errors.Is(err, settleErr) {
		t.Fatalf("error = %v", err)
	}
	if summary == nil || summary.Terminal != canonical.EventCompleted || summary.Response == nil {
		t.Fatalf("terminal summary = %#v", summary)
	}
	if !strings.Contains(output.String(), "event: response.completed") || events.closed != 1 {
		t.Fatalf("output=%q close=%d", output.String(), events.closed)
	}
}

func v2SSEEventNames(stream string) []string {
	lines := strings.Split(stream, "\n")
	result := make([]string, 0)
	for _, line := range lines {
		if strings.HasPrefix(line, "event: ") {
			result = append(result, strings.TrimPrefix(line, "event: "))
		}
	}
	return result
}

func equalV2Strings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func countV2String(values []string, target string) int {
	count := 0
	for _, value := range values {
		if value == target {
			count++
		}
	}
	return count
}

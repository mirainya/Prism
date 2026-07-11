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
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"q\\\":\\\"x\\\"}\"}}\n\n" +
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
	want := []canonical.EventType{canonical.EventResponseCreated, canonical.EventOutputItemAdded, canonical.EventToolArgumentsDelta, canonical.EventOutputItemDone, canonical.EventUsage, canonical.EventCompleted}
	var events []canonical.Event
	for range want {
		event, nextErr := stream.Next(context.Background())
		if nextErr != nil {
			t.Fatal(nextErr)
		}
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
	terminal := events[len(events)-1]
	if terminal.ProviderResponseID != "msg_1" || terminal.Usage == nil || terminal.Usage.TotalTokens != 5 {
		t.Fatalf("terminal = %#v", terminal)
	}
}

func TestHTTPErrorIncludesAnthropicDetails(t *testing.T) {
	err := newHTTPError(http.StatusTooManyRequests, []byte(`{"type":"error","error":{"type":"rate_limit_error","message":"slow"}}`))
	var upstream *HTTPError
	if !errors.As(err, &upstream) || upstream.Details == nil || upstream.Details.Status != http.StatusTooManyRequests || upstream.Details.Code != "rate_limit_error" || upstream.Details.Message != "slow" || !upstream.Details.Retryable {
		t.Fatalf("error = %#v", err)
	}
}

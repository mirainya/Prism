package google

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/gateway/transport"
)

func TestGoogleConvertsAnthropicMessages(t *testing.T) {
	plan := New(nil).Plan(transport.OperationMessages, canonical.Request{Endpoint: canonical.EndpointAnthropic}, canonical.FeatureSet{})
	if !plan.Supported() || plan.Kind != transport.PlanConverted {
		t.Fatalf("plan=%#v", plan)
	}
}

func TestExecuteGenerateContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1beta/models/gemini-test:generateContent" {
			t.Errorf("path = %s", request.URL.Path)
		}
		if request.URL.Query().Get("key") != "key" || request.Header.Get("X-Test") != "yes" {
			t.Errorf("authentication or headers missing")
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if _, ok := body["contents"]; !ok {
			t.Fatal("contents missing")
		}
		_, _ = writer.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"hello"},{"functionCall":{"name":"weather","args":{"city":"Shanghai"}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":3,"totalTokenCount":5}}`))
	}))
	defer server.Close()

	invocation := transport.Invocation{
		Route:     transport.Route{BaseURL: server.URL, APIKey: "key", VendorModel: "gemini-test", PublicModel: "public", ExtraHeaders: map[string]string{"X-Test": "yes"}},
		Operation: transport.OperationChat,
		Request:   canonical.Request{Model: "public", Items: []canonical.Item{{Type: "message", Role: canonical.RoleUser, Content: []canonical.Content{{Type: "input_text", Text: "hi"}}}}},
	}
	response, prepared, err := transport.Execute(context.Background(), New(nil), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Stream {
		t.Fatal("non-streaming invocation prepared a stream")
	}
	if response.Model != "public" || response.Usage == nil || response.Usage.TotalTokens != 5 || len(response.Output) != 2 {
		t.Fatalf("response = %#v", response)
	}
}

func TestStreamGenerateContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1beta/models/gemini-test:streamGenerateContent" || request.URL.Query().Get("alt") != "sse" {
			t.Errorf("stream URL = %s", request.URL.String())
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hel\"}]}}]}\n\n"))
		_, _ = writer.Write([]byte("data: {\"candidates\":[{\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":1,\"candidatesTokenCount\":2,\"totalTokenCount\":3}}\n\n"))
	}))
	defer server.Close()

	invocation := transport.Invocation{Route: transport.Route{BaseURL: server.URL, VendorModel: "gemini-test"}, Operation: transport.OperationChat, Request: canonical.Request{Model: "m", Stream: true, Items: []canonical.Item{{Type: "message", Role: canonical.RoleUser, Content: []canonical.Content{{Type: "input_text", Text: "hi"}}}}}}
	stream, prepared, err := transport.Stream(context.Background(), New(nil), invocation)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if !prepared.Stream {
		t.Fatal("stream request was not marked streaming")
	}
	first, err := stream.Next(context.Background())
	if err != nil || first.Type != canonical.EventTextDelta || first.Delta != "hel" {
		t.Fatalf("first event=%#v err=%v", first, err)
	}
	second, err := stream.Next(context.Background())
	if err != nil || second.Type != canonical.EventCompleted || second.Usage == nil || second.Usage.TotalTokens != 3 {
		t.Fatalf("second event=%#v err=%v", second, err)
	}
	_, err = stream.Next(context.Background())
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestPlanStatesConversionLimits(t *testing.T) {
	item := New(nil)
	for _, operation := range []transport.Operation{transport.OperationChat, transport.OperationResponses, transport.OperationMessages} {
		converted := item.Plan(operation, canonical.Request{}, canonical.NewFeatureSet())
		if converted.Kind != transport.PlanConverted || converted.Upstream != transport.OperationChat {
			t.Fatalf("%s plan = %#v", operation, converted)
		}
	}
	multimodal := item.Plan(transport.OperationResponses, canonical.Request{}, canonical.NewFeatureSet(canonical.FeatureAudio, canonical.FeatureVideo))
	if multimodal.Kind != transport.PlanConverted {
		t.Fatalf("multimodal plan = %#v", multimodal)
	}
	unsupported := item.Plan(transport.OperationResponses, canonical.Request{Reasoning: &canonical.Reasoning{Effort: "high"}}, canonical.NewFeatureSet(canonical.FeatureReasoning))
	if unsupported.Kind != transport.PlanUnsupported {
		t.Fatalf("reasoning plan = %#v", unsupported)
	}
	withItemExtension := canonical.Request{Items: []canonical.Item{{Type: "message", Extra: map[string]json.RawMessage{"future": json.RawMessage(`true`)}}}}
	if plan := item.Plan(transport.OperationChat, withItemExtension, canonical.FeatureSet{}); plan.Supported() {
		t.Fatalf("item extension plan = %#v", plan)
	}
	withToolOptions := canonical.Request{Tools: []canonical.Tool{{Type: "function", Name: "lookup", Options: json.RawMessage(`{"future":true}`)}}}
	if plan := item.Plan(transport.OperationResponses, withToolOptions, canonical.NewFeatureSet(canonical.FeatureTools)); plan.Supported() {
		t.Fatalf("tool options plan = %#v", plan)
	}
	withChoiceExtension := canonical.Request{ToolChoice: &canonical.ToolChoice{Mode: "auto", Raw: json.RawMessage(`{"type":"auto","future":true}`)}}
	if plan := item.Plan(transport.OperationMessages, withChoiceExtension, canonical.NewFeatureSet(canonical.FeatureTools)); plan.Supported() {
		t.Fatalf("tool choice extension plan = %#v", plan)
	}
}

func TestResponsesStoreIsLocalForGooglePlan(t *testing.T) {
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

func TestPreparePreservesMultimodalAndFunctionCallID(t *testing.T) {
	request := canonical.Request{Model: "m", Modalities: []string{"TEXT"}, Items: []canonical.Item{
		{Type: "message", Role: canonical.RoleUser, Content: []canonical.Content{{Type: "input_audio", Data: "YQ==", MediaType: "audio/wav"}, {Type: "input_video", URL: "gs://bucket/video.mp4", MediaType: "video/mp4"}}},
		{Type: "function_call", CallID: "call_1", Name: "lookup", Arguments: json.RawMessage(`{"q":"x"}`)},
		{Type: "function_call_output", CallID: "call_1", Output: json.RawMessage(`"ok"`)},
	}}
	prepared, err := New(nil).Prepare(context.Background(), transport.Invocation{Route: transport.Route{BaseURL: "https://example.test", VendorModel: "gemini"}, Operation: transport.OperationChat, Request: request})
	if err != nil {
		t.Fatal(err)
	}
	body := string(prepared.Body)
	for _, expected := range []string{`"mimeType":"audio/wav"`, `"fileUri":"gs://bucket/video.mp4"`, `"id":"call_1"`, `"responseModalities":["TEXT"]`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("prepared body missing %s: %s", expected, body)
		}
	}
}

func TestPrepareNormalizesAnthropicToolChoice(t *testing.T) {
	request := canonical.Request{
		Model: "m", Items: []canonical.Item{{Type: "message", Role: canonical.RoleUser, Content: []canonical.Content{{Type: "input_text", Text: "hi"}}}},
		ToolChoice: &canonical.ToolChoice{Mode: "tool", Type: "tool", Name: "lookup", Raw: json.RawMessage(`{"type":"tool","name":"lookup"}`)},
	}
	item := New(nil)
	if plan := item.Plan(transport.OperationMessages, request, canonical.NewFeatureSet(canonical.FeatureTools)); !plan.Supported() {
		t.Fatalf("standard Anthropic tool choice was rejected: %#v", plan)
	}
	prepared, err := item.Prepare(context.Background(), transport.Invocation{Route: transport.Route{BaseURL: "https://example.test", VendorModel: "gemini"}, Operation: transport.OperationMessages, Request: request})
	if err != nil {
		t.Fatal(err)
	}
	if body := string(prepared.Body); !strings.Contains(body, `"allowedFunctionNames":["lookup"]`) {
		t.Fatalf("tool choice was not normalized: %s", body)
	}
}

func TestDecodeResponsePreservesProviderIDToolIDAndInlineMedia(t *testing.T) {
	response, err := decodeResponse([]byte(`{"responseId":"resp_1","modelVersion":"gemini-2","candidates":[{"content":{"parts":[{"functionCall":{"id":"call_1","name":"lookup","args":{"q":"x"}}},{"inlineData":{"mimeType":"image/png","data":"aW1n"}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":2,"totalTokenCount":3}}`), transport.Route{PublicModel: "public"})
	if err != nil {
		t.Fatal(err)
	}
	if response.ID != "resp_1" || response.ProviderResponseID != "resp_1" || response.Model != "public" || len(response.Output) != 2 || response.Output[0].CallID != "call_1" || response.Output[1].Content[0].Type != "output_image" {
		t.Fatalf("response = %#v", response)
	}
}

func TestStreamMapsMaxTokensToIncompleteWithUsage(t *testing.T) {
	events, err := decodeStreamEvents([]byte(`{"responseId":"resp_2","candidates":[{"content":{"parts":[{"text":"partial"}]},"finishReason":"MAX_TOKENS"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":3,"totalTokenCount":5}}`), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Type != canonical.EventTextDelta || events[1].Type != canonical.EventIncomplete || events[1].Usage == nil || events[1].Usage.TotalTokens != 5 || events[1].ProviderResponseID != "resp_2" {
		t.Fatalf("events = %#v", events)
	}
}

func TestHTTPErrorPreservesNumericCode(t *testing.T) {
	err := newHTTPError(http.StatusTooManyRequests, []byte(`{"error":{"code":429,"message":"slow","status":"RESOURCE_EXHAUSTED"}}`))
	var upstream *HTTPError
	if !errors.As(err, &upstream) || upstream.Details == nil || upstream.Details.Code != "RESOURCE_EXHAUSTED" || upstream.Details.Message != "slow" || !upstream.Details.Retryable {
		t.Fatalf("error = %#v", err)
	}
}

package volcengine

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
	anthropiccodec "github.com/mirainya/Prism/internal/gateway/codec/anthropic"
	openaichatcodec "github.com/mirainya/Prism/internal/gateway/codec/openai_chat"
	gatewaytransport "github.com/mirainya/Prism/internal/gateway/transport"
	"github.com/mirainya/Prism/internal/provider/chat"
)

func TestVolcengineConvertsAnthropicMessages(t *testing.T) {
	plan := New(gatewaytransport.HTTPClient{}).Plan(gatewaytransport.OperationMessages, canonical.Request{Endpoint: canonical.EndpointAnthropic}, canonical.FeatureSet{})
	if !plan.Supported() || plan.Kind != gatewaytransport.PlanConverted {
		t.Fatalf("plan=%#v", plan)
	}
}

func TestPlanKindsAndExtensionBoundaries(t *testing.T) {
	item := New(gatewaytransport.HTTPClient{})
	for _, test := range []struct {
		name      string
		operation gatewaytransport.Operation
		kind      gatewaytransport.PlanKind
	}{
		{name: "responses native", operation: gatewaytransport.OperationResponses, kind: gatewaytransport.PlanExact},
		{name: "chat converted", operation: gatewaytransport.OperationChat, kind: gatewaytransport.PlanConverted},
		{name: "messages converted", operation: gatewaytransport.OperationMessages, kind: gatewaytransport.PlanConverted},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := item.Plan(test.operation, canonical.Request{}, canonical.FeatureSet{})
			if plan.Kind != test.kind || plan.Upstream != gatewaytransport.OperationResponses {
				t.Fatalf("plan = %#v", plan)
			}
		})
	}

	for _, field := range []string{"conversation", "prompt", "stream_options", "top_logprobs", "metadata", "truncation", "prompt_cache_retention", "user"} {
		t.Run(field, func(t *testing.T) {
			request := canonical.Request{ClientExtensions: map[string]json.RawMessage{
				"openai_responses.request_extras": json.RawMessage(`{"` + field + `":true}`),
			}}
			if plan := item.Plan(gatewaytransport.OperationResponses, request, canonical.FeatureSet{}); plan.Supported() {
				t.Fatalf("documented unsupported field %q was accepted: %#v", field, plan)
			}
		})
	}
	if plan := item.Plan(gatewaytransport.OperationResponses, canonical.Request{User: "user-1"}, canonical.FeatureSet{}); plan.Supported() {
		t.Fatalf("documented unsupported user field was accepted: %#v", plan)
	}
	metadata := canonical.Request{Metadata: map[string]string{"trace": "x"}}
	if plan := item.Plan(gatewaytransport.OperationResponses, metadata, canonical.FeatureSet{}); plan.Supported() {
		t.Fatalf("documented unsupported metadata was accepted: %#v", plan)
	}
	metadata.ClientExtensions = map[string]json.RawMessage{"openai_responses.request_extras": json.RawMessage(`{"metadata":{"trace":"x"}}`)}
	metadata.Metadata = nil
	if plan := item.Plan(gatewaytransport.OperationResponses, metadata, canonical.FeatureSet{}); plan.Supported() {
		t.Fatalf("metadata hidden in request_extras was accepted: %#v", plan)
	}
	metadata.ClientExtensions = nil
	metadata.ProviderOptions.Volcengine = &canonical.VolcengineOptions{Unknown: map[string]json.RawMessage{"metadata": json.RawMessage(`{"trace":"x"}`)}}
	if plan := item.Plan(gatewaytransport.OperationResponses, metadata, canonical.FeatureSet{}); plan.Supported() {
		t.Fatalf("metadata hidden in provider extensions was accepted: %#v", plan)
	}
	fileSearch := canonical.Request{Tools: []canonical.Tool{{Type: "file_search"}}}
	if plan := item.Plan(gatewaytransport.OperationResponses, fileSearch, canonical.NewFeatureSet(canonical.FeatureTools, canonical.FeatureFileSearch)); plan.Supported() {
		t.Fatalf("documented unsupported file_search was accepted: %#v", plan)
	}
}

func TestPrepareAddsOnlyDocumentedBetaHeaders(t *testing.T) {
	transport := New(gatewaytransport.HTTPClient{})
	call := invocation(false, []canonical.Tool{{Type: "web_search"}, {Type: "mcp"}, {Type: "image_process"}, {Type: "knowledge_search"}, {Type: "doubao_app"}})
	call.Request.ClientExtensions = map[string]json.RawMessage{"openai_responses.request_extras": json.RawMessage(`{"future_response_option":true}`)}
	prepared, err := transport.Prepare(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.URL != "https://ark.example"+responsesPath || prepared.Headers.Get("ark-beta-web-search") != "" {
		t.Fatalf("unexpected URL/headers: %s %#v", prepared.URL, prepared.Headers)
	}
	for _, name := range []string{"ark-beta-mcp", "ark-beta-image-process", "ark-beta-knowledge-search", "ark-beta-doubao-app"} {
		if prepared.Headers.Get(name) != "true" {
			t.Fatalf("%s header = %q", name, prepared.Headers.Get(name))
		}
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(prepared.Body, &body); err != nil {
		t.Fatal(err)
	}
	if string(body["thinking"]) != `{"type":"enabled"}` || string(body["future_option"]) != `true` || string(body["future_response_option"]) != `true` {
		t.Fatalf("Volcengine options were not forwarded: %s", prepared.Body)
	}
}

func TestPrepareCapsOutputTokensToArkLimit(t *testing.T) {
	call := invocation(false, nil)
	requested := 200000
	call.Request.MaxOutputTokens = &requested
	prepared, err := New(gatewaytransport.HTTPClient{}).Prepare(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(prepared.Body, &body); err != nil {
		t.Fatal(err)
	}
	if got, ok := body["max_output_tokens"].(float64); !ok || int(got) != maxOutputTokensUpperBound {
		t.Fatalf("max_output_tokens = %#v, want %d", body["max_output_tokens"], maxOutputTokensUpperBound)
	}
}

func TestConvertedToolChoiceIsNormalizedForVolcengine(t *testing.T) {
	call := invocation(false, nil)
	call.Operation = gatewaytransport.OperationMessages
	call.Request.ToolChoice = &canonical.ToolChoice{
		Mode: "tool", Type: "tool", Name: "lookup",
		Raw: json.RawMessage(`{"type":"tool","name":"lookup","disable_parallel_tool_use":false}`),
	}
	item := New(gatewaytransport.HTTPClient{})
	if plan := item.Plan(call.Operation, call.Request, canonical.NewFeatureSet(canonical.FeatureTools)); !plan.Supported() {
		t.Fatalf("standard tool choice was rejected: %#v", plan)
	}
	prepared, err := item.Prepare(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	body := string(prepared.Body)
	if !strings.Contains(body, `"tool_choice":{"name":"lookup","type":"function"}`) || strings.Contains(body, "disable_parallel_tool_use") {
		t.Fatalf("tool choice was not normalized: %s", body)
	}
}

func TestExecuteAndStreamUseArkResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != responsesPath || request.Header.Get("Authorization") != "Bearer key" {
			http.Error(writer, "bad request", http.StatusBadRequest)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(request.Body).Decode(&body)
		if body["stream"] == true {
			writer.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(writer, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"sequence_number\":1,\"item_id\":\"msg_1\",\"delta\":\"hi\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"sequence_number\":2,\"response\":{\"id\":\"up_1\",\"object\":\"response\",\"status\":\"completed\",\"model\":\"vendor\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":2,\"total_tokens\":3,\"tool_usage\":{\"web_search\":1}}}}\n\ndata: [DONE]\n\n")
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"id":"up_1","object":"response","status":"completed","model":"vendor","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3,"tool_usage":{"web_search":1}},"future_response":true}`)
	}))
	defer server.Close()
	transport := New(gatewaytransport.HTTPClient{Client: server.Client()})
	call := invocation(false, nil)
	call.Route.BaseURL = server.URL
	response, _, err := transport.Execute(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	if response.ID != "up_1" || len(response.Output) != 1 || len(response.Usage.Extra["tool_usage"]) == 0 || string(response.ProviderExtensions["future_response"]) != "true" {
		t.Fatalf("unexpected response: %#v", response)
	}

	streamCall := invocation(true, nil)
	streamCall.Route.BaseURL = server.URL
	stream, _, err := transport.Stream(context.Background(), streamCall)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	first, err := stream.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.Type != canonical.EventTextDelta || first.Delta != "hi" {
		t.Fatalf("unexpected first event: %#v", first)
	}
	last, err := stream.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if last.Type != canonical.EventCompleted || last.Response == nil || last.Usage == nil || last.Usage.TotalTokens != 3 || last.ProviderResponseID != "up_1" || len(last.Response.Usage.Extra["tool_usage"]) == 0 {
		t.Fatalf("unexpected completed event: %#v", last)
	}
}

func TestPreparePreservesCallIDAndMultimodalMetadata(t *testing.T) {
	call := invocation(false, nil)
	call.Request.Items = []canonical.Item{
		{Type: "function_call", CallID: "call_1", Name: "lookup", Arguments: json.RawMessage(`{"q":"x"}`), Status: "completed"},
		{Type: "function_call_output", CallID: "call_1", Output: json.RawMessage(`"ok"`)},
		{Type: "message", Role: canonical.RoleUser, Content: []canonical.Content{{Type: "input_file", FileID: "file_1", Filename: "report.pdf", MediaType: "application/pdf"}, {Type: "input_audio", URL: "https://example.test/a.wav", MediaType: "audio/wav", Format: "wav"}}},
	}
	prepared, err := New(gatewaytransport.HTTPClient{}).Prepare(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	body := string(prepared.Body)
	for _, expected := range []string{`"call_id":"call_1"`, `"arguments":"{\"q\":\"x\"}"`, `"filename":"report.pdf"`, `"file_id":"file_1"`, `"audio_url":"https://example.test/a.wav"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("prepared body missing %s: %s", expected, body)
		}
	}
	for _, forbidden := range []string{`"content_type"`, `"format"`, `"transcript"`} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("prepared body contains unsupported Ark field %s: %s", forbidden, body)
		}
	}
}

func TestPrepareUsesArkMultimodalContentShapes(t *testing.T) {
	request := canonical.Request{Model: "public", Items: []canonical.Item{{
		Type: "message", Role: canonical.RoleUser, Content: []canonical.Content{
			{Type: "input_image", Data: "aW1hZ2U=", MediaType: "image/png", Detail: "high"},
			{Type: "input_video", Data: "dmlkZW8=", MediaType: "video/mp4", Extra: map[string]json.RawMessage{"fps": json.RawMessage(`2`)}},
			{Type: "input_audio", Data: "YXVkaW8=", Format: "wav"},
			{Type: "input_file", Data: "JVBERi0=", MediaType: "application/pdf"},
		},
	}}}
	input := prepareInput(t, request, gatewaytransport.OperationResponses)
	if len(input) != 1 {
		t.Fatalf("input count = %d, want 1", len(input))
	}
	content, ok := input[0]["content"].([]any)
	if !ok || len(content) != 4 {
		t.Fatalf("content = %#v", input[0]["content"])
	}
	image := content[0].(map[string]any)
	requireExactKeys(t, image, "type", "image_url", "detail")
	if image["image_url"] != "data:image/png;base64,aW1hZ2U=" {
		t.Fatalf("image URL = %#v", image["image_url"])
	}
	video := content[1].(map[string]any)
	requireExactKeys(t, video, "type", "video_url", "fps")
	if video["video_url"] != "data:video/mp4;base64,dmlkZW8=" || video["fps"] != float64(2) {
		t.Fatalf("video = %#v", video)
	}
	audio := content[2].(map[string]any)
	requireExactKeys(t, audio, "type", "audio_url")
	if audio["audio_url"] != "data:audio/wav;base64,YXVkaW8=" {
		t.Fatalf("audio URL = %#v", audio["audio_url"])
	}
	file := content[3].(map[string]any)
	requireExactKeys(t, file, "type", "file_data", "filename")
	if file["file_data"] != "JVBERi0=" || file["filename"] != "document.pdf" {
		t.Fatalf("file = %#v", file)
	}
}

func TestPrepareUsesArkInputUnionShapes(t *testing.T) {
	call := invocation(false, nil)
	call.Request.Items = []canonical.Item{
		{ID: "msg_1", Type: "message", Role: canonical.RoleAssistant, Status: "completed", Content: []canonical.Content{{Type: "output_text", Text: "running the tool", Extra: map[string]json.RawMessage{"annotations": json.RawMessage(`[{"type":"url_citation"}]`), "logprobs": json.RawMessage(`[]`)}}}},
		{Type: "function_call", Role: canonical.RoleAssistant, ID: "provider_item_1", CallID: "fc_1", Name: "lookup", Arguments: json.RawMessage(`{"q":"x"}`), Status: "completed"},
		{Type: "function_call_output", Role: canonical.RoleTool, CallID: "fc_1", Output: json.RawMessage(`{"ok":true}`), Status: "completed"},
		{Type: "reasoning", Role: canonical.RoleAssistant, ID: "reasoning_1", Status: "completed", Content: []canonical.Content{{Type: "reasoning_text", Text: "tool result is ready"}}},
	}
	prepared, err := New(gatewaytransport.HTTPClient{}).Prepare(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(prepared.Body, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Input) != 4 {
		t.Fatalf("input count = %d, want 4", len(body.Input))
	}
	requireExactKeys(t, body.Input[0], "type", "role", "content")
	if body.Input[0]["role"] != string(canonical.RoleAssistant) {
		t.Fatalf("message role = %#v, want assistant", body.Input[0]["role"])
	}
	messageContent, ok := body.Input[0]["content"].([]any)
	if !ok || len(messageContent) != 1 {
		t.Fatalf("assistant history content = %#v", body.Input[0]["content"])
	}
	messagePart, ok := messageContent[0].(map[string]any)
	if !ok {
		t.Fatalf("assistant history part = %#v", messageContent[0])
	}
	requireExactKeys(t, messagePart, "type", "text")
	if messagePart["type"] != "input_text" || messagePart["text"] != "running the tool" {
		t.Fatalf("assistant history part = %#v", messagePart)
	}

	requireExactKeys(t, body.Input[1], "type", "call_id", "name", "arguments")
	if body.Input[1]["call_id"] != "fc_1" || body.Input[1]["arguments"] != `{"q":"x"}` {
		t.Fatalf("function call = %#v", body.Input[1])
	}

	requireExactKeys(t, body.Input[2], "type", "call_id", "output")
	if body.Input[2]["output"] != `{"ok":true}` {
		t.Fatalf("function output = %#v, want JSON string", body.Input[2]["output"])
	}

	requireExactKeys(t, body.Input[3], "type", "summary")
	summary, ok := body.Input[3]["summary"].([]any)
	if !ok || len(summary) != 1 {
		t.Fatalf("reasoning summary = %#v", body.Input[3]["summary"])
	}
	summaryPart, ok := summary[0].(map[string]any)
	if !ok || summaryPart["type"] != "summary_text" || summaryPart["text"] != "tool result is ready" {
		t.Fatalf("reasoning summary part = %#v", summary[0])
	}
}

func requireExactKeys(t *testing.T, value map[string]any, expected ...string) {
	t.Helper()
	if len(value) != len(expected) {
		t.Fatalf("item keys = %#v, want %v", value, expected)
	}
	for _, key := range expected {
		if _, exists := value[key]; !exists {
			t.Fatalf("item missing key %q: %#v", key, value)
		}
	}
}

func TestPrepareConvertedToolHistoryUsesArkShapes(t *testing.T) {
	anthropicRequest, err := anthropiccodec.DecodeRequestJSON([]byte(`{
		"model":"public","max_tokens":64,"messages":[
			{"role":"assistant","content":[
				{"type":"thinking","thinking":"checking the tool"},
				{"type":"tool_use","id":"fc_1","name":"lookup","input":{"q":"x"}}
			]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"fc_1","content":[{"type":"text","text":"ok"}]}
			]}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	anthropicInput := prepareInput(t, anthropicRequest, gatewaytransport.OperationMessages)
	if len(anthropicInput) != 3 {
		t.Fatalf("Anthropic input count = %d, want 3", len(anthropicInput))
	}
	requireExactKeys(t, anthropicInput[0], "type", "summary")
	requireExactKeys(t, anthropicInput[1], "type", "call_id", "name", "arguments")
	requireExactKeys(t, anthropicInput[2], "type", "call_id", "output")
	if _, ok := anthropicInput[2]["output"].(string); !ok {
		t.Fatalf("Anthropic tool output = %#v, want string", anthropicInput[2]["output"])
	}

	var chatRequest chat.ChatRequest
	if err := json.Unmarshal([]byte(`{
		"model":"public","messages":[
			{"role":"assistant","content":null,"tool_calls":[{"id":"fc_2","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"y\"}"}}]},
			{"role":"tool","tool_call_id":"fc_2","content":{"ok":true}}
		]
	}`), &chatRequest); err != nil {
		t.Fatal(err)
	}
	convertedChat, err := openaichatcodec.DecodeRequest(chatRequest)
	if err != nil {
		t.Fatal(err)
	}
	chatInput := prepareInput(t, convertedChat, gatewaytransport.OperationChat)
	if len(chatInput) != 2 {
		t.Fatalf("Chat input count = %d, want 2", len(chatInput))
	}
	requireExactKeys(t, chatInput[0], "type", "call_id", "name", "arguments")
	requireExactKeys(t, chatInput[1], "type", "call_id", "output")
	if _, ok := chatInput[1]["output"].(string); !ok {
		t.Fatalf("Chat tool output = %#v, want string", chatInput[1]["output"])
	}
}

func prepareInput(t *testing.T, request canonical.Request, operation gatewaytransport.Operation) []map[string]any {
	t.Helper()
	call := invocation(false, nil)
	call.Operation = operation
	call.Request = request
	prepared, err := New(gatewaytransport.HTTPClient{}).Prepare(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(prepared.Body, &body); err != nil {
		t.Fatal(err)
	}
	return body.Input
}

func TestDecodeFailedEventCarriesErrorUsageAndPublicModel(t *testing.T) {
	event, err := decodeEvent([]byte(`{"type":"response.failed","response":{"id":"resp_1","status":"failed","model":"vendor","output":[],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3},"error":{"type":"server_error","code":"upstream_failed","message":"failed"}}}`), "response.failed", "public")
	if err != nil {
		t.Fatal(err)
	}
	if event.Type != canonical.EventFailed || event.Response == nil || event.Response.Model != "public" || event.ProviderResponseID != "resp_1" || event.Usage == nil || event.Usage.TotalTokens != 3 || event.Error == nil || event.Error.Code != "upstream_failed" {
		t.Fatalf("event = %#v", event)
	}
}

func TestHTTPErrorIncludesVolcengineDetails(t *testing.T) {
	err := newHTTPError(http.StatusTooManyRequests, []byte(`{"error":{"type":"rate_limit_error","code":"rate_limit","message":"slow"}}`))
	var upstream *HTTPError
	if !errors.As(err, &upstream) || upstream.Details == nil || upstream.Details.Status != http.StatusTooManyRequests || upstream.Details.Code != "rate_limit" || upstream.Details.Message != "slow" || !upstream.Details.Retryable {
		t.Fatalf("error = %#v", err)
	}
	if !strings.Contains(err.Error(), "slow") {
		t.Fatalf("error message does not include upstream detail: %v", err)
	}
}

func invocation(stream bool, tools []canonical.Tool) gatewaytransport.Invocation {
	return gatewaytransport.Invocation{Route: gatewaytransport.Route{BaseURL: "https://ark.example", APIKey: "key", VendorModel: "vendor", PublicModel: "public"}, Operation: gatewaytransport.OperationResponses, Request: canonical.Request{Endpoint: canonical.EndpointOpenAIResponses, Model: "public", Stream: stream, Items: []canonical.Item{{Type: "message", Role: canonical.RoleUser, Content: []canonical.Content{{Type: "input_text", Text: "hello"}}}}, Tools: tools, ProviderOptions: canonical.ProviderOptions{Volcengine: &canonical.VolcengineOptions{Thinking: json.RawMessage(`{"type":"enabled"}`), Unknown: map[string]json.RawMessage{"future_option": json.RawMessage(`true`)}}}}}
}

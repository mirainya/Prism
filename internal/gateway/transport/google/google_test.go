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
	responsescodec "github.com/mirainya/Prism/internal/gateway/codec/openai_responses"
	"github.com/mirainya/Prism/internal/gateway/transport"
)

func TestGoogleRequiresResponsesDownstream(t *testing.T) {
	plan := New(nil).Plan(transport.OperationMessages, canonical.Request{Endpoint: canonical.EndpointAnthropic}, canonical.FeatureSet{})
	if plan.Supported() {
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
		if operation == transport.OperationResponses {
			if converted.Kind != transport.PlanConverted || converted.Upstream != transport.OperationChat {
				t.Fatalf("%s plan = %#v", operation, converted)
			}
		} else if converted.Supported() {
			t.Fatalf("%s unexpectedly supported: %#v", operation, converted)
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
	reserved := canonical.Request{ClientExtensions: map[string]json.RawMessage{"google.generate_content.request_extras": json.RawMessage(`{"contents":[]}`)}}
	if plan := item.Plan(transport.OperationResponses, reserved, canonical.FeatureSet{}); plan.Supported() {
		t.Fatalf("reserved Google request extra was accepted: %#v", plan)
	}
}

func TestPlanAcceptsOnlyGoogleProofsOnReplayableItems(t *testing.T) {
	item := New(nil)
	googleProof := &canonical.ProviderProof{Provider: canonical.ProofProviderGoogle, Value: "google-proof"}
	accepted := canonical.Request{Items: []canonical.Item{
		{Type: "reasoning", Proof: googleProof},
		{Type: "reasoning", Content: []canonical.Content{{Type: "reasoning_text", Text: "unsigned"}}},
		{Type: "function_call", Proof: googleProof},
		{Type: "function_call"},
	}}
	if plan := item.Plan(transport.OperationResponses, accepted, canonical.NewFeatureSet(canonical.FeatureReasoning, canonical.FeatureTools)); !plan.Supported() {
		t.Fatalf("Google proof plan = %#v", plan)
	}
	interleaved := canonical.Request{Items: []canonical.Item{
		{Type: "function_call", Proof: googleProof},
		{Type: "message", Role: canonical.RoleAssistant, Content: []canonical.Content{{Type: "output_text", Text: "between"}}},
		{Type: "reasoning", Content: []canonical.Content{{Type: "reasoning_text", Text: "thinking"}}},
		{Type: "function_call"},
	}}
	if plan := item.Plan(transport.OperationResponses, interleaved, canonical.NewFeatureSet(canonical.FeatureReasoning, canonical.FeatureTools)); !plan.Supported() {
		t.Fatalf("interleaved model-turn calls were rejected: %#v", plan)
	}
	newTurn := interleaved
	newTurn.Items = append([]canonical.Item(nil), interleaved.Items...)
	newTurn.Items[2] = canonical.Item{Type: "message", Role: canonical.RoleUser, Content: []canonical.Content{{Type: "input_text", Text: "next"}}}
	if plan := item.Plan(transport.OperationResponses, newTurn, canonical.NewFeatureSet(canonical.FeatureTools)); plan.Supported() {
		t.Fatalf("unsigned first call in a new model turn was accepted: %#v", plan)
	}

	for name, request := range map[string]canonical.Request{
		"foreign provider": {Items: []canonical.Item{{Type: "reasoning", Proof: &canonical.ProviderProof{Provider: canonical.ProofProviderAnthropic, Value: "foreign"}}}},
		"empty proof":      {Items: []canonical.Item{{Type: "reasoning", Proof: &canonical.ProviderProof{Provider: canonical.ProofProviderGoogle}}}},
		"message proof":    {Items: []canonical.Item{{Type: "message", Proof: googleProof}}},
		"unsigned call":    {Items: []canonical.Item{{Type: "function_call"}}},
		"namespace":        {Items: []canonical.Item{{Type: "function_call", Namespace: "tools"}}},
	} {
		t.Run(name, func(t *testing.T) {
			if plan := item.Plan(transport.OperationResponses, request, canonical.FeatureSet{}); plan.Supported() {
				t.Fatalf("incompatible proof plan = %#v", plan)
			}
		})
	}
}

func TestPlanRejectsToolsWhenDownstreamCannotCarryGoogleProofs(t *testing.T) {
	request := canonical.Request{Tools: []canonical.Tool{{Type: "function", Name: "lookup"}}}
	item := New(nil)
	if plan := item.Plan(transport.OperationResponses, request, canonical.NewFeatureSet(canonical.FeatureTools)); !plan.Supported() {
		t.Fatalf("Responses tool request was rejected: %#v", plan)
	}
	for _, operation := range []transport.Operation{transport.OperationChat, transport.OperationMessages} {
		if plan := item.Plan(operation, request, canonical.NewFeatureSet(canonical.FeatureTools)); plan.Supported() {
			t.Fatalf("%s tool request could lose Google proof: %#v", operation, plan)
		}
		history := canonical.Request{Items: []canonical.Item{{Type: "function_call", CallID: "call_1"}, {Type: "function_call_output", CallID: "call_1"}}}
		if plan := item.Plan(operation, history, canonical.FeatureSet{}); plan.Supported() {
			t.Fatalf("%s tool history bypassed proof protection: %#v", operation, plan)
		}
	}
}

func TestPlanAcceptsEnabledParallelToolCallsForResponses(t *testing.T) {
	enabled, disabled := true, false
	request := canonical.Request{
		Tools:             []canonical.Tool{{Type: "function", Name: "lookup"}},
		ParallelToolCalls: &enabled,
	}
	item := New(nil)
	if plan := item.Plan(transport.OperationResponses, request, canonical.NewFeatureSet(canonical.FeatureTools)); !plan.Supported() {
		t.Fatalf("parallel tool calls were rejected: %#v", plan)
	}
	request.ParallelToolCalls = &disabled
	if plan := item.Plan(transport.OperationResponses, request, canonical.NewFeatureSet(canonical.FeatureTools)); plan.Supported() {
		t.Fatalf("disabled parallel tool calls were accepted: %#v", plan)
	}
	request.Tools = nil
	if plan := item.Plan(transport.OperationResponses, request, canonical.FeatureSet{}); !plan.Supported() {
		t.Fatalf("irrelevant parallel_tool_calls=false was rejected: %#v", plan)
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
	if plan := item.Plan(transport.OperationChat, canonical.Request{Store: &noStore}, canonical.FeatureSet{}); plan.Supported() {
		t.Fatalf("Chat should remain unsupported even with store=false: %#v", plan)
	}
	include := canonical.Request{Include: []string{transport.ResponsesReasoningProofInclude}}
	if plan := item.Plan(transport.OperationResponses, include, canonical.FeatureSet{}); !plan.Supported() {
		t.Fatalf("Prism-owned Responses include was rejected: %#v", plan)
	}
	if plan := item.Plan(transport.OperationMessages, include, canonical.FeatureSet{}); plan.Supported() {
		t.Fatalf("foreign include was accepted: %#v", plan)
	}
}

func TestPreparePreservesMultimodalAndFunctionCallID(t *testing.T) {
	request := canonical.Request{Model: "m", Modalities: []string{"TEXT"}, Items: []canonical.Item{
		{Type: "message", Role: canonical.RoleUser, Content: []canonical.Content{{Type: "input_audio", Data: "YQ==", MediaType: "audio/wav"}, {Type: "input_video", URL: "gs://bucket/video.mp4", MediaType: "video/mp4"}}},
		{Type: "function_call", CallID: "call_1", Name: "lookup", Arguments: json.RawMessage(`{"q":"x"}`), Proof: &canonical.ProviderProof{Provider: canonical.ProofProviderGoogle, Value: "tool-proof"}},
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

func TestPrepareReplaysOnlyNativeGoogleProofs(t *testing.T) {
	request := canonical.Request{Model: "m", Items: []canonical.Item{
		{Type: "reasoning", Content: []canonical.Content{{Type: "reasoning_text", Text: "think"}}, Proof: &canonical.ProviderProof{Provider: canonical.ProofProviderGoogle, Value: "reasoning-proof"}},
		{Type: "reasoning", Content: []canonical.Content{{Type: "reasoning_text", Text: "unsigned"}}},
		{Type: "function_call", CallID: "call_1", Name: "lookup", Arguments: json.RawMessage(`{"q":"x"}`), Proof: &canonical.ProviderProof{Provider: canonical.ProofProviderGoogle, Value: "tool-proof"}},
		{Type: "function_call", CallID: "call_2", Name: "other", Arguments: json.RawMessage(`{}`)},
	}}
	prepared, err := New(nil).Prepare(context.Background(), transport.Invocation{Route: transport.Route{BaseURL: "https://example.test", VendorModel: "gemini"}, Operation: transport.OperationResponses, Request: request})
	if err != nil {
		t.Fatal(err)
	}
	body := string(prepared.Body)
	for _, expected := range []string{`"text":"think","thought":true,"thoughtSignature":"reasoning-proof"`, `"thoughtSignature":"tool-proof"`, `"functionCall":{"args":{"q":"x"},"id":"call_1","name":"lookup"}`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("prepared body missing %s: %s", expected, body)
		}
	}
	if count := strings.Count(body, `"thoughtSignature"`); count != 2 {
		t.Fatalf("thoughtSignature count = %d, body = %s", count, body)
	}
}

func TestPrepareGroupsParallelFunctionCallsAndResponses(t *testing.T) {
	parallel := true
	request := canonical.Request{Model: "m", ParallelToolCalls: &parallel, Items: []canonical.Item{
		{Type: "function_call", CallID: "call_1", Name: "first", Arguments: json.RawMessage(`{"q":1}`), Proof: &canonical.ProviderProof{Provider: canonical.ProofProviderGoogle, Value: "proof-1"}},
		{Type: "function_call", CallID: "call_2", Name: "second", Arguments: json.RawMessage(`{"q":2}`)},
		{Type: "function_call_output", CallID: "call_1", Output: json.RawMessage(`{"result":1}`)},
		{Type: "function_call_output", CallID: "call_2", Output: json.RawMessage(`{"result":2}`)},
	}}
	prepared, err := New(nil).Prepare(context.Background(), transport.Invocation{
		Route:     transport.Route{BaseURL: "https://example.test", VendorModel: "gemini"},
		Operation: transport.OperationResponses, Request: request,
	})
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Contents []struct {
			Role  string            `json:"role"`
			Parts []json.RawMessage `json:"parts"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(prepared.Body, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Contents) != 2 || body.Contents[0].Role != "model" || len(body.Contents[0].Parts) != 2 || body.Contents[1].Role != "user" || len(body.Contents[1].Parts) != 2 {
		t.Fatalf("parallel tool turns were split: %s", prepared.Body)
	}
	for index, proof := range []string{"proof-1", ""} {
		var part struct {
			ThoughtSignature string `json:"thoughtSignature"`
		}
		if err := json.Unmarshal(body.Contents[0].Parts[index], &part); err != nil || part.ThoughtSignature != proof {
			t.Fatalf("model part %d proof = %q, err = %v; body = %s", index, part.ThoughtSignature, err, prepared.Body)
		}
	}
}

func TestParallelCallProofAndOmittedIDsRoundTripThroughResponses(t *testing.T) {
	response, err := decodeResponse([]byte(`{"responseId":"resp_calls","candidates":[{"content":{"parts":[{"functionCall":{"name":"first","args":{"q":1}},"thoughtSignature":"proof-1"},{"functionCall":{"name":"second","args":{"q":2}}}]},"finishReason":"STOP"}]}`), transport.Route{PublicModel: "public"})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Output) != 2 || response.Output[0].Proof == nil || response.Output[1].Proof != nil || !response.Output[0].ProviderCallIDOmitted || !response.Output[1].ProviderCallIDOmitted {
		t.Fatalf("decoded calls = %#v", response.Output)
	}
	wire, err := responsescodec.EncodeResponseJSON(response)
	if err != nil {
		t.Fatal(err)
	}
	var public struct {
		Output json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(wire, &public); err != nil {
		t.Fatal(err)
	}
	replayed, err := responsescodec.DecodeItems(public.Output)
	if err != nil {
		t.Fatal(err)
	}
	if len(replayed) != 2 || replayed[0].Proof == nil || replayed[1].Proof != nil || !replayed[0].ProviderCallIDOmitted || !replayed[1].ProviderCallIDOmitted {
		t.Fatalf("replayed calls = %#v; wire = %s", replayed, wire)
	}
	prepared, err := New(nil).Prepare(context.Background(), transport.Invocation{
		Route:     transport.Route{BaseURL: "https://example.test", VendorModel: "gemini"},
		Operation: transport.OperationResponses, Request: canonical.Request{Model: "public", Items: replayed},
	})
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Contents []struct {
			Parts []map[string]json.RawMessage `json:"parts"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(prepared.Body, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Contents) != 1 || len(body.Contents[0].Parts) != 2 {
		t.Fatalf("prepared calls = %s", prepared.Body)
	}
	for index, part := range body.Contents[0].Parts {
		var call map[string]json.RawMessage
		if err := json.Unmarshal(part["functionCall"], &call); err != nil {
			t.Fatal(err)
		}
		if _, exists := call["id"]; exists {
			t.Fatalf("call %d regained a provider ID: %s", index, prepared.Body)
		}
	}
}

func TestPrepareNormalizesAnthropicToolChoiceButPlanRejectsLossyOutput(t *testing.T) {
	request := canonical.Request{
		Model: "m", Items: []canonical.Item{{Type: "message", Role: canonical.RoleUser, Content: []canonical.Content{{Type: "input_text", Text: "hi"}}}},
		ToolChoice: &canonical.ToolChoice{Mode: "tool", Type: "tool", Name: "lookup", Raw: json.RawMessage(`{"type":"tool","name":"lookup"}`)},
	}
	item := New(nil)
	if plan := item.Plan(transport.OperationMessages, request, canonical.NewFeatureSet(canonical.FeatureTools)); plan.Supported() {
		t.Fatalf("Messages tool request could lose Google proof: %#v", plan)
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

func TestDecodeResponseRejectsMultipleCandidates(t *testing.T) {
	_, err := decodeResponse([]byte(`{"candidates":[{"index":0,"content":{"parts":[{"text":"first"}]}},{"index":1,"content":{"parts":[{"text":"second"}]}}]}`), transport.Route{PublicModel: "public"})
	if err == nil || !strings.Contains(err.Error(), "multiple candidates") {
		t.Fatalf("multiple candidates error = %v", err)
	}
}

func TestGoogleAudioRoundTripsThroughResponses(t *testing.T) {
	response, err := decodeResponse([]byte(`{"responseId":"resp_audio","candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"audio/wav","data":"YXVkaW8="}}]},"finishReason":"STOP"}]}`), transport.Route{PublicModel: "public"})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Output) != 1 || len(response.Output[0].Content) != 1 || response.Output[0].Content[0].Format != "wav" {
		t.Fatalf("Google audio = %#v", response.Output)
	}
	wire, err := responsescodec.EncodeResponseJSON(response)
	if err != nil {
		t.Fatal(err)
	}
	var public struct {
		Output json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(wire, &public); err != nil {
		t.Fatal(err)
	}
	items, err := responsescodec.DecodeItems(public.Output)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || len(items[0].Content) != 1 || items[0].Content[0].Data != "YXVkaW8=" || items[0].Content[0].Format != "wav" {
		t.Fatalf("Responses audio = %#v; wire=%s", items, wire)
	}
}

func TestDecodeResponsePreservesGoogleProofs(t *testing.T) {
	response, err := decodeResponse([]byte(`{"responseId":"resp_proof","candidates":[{"content":{"parts":[{"text":"think","thought":true,"thoughtSignature":"reasoning-proof"},{"functionCall":{"id":"call_1","name":"lookup","args":{"q":"x"}},"thoughtSignature":"tool-proof"},{"text":"","thoughtSignature":"standalone-proof"}]},"finishReason":"STOP"}]}`), transport.Route{PublicModel: "public"})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Output) != 4 {
		t.Fatalf("output = %#v", response.Output)
	}
	for index, expected := range []string{"reasoning-proof", "tool-proof", "standalone-proof"} {
		proof := response.Output[index].Proof
		if proof == nil || proof.Provider != canonical.ProofProviderGoogle || proof.Value != expected {
			t.Fatalf("output[%d] proof = %#v", index, proof)
		}
	}
	if response.Output[0].Type != "reasoning" || response.Output[0].Content[0].Type != "reasoning_text" || response.Output[1].Type != "function_call" || response.Output[2].Type != "reasoning" || response.Output[2].Proof.Subject != canonical.ProofSubjectGooglePart || response.Output[3].Type != "message" || response.Output[2].Proof.TargetID != response.Output[3].ID {
		t.Fatalf("output types = %#v", response.Output)
	}
}

func TestDecodeResponseAttachesSignatureOnlyPartToReasoning(t *testing.T) {
	response, err := decodeResponse([]byte(`{"responseId":"resp_thinking","candidates":[{"content":{"parts":[{"text":"thinking text","thought":true},{"text":"","thoughtSignature":"sig-test"}]},"finishReason":"STOP"}]}`), transport.Route{PublicModel: "public"})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Output) != 1 || response.Output[0].Type != "reasoning" || response.Output[0].Proof == nil || response.Output[0].Proof.Value != "sig-test" || response.Output[0].Proof.Subject != "" {
		t.Fatalf("reasoning output = %#v", response.Output)
	}
	prepared, err := New(nil).Prepare(context.Background(), transport.Invocation{
		Route:     transport.Route{BaseURL: "https://example.test", VendorModel: "gemini"},
		Operation: transport.OperationResponses,
		Request:   canonical.Request{Model: "public", Items: response.Output},
	})
	if err != nil {
		t.Fatal(err)
	}
	if body := string(prepared.Body); !strings.Contains(body, `"text":"thinking text","thought":true,"thoughtSignature":"sig-test"`) {
		t.Fatalf("reasoning proof was not replayed: %s", body)
	}
}

func TestDecodeResponsePreservesTextAndToolPartOrder(t *testing.T) {
	response, err := decodeResponse([]byte(`{"responseId":"resp_order","candidates":[{"content":{"parts":[{"text":"before"},{"functionCall":{"id":"call_1","name":"lookup","args":{}}},{"text":"after"}]},"finishReason":"STOP"}]}`), transport.Route{PublicModel: "public"})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Output) != 3 || response.Output[0].Type != "message" || response.Output[1].Type != "function_call" || response.Output[2].Type != "message" || response.Output[0].Content[0].Text != "before" || response.Output[2].Content[0].Text != "after" {
		t.Fatalf("part order = %#v", response.Output)
	}
}

func TestSignedGooglePartsRoundTripThroughResponses(t *testing.T) {
	response, err := decodeResponse([]byte(`{"responseId":"resp_parts","candidates":[{"content":{"parts":[{"text":"answer","thoughtSignature":"text-proof"},{"inlineData":{"mimeType":"image/png","data":"aW1n"},"thoughtSignature":"image-proof"}]},"finishReason":"STOP"}]}`), transport.Route{PublicModel: "public"})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Output) != 4 || response.Output[0].Proof == nil || response.Output[0].Proof.Subject != canonical.ProofSubjectGooglePart || response.Output[2].Proof == nil || response.Output[3].Content[0].Type != "output_image" {
		t.Fatalf("signed parts = %#v", response.Output)
	}
	wire, err := responsescodec.EncodeResponseJSON(response)
	if err != nil {
		t.Fatal(err)
	}
	var public struct {
		Output json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(wire, &public); err != nil {
		t.Fatal(err)
	}
	replayed, err := responsescodec.DecodeItems(public.Output)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := New(nil).Prepare(context.Background(), transport.Invocation{
		Route:     transport.Route{BaseURL: "https://example.test", VendorModel: "gemini"},
		Operation: transport.OperationResponses, Request: canonical.Request{Model: "public", Items: replayed},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := string(prepared.Body)
	for _, expected := range []string{`"text":"answer","thoughtSignature":"text-proof"`, `"inlineData":{"data":"aW1n","mimeType":"image/png"},"thoughtSignature":"image-proof"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("signed part missing %s: %s", expected, body)
		}
	}
}

func TestStreamLateGooglePartProofTargetsActiveMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hel\"}]}}]}\n\n")
		_, _ = io.WriteString(writer, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"lo\",\"thoughtSignature\":\"late-proof\"}]}}]}\n\n")
		_, _ = io.WriteString(writer, "data: {\"responseId\":\"resp_late\",\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n")
	}))
	defer server.Close()
	stream, _, err := transport.Stream(context.Background(), New(server.Client()), transport.Invocation{
		Route: transport.Route{BaseURL: server.URL, VendorModel: "gemini"}, Operation: transport.OperationResponses,
		Request: canonical.Request{Model: "public", Stream: true, Items: []canonical.Item{{Type: "message", Role: canonical.RoleUser, Content: []canonical.Content{{Type: "input_text", Text: "hi"}}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	accumulator := canonical.NewEventAccumulator()
	for {
		event, nextErr := stream.Next(context.Background())
		if nextErr != nil {
			t.Fatal(nextErr)
		}
		accumulator.Observe(event)
		if event.Type == canonical.EventCompleted {
			break
		}
	}
	output := accumulator.Snapshot().Output
	if len(output) != 2 || output[0].Type != "message" || output[0].Content[0].Text != "hello" || output[1].Proof == nil || output[1].Proof.TargetID != output[0].ID {
		t.Fatalf("late signed output = %#v", output)
	}
	prepared, err := New(nil).Prepare(context.Background(), transport.Invocation{
		Route:     transport.Route{BaseURL: "https://example.test", VendorModel: "gemini"},
		Operation: transport.OperationResponses, Request: canonical.Request{Model: "public", Items: output},
	})
	if err != nil {
		t.Fatal(err)
	}
	if body := string(prepared.Body); !strings.Contains(body, `"text":"hello","thoughtSignature":"late-proof"`) {
		t.Fatalf("late proof was not replayed on the original part: %s", body)
	}
}

func TestStreamAttachesSignatureOnlyPartToActiveReasoning(t *testing.T) {
	decoder := &sse{}
	thinking, err := decodeStreamEvents([]byte(`{"candidates":[{"content":{"parts":[{"text":"thinking text","thought":true}]}}]}`), 1)
	if err != nil {
		t.Fatal(err)
	}
	thinking = decoder.normalizeEvents(thinking)
	signature, err := decodeStreamEvents([]byte(`{"candidates":[{"content":{"parts":[{"text":"","thoughtSignature":"sig-test"}]}}]}`), 2)
	if err != nil {
		t.Fatal(err)
	}
	signature = decoder.normalizeEvents(signature)
	if len(thinking) != 1 || len(signature) != 1 || signature[0].Type != canonical.EventProviderProof || signature[0].Item == nil || signature[0].Item.Proof == nil {
		t.Fatalf("thinking=%#v signature=%#v", thinking, signature)
	}
	proof := signature[0].Item.Proof
	if signature[0].OutputIndex != thinking[0].OutputIndex || signature[0].Item.ID != thinking[0].Item.ID || proof.Value != "sig-test" || proof.Subject != "" || proof.TargetID != "" {
		t.Fatalf("signature was not attached to reasoning: thinking=%#v signature=%#v", thinking, signature)
	}
	accumulator := canonical.NewEventAccumulator()
	accumulator.Observe(thinking[0])
	accumulator.Observe(signature[0])
	output := accumulator.Snapshot().Output
	if len(output) != 1 || output[0].Proof == nil || output[0].Content[0].Text != "thinking text" {
		t.Fatalf("reasoning output = %#v", output)
	}
	prepared, err := New(nil).Prepare(context.Background(), transport.Invocation{
		Route:     transport.Route{BaseURL: "https://example.test", VendorModel: "gemini"},
		Operation: transport.OperationResponses,
		Request:   canonical.Request{Model: "public", Items: output},
	})
	if err != nil {
		t.Fatal(err)
	}
	if body := string(prepared.Body); !strings.Contains(body, `"text":"thinking text","thought":true,"thoughtSignature":"sig-test"`) {
		t.Fatalf("reasoning proof was not replayed: %s", body)
	}
}

func TestStreamAttachesSameFrameSignatureOnlyPartToReasoning(t *testing.T) {
	decoder := &sse{}
	events, err := decodeStreamEvents([]byte(`{"candidates":[{"content":{"parts":[{"text":"thinking text","thought":true},{"text":"","thoughtSignature":"sig-test"}]}}]}`), 1)
	if err != nil {
		t.Fatal(err)
	}
	events = decoder.normalizeEvents(events)
	if len(events) != 2 || events[0].Type != canonical.EventReasoningDelta || events[1].Type != canonical.EventProviderProof || events[1].Item == nil || events[1].Item.Proof == nil {
		t.Fatalf("events = %#v", events)
	}
	proof := events[1].Item.Proof
	if events[1].OutputIndex != events[0].OutputIndex || events[1].Item.ID != events[0].Item.ID || proof.Value != "sig-test" || proof.Subject != "" || proof.TargetID != "" {
		t.Fatalf("signature was not attached to same-frame reasoning: %#v", events)
	}

	accumulator := canonical.NewEventAccumulator()
	for _, event := range events {
		accumulator.Observe(event)
	}
	output := accumulator.Snapshot().Output
	if len(output) != 1 || output[0].Proof == nil || output[0].Content[0].Text != "thinking text" {
		t.Fatalf("reasoning output = %#v", output)
	}
	prepared, err := New(nil).Prepare(context.Background(), transport.Invocation{
		Route:     transport.Route{BaseURL: "https://example.test", VendorModel: "gemini"},
		Operation: transport.OperationResponses,
		Request:   canonical.Request{Model: "public", Items: output},
	})
	if err != nil {
		t.Fatal(err)
	}
	if body := string(prepared.Body); !strings.Contains(body, `"text":"thinking text","thought":true,"thoughtSignature":"sig-test"`) {
		t.Fatalf("same-frame reasoning proof was not replayed: %s", body)
	}
}

func TestStreamAssignsUniqueIDsToSeparateReasoningSpans(t *testing.T) {
	decoder := &sse{}
	decode := func(raw string, sequence int64) canonical.Event {
		events, err := decodeStreamEvents([]byte(raw), sequence)
		if err != nil {
			t.Fatal(err)
		}
		events = decoder.normalizeEvents(events)
		if len(events) != 1 || events[0].Item == nil {
			t.Fatalf("events = %#v", events)
		}
		return events[0]
	}
	first := decode(`{"candidates":[{"content":{"parts":[{"text":"first thought","thought":true}]}}]}`, 1)
	_ = decode(`{"candidates":[{"content":{"parts":[{"text":"visible"}]}}]}`, 2)
	second := decode(`{"candidates":[{"content":{"parts":[{"text":"second thought","thought":true}]}}]}`, 3)
	if first.OutputIndex == second.OutputIndex || first.Item.ID == second.Item.ID {
		t.Fatalf("reasoning spans were merged: first=%#v second=%#v", first, second)
	}
}

func TestStreamKeepsSameFrameGooglePartsSeparate(t *testing.T) {
	for name, parts := range map[string]string{
		"signed signed":   `[{"text":"one","thoughtSignature":"proof-1"},{"text":"two","thoughtSignature":"proof-2"}]`,
		"unsigned signed": `[{"text":"one"},{"text":"two","thoughtSignature":"proof-2"}]`,
		"signed unsigned": `[{"text":"one","thoughtSignature":"proof-1"},{"text":"two"}]`,
	} {
		t.Run(name, func(t *testing.T) {
			events, err := decodeStreamEvents([]byte(`{"candidates":[{"content":{"parts":`+parts+`}}]}`), 1)
			if err != nil {
				t.Fatal(err)
			}
			decoder := &sse{}
			events = decoder.normalizeEvents(events)
			messageIDs := make([]string, 0, 2)
			targets := make(map[string]bool)
			for _, event := range events {
				if event.Type == canonical.EventTextDelta && event.Item != nil {
					messageIDs = append(messageIDs, event.Item.ID)
				}
				if event.Type == canonical.EventProviderProof && event.Item != nil && event.Item.Proof != nil {
					targets[event.Item.Proof.TargetID] = true
				}
			}
			if len(messageIDs) != 2 || messageIDs[0] == "" || messageIDs[0] == messageIDs[1] {
				t.Fatalf("same-frame messages merged: %#v; events=%#v", messageIDs, events)
			}
			for target := range targets {
				if target != messageIDs[0] && target != messageIDs[1] {
					t.Fatalf("proof target %q does not identify its part: %#v", target, events)
				}
			}
		})
	}
}

func TestStreamUsesNativeCandidateIndexAcrossFrames(t *testing.T) {
	decoder := &sse{}
	decode := func(raw string, sequence int64) canonical.Event {
		events, err := decodeStreamEvents([]byte(raw), sequence)
		if err != nil {
			t.Fatal(err)
		}
		events = decoder.normalizeEvents(events)
		if len(events) != 1 || events[0].Type != canonical.EventTextDelta || events[0].Item == nil {
			t.Fatalf("events = %#v", events)
		}
		return events[0]
	}
	first := decode(`{"candidates":[{"index":2,"content":{"parts":[{"text":"a"}]}}]}`, 1)
	second := decode(`{"candidates":[{"index":5,"content":{"parts":[{"text":"b"}]}}]}`, 2)
	third := decode(`{"candidates":[{"index":2,"content":{"parts":[{"text":"c"}]}}]}`, 3)
	if first.ChoiceIndex != 2 || second.ChoiceIndex != 5 || third.ChoiceIndex != 2 {
		t.Fatalf("candidate indexes = %d, %d, %d", first.ChoiceIndex, second.ChoiceIndex, third.ChoiceIndex)
	}
	if first.Item.ID == second.Item.ID || first.Item.ID != third.Item.ID {
		t.Fatalf("candidate message IDs = %q, %q, %q", first.Item.ID, second.Item.ID, third.Item.ID)
	}
}

func TestStreamRejectsCandidateChanges(t *testing.T) {
	decoder := &sse{}
	first, err := decodeStreamEvents([]byte(`{"candidates":[{"index":0,"content":{"parts":[{"text":"first"}]}}]}`), 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := decoder.validateCandidate(first); err != nil {
		t.Fatalf("first candidate was rejected: %v", err)
	}
	second, err := decodeStreamEvents([]byte(`{"candidates":[{"index":1,"content":{"parts":[{"text":"second"}]}}]}`), 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := decoder.validateCandidate(second); err == nil || !strings.Contains(err.Error(), "multiple candidates") {
		t.Fatalf("candidate change error = %v", err)
	}
	if _, err := decodeStreamEvents([]byte(`{"candidates":[{"index":0,"finishReason":"STOP"},{"index":1,"finishReason":"MAX_TOKENS"}]}`), 3); err == nil || !strings.Contains(err.Error(), "multiple candidates") {
		t.Fatalf("same-frame candidates error = %v", err)
	}
}

func TestStreamKeepsFunctionCallsFromSeparateFramesDistinct(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"functionCall\":{\"name\":\"first\",\"args\":{}},\"thoughtSignature\":\"proof-1\"}]}}]}\n\n")
		_, _ = io.WriteString(writer, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"functionCall\":{\"name\":\"second\",\"args\":{}}}]}}]}\n\n")
		_, _ = io.WriteString(writer, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"functionCall\":{\"name\":\"third\",\"args\":{\"a\":1}}}]}}]}\n\n")
		_, _ = io.WriteString(writer, "data: {\"responseId\":\"resp_tools\",\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n")
	}))
	defer server.Close()
	stream, _, err := transport.Stream(context.Background(), New(server.Client()), transport.Invocation{
		Route: transport.Route{BaseURL: server.URL, VendorModel: "gemini"}, Operation: transport.OperationResponses,
		Request: canonical.Request{Model: "public", Stream: true, Items: []canonical.Item{{Type: "message", Role: canonical.RoleUser, Content: []canonical.Content{{Type: "input_text", Text: "hi"}}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	accumulator := canonical.NewEventAccumulator()
	for {
		event, nextErr := stream.Next(context.Background())
		if nextErr != nil {
			t.Fatal(nextErr)
		}
		accumulator.Observe(event)
		if event.Type == canonical.EventCompleted {
			break
		}
	}
	output := accumulator.Snapshot().Output
	if len(output) != 3 {
		t.Fatalf("separate calls merged: %#v", output)
	}
	for index, name := range []string{"first", "second", "third"} {
		if output[index].Type != "function_call" || output[index].Name != name || !output[index].ProviderCallIDOmitted {
			t.Fatalf("call %d = %#v", index, output[index])
		}
	}
	if output[0].Proof == nil || output[0].Proof.Value != "proof-1" || output[1].Proof != nil || output[2].Proof != nil {
		t.Fatalf("parallel call proofs = %#v", output)
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

func TestStreamFunctionCallStartsBeforeArgumentsDelta(t *testing.T) {
	events, err := decodeStreamEvents([]byte(`{"candidates":[{"content":{"parts":[{"functionCall":{"id":"call_1","name":"lookup","args":{"q":"x"}}}]}}]}`), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Type != canonical.EventOutputItemAdded || events[1].Type != canonical.EventToolArgumentsDelta {
		t.Fatalf("events = %#v", events)
	}
	if events[0].Item == nil || events[0].Item.CallID != "call_1" || events[0].Item.Name != "lookup" {
		t.Fatalf("tool start = %#v", events[0])
	}
	if events[1].Item == nil || events[1].Item.CallID != "call_1" || events[1].Delta != `{"q":"x"}` {
		t.Fatalf("tool delta = %#v", events[1])
	}
}

func TestStreamPreservesGoogleProofs(t *testing.T) {
	reasoning, err := decodeStreamEvents([]byte(`{"candidates":[{"content":{"parts":[{"text":"think","thought":true,"thoughtSignature":"reasoning-proof"}]}}]}`), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(reasoning) != 1 || reasoning[0].Type != canonical.EventReasoningDelta || reasoning[0].Item == nil || reasoning[0].Item.Proof == nil || reasoning[0].Item.Proof.Value != "reasoning-proof" {
		t.Fatalf("reasoning events = %#v", reasoning)
	}
	multiple, err := decodeStreamEvents([]byte(`{"candidates":[{"content":{"parts":[{"text":"first","thought":true,"thoughtSignature":"proof-1"},{"text":"second","thought":true,"thoughtSignature":"proof-2"}]}}]}`), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(multiple) != 2 || multiple[0].OutputIndex != 0 || multiple[1].OutputIndex != 1 || multiple[0].Item == nil || multiple[1].Item == nil || multiple[0].Item.Proof.Value != "proof-1" || multiple[1].Item.Proof.Value != "proof-2" {
		t.Fatalf("multiple reasoning events = %#v", multiple)
	}

	tool, err := decodeStreamEvents([]byte(`{"candidates":[{"content":{"parts":[{"functionCall":{"id":"call_1","name":"lookup","args":{"q":"x"}},"thoughtSignature":"tool-proof"}]}}]}`), 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(tool) != 2 || tool[0].Type != canonical.EventOutputItemAdded || tool[0].Item == nil || tool[0].Item.Proof == nil || tool[0].Item.Proof.Value != "tool-proof" || tool[1].Item == nil || tool[1].Item.Proof == nil {
		t.Fatalf("tool events = %#v", tool)
	}

	standalone, err := decodeStreamEvents([]byte(`{"candidates":[{"content":{"parts":[{"text":"","thoughtSignature":"standalone-proof"}]}}]}`), 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(standalone) != 2 || standalone[0].Type != canonical.EventTextDelta || standalone[0].Item == nil || standalone[1].Type != canonical.EventProviderProof || standalone[1].Item == nil || standalone[1].Item.Proof == nil || standalone[1].Item.Proof.Provider != canonical.ProofProviderGoogle || standalone[1].Item.Proof.Value != "standalone-proof" || standalone[1].Item.Proof.Subject != canonical.ProofSubjectGooglePart || standalone[1].Item.Proof.TargetID != standalone[0].Item.ID {
		t.Fatalf("standalone events = %#v", standalone)
	}
}

func TestHTTPErrorPreservesNumericCode(t *testing.T) {
	err := newHTTPError(http.StatusTooManyRequests, []byte(`{"error":{"code":429,"message":"slow","status":"RESOURCE_EXHAUSTED"}}`))
	var upstream *HTTPError
	if !errors.As(err, &upstream) || upstream.Details == nil || upstream.Details.Code != "RESOURCE_EXHAUSTED" || upstream.Details.Message != "slow" || !upstream.Details.Retryable {
		t.Fatalf("error = %#v", err)
	}
}

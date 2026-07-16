package conformance_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mirainya/Prism/internal/gateway/canonical"
	anthropiccodec "github.com/mirainya/Prism/internal/gateway/codec/anthropic"
	responsescodec "github.com/mirainya/Prism/internal/gateway/codec/openai_responses"
	"github.com/mirainya/Prism/internal/gateway/transport"
	anthropictransport "github.com/mirainya/Prism/internal/gateway/transport/anthropic"
	googletransport "github.com/mirainya/Prism/internal/gateway/transport/google"
	responsesprotocol "github.com/mirainya/Prism/internal/provider/responses"
)

func TestAnthropicThinkingStreamEncodesAsResponsesReasoning(t *testing.T) {
	frames := [][]byte{
		[]byte(`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"consider the evidence"}}`),
		[]byte(`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-anthropic"}}`),
	}
	accumulator := canonical.NewEventAccumulator()
	for _, frame := range frames {
		event, err := anthropiccodec.DecodeEvent("content_block_delta", frame)
		if err != nil {
			t.Fatal(err)
		}
		accumulator.Observe(event)
	}

	encoded, err := responsescodec.EncodeResponseJSON(accumulator.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Output []struct {
			Type             string `json:"type"`
			EncryptedContent string `json:"encrypted_content"`
			Summary          []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"summary"`
		} `json:"output"`
	}
	if err := json.Unmarshal(encoded, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Output) != 1 || response.Output[0].Type != "reasoning" {
		t.Fatalf("Responses reasoning output = %s", encoded)
	}
	reasoning := response.Output[0]
	if len(reasoning.Summary) != 1 || reasoning.Summary[0].Type != "summary_text" || reasoning.Summary[0].Text != "consider the evidence" {
		t.Fatalf("Responses reasoning summary = %#v", reasoning.Summary)
	}
	proof, ok := canonical.ParseTaggedProviderProof(reasoning.EncryptedContent)
	if !ok || proof.Provider != canonical.ProofProviderAnthropic || proof.Value != "sig-anthropic" {
		t.Fatalf("Responses encrypted_content = %q, proof = %#v", reasoning.EncryptedContent, proof)
	}
}

func TestResponsesAnthropicProofRestoresNativeSignature(t *testing.T) {
	proof := canonical.EncodeResponsesEncryptedContent(&canonical.ProviderProof{Provider: canonical.ProofProviderAnthropic, Value: "sig-native"})
	request := decodeResponsesRequest(t, `{
		"model":"claude",
		"input":[
			{"type":"reasoning","id":"rs_1","encrypted_content":"`+proof+`","summary":[{"type":"summary_text","text":"analysis"}]},
			{"role":"user","content":[{"type":"input_text","text":"continue"}]}
		]
	}`)

	body, err := anthropiccodec.EncodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var messages struct {
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type      string `json:"type"`
				Thinking  string `json:"thinking"`
				Signature string `json:"signature"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(encoded, &messages); err != nil {
		t.Fatal(err)
	}
	if len(messages.Messages) == 0 || messages.Messages[0].Role != "assistant" || len(messages.Messages[0].Content) != 1 {
		t.Fatalf("Anthropic messages = %s", encoded)
	}
	thinking := messages.Messages[0].Content[0]
	if thinking.Type != "thinking" || thinking.Thinking != "analysis" || thinking.Signature != "sig-native" {
		t.Fatalf("Anthropic thinking block = %#v", thinking)
	}
}

func TestAnthropicRedactedThinkingRoundTripsThroughResponses(t *testing.T) {
	request, err := anthropiccodec.DecodeRequestJSON([]byte(`{"model":"claude","max_tokens":64,"messages":[{"role":"assistant","content":[{"type":"redacted_thinking","data":"opaque-redacted"}]},{"role":"user","content":[{"type":"text","text":"continue"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	encodedResponse, err := responsescodec.EncodeResponseJSON(canonical.Response{ID: "resp_1", Model: "claude", Output: request.Items[:1]})
	if err != nil {
		t.Fatal(err)
	}
	var wire responsesprotocol.Response
	if err := json.Unmarshal(encodedResponse, &wire); err != nil {
		t.Fatal(err)
	}
	replayed, err := responsescodec.DecodeItems(wire.Output)
	if err != nil {
		t.Fatal(err)
	}
	if len(replayed) != 1 || replayed[0].Proof == nil || replayed[0].Proof.Subject != canonical.ProofSubjectAnthropicRedacted || replayed[0].Proof.Value != "opaque-redacted" {
		t.Fatalf("Responses redacted proof = %#v; wire=%s", replayed, encodedResponse)
	}
	body, err := anthropiccodec.EncodeRequest(canonical.Request{Model: "claude", Items: replayed})
	if err != nil {
		t.Fatal(err)
	}
	encodedBody, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encodedBody), `"type":"redacted_thinking"`) || !strings.Contains(string(encodedBody), `"data":"opaque-redacted"`) || strings.Contains(string(encodedBody), `"signature"`) {
		t.Fatalf("Anthropic redacted replay = %s", encodedBody)
	}
}

func TestAnthropicPlanRejectsGoogleProviderProof(t *testing.T) {
	proof := canonical.EncodeResponsesEncryptedContent(&canonical.ProviderProof{Provider: canonical.ProofProviderGoogle, Value: "google-proof"})
	request := decodeResponsesRequest(t, `{
		"model":"claude",
		"input":[
			{"type":"reasoning","encrypted_content":"`+proof+`","summary":[{"type":"summary_text","text":"analysis"}]},
			{"role":"user","content":[{"type":"input_text","text":"continue"}]}
		]
	}`)

	plan := anthropictransport.New(nil).Plan(
		transport.OperationResponses,
		request,
		canonical.NewFeatureSet(canonical.FeatureReasoning),
	)
	if plan.Supported() {
		t.Fatalf("Anthropic plan accepted Google provider proof: %#v", plan)
	}
}

func TestGeminiParallelFunctionCallProofsRoundTripThroughResponses(t *testing.T) {
	wantProofs := map[string]string{
		"call_weather": "sig-weather#opaque+/=",
		"call_time":    "sig-time#opaque+/=",
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"responseId":"gemini_response_1",
			"candidates":[{
				"content":{"role":"model","parts":[
					{"functionCall":{"id":"call_weather","name":"weather","args":{"city":"Tokyo"}},"thoughtSignature":"sig-weather#opaque+/="},
					{"functionCall":{"id":"call_time","name":"time","args":{"zone":"Asia/Tokyo"}},"thoughtSignature":"sig-time#opaque+/="}
				]},
				"finishReason":"STOP"
			}]
		}`))
	}))
	defer upstream.Close()

	gemini := googletransport.New(upstream.Client())
	invocation := transport.Invocation{
		Route: transport.Route{
			BaseURL:     upstream.URL,
			VendorModel: "gemini-test",
			PublicModel: "public-gemini",
		},
		Operation: transport.OperationResponses,
		Request: canonical.Request{
			Endpoint: canonical.EndpointOpenAIResponses,
			Model:    "public-gemini",
			Items: []canonical.Item{{
				Type: "message", Role: canonical.RoleUser,
				Content: []canonical.Content{{Type: "input_text", Text: "Use both tools"}},
			}},
		},
	}
	canonicalResponse, _, err := transport.Execute(context.Background(), gemini, invocation)
	if err != nil {
		t.Fatal(err)
	}
	assertFunctionCallProofs(t, canonicalResponse.Output, wantProofs)

	encodedResponse, err := responsescodec.EncodeResponseJSON(canonicalResponse)
	if err != nil {
		t.Fatal(err)
	}
	var wireResponse responsesprotocol.Response
	if err := json.Unmarshal(encodedResponse, &wireResponse); err != nil {
		t.Fatal(err)
	}
	replayedRequest, err := responsescodec.DecodeRequest(responsesprotocol.Request{
		Model: "public-gemini",
		Input: wireResponse.Output,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertFunctionCallProofs(t, replayedRequest.Items, wantProofs)

	prepared, err := gemini.Prepare(context.Background(), transport.Invocation{
		Route: transport.Route{
			BaseURL:     "https://generativelanguage.googleapis.com",
			VendorModel: "gemini-test",
			PublicModel: "public-gemini",
		},
		Operation: transport.OperationResponses,
		Request:   replayedRequest,
	})
	if err != nil {
		t.Fatal(err)
	}
	var replayBody struct {
		Contents []struct {
			Parts []struct {
				ThoughtSignature string `json:"thoughtSignature"`
				FunctionCall     *struct {
					ID string `json:"id"`
				} `json:"functionCall"`
			} `json:"parts"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(prepared.Body, &replayBody); err != nil {
		t.Fatal(err)
	}
	if len(replayBody.Contents) != 1 || len(replayBody.Contents[0].Parts) != len(wantProofs) {
		t.Fatalf("parallel Gemini function calls were not sibling parts: %s", prepared.Body)
	}
	gotProofs := make(map[string]string)
	for _, content := range replayBody.Contents {
		for _, part := range content.Parts {
			if part.FunctionCall != nil {
				gotProofs[part.FunctionCall.ID] = part.ThoughtSignature
			}
		}
	}
	if len(gotProofs) != len(wantProofs) {
		t.Fatalf("replayed Gemini function calls = %#v; body = %s", gotProofs, prepared.Body)
	}
	for callID, want := range wantProofs {
		if got := gotProofs[callID]; got != want {
			t.Fatalf("Gemini functionCall %q thoughtSignature = %q, want %q; body = %s", callID, got, want, prepared.Body)
		}
	}
}

func assertFunctionCallProofs(t *testing.T, items []canonical.Item, want map[string]string) {
	t.Helper()
	got := make(map[string]string)
	for _, item := range items {
		if item.Type != "function_call" || item.Proof == nil {
			continue
		}
		if item.Proof.Provider != canonical.ProofProviderGoogle {
			t.Fatalf("function call %q proof provider = %q", item.CallID, item.Proof.Provider)
		}
		got[item.CallID] = item.Proof.Value
	}
	if len(got) != len(want) {
		t.Fatalf("function call proofs = %#v, want %#v; items = %#v", got, want, items)
	}
	for callID, proof := range want {
		if got[callID] != proof {
			t.Fatalf("function call %q proof = %q, want %q", callID, got[callID], proof)
		}
	}
}

func decodeResponsesRequest(t *testing.T, raw string) canonical.Request {
	t.Helper()
	var wire responsesprotocol.Request
	if err := json.Unmarshal([]byte(raw), &wire); err != nil {
		t.Fatal(err)
	}
	request, err := responsescodec.DecodeRequest(wire)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

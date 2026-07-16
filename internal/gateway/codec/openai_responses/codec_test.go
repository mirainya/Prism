package openai_responses

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mirainya/Prism/internal/gateway/canonical"
	protocol "github.com/mirainya/Prism/internal/provider/responses"
)

func TestDecodeRequestPreservesExtensionsAndVideo(t *testing.T) {
	var request protocol.Request
	if err := json.Unmarshal([]byte(`{
		"model":"doubao","input":[{"role":"user","content":[
			{"type":"input_text","text":"describe"},
			{"type":"input_video","video_url":"https://example.test/v.mp4","fps":2}
		]}],"thinking":{"type":"enabled"},"future_control":{"enabled":true}
	}`), &request); err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Endpoint != canonical.EndpointOpenAIResponses || len(decoded.Items) != 1 || len(decoded.Items[0].Content) != 2 {
		t.Fatalf("unexpected request: %#v", decoded)
	}
	video := decoded.Items[0].Content[1]
	if video.Type != "input_video" || video.URL != "https://example.test/v.mp4" || string(video.Extra["fps"]) != "2" {
		t.Fatalf("video was not preserved: %#v", video)
	}
	if decoded.ProviderOptions.Volcengine == nil || len(decoded.ProviderOptions.Volcengine.Thinking) == 0 || len(decoded.ProviderOptions.Volcengine.Unknown["future_control"]) == 0 {
		t.Fatalf("Volcengine extensions missing: %#v", decoded.ProviderOptions)
	}
	if len(decoded.ClientExtensions[extraRequest]) == 0 {
		t.Fatal("client extension copy missing")
	}
}

func TestDecodeRequestNormalizesCallsAudioFilesAndKnownExtras(t *testing.T) {
	var request protocol.Request
	if err := json.Unmarshal([]byte(`{
		"model":"gpt","input":[
			{"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup","arguments":"{\"q\":\"x\"}"},
			{"type":"function_call_output","call_id":"call_1","output":"ok"},
			{"role":"user","content":[
				{"type":"input_audio","input_audio":{"data":"YWJj","format":"wav"}},
				{"type":"input_file","file_data":"ZmlsZQ==","filename":"notes.txt","content_type":"text/plain"}
			]}
		],"conversation":"conv_1","max_tool_calls":3,"text":{"format":{"type":"text"},"verbosity":"low"}
	}`), &request); err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if got := decoded.Items[0]; got.CallID != "call_1" || string(got.Arguments) != `{"q":"x"}` {
		t.Fatalf("function call = %#v", got)
	}
	if got := decoded.Items[2].Content[0]; got.Data != "YWJj" || got.Format != "wav" {
		t.Fatalf("audio = %#v", got)
	}
	if got := decoded.Items[2].Content[1]; got.Filename != "notes.txt" || got.MediaType != "text/plain" {
		t.Fatalf("file = %#v", got)
	}
	if extras := string(decoded.ClientExtensions[extraRequest]); !strings.Contains(extras, `"conversation":"conv_1"`) || !strings.Contains(extras, `"max_tool_calls":3`) || !strings.Contains(extras, `"verbosity":"low"`) {
		t.Fatalf("known extensions were lost: %s", extras)
	}
}

func TestResponsesReasoningProofAndNamespaceRoundTrip(t *testing.T) {
	proof := canonical.EncodeResponsesEncryptedContent(&canonical.ProviderProof{Provider: canonical.ProofProviderAnthropic, Value: "sig_1"})
	var request protocol.Request
	if err := json.Unmarshal([]byte(`{
		"model":"claude","input":[
			{"type":"reasoning","id":"rs_1","encrypted_content":"`+proof+`","summary":[{"type":"summary_text","text":"analysis"}]},
			{"type":"function_call","id":"fc_1","call_id":"call_1","namespace":"tools","name":"lookup","arguments":"{}"}
		]
	}`), &request); err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Items) != 2 || decoded.Items[0].Proof == nil || decoded.Items[0].Proof.Provider != canonical.ProofProviderAnthropic || decoded.Items[0].Proof.Value != "sig_1" {
		t.Fatalf("reasoning proof was not decoded: %#v", decoded.Items)
	}
	if len(decoded.Items[0].Content) != 1 || decoded.Items[0].Content[0].Type != "reasoning_text" || decoded.Items[0].Content[0].Text != "analysis" {
		t.Fatalf("reasoning summary was not decoded: %#v", decoded.Items[0].Content)
	}
	if decoded.Items[1].Namespace != "tools" {
		t.Fatalf("function namespace = %q", decoded.Items[1].Namespace)
	}

	encoded, err := EncodeResponseJSON(canonical.Response{ID: "resp_1", Model: "claude", Output: decoded.Items})
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, expected := range []string{`"encrypted_content":"` + proof + `"`, `"summary":[{"text":"analysis","type":"summary_text"}]`, `"namespace":"tools"`} {
		if !strings.Contains(text, expected) {
			t.Fatalf("encoded response missing %s: %s", expected, text)
		}
	}
}

func TestResponsesUntaggedEncryptedContentRemainsNativeExtension(t *testing.T) {
	var request protocol.Request
	if err := json.Unmarshal([]byte(`{"model":"doubao","input":[{"type":"reasoning","encrypted_content":"native-opaque","summary":[]}]}`), &request); err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Items) != 1 || decoded.Items[0].Proof != nil || string(decoded.Items[0].Extra["encrypted_content"]) != `"native-opaque"` {
		t.Fatalf("untagged encrypted_content was reclassified: %#v", decoded.Items)
	}
	encoded, err := EncodeResponseJSON(canonical.Response{ID: "resp_1", Model: "doubao", Output: decoded.Items})
	if err != nil || !strings.Contains(string(encoded), `"encrypted_content":"native-opaque"`) {
		t.Fatalf("native encrypted_content was lost: %s err=%v", encoded, err)
	}
	if !strings.Contains(string(encoded), `"summary":[]`) {
		t.Fatalf("empty reasoning summary was lost: %s", encoded)
	}
}

func TestResponsesEncoderFiltersInternalExtrasAndRejectsMalformedProof(t *testing.T) {
	item := canonical.Item{
		ID: "rs_1", Type: "reasoning",
		Content: []canonical.Content{{Type: "reasoning_text", Text: "think", Extra: map[string]json.RawMessage{
			"anthropic.raw_block": json.RawMessage(`{"type":"thinking","signature":"raw-secret"}`),
			"summary_future":      json.RawMessage(`true`),
		}}},
		Extra: map[string]json.RawMessage{
			"openai_chat.finish_reason": json.RawMessage(`"stop"`),
			"prism.choice_index":        json.RawMessage(`1`),
			"future":                    json.RawMessage(`true`),
		},
	}
	encoded, err := EncodeResponseJSON(canonical.Response{ID: "resp_1", Output: []canonical.Item{item}})
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, forbidden := range []string{"anthropic.raw_block", "openai_chat.finish_reason", "prism.choice_index", "raw-secret"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("internal extra %q leaked: %s", forbidden, text)
		}
	}
	for _, expected := range []string{`"future":true`, `"summary_future":true`} {
		if !strings.Contains(text, expected) {
			t.Fatalf("native Responses extra %s was lost: %s", expected, text)
		}
	}
	frame, err := EncodeSSEFrame(canonical.Event{Type: canonical.EventOutputItemDone, OutputIndex: 0, Item: &item})
	if err != nil || strings.Contains(string(frame), "anthropic.raw_block") || strings.Contains(string(frame), "openai_chat.finish_reason") {
		t.Fatalf("internal SSE extra leaked: %s err=%v", frame, err)
	}
	item.Proof = &canonical.ProviderProof{Provider: canonical.ProofProvider("future"), Value: "opaque"}
	if _, err := EncodeResponseJSON(canonical.Response{ID: "resp_1", Output: []canonical.Item{item}}); err == nil {
		t.Fatal("malformed provider proof was silently dropped")
	}
}

func TestResponsesEncoderPreservesOutputMediaAndTypedFields(t *testing.T) {
	response := canonical.Response{ID: "resp_1", Output: []canonical.Item{
		{Type: "message", Role: canonical.RoleAssistant, Content: []canonical.Content{
			{Type: "output_text"},
			{Type: "output_image", Data: "aW1n", MediaType: "image/png"},
			{Type: "output_audio", Data: "YXVkaW8=", Format: "wav", MediaType: "audio/wav"},
			{Type: "output_video", URL: "https://example.test/video.mp4", MediaType: "video/mp4"},
			{Type: "output_file", Data: "ZmlsZQ==", Filename: "report.pdf", MediaType: "application/pdf"},
		}},
		{Type: "function_call", Role: canonical.RoleAssistant, CallID: "call_1", Name: "lookup", Arguments: json.RawMessage(`{}`)},
	}}
	encoded, err := EncodeResponseJSON(response)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, expected := range []string{`"text":""`, `data:image/png;base64,aW1n`, `"data":"YXVkaW8="`, `"video_url":"https://example.test/video.mp4"`, `"file_data":"ZmlsZQ=="`} {
		if !strings.Contains(text, expected) {
			t.Fatalf("output media field %q missing: %s", expected, text)
		}
	}
	if strings.Contains(text, `"type":"function_call","role"`) || strings.Contains(text, `"role":"assistant","name":"lookup"`) {
		t.Fatalf("function_call leaked message role: %s", text)
	}
	var wire protocol.Response
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	items, err := DecodeItems(wire.Output)
	if err != nil {
		t.Fatal(err)
	}
	content := items[0].Content
	if len(content) != 5 || content[1].URL != "data:image/png;base64,aW1n" || content[2].Data != "YXVkaW8=" || content[2].Format != "wav" || content[3].URL != "https://example.test/video.mp4" || content[4].Data != "ZmlsZQ==" {
		t.Fatalf("output media round trip = %#v", content)
	}
}

func TestResponsesFunctionCallProofCarrierRoundTrip(t *testing.T) {
	source := canonical.Response{ID: "resp_1", Model: "gemini", Output: []canonical.Item{
		{ID: "fc_1", Type: "function_call", CallID: "call_1", Name: "first", Arguments: json.RawMessage(`{"q":1}`), Proof: &canonical.ProviderProof{Provider: canonical.ProofProviderGoogle, Value: "signature-1"}},
		{ID: "fc_2", Type: "function_call", CallID: "call_2", Name: "second", Arguments: json.RawMessage(`{"q":2}`), Proof: &canonical.ProviderProof{Provider: canonical.ProofProviderGoogle, Value: "signature-2"}},
	}}
	encoded, err := EncodeResponseJSON(source)
	if err != nil {
		t.Fatal(err)
	}
	var wire protocol.Response
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	var output []map[string]json.RawMessage
	if err := json.Unmarshal(wire.Output, &output); err != nil {
		t.Fatal(err)
	}
	if len(output) != 4 || rawString(output[0]["type"]) != "reasoning" || rawString(output[1]["type"]) != "function_call" || rawString(output[2]["type"]) != "reasoning" || rawString(output[3]["type"]) != "function_call" {
		t.Fatalf("carrier output = %s", wire.Output)
	}
	for _, index := range []int{0, 2} {
		if !strings.HasPrefix(rawString(output[index]["encrypted_content"]), functionProofPrefix) || string(output[index]["summary"]) != "[]" {
			t.Fatalf("carrier %d = %s", index, wire.Output)
		}
	}

	decoded, err := DecodeItems(wire.Output)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 2 {
		t.Fatalf("decoded items = %#v", decoded)
	}
	for index, signature := range []string{"signature-1", "signature-2"} {
		if decoded[index].Type != "function_call" || decoded[index].Proof == nil || decoded[index].Proof.Provider != canonical.ProofProviderGoogle || decoded[index].Proof.Value != signature {
			t.Fatalf("decoded[%d] = %#v", index, decoded[index])
		}
	}
}

func TestResponsesFunctionCallProofCarrierRequiresUniqueMatchingCall(t *testing.T) {
	functionCall := canonical.Item{ID: "fc_1", Type: "function_call", CallID: "call_1", Proof: &canonical.ProviderProof{Provider: canonical.ProofProviderGoogle, Value: "signature"}}
	carrier, ok := FunctionCallProofCarrier(functionCall)
	if !ok {
		t.Fatal("carrier was not created")
	}
	withoutProof := functionCall
	withoutProof.Proof = nil
	trailing := RestoreFunctionCallProofCarriers([]canonical.Item{withoutProof, carrier})
	if len(trailing) != 1 || trailing[0].Proof == nil || trailing[0].Proof.Value != "signature" {
		t.Fatalf("trailing carrier was not restored: %#v", trailing)
	}
	nonAdjacent := RestoreFunctionCallProofCarriers([]canonical.Item{carrier, {Type: "message", Role: canonical.RoleAssistant}, withoutProof})
	if len(nonAdjacent) != 2 || nonAdjacent[1].Proof == nil || nonAdjacent[1].Proof.Value != "signature" {
		t.Fatalf("non-adjacent carrier was not restored: %#v", nonAdjacent)
	}
	ordinary := canonical.Item{Type: "reasoning", Proof: &canonical.ProviderProof{Provider: canonical.ProofProviderGoogle, Value: "ordinary"}}
	wrongCall := functionCall
	wrongCall.CallID = "call_2"
	wrongCall.Proof = nil
	tampered := canonical.CloneItems([]canonical.Item{carrier})[0]
	tampered.Extra["encrypted_content"] = json.RawMessage(`"prism-proof-v1#invalid"`)
	withExtension := canonical.CloneItems([]canonical.Item{carrier})[0]
	withExtension.Extra["future"] = json.RawMessage(`true`)

	for name, items := range map[string][]canonical.Item{
		"wrong call":         {carrier, wrongCall},
		"ambiguous call":     {carrier, withoutProof, withoutProof},
		"tampered envelope":  {tampered, functionCall},
		"unknown extension":  {withExtension, functionCall},
		"ordinary reasoning": {ordinary, functionCall},
	} {
		t.Run(name, func(t *testing.T) {
			restored := RestoreFunctionCallProofCarriers(items)
			if len(restored) != len(items) {
				t.Fatalf("carrier was incorrectly folded: %#v", restored)
			}
		})
	}
}

func TestEncodeResponsePreservesProviderAndUsageExtensions(t *testing.T) {
	encoded, err := EncodeResponseJSON(canonical.Response{
		ID: "resp_1", Model: "public", Status: "completed", CreatedAt: 7,
		Output:             []canonical.Item{{ID: "msg_1", Type: "message", Role: canonical.RoleAssistant, Content: []canonical.Content{{Type: "output_text", Text: "done"}}}},
		Usage:              &canonical.Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5, Extra: map[string]json.RawMessage{"tool_usage": json.RawMessage(`{"web_search":1}`)}},
		ProviderExtensions: map[string]json.RawMessage{"service_status": json.RawMessage(`{"tier":"fast"}`), "future_response": json.RawMessage(`true`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	if string(result["future_response"]) != "true" || !strings.Contains(string(result["service_status"]), `"tier":"fast"`) || !strings.Contains(string(result["usage"]), "tool_usage") || !strings.Contains(string(result["output"]), "output_text") {
		t.Fatalf("extensions were lost: %s", encoded)
	}
}

func TestEncodeResponseUsesWireStringsForCalls(t *testing.T) {
	encoded, err := EncodeResponseJSON(canonical.Response{
		ID: "resp_1", Model: "gpt", Output: []canonical.Item{
			{ID: "fc_1", Type: "function_call", CallID: "call_1", Name: "lookup", Arguments: json.RawMessage(`{"q":"x"}`)},
			{Type: "function_call_output", CallID: "call_1", Output: json.RawMessage(`"ok"`)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Output []struct {
			CallID    string `json:"call_id"`
			Arguments string `json:"arguments"`
			Output    string `json:"output"`
		} `json:"output"`
	}
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	if result.Output[0].CallID != "call_1" || result.Output[0].Arguments != `{"q":"x"}` || result.Output[1].Output != "ok" {
		t.Fatalf("response = %s", encoded)
	}
}

func TestEncodeResponseAssignsStableOutputItemIDs(t *testing.T) {
	tests := []struct {
		name   string
		item   canonical.Item
		prefix string
	}{
		{
			name: "message",
			item: canonical.Item{
				Type: "message", Role: canonical.RoleAssistant, Status: "completed",
				Content: []canonical.Content{{Type: "output_text", Text: "done"}},
			},
			prefix: "msg_prism_",
		},
		{
			name: "reasoning",
			item: canonical.Item{
				Type: "reasoning", Status: "completed",
				Content: []canonical.Content{{Type: "reasoning_text", Text: "analysis"}},
			},
			prefix: "rs_prism_",
		},
		{
			name: "function call",
			item: canonical.Item{
				Type: "function_call", Status: "completed", Name: "lookup",
				Arguments: json.RawMessage(`{"q":"x"}`),
			},
			prefix: "fc_prism_",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := canonical.Response{ID: "resp_stable", Model: "public", CreatedAt: 7, Output: []canonical.Item{test.item}}
			first, err := EncodeResponseJSON(source)
			if err != nil {
				t.Fatal(err)
			}
			second, err := EncodeResponseJSON(source)
			if err != nil {
				t.Fatal(err)
			}

			decode := func(raw []byte) struct {
				ID     string `json:"id"`
				CallID string `json:"call_id"`
			} {
				var response protocol.Response
				if err := json.Unmarshal(raw, &response); err != nil {
					t.Fatal(err)
				}
				var output []struct {
					ID     string `json:"id"`
					CallID string `json:"call_id"`
				}
				if err := json.Unmarshal(response.Output, &output); err != nil {
					t.Fatal(err)
				}
				if len(output) != 1 {
					t.Fatalf("output = %s", response.Output)
				}
				return output[0]
			}

			firstItem, secondItem := decode(first), decode(second)
			if !strings.HasPrefix(firstItem.ID, test.prefix) || firstItem.ID != secondItem.ID {
				t.Fatalf("unstable output ID: first=%q second=%q", firstItem.ID, secondItem.ID)
			}
			if test.item.Type == "function_call" && firstItem.CallID != firstItem.ID {
				t.Fatalf("function call ID=%q call_id=%q", firstItem.ID, firstItem.CallID)
			}
			if source.Output[0].ID != "" || source.Output[0].CallID != "" {
				t.Fatalf("source response was mutated: %#v", source.Output[0])
			}
		})
	}
}

func TestEncodeSSEFrameUsesResponsesWireFormat(t *testing.T) {
	frame, err := EncodeSSEFrame(canonical.Event{Type: canonical.EventTextDelta, SequenceNumber: 3, OutputIndex: 1, ContentIndex: 0, Item: &canonical.Item{ID: "msg_1"}, Delta: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	text := string(frame)
	if !strings.HasPrefix(text, "event: response.output_text.delta\ndata: ") || !strings.Contains(text, `"sequence_number":3`) || !strings.Contains(text, `"item_id":"msg_1"`) || !strings.Contains(text, `"content_index":0`) {
		t.Fatalf("invalid Responses SSE frame: %s", text)
	}

	errorFrame, err := EncodeSSEFrame(canonical.Event{Type: canonical.EventError, Error: &canonical.Error{Code: "rate_limit", Message: "slow down", Param: "model"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(errorFrame), "event: error") || !strings.Contains(string(errorFrame), `"code":"rate_limit"`) || strings.Contains(string(errorFrame), `"error":{`) {
		t.Fatalf("invalid error frame: %s", errorFrame)
	}
}

func TestEncodeSSEFrameIgnoresStandardRawAndBuildsTerminalResponse(t *testing.T) {
	frame, err := EncodeSSEFrame(canonical.Event{
		Type: canonical.EventToolArgumentsDelta, SequenceNumber: 0, OutputIndex: 0,
		Item: &canonical.Item{ID: "fc_1", CallID: "call_1"}, Delta: `{"q":`,
		Raw: json.RawMessage(`{"choices":[{"delta":{"content":"wrong protocol"}}]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(frame)
	if !strings.Contains(text, `"sequence_number":0`) || !strings.Contains(text, `"output_index":0`) || !strings.Contains(text, `"item_id":"fc_1"`) || strings.Contains(text, "choices") {
		t.Fatalf("tool delta = %s", frame)
	}

	terminal, err := EncodeSSEFrame(canonical.Event{Type: canonical.EventFailed, SequenceNumber: 4, ProviderResponseID: "resp_1", Error: &canonical.Error{Type: "server_error", Code: "upstream", Message: "failed"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(terminal), "event: response.failed") || !strings.Contains(string(terminal), `"status":"failed"`) || !strings.Contains(string(terminal), `"response":{`) {
		t.Fatalf("terminal = %s", terminal)
	}

	errorFrame, err := EncodeSSEFrame(canonical.Event{Type: canonical.EventError, Error: &canonical.Error{Type: "server_error", Code: "upstream", Message: "failed"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(errorFrame), `"type":"error"`) || strings.Contains(string(errorFrame), `"type":"server_error"`) {
		t.Fatalf("error = %s", errorFrame)
	}
}

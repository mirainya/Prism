package canonical

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRequiredFeatures(t *testing.T) {
	req := &Request{
		Stream: true,
		Tools:  []Tool{{Type: "web_search"}},
		Items:  []Item{{Content: []Content{{Type: "input_image"}, {Type: "input_video"}}}},
	}
	required := req.RequiredFeatures()
	for _, feature := range []Feature{FeatureStream, FeatureTools, FeatureWebSearch, FeatureVision, FeatureVideo} {
		if !required.Has(feature) {
			t.Fatalf("required features missing %q", feature)
		}
	}
}

func TestFeatureSetContains(t *testing.T) {
	provided := FeatureSet{FeatureTools: true, FeatureVision: true}
	if !provided.Contains(FeatureSet{FeatureTools: true}) {
		t.Fatal("expected provided features to contain tools")
	}
	if provided.Contains(FeatureSet{FeatureVideo: true}) {
		t.Fatal("did not expect provided features to contain video")
	}
}

func TestCanonicalJSONUsesStableSnakeCaseFields(t *testing.T) {
	maxOutput := 32
	request := Request{
		Endpoint:           EndpointOpenAIResponses,
		Model:              "public-model",
		MaxOutputTokens:    &maxOutput,
		PreviousResponseID: "resp_previous",
		Items: []Item{{
			Type:   "function_call_output",
			CallID: "call_1",
			Output: json.RawMessage(`"done"`),
			Content: []Content{{
				Type: "input_file", FileID: "file_1", Filename: "report.pdf",
				MediaType: "application/pdf", Format: "pdf",
			}},
		}},
	}

	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, field := range []string{`"previous_response_id"`, `"max_output_tokens"`, `"call_id"`, `"file_id"`, `"filename"`, `"media_type"`, `"format"`} {
		if !strings.Contains(text, field) {
			t.Fatalf("missing %s in %s", field, text)
		}
	}
	for _, field := range []string{`"PreviousResponseID"`, `"CallID"`, `"MediaType"`} {
		if strings.Contains(text, field) {
			t.Fatalf("unstable Go field %s leaked in %s", field, text)
		}
	}

	var decoded Request
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	content := decoded.Items[0].Content[0]
	if decoded.Items[0].CallID != "call_1" || content.Filename != "report.pdf" || content.MediaType != "application/pdf" || content.Format != "pdf" {
		t.Fatalf("round trip lost fields: %#v", decoded)
	}
}

func TestCanonicalEventJSONPreservesTerminalErrorAndUsage(t *testing.T) {
	event := Event{
		Type:           EventFailed,
		SequenceNumber: 7,
		Usage:          &Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5},
		Error:          &Error{Status: 429, Type: "rate_limit_error", Code: "limited", Message: "slow down", Retryable: true},
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Event
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Type != EventFailed || decoded.SequenceNumber != 7 || decoded.Usage.TotalTokens != 5 || decoded.Error.Status != 429 || !decoded.Error.Retryable {
		t.Fatalf("round trip lost event data: %#v", decoded)
	}
}

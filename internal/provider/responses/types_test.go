package responses

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRequestPreservesUnknownNativeFields(t *testing.T) {
	var request Request
	raw := []byte(`{"model":"m","input":"hello","future_control":{"enabled":true}}`)
	if err := json.Unmarshal(raw, &request); err != nil {
		t.Fatal(err)
	}
	if _, ok := request.ExtraFields["future_control"]; !ok {
		t.Fatalf("unknown request field was not captured: %#v", request.ExtraFields)
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"future_control":{"enabled":true}`) {
		t.Fatalf("unknown request field was not preserved: %s", encoded)
	}
}

func TestResponseAndUsagePreserveVolcengineExtensions(t *testing.T) {
	var response Response
	raw := []byte(`{
		"id":"provider_id","object":"response","status":"completed","model":"vendor","output":[],
		"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3,"tool_usage":{"web_search":1},"future_usage":4},
		"service_status":{"model_fallback":{"from":"a","to":"b"}},"future_response":{"enabled":true}
	}`)
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	response.ID = "resp_public"
	response.Model = "public"
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, expected := range []string{
		`"id":"resp_public"`, `"model":"public"`, `"service_status"`, `"future_response"`, `"tool_usage"`, `"future_usage":4`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("response extension %s was not preserved: %s", expected, text)
		}
	}
	if strings.Contains(text, `"id":"provider_id"`) {
		t.Fatalf("unknown fields overwrote a normalized field: %s", text)
	}
}

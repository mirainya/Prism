package provider

import (
	"strings"
	"testing"
)

func TestParseSubmitResponseExtractsB64Data(t *testing.T) {
	parser := NewDefaultParser()
	body := []byte(`{
		"created": 1783320587,
		"data": [
			{
				"b64_json": "iVBORw0KGgoAAAANSUhEUgAAAAEAAAAB",
				"revised_prompt": "A detailed magical girl illustration"
			}
		]
	}`)

	result, err := parser.ParseSubmitResponse(body, &ResponseMapping{
		OutputB64:     "data.0.b64_json",
		OutputURL:     "data.0.url",
		RevisedPrompt: "data.0.revised_prompt",
	})
	if err != nil {
		t.Fatalf("ParseSubmitResponse() error = %v", err)
	}
	if len(result.B64Data) != 1 || result.B64Data[0] != "iVBORw0KGgoAAAANSUhEUgAAAAEAAAAB" {
		t.Fatalf("B64Data = %#v", result.B64Data)
	}
	if len(result.URLs) != 0 {
		t.Fatalf("URLs = %#v, want empty", result.URLs)
	}
	if result.RevisedPrompt != "A detailed magical girl illustration" {
		t.Fatalf("RevisedPrompt = %q", result.RevisedPrompt)
	}
}

func TestParseResponseMappingSupportsB64Aliases(t *testing.T) {
	mapping, err := ParseResponseMapping([]byte(`{
		"field_mapping": {
			"b64_json": "data.0.b64_json"
		}
	}`))
	if err != nil {
		t.Fatalf("ParseResponseMapping() error = %v", err)
	}
	if mapping.OutputB64 != "data.0.b64_json" {
		t.Fatalf("OutputB64 = %q", mapping.OutputB64)
	}

	mapping, err = ParseResponseMapping([]byte(`{
		"field_mapping": {
			"b64": "data.0.b64_json"
		}
	}`))
	if err != nil {
		t.Fatalf("ParseResponseMapping() error = %v", err)
	}
	if mapping.OutputB64 != "data.0.b64_json" {
		t.Fatalf("OutputB64 = %q", mapping.OutputB64)
	}
}

func TestParseResponseMappingSupportsRevisedPrompt(t *testing.T) {
	mapping, err := ParseResponseMapping([]byte(`{
		"field_mapping": {
			"revised_prompt": "data.0.revised_prompt"
		}
	}`))
	if err != nil {
		t.Fatalf("ParseResponseMapping() error = %v", err)
	}
	if mapping.RevisedPrompt != "data.0.revised_prompt" {
		t.Fatalf("RevisedPrompt = %q", mapping.RevisedPrompt)
	}
}

func TestParseProgressResponseNormalizesFailedStatus(t *testing.T) {
	parser := NewDefaultParser()
	result, err := parser.ParseProgressResponse([]byte(`{
		"status":"FAILED",
		"error":"API Error: openai returned 451: unsafe image"
	}`), &ResponseMapping{
		Status: "status",
		Error:  "error",
	})
	if err != nil {
		t.Fatalf("ParseProgressResponse() error = %v", err)
	}
	if result.Status != StatusFail {
		t.Fatalf("Status = %q, want %q", result.Status, StatusFail)
	}
	if result.Error != "API Error: openai returned 451: unsafe image" {
		t.Fatalf("Error = %q", result.Error)
	}
}

func TestParseProgressResponseNormalizesMappedFailedStatus(t *testing.T) {
	parser := NewDefaultParser()
	result, err := parser.ParseProgressResponse([]byte(`{"status":"failed"}`), &ResponseMapping{
		Status:        "status",
		StatusMapping: map[string]string{"FAILED": "failed"},
	})
	if err != nil {
		t.Fatalf("ParseProgressResponse() error = %v", err)
	}
	if result.Status != StatusFail {
		t.Fatalf("Status = %q, want %q", result.Status, StatusFail)
	}
}

func TestParseProgressResponseExtractsUnmappedFailureMessage(t *testing.T) {
	parser := NewDefaultParser()
	message := `API Error: openai returned 400: {"error":{"message":"The generated images appear to be unsafe. Try modifying the prompts or the seeds.","type":"invalid_request_error","param":"","code":"ERR-5CCF05E363"}}`
	result, err := parser.ParseProgressResponse([]byte(`{
		"status":"FAILED",
		"data":{"error":"`+strings.ReplaceAll(message, `"`, `\"`)+`"}
	}`), &ResponseMapping{
		Status: "status",
		Error:  "missing.error.path",
	})
	if err != nil {
		t.Fatalf("ParseProgressResponse() error = %v", err)
	}
	if result.Status != StatusFail || result.Error != message {
		t.Fatalf("result = %#v", result)
	}
}

func TestParseProgressResponsePreservesUnknownFailurePayload(t *testing.T) {
	parser := NewDefaultParser()
	body := `{"status":"FAILED","details":{"vendor_failure":"openai returned 451"}}`
	result, err := parser.ParseProgressResponse([]byte(body), &ResponseMapping{Status: "status"})
	if err != nil {
		t.Fatalf("ParseProgressResponse() error = %v", err)
	}
	if result.Error != body {
		t.Fatalf("Error = %q, want raw failed payload", result.Error)
	}
}

package provider

import (
	"strings"
	"testing"
)

func TestParseImageSSEStream(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantB64   string
		wantErr   bool
		errSubstr string
	}{
		{
			name: "partial then completed",
			input: "data: {\"type\":\"image_generation.partial_image\",\"b64_json\":\"cGFydGlhbA==\",\"partial_image_index\":0}\n\n" +
				"data: {\"type\":\"image_generation.completed\",\"b64_json\":\"ZmluYWw=\",\"output_format\":\"png\"}\n\n" +
				"data: [DONE]\n\n",
			wantB64: "ZmluYWw=",
		},
		{
			name: "completed with revised_prompt",
			input: "data: {\"type\":\"image_generation.completed\",\"b64_json\":\"aW1n\",\"revised_prompt\":\"a cute cat\"}\n\n" +
				"data: [DONE]\n\n",
			wantB64: "aW1n",
		},
		{
			name: "multiline data block",
			input: "data: {\"type\":\"image_generation.completed\",\n" +
				"data: \"b64_json\":\"ZmluYWw=\",\"output_format\":\"png\"}\n\n" +
				"data: [DONE]\n\n",
			wantB64: "ZmluYWw=",
		},
		{
			name:      "error event",
			input:     "data: {\"type\":\"error\",\"error\":{\"message\":\"rejected by safety system\",\"code\":\"moderation_blocked\"}}\n\n",
			wantErr:   true,
			errSubstr: "moderation_blocked",
		},
		{
			name: "failed event",
			input: "data: {\"type\":\"response.failed\",\"error\":{\"message\":\"image service unavailable\"}}\n\n" +
				"data: [DONE]\n\n",
			wantErr:   true,
			errSubstr: "unavailable",
		},
		{
			name: "partial only fallback to last partial",
			input: "data: {\"type\":\"image_generation.partial_image\",\"b64_json\":\"cDA=\"}\n\n" +
				"data: {\"type\":\"image_generation.partial_image\",\"b64_json\":\"cDE=\"}\n\n" +
				"data: [DONE]\n\n",
			wantB64: "cDE=",
		},
		{
			name:      "no image output",
			input:     "data: [DONE]\n\n",
			wantErr:   true,
			errSubstr: "did not return image",
		},
		{
			name: "invalid json block skipped",
			input: "data: {not valid json}\n\n" +
				"data: {\"type\":\"image_generation.completed\",\"b64_json\":\"b2s=\"}\n\n",
			wantB64: "b2s=",
		},
		{
			name: "with event lines ignored",
			input: "event: image_generation.completed\n" +
				"data: {\"type\":\"image_generation.completed\",\"b64_json\":\"ZXY=\"}\n\n",
			wantB64: "ZXY=",
		},
		{
			name:    "completed without trailing blank line at EOF",
			input:   "data: {\"type\":\"image_generation.completed\",\"b64_json\":\"ZW9m\"}",
			wantB64: "ZW9m",
		},
		{
			name: "sub2api object result event",
			input: "data: {\"object\":\"image.generation.chunk\",\"index\":1,\"data\":[]}\n\n" +
				"data: {\"object\":\"image.generation.result\",\"data\":[{\"b64_json\":\"c3ViMmFwaQ==\",\"revised_prompt\":\"a red apple\"}]}\n\n" +
				"data: [DONE]\n\n",
			wantB64: "c3ViMmFwaQ==",
		},
		{
			name: "sub2api edit result event",
			input: "data: {\"object\":\"image.edit.result\",\"data\":[{\"b64_json\":\"ZWRpdGVk\"}]}\n\n" +
				"data: [DONE]\n\n",
			wantB64: "ZWRpdGVk",
		},
		{
			name: "sub2api chunk partial fallback",
			input: "data: {\"object\":\"image.generation.chunk\",\"data\":[{\"b64_json\":\"cGFydA==\"}]}\n\n" +
				"data: [DONE]\n\n",
			wantB64: "cGFydA==",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := parseImageSSEStream(strings.NewReader(tt.input), nil)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (result=%+v)", res)
				}
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(res.B64Data) != 1 || res.B64Data[0] != tt.wantB64 {
				t.Fatalf("B64Data = %v, want [%s]", res.B64Data, tt.wantB64)
			}
			if res.Status != StatusSuccess {
				t.Fatalf("Status = %s, want %s", res.Status, StatusSuccess)
			}
		})
	}
}

func TestParseImageSSEStream_RevisedPromptFromPartial(t *testing.T) {
	input := "data: {\"type\":\"image_generation.partial_image\",\"b64_json\":\"cA==\",\"revised_prompt\":\"cat\"}\n\n" +
		"data: [DONE]\n\n"
	res, err := parseImageSSEStream(strings.NewReader(input), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RevisedPrompt != "cat" {
		t.Fatalf("RevisedPrompt = %q, want cat", res.RevisedPrompt)
	}
}

func TestParseImageSSEStreamInfersMissingType(t *testing.T) {
	input := "data: {\"data\":[{\"b64_json\":\"aW1hZ2U=\"}]}\n\n" +
		"data: [DONE]\n\n"
	res, err := parseImageSSEStream(strings.NewReader(input), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.B64Data) != 1 || res.B64Data[0] != "aW1hZ2U=" {
		t.Fatalf("B64Data = %v", res.B64Data)
	}
}

func TestParseImageSSEStreamUsesEventNameForMissingType(t *testing.T) {
	input := "event: image_generation.partial_image\n" +
		"data: {\"b64_json\":\"cA==\",\"partial_image_index\":0}\n\n" +
		"event: image_generation.completed\n" +
		"data: {\"b64_json\":\"aW1hZ2U=\"}\n\n"
	res, err := parseImageSSEStream(strings.NewReader(input), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.B64Data) != 1 || res.B64Data[0] != "aW1hZ2U=" {
		t.Fatalf("B64Data = %v", res.B64Data)
	}
}

// response_format=url 时 sub2api 把图放在 data[0].url，且值是 data URI 而非 http 链接。
// 解析器必须能取到，否则用户选了 url 就永远拿不到图。
func TestParseImageSSEStream_URLOutput(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantB64  string
		wantURLs []string
		wantRP   string
	}{
		{
			name: "result event with data URI url",
			input: "data: {\"object\":\"image.generation.chunk\",\"data\":[]}\n\n" +
				"data: {\"object\":\"image.generation.result\",\"data\":[{\"url\":\"data:image/png;base64,aW1n\"}]}\n\n" +
				"data: [DONE]\n\n",
			wantB64: "aW1n",
		},
		{
			name: "result event with http url",
			input: "data: {\"object\":\"image.generation.result\",\"data\":[{\"url\":\"https://cdn.test/a.png\",\"revised_prompt\":\"an apple\"}]}\n\n" +
				"data: [DONE]\n\n",
			wantURLs: []string{"https://cdn.test/a.png"},
			wantRP:   "an apple",
		},
		{
			name: "completed event with top-level data URI url",
			input: "data: {\"type\":\"image_generation.completed\",\"url\":\"data:image/png;base64,ZG9uZQ==\"}\n\n" +
				"data: [DONE]\n\n",
			wantB64: "ZG9uZQ==",
		},
		{
			name: "completed event with nested url",
			input: "data: {\"type\":\"image_generation.completed\",\"data\":[{\"url\":\"https://cdn.test/nested.png\"}]}\n\n" +
				"data: [DONE]\n\n",
			wantURLs: []string{"https://cdn.test/nested.png"},
		},
		{
			name: "chunk url as partial fallback",
			input: "data: {\"object\":\"image.generation.chunk\",\"data\":[{\"url\":\"data:image/png;base64,cGFydA==\"}]}\n\n" +
				"data: [DONE]\n\n",
			wantB64: "cGFydA==",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := parseImageSSEStream(strings.NewReader(tt.input), nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.Status != StatusSuccess {
				t.Fatalf("Status = %s, want %s", res.Status, StatusSuccess)
			}
			if tt.wantB64 != "" {
				if len(res.B64Data) != 1 || res.B64Data[0] != tt.wantB64 {
					t.Fatalf("B64Data = %v, want [%s]", res.B64Data, tt.wantB64)
				}
				if len(res.URLs) != 0 {
					t.Fatalf("URLs = %v, want empty (data URI must decode into B64Data)", res.URLs)
				}
			}
			if len(tt.wantURLs) > 0 {
				if len(res.URLs) != len(tt.wantURLs) || res.URLs[0] != tt.wantURLs[0] {
					t.Fatalf("URLs = %v, want %v", res.URLs, tt.wantURLs)
				}
				if len(res.B64Data) != 0 {
					t.Fatalf("B64Data = %v, want empty for http url", res.B64Data)
				}
			}
			if tt.wantRP != "" && res.RevisedPrompt != tt.wantRP {
				t.Fatalf("RevisedPrompt = %q, want %q", res.RevisedPrompt, tt.wantRP)
			}
		})
	}
}

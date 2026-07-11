package pipeline

import (
	"encoding/json"
	"testing"

	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/gateway/transport"
)

func TestNormalizeV2ChatEventKeepsOnlyNativeRaw(t *testing.T) {
	foreign := canonical.Event{Type: canonical.EventRaw, RawType: "response.vendor.trace", Raw: json.RawMessage(`{"type":"response.vendor.trace"}`)}
	if normalizeV2ChatEvent(&foreign, "public-model", transport.OpenAIResponses) {
		t.Fatal("foreign Responses raw event was forwarded to Chat")
	}

	native := canonical.Event{Type: canonical.EventRaw, RawType: "openai.chat.chunk", Raw: json.RawMessage(`{"vendor":true}`)}
	if !normalizeV2ChatEvent(&native, "public-model", transport.OpenAIChat) {
		t.Fatal("native Chat raw event was dropped")
	}

	known := canonical.Event{Type: canonical.EventTextDelta, RawType: "gemini.generate_content", Raw: json.RawMessage(`{"vendor":true}`), Response: &canonical.Response{Model: "vendor-model"}}
	if !normalizeV2ChatEvent(&known, "public-model", transport.GoogleGenerateContent) {
		t.Fatal("known converted event was dropped")
	}
	if len(known.Raw) != 0 || known.RawType != "" || known.Response.Model != "public-model" {
		t.Fatalf("known event was not normalized: %#v", known)
	}
}

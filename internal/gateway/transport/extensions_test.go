package transport

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mirainya/Prism/internal/gateway/canonical"
)

func TestUnsupportedRequestExtensionHonorsWirePolicy(t *testing.T) {
	request := canonical.Request{
		Items: []canonical.Item{{
			Type:  "message",
			Extra: map[string]json.RawMessage{"future_item": json.RawMessage(`true`)},
			Content: []canonical.Content{{
				Type:  "input_text",
				Text:  "hello",
				Extra: map[string]json.RawMessage{"future_content": json.RawMessage(`1`)},
			}},
		}},
		Tools: []canonical.Tool{{Type: "function", Name: "lookup", Options: json.RawMessage(`{"cache_control":{"type":"ephemeral"}}`)}},
	}
	policy := ExtensionPolicy{PreserveGenericItemFields: true, PreserveGenericContentFields: true, PreserveToolOptions: true}
	if extension := UnsupportedRequestExtension(request, policy); extension != "" {
		t.Fatalf("lossless policy rejected %s", extension)
	}

	policy.PreserveGenericItemFields = false
	if extension := UnsupportedRequestExtension(request, policy); !strings.Contains(extension, "future_item") {
		t.Fatalf("unsupported extension = %q", extension)
	}
}

func TestUnsupportedRequestExtensionChecksAnthropicRawBlocks(t *testing.T) {
	request := canonical.Request{Items: []canonical.Item{{Type: "message", Content: []canonical.Content{{
		Type: "input_text", Text: "hello", Extra: map[string]json.RawMessage{
			ExtensionAnthropicRawBlock: json.RawMessage(`{"type":"text","text":"hello"}`),
		},
	}}}}}
	if extension := UnsupportedRequestExtension(request, ExtensionPolicy{}); extension != "" {
		t.Fatalf("canonical Anthropic block rejected: %s", extension)
	}
	request.Items[0].Content[0].Extra[ExtensionAnthropicRawBlock] = json.RawMessage(`{"type":"text","text":"hello","cache_control":{"type":"ephemeral"}}`)
	if extension := UnsupportedRequestExtension(request, ExtensionPolicy{}); !strings.Contains(extension, ExtensionAnthropicRawBlock) {
		t.Fatalf("native-only block extension = %q", extension)
	}
}

func TestUnsupportedNamespaceFindsTypedFields(t *testing.T) {
	request := canonical.Request{Items: []canonical.Item{{Type: "function_call", Namespace: "runtime"}}}
	if field := UnsupportedNamespace(request); field != "item 0 namespace" {
		t.Fatalf("item namespace = %q", field)
	}
	request.Items[0].Namespace = ""
	request.Tools = []canonical.Tool{{Type: "function", Namespace: "runtime"}}
	if field := UnsupportedNamespace(request); field != "tool 0 namespace" {
		t.Fatalf("tool namespace = %q", field)
	}
}

func TestUnsupportedProviderCallIDStateFindsTypedField(t *testing.T) {
	request := canonical.Request{Items: []canonical.Item{{Type: "function_call", ProviderCallIDOmitted: true}}}
	if field := UnsupportedProviderCallIDState(request); field != "item 0 provider call-ID state" {
		t.Fatalf("provider call-ID field = %q", field)
	}
}

func TestSupportsLocalResponsesInclude(t *testing.T) {
	if !SupportsLocalResponsesInclude(OperationResponses, []string{ResponsesReasoningProofInclude}) {
		t.Fatal("Responses reasoning proof include was rejected")
	}
	if SupportsLocalResponsesInclude(OperationChat, []string{ResponsesReasoningProofInclude}) || SupportsLocalResponsesInclude(OperationResponses, []string{"future.field"}) {
		t.Fatal("unsupported include was accepted")
	}
}

func TestToolChoiceRawHasExtensionsByDownstreamOperation(t *testing.T) {
	tests := []struct {
		name      string
		operation Operation
		raw       string
		want      bool
	}{
		{name: "chat standard", operation: OperationChat, raw: `{"type":"function","function":{"name":"lookup"}}`},
		{name: "responses standard", operation: OperationResponses, raw: `{"type":"function","name":"lookup"}`},
		{name: "messages standard", operation: OperationMessages, raw: `{"type":"tool","name":"lookup","disable_parallel_tool_use":true}`},
		{name: "chat extension", operation: OperationChat, raw: `{"type":"function","function":{"name":"lookup","future":true}}`, want: true},
		{name: "responses extension", operation: OperationResponses, raw: `{"type":"function","name":"lookup","future":true}`, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			choice := &canonical.ToolChoice{Raw: json.RawMessage(test.raw)}
			if got := ToolChoiceRawHasExtensions(choice, test.operation); got != test.want {
				t.Fatalf("ToolChoiceRawHasExtensions() = %v, want %v", got, test.want)
			}
		})
	}
}

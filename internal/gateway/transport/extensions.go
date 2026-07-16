package transport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mirainya/Prism/internal/gateway/canonical"
)

const (
	ExtensionAnthropicRawBlock     = "anthropic.raw_block"
	ExtensionChatChoiceIndex       = "openai_chat.choice_index"
	ExtensionChatContentMode       = "openai_chat.content_mode"
	ExtensionChatFinishReason      = "openai_chat.finish_reason"
	ExtensionChatReasoningContent  = "openai_chat.reasoning_content"
	ExtensionChatRawContent        = "openai_chat.raw"
	ExtensionChatToolCalls         = "openai_chat.tool_calls"
	ExtensionResponsesAudioOptions = "openai_responses.input_audio_options"
	ResponsesReasoningProofInclude = "reasoning.encrypted_content"
)

// ExtensionPolicy describes which protocol-specific request data a transport
// actually writes to its upstream wire format.
type ExtensionPolicy struct {
	Client                       map[string]bool
	Item                         map[string]bool
	Content                      map[string]bool
	PreserveGenericItemFields    bool
	PreserveGenericContentFields bool
	PreserveAnthropicRawBlocks   bool
	PreserveToolOptions          bool
}

// UnsupportedRequestExtension returns the first request extension that would
// be discarded by a transport. An empty result means the policy is lossless.
func UnsupportedRequestExtension(request canonical.Request, policy ExtensionPolicy) string {
	for _, key := range rawKeys(request.ClientExtensions) {
		raw := request.ClientExtensions[key]
		if HasJSONValue(raw) && (!rawJSONObject(raw) || !policy.Client[key]) {
			return fmt.Sprintf("client extension %q", key)
		}
	}
	for itemIndex, item := range request.Items {
		for _, key := range rawKeys(item.Extra) {
			raw := item.Extra[key]
			if !HasJSONValue(raw) || (json.Valid(raw) && extensionAllowed(key, raw, policy.Item, policy.PreserveGenericItemFields, policy.PreserveAnthropicRawBlocks)) {
				continue
			}
			return fmt.Sprintf("item %d extension %q", itemIndex, key)
		}
		for contentIndex, content := range item.Content {
			for _, key := range rawKeys(content.Extra) {
				raw := content.Extra[key]
				if !HasJSONValue(raw) || (json.Valid(raw) && extensionAllowed(key, raw, policy.Content, policy.PreserveGenericContentFields, policy.PreserveAnthropicRawBlocks)) {
					continue
				}
				return fmt.Sprintf("item %d content %d extension %q", itemIndex, contentIndex, key)
			}
		}
	}
	if !policy.PreserveToolOptions {
		for toolIndex, tool := range request.Tools {
			if HasJSONValue(tool.Options) {
				return fmt.Sprintf("tool %d options", toolIndex)
			}
		}
	} else {
		for toolIndex, tool := range request.Tools {
			if HasJSONValue(tool.Options) && !rawJSONObject(tool.Options) {
				return fmt.Sprintf("tool %d options", toolIndex)
			}
		}
	}
	return ""
}

// UnsupportedNamespace returns the first typed namespace field that a wire
// protocol must explicitly encode or reject.
func UnsupportedNamespace(request canonical.Request) string {
	for index, item := range request.Items {
		if strings.TrimSpace(item.Namespace) != "" {
			return fmt.Sprintf("item %d namespace", index)
		}
	}
	for index, tool := range request.Tools {
		if strings.TrimSpace(tool.Namespace) != "" {
			return fmt.Sprintf("tool %d namespace", index)
		}
	}
	return ""
}

// UnsupportedProviderCallIDState returns the first provider-native omitted-ID
// marker that a non-Google transport must reject instead of silently dropping.
func UnsupportedProviderCallIDState(request canonical.Request) string {
	for index, item := range request.Items {
		if item.ProviderCallIDOmitted {
			return fmt.Sprintf("item %d provider call-ID state", index)
		}
	}
	return ""
}

// SupportsLocalResponsesInclude reports whether include can be fulfilled by
// Prism while converting a Responses request to another upstream protocol.
func SupportsLocalResponsesInclude(operation Operation, include []string) bool {
	if len(include) == 0 {
		return true
	}
	if operation != OperationResponses {
		return false
	}
	for _, value := range include {
		if value != ResponsesReasoningProofInclude {
			return false
		}
	}
	return true
}

// ToolChoiceRawHasExtensions reports whether a downstream native tool_choice
// contains fields that are not represented by canonical.ToolChoice.
func ToolChoiceRawHasExtensions(choice *canonical.ToolChoice, operation Operation) bool {
	if choice == nil || !HasJSONValue(choice.Raw) {
		return false
	}
	var scalar string
	if json.Unmarshal(choice.Raw, &scalar) == nil {
		return false
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(choice.Raw, &object) != nil {
		return true
	}
	switch operation {
	case OperationChat:
		if !rawMapHasOnly(object, "type", "function") {
			return true
		}
		if raw := object["function"]; HasJSONValue(raw) {
			var function map[string]json.RawMessage
			return json.Unmarshal(raw, &function) != nil || !rawMapHasOnly(function, "name")
		}
		return false
	case OperationResponses:
		return !rawMapHasOnly(object, "type", "name")
	case OperationMessages:
		return !rawMapHasOnly(object, "type", "name", "disable_parallel_tool_use")
	default:
		return true
	}
}

// RawObjectHasOnlyFields accepts absent JSON and objects containing only the
// named fields. Non-object JSON is rejected.
func RawObjectHasOnlyFields(raw json.RawMessage, fields ...string) bool {
	if !HasJSONValue(raw) {
		return true
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(raw, &object) == nil && rawMapHasOnly(object, fields...)
}

// HasJSONValue treats null and empty containers as absent extension values.
func HasJSONValue(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || bytes.Equal(trimmed, []byte("{}")) || bytes.Equal(trimmed, []byte("[]")) {
		return false
	}
	return true
}

func extensionAllowed(key string, raw json.RawMessage, allowed map[string]bool, generic, preserveAnthropic bool) bool {
	if allowed[key] {
		return true
	}
	if key == ExtensionAnthropicRawBlock {
		return preserveAnthropic || portableAnthropicBlock(raw)
	}
	return generic && !strings.Contains(key, ".")
}

// portableAnthropicBlock verifies that the canonical item contains every
// meaningful field from a native Anthropic block before another transport
// discards the raw copy.
func portableAnthropicBlock(raw json.RawMessage) bool {
	var block map[string]json.RawMessage
	if json.Unmarshal(raw, &block) != nil {
		return false
	}
	var blockType string
	_ = json.Unmarshal(block["type"], &blockType)
	switch blockType {
	case "text":
		return rawMapHasOnly(block, "type", "text")
	case "image":
		return rawMapHasOnly(block, "type", "source") && portableAnthropicSource(block["source"])
	case "document":
		return rawMapHasOnly(block, "type", "source", "title") && portableAnthropicSource(block["source"])
	case "tool_use", "server_tool_use":
		return rawMapHasOnly(block, "type", "id", "name", "input")
	case "tool_result":
		return rawMapHasOnly(block, "type", "tool_use_id", "content")
	case "thinking":
		return rawMapHasOnly(block, "type", "thinking")
	default:
		return false
	}
}

func portableAnthropicSource(raw json.RawMessage) bool {
	var source map[string]json.RawMessage
	if json.Unmarshal(raw, &source) != nil || !rawMapHasOnly(source, "type", "url", "media_type", "data", "file_id") {
		return false
	}
	var sourceType string
	_ = json.Unmarshal(source["type"], &sourceType)
	switch sourceType {
	case "url":
		return HasJSONValue(source["url"])
	case "file":
		return HasJSONValue(source["file_id"])
	case "base64", "text":
		return HasJSONValue(source["data"])
	default:
		return false
	}
}

func rawMapHasOnly(source map[string]json.RawMessage, fields ...string) bool {
	allowed := make(map[string]bool, len(fields))
	for _, field := range fields {
		allowed[field] = true
	}
	for key, raw := range source {
		if HasJSONValue(raw) && !allowed[key] {
			return false
		}
	}
	return true
}

func rawJSONObject(raw json.RawMessage) bool {
	var object map[string]json.RawMessage
	return json.Unmarshal(raw, &object) == nil && object != nil
}

func rawKeys(source map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(source))
	for key := range source {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

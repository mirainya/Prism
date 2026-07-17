// Package openai_chat maps OpenAI Chat Completions wire objects to canonical.
package openai_chat

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/provider/chat"
)

const (
	extraRequest      = "openai_chat.request_extras"
	extraResponse     = "openai_chat.response_extras"
	extraRaw          = "openai_chat.raw"
	extraContentMode  = "openai_chat.content_mode"
	extraReasoning    = "openai_chat.reasoning_content"
	extraFinishReason = "openai_chat.finish_reason"
	extraChoiceIndex  = "openai_chat.choice_index"
	extraLogprobs     = "openai_chat.logprobs"
	extraToolCalls    = "openai_chat.tool_calls"
	extraMessageName  = "openai_chat.message_name"
	extraRefusal      = "openai_chat.refusal"
	extraAnnotations  = "openai_chat.annotations"
	extraAudio        = "openai_chat.audio"
	extraSystemFP     = "openai_chat.system_fingerprint"
	extraServiceTier  = "openai_chat.service_tier"
)

// DecodeRequest maps a parsed OpenAI Chat request. Its unmodeled OpenAI fields
// are retained in ClientExtensions and are never treated as provider options.
func DecodeRequest(source chat.ChatRequest) (canonical.Request, error) {
	items, err := decodeMessages(source.Messages)
	if err != nil {
		return canonical.Request{}, err
	}
	tools := make([]canonical.Tool, 0, len(source.Tools))
	for _, tool := range source.Tools {
		if tool.Type != "function" {
			return canonical.Request{}, fmt.Errorf("unsupported OpenAI Chat tool type %q", tool.Type)
		}
		tools = append(tools, canonical.Tool{
			Type: "function", Name: tool.Function.Name, Description: tool.Function.Description,
			InputSchema: append(json.RawMessage(nil), tool.Function.Parameters...), Strict: cloneBool(tool.Function.Strict),
		})
	}
	choice, err := decodeToolChoice(source.ToolChoice)
	if err != nil {
		return canonical.Request{}, err
	}
	format, err := decodeFormat(source.ResponseFormat)
	if err != nil {
		return canonical.Request{}, err
	}
	extras, err := requestExtras(source)
	if err != nil {
		return canonical.Request{}, err
	}
	maxOutput := cloneInt(source.MaxCompletionTokens)
	if maxOutput == nil && source.MaxTokens > 0 {
		value := source.MaxTokens
		maxOutput = &value
	}
	return canonical.Request{
		Endpoint: canonical.EndpointOpenAIChat, Model: source.Model, Items: items,
		Tools: tools, ToolChoice: choice, ParallelToolCalls: cloneBool(source.ParallelToolCalls), ResponseFormat: format,
		Stream: source.Stream, Store: cloneBool(source.Store), MaxOutputTokens: maxOutput,
		Temperature: cloneFloat(source.Temperature), TopP: cloneFloat(source.TopP), Stop: append([]string(nil), source.Stop...),
		User: source.User, Metadata: cloneStrings(source.Metadata), Modalities: append([]string(nil), source.Modalities...),
		ClientExtensions: extras,
	}, nil
}

// EncodeResponseJSON renders a canonical response as an OpenAI Chat response.
// ProviderExtensions are deliberately not emitted on this client-facing wire.
func EncodeResponseJSON(source canonical.Response) ([]byte, error) {
	choices, err := encodeChoices(source.Output, source.FinishReason)
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"id": source.ID, "object": "chat.completion", "created": source.CreatedAt,
		"model": source.Model, "choices": choices,
	}
	if source.Usage != nil {
		body["usage"] = encodeUsage(source.Usage)
	}
	if extras, err := extensionObject(source.ProviderExtensions, extraResponse); err != nil {
		return nil, err
	} else {
		for key, value := range extras {
			body[key] = value
		}
	}
	if value := extensionString(source.ProviderExtensions, extraSystemFP); value != "" {
		body["system_fingerprint"] = value
	}
	if value := extensionString(source.ProviderExtensions, extraServiceTier); value != "" {
		body["service_tier"] = value
	}
	return json.Marshal(body)
}

// EncodeSSEFrame renders a canonical event as one OpenAI Chat SSE frame. A raw
// event is copied verbatim so native extension payloads survive conversion.
func EncodeSSEFrame(event canonical.Event) ([]byte, error) {
	payload, err := EncodeStreamChunk(event)
	if err != nil {
		return nil, err
	}
	frame := append(append([]byte("data: "), payload...), []byte("\n\n")...)
	if isChatTerminal(event) {
		frame = append(frame, []byte("data: [DONE]\n\n")...)
	}
	return frame, nil
}

// EncodeStreamChunk renders a canonical event as an OpenAI Chat chunk JSON.
func EncodeStreamChunk(event canonical.Event) ([]byte, error) {
	if event.Type == canonical.EventRaw {
		if len(event.Raw) == 0 {
			return nil, errors.New("event raw payload is required")
		}
		if !json.Valid(event.Raw) {
			return nil, errors.New("event raw payload is not valid JSON")
		}
		return append([]byte(nil), event.Raw...), nil
	}
	if event.Type == canonical.EventError || (event.Type == canonical.EventFailed && event.Error != nil) {
		return json.Marshal(map[string]any{"error": encodeError(event.Error)})
	}
	body := map[string]any{"object": "chat.completion.chunk"}
	if event.ProviderResponseID != "" {
		body["id"] = event.ProviderResponseID
	}
	if event.Response != nil {
		if event.Response.ID != "" {
			body["id"] = event.Response.ID
		}
		if event.Response.CreatedAt != 0 {
			body["created"] = event.Response.CreatedAt
		}
		if event.Response.Model != "" {
			body["model"] = event.Response.Model
		}
		if value := extensionString(event.Response.ProviderExtensions, extraSystemFP); value != "" {
			body["system_fingerprint"] = value
		}
		if value := extensionString(event.Response.ProviderExtensions, extraServiceTier); value != "" {
			body["service_tier"] = value
		}
	}
	if event.Type == canonical.EventUsage {
		body["choices"] = []any{}
		body["usage"] = encodeUsage(event.Usage)
		return json.Marshal(body)
	}
	choice := map[string]any{"index": event.ChoiceIndex, "delta": map[string]any{}, "finish_reason": nil}
	delta := choice["delta"].(map[string]any)
	switch event.Type {
	case canonical.EventResponseCreated, canonical.EventOutputItemAdded:
		if event.Item == nil || event.Item.Type == "message" {
			delta["role"] = "assistant"
		}
	case canonical.EventTextDelta:
		delta["content"] = event.Delta
	case canonical.EventReasoningDelta:
		delta["reasoning_content"] = event.Delta
	case canonical.EventToolArgumentsDelta:
		delta["tool_calls"] = []map[string]any{encodeStreamToolCall(event, event.Delta, false)}
	}
	if event.Item != nil {
		if event.Item.Role != "" {
			delta["role"] = event.Item.Role
		}
		if reasoning := extensionString(event.Item.Extra, extraReasoning); reasoning != "" {
			delta["reasoning_content"] = reasoning
		}
		if raw := event.Item.Extra[extraToolCalls]; len(raw) > 0 {
			var calls any
			if err := json.Unmarshal(raw, &calls); err != nil {
				return nil, err
			}
			delta["tool_calls"] = calls
		}
		if event.Type == canonical.EventOutputItemAdded && event.Item.Type == "function_call" {
			delta["tool_calls"] = []map[string]any{encodeStreamToolCall(event, "", true)}
		}
	}
	if event.Type == canonical.EventCompleted || event.Type == canonical.EventFailed || event.Type == canonical.EventIncomplete {
		finish := extensionString(itemExtra(event.Item), extraFinishReason)
		if finish == "" {
			finish = sourceFinishReason(event)
		}
		choice["finish_reason"] = chatFinishReason(finish)
	}
	body["choices"] = []map[string]any{choice}
	if event.Usage != nil {
		body["usage"] = encodeUsage(event.Usage)
	}
	return json.Marshal(body)
}

// DecodeSSEFrame is supplied for transport tests and simple streaming codecs.
// done is true only for the OpenAI [DONE] marker.
func DecodeSSEFrame(frame []byte) (canonical.Event, bool, error) {
	trimmed := bytes.TrimSpace(frame)
	if !bytes.HasPrefix(trimmed, []byte("data:")) {
		return canonical.Event{}, false, errors.New("OpenAI Chat SSE frame must start with data:")
	}
	payload := bytes.TrimSpace(bytes.TrimPrefix(trimmed, []byte("data:")))
	if bytes.Equal(payload, []byte("[DONE]")) {
		return canonical.Event{Type: canonical.EventCompleted}, true, nil
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(payload, &root); err != nil {
		return canonical.Event{}, false, err
	}
	event := canonical.Event{Raw: append(json.RawMessage(nil), payload...), ProviderResponseID: rawString(root["id"])}
	if raw, ok := root["error"]; ok {
		errValue, err := decodeError(raw)
		if err != nil {
			return canonical.Event{}, false, err
		}
		event.Type, event.Error = canonical.EventError, errValue
		return event, false, nil
	}
	if raw, ok := root["usage"]; ok {
		var usage chat.ChatUsage
		if err := json.Unmarshal(raw, &usage); err != nil {
			return canonical.Event{}, false, err
		}
		event.Usage = decodeUsage(&usage)
	}
	response := &canonical.Response{
		ID: event.ProviderResponseID, ProviderResponseID: event.ProviderResponseID,
		Model: rawString(root["model"]), CreatedAt: rawInt64(root["created"]),
		ProviderExtensions: map[string]json.RawMessage{},
	}
	if raw := root["system_fingerprint"]; len(raw) > 0 {
		response.ProviderExtensions[extraSystemFP] = cloneRaw(raw)
	}
	if raw := root["service_tier"]; len(raw) > 0 {
		response.ProviderExtensions[extraServiceTier] = cloneRaw(raw)
	}
	if response.ID != "" || response.Model != "" || response.CreatedAt != 0 || len(response.ProviderExtensions) > 0 {
		event.Response = response
	}
	var choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role             string  `json:"role"`
			Content          string  `json:"content"`
			ReasoningContent string  `json:"reasoning_content"`
			Refusal          *string `json:"refusal"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	}
	if raw, ok := root["choices"]; ok {
		if err := json.Unmarshal(raw, &choices); err != nil {
			return canonical.Event{}, false, err
		}
	}
	if len(choices) == 0 {
		event.Type = canonical.EventUsage
		return event, false, nil
	}
	choice := choices[0]
	event.ChoiceIndex = choice.Index
	switch {
	case len(choice.Delta.ToolCalls) > 0:
		call := choice.Delta.ToolCalls[0]
		event.Type = canonical.EventToolArgumentsDelta
		if call.Function.Arguments == "" {
			event.Type = canonical.EventOutputItemAdded
		}
		event.ToolIndex, event.Delta = call.Index, call.Function.Arguments
		event.Item = &canonical.Item{ID: call.ID, Type: "function_call", Role: canonical.RoleAssistant, CallID: call.ID, Name: call.Function.Name}
	case choice.Delta.ReasoningContent != "":
		event.Type, event.Delta = canonical.EventReasoningDelta, choice.Delta.ReasoningContent
		event.Item = &canonical.Item{Type: "reasoning", Role: canonical.RoleAssistant}
	case choice.Delta.Content != "":
		event.Type, event.Delta = canonical.EventTextDelta, choice.Delta.Content
	case choice.Delta.Refusal != nil:
		event.Type, event.Delta = canonical.EventTextDelta, *choice.Delta.Refusal
		event.RawType = "response.refusal.delta"
	case choice.Delta.Role != "":
		event.Type = canonical.EventOutputItemAdded
		event.Item = &canonical.Item{Type: "message", Role: canonical.Role(choice.Delta.Role)}
	default:
		event.Type = canonical.EventTextDelta
	}
	if choice.FinishReason != "" {
		event.Type = chatTerminalEvent(choice.FinishReason)
		if event.Item == nil {
			event.Item = &canonical.Item{Extra: map[string]json.RawMessage{}}
		} else if event.Item.Extra == nil {
			event.Item.Extra = map[string]json.RawMessage{}
		}
		event.Item.Extra[extraFinishReason] = mustRaw(choice.FinishReason)
		if event.Response != nil {
			event.Response.FinishReason = choice.FinishReason
			event.Response.Status = chatResponseStatus(event.Type)
			event.Response.Usage = event.Usage
		}
	}
	return event, false, nil
}

func decodeMessages(messages []chat.ChatMessage) ([]canonical.Item, error) {
	// 一条 assistant Chat message 可能同时包含正文和多个 tool_call，canonical 中需拆成有序 Item。
	items := make([]canonical.Item, 0, len(messages))
	for _, message := range messages {
		if message.Role == "tool" {
			items = append(items, canonical.Item{
				Type: "function_call_output", Role: canonical.RoleTool,
				CallID: message.ToolCallID, Output: mustRaw(message.Content),
			})
			continue
		}
		content, mode, err := decodeContent(message.Content)
		if err != nil {
			return nil, err
		}
		item := canonical.Item{Type: "message", Role: canonical.Role(message.Role), Content: content, Extra: map[string]json.RawMessage{extraContentMode: mustRaw(mode)}}
		if message.Name != "" {
			item.Extra[extraMessageName] = mustRaw(message.Name)
		}
		if message.ReasoningContent != "" {
			item.Extra[extraReasoning] = mustRaw(message.ReasoningContent)
		}
		if message.Refusal != nil {
			item.Extra[extraRefusal] = mustRaw(*message.Refusal)
		}
		if len(message.Annotations) > 0 {
			item.Extra[extraAnnotations] = cloneRaw(message.Annotations)
		}
		if len(message.Audio) > 0 {
			item.Extra[extraAudio] = cloneRaw(message.Audio)
		}
		if message.Content != nil || len(message.ToolCalls) == 0 || len(item.Extra) > 1 {
			items = append(items, item)
		}
		for _, call := range message.ToolCalls {
			if call.Type != "" && call.Type != "function" {
				return nil, fmt.Errorf("unsupported OpenAI Chat tool call type %q", call.Type)
			}
			items = append(items, canonical.Item{
				ID: call.ID, Type: "function_call", Role: canonical.RoleAssistant,
				CallID: call.ID, Name: call.Function.Name, Arguments: json.RawMessage(call.Function.Arguments),
			})
		}
	}
	return items, nil
}

func decodeContent(value any) ([]canonical.Content, string, error) {
	// 返回原始形态用于往返编码：相同文本在 Chat 中的 string、array 与 null 语义并不完全等价。
	switch current := value.(type) {
	case nil:
		return nil, "null", nil
	case string:
		return []canonical.Content{{Type: "input_text", Text: current}}, "string", nil
	case []any:
		content := make([]canonical.Content, 0, len(current))
		for _, raw := range current {
			part, ok := raw.(map[string]any)
			if !ok {
				return nil, "", errors.New("OpenAI Chat content parts must be objects")
			}
			content = append(content, decodeContentPart(part))
		}
		return content, "array", nil
	default:
		return nil, "", fmt.Errorf("unsupported OpenAI Chat content %T", value)
	}
}

func decodeContentPart(part map[string]any) canonical.Content {
	switch part["type"] {
	case "text":
		return canonical.Content{Type: "input_text", Text: stringValue(part["text"])}
	case "image_url":
		image, _ := part["image_url"].(map[string]any)
		return canonical.Content{Type: "input_image", URL: stringValue(image["url"]), Detail: stringValue(image["detail"])}
	case "file":
		file, _ := part["file"].(map[string]any)
		return canonical.Content{Type: "input_file", FileID: stringValue(file["file_id"]), Data: stringValue(file["file_data"]), URL: stringValue(file["url"]), Filename: stringValue(file["filename"]), MediaType: stringValue(file["content_type"])}
	case "file_url":
		file, _ := part["file_url"].(map[string]any)
		return canonical.Content{Type: "input_file", URL: stringValue(file["url"]), Filename: stringValue(file["filename"]), MediaType: stringValue(file["content_type"])}
	case "input_audio":
		audio, _ := part["input_audio"].(map[string]any)
		return canonical.Content{Type: "input_audio", Data: stringValue(audio["data"]), Format: stringValue(audio["format"])}
	default:
		return canonical.Content{Type: stringValue(part["type"]), Extra: map[string]json.RawMessage{extraRaw: mustRaw(part)}}
	}
}

func decodeToolChoice(value any) (*canonical.ToolChoice, error) {
	if value == nil {
		return nil, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	choice := &canonical.ToolChoice{Raw: raw}
	if mode, ok := value.(string); ok {
		choice.Mode = mode
		return choice, nil
	}
	if object, ok := value.(map[string]any); ok {
		choice.Type, _ = object["type"].(string)
		if function, ok := object["function"].(map[string]any); ok {
			choice.Name, _ = function["name"].(string)
		}
	}
	return choice, nil
}

func decodeFormat(source *chat.ResponseFormat) (*canonical.ResponseFormat, error) {
	if source == nil {
		return nil, nil
	}
	result := &canonical.ResponseFormat{Type: source.Type}
	if len(source.JSONSchema) == 0 {
		return result, nil
	}
	var raw struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Schema      json.RawMessage `json:"schema"`
		Strict      *bool           `json:"strict"`
	}
	if err := json.Unmarshal(source.JSONSchema, &raw); err != nil {
		return nil, err
	}
	result.Name, result.Description, result.Schema, result.Strict = raw.Name, raw.Description, append(json.RawMessage(nil), raw.Schema...), cloneBool(raw.Strict)
	return result, nil
}

func encodeChoices(items []canonical.Item, defaultFinish string) ([]map[string]any, error) {
	choices := map[int]map[string]any{}
	order := []int{}
	defaultFinish = chatFinishReason(defaultFinish)
	for _, item := range items {
		if item.Type != "message" && item.Type != "function_call" && item.Type != "reasoning" {
			continue
		}
		index := itemChoiceIndex(item)
		choice, ok := choices[index]
		if !ok {
			choice = map[string]any{"index": index, "message": map[string]any{"role": "assistant", "content": ""}, "finish_reason": defaultFinish}
			choices[index] = choice
			order = append(order, index)
		}
		message := choice["message"].(map[string]any)
		switch item.Type {
		case "message":
			content, err := encodeContent(item.Content, extensionString(item.Extra, extraContentMode))
			if err != nil {
				return nil, err
			}
			message["role"] = item.Role
			message["content"] = content
			if reasoning := extensionString(item.Extra, extraReasoning); reasoning != "" {
				message["reasoning_content"] = reasoning
			}
			if name := extensionString(item.Extra, extraMessageName); name != "" {
				message["name"] = name
			}
			if refusal := extensionString(item.Extra, extraRefusal); refusal != "" {
				message["refusal"] = refusal
			}
			for field, key := range map[string]string{"annotations": extraAnnotations, "audio": extraAudio, "tool_calls": extraToolCalls} {
				if raw := item.Extra[key]; len(raw) > 0 {
					var value any
					if err := json.Unmarshal(raw, &value); err != nil {
						return nil, err
					}
					message[field] = value
				}
			}
			if finish := extensionString(item.Extra, extraFinishReason); finish != "" {
				choice["finish_reason"] = chatFinishReason(finish)
			}
			if raw := item.Extra[extraLogprobs]; len(raw) > 0 {
				var value any
				if err := json.Unmarshal(raw, &value); err != nil {
					return nil, err
				}
				choice["logprobs"] = value
			}
		case "reasoning":
			var text strings.Builder
			for _, part := range item.Content {
				text.WriteString(part.Text)
			}
			if text.Len() > 0 {
				current, _ := message["reasoning_content"].(string)
				message["reasoning_content"] = current + text.String()
			}
		case "function_call":
			calls, _ := message["tool_calls"].([]map[string]any)
			callID := item.CallID
			if callID == "" {
				callID = item.ID
			}
			calls = append(calls, map[string]any{"id": callID, "type": "function", "function": map[string]any{"name": item.Name, "arguments": jsonStringValue(item.Arguments)}})
			message["tool_calls"] = calls
			message["content"] = nil
			if choice["finish_reason"] == "" || choice["finish_reason"] == "stop" {
				choice["finish_reason"] = "tool_calls"
			}
		}
	}
	result := make([]map[string]any, 0, len(order))
	for _, index := range order {
		result = append(result, choices[index])
	}
	return result, nil
}

func encodeContent(parts []canonical.Content, mode string) (any, error) {
	if mode == "null" {
		return nil, nil
	}
	if mode != "array" {
		var text strings.Builder
		allText := len(parts) > 0
		for _, part := range parts {
			if part.Type != "" && part.Type != "input_text" && part.Type != "output_text" && part.Type != "text" {
				allText = false
				break
			}
			text.WriteString(part.Text)
		}
		if allText || mode == "string" {
			return text.String(), nil
		}
	}
	result := make([]any, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "input_text", "output_text", "text":
			result = append(result, map[string]any{"type": "text", "text": part.Text})
		case "input_image", "image_url":
			url := part.URL
			if url == "" && part.Data != "" {
				url = dataURL(part.MediaType, part.Data)
			}
			result = append(result, map[string]any{"type": "image_url", "image_url": compactMap(map[string]any{"url": url, "detail": part.Detail})})
		case "input_file", "file":
			if part.URL != "" {
				result = append(result, map[string]any{"type": "file_url", "file_url": compactMap(map[string]any{"url": part.URL, "filename": part.Filename, "content_type": part.MediaType})})
			} else {
				result = append(result, map[string]any{"type": "file", "file": compactMap(map[string]any{"file_id": part.FileID, "file_data": part.Data, "filename": part.Filename, "content_type": part.MediaType})})
			}
		case "input_audio", "audio":
			result = append(result, map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": part.Data, "format": part.Format}})
		default:
			raw := part.Extra[extraRaw]
			if len(raw) == 0 || !json.Valid(raw) {
				return nil, fmt.Errorf("content type %q has no OpenAI Chat representation", part.Type)
			}
			var value any
			if err := json.Unmarshal(raw, &value); err != nil {
				return nil, err
			}
			result = append(result, value)
		}
	}
	return result, nil
}

func requestExtras(source chat.ChatRequest) (map[string]json.RawMessage, error) {
	encoded, err := json.Marshal(source)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return nil, err
	}
	for _, key := range []string{
		"model", "messages", "temperature", "max_tokens", "max_completion_tokens", "top_p", "stop", "stream",
		"stream_options", "tools", "tool_choice", "parallel_tool_calls", "response_format", "user", "modalities", "store", "metadata",
	} {
		delete(fields, key)
	}
	if len(fields) == 0 {
		return nil, nil
	}
	return map[string]json.RawMessage{extraRequest: mustRaw(fields)}, nil
}

func extensionObject(extras map[string]json.RawMessage, key string) (map[string]json.RawMessage, error) {
	result := map[string]json.RawMessage{}
	if len(extras) == 0 || len(extras[key]) == 0 {
		return result, nil
	}
	if err := json.Unmarshal(extras[key], &result); err != nil {
		return nil, err
	}
	return result, nil
}
func extensionString(extras map[string]json.RawMessage, key string) string {
	var result string
	_ = json.Unmarshal(extras[key], &result)
	return result
}
func itemChoiceIndex(item canonical.Item) int {
	value, err := strconv.Atoi(strings.Trim(string(item.Extra[extraChoiceIndex]), `"`))
	if err != nil {
		return 0
	}
	return value
}
func sourceFinishReason(event canonical.Event) string {
	if event.Response != nil && event.Response.FinishReason != "" {
		return chatFinishReason(event.Response.FinishReason)
	}
	if event.Response != nil {
		for _, item := range event.Response.Output {
			if item.Type == "function_call" {
				return "tool_calls"
			}
		}
	}
	if event.Type == canonical.EventIncomplete {
		return "length"
	}
	if event.Type == canonical.EventFailed {
		return "content_filter"
	}
	return "stop"
}
func itemExtra(item *canonical.Item) map[string]json.RawMessage {
	if item == nil {
		return nil
	}
	return item.Extra
}
func encodeUsage(source *canonical.Usage) map[string]any {
	if source == nil {
		return nil
	}
	result := map[string]any{"prompt_tokens": source.InputTokens, "completion_tokens": source.OutputTokens, "total_tokens": source.TotalTokens}
	for key, raw := range source.Extra {
		if key == "prompt_tokens" || key == "completion_tokens" || key == "total_tokens" || !json.Valid(raw) {
			continue
		}
		var value any
		if json.Unmarshal(raw, &value) == nil {
			result[key] = value
		}
	}
	if source.CachedInputTokens != 0 {
		details, _ := result["prompt_tokens_details"].(map[string]any)
		if details == nil {
			details = map[string]any{}
		}
		details["cached_tokens"] = source.CachedInputTokens
		result["prompt_tokens_details"] = details
	}
	if source.ReasoningOutputTokens != 0 {
		details, _ := result["completion_tokens_details"].(map[string]any)
		if details == nil {
			details = map[string]any{}
		}
		details["reasoning_tokens"] = source.ReasoningOutputTokens
		result["completion_tokens_details"] = details
	}
	return result
}
func decodeUsage(source *chat.ChatUsage) *canonical.Usage {
	if source == nil {
		return nil
	}
	result := &canonical.Usage{InputTokens: source.PromptTokens, OutputTokens: source.CompletionTokens, TotalTokens: source.TotalTokens, Extra: map[string]json.RawMessage{}}
	if len(source.PromptTokensDetails) > 0 {
		result.Extra["prompt_tokens_details"] = cloneRaw(source.PromptTokensDetails)
		var details struct {
			CachedTokens int `json:"cached_tokens"`
		}
		_ = json.Unmarshal(source.PromptTokensDetails, &details)
		result.CachedInputTokens = details.CachedTokens
	}
	if len(source.CompletionTokensDetails) > 0 {
		result.Extra["completion_tokens_details"] = cloneRaw(source.CompletionTokensDetails)
		var details struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		}
		_ = json.Unmarshal(source.CompletionTokensDetails, &details)
		result.ReasoningOutputTokens = details.ReasoningTokens
	}
	if len(result.Extra) == 0 {
		result.Extra = nil
	}
	return result
}
func decodeError(raw json.RawMessage) (*canonical.Error, error) {
	var source struct {
		Message, Type string
		Code          any `json:"code"`
		Param         any `json:"param"`
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		return nil, err
	}
	code := ""
	if source.Code != nil {
		code = fmt.Sprint(source.Code)
	}
	return &canonical.Error{Message: source.Message, Type: source.Type, Code: code, Param: source.Param, Raw: append(json.RawMessage(nil), raw...)}, nil
}
func encodeError(source *canonical.Error) map[string]any {
	if source == nil {
		return map[string]any{"message": "upstream error"}
	}
	result := map[string]any{"message": source.Message}
	if source.Type != "" {
		result["type"] = source.Type
	}
	if source.Code != "" {
		result["code"] = source.Code
	}
	if source.Param != nil {
		result["param"] = source.Param
	}
	return result
}
func cloneBool(source *bool) *bool {
	if source == nil {
		return nil
	}
	value := *source
	return &value
}
func cloneInt(source *int) *int {
	if source == nil {
		return nil
	}
	value := *source
	return &value
}
func cloneFloat(source *float64) *float64 {
	if source == nil {
		return nil
	}
	value := *source
	return &value
}
func cloneStrings(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
func isChatTerminal(event canonical.Event) bool {
	switch event.Type {
	case canonical.EventCompleted, canonical.EventIncomplete:
		return true
	case canonical.EventFailed:
		return event.Error == nil
	default:
		return false
	}
}
func encodeStreamToolCall(event canonical.Event, arguments string, includeIdentity bool) map[string]any {
	function := map[string]any{"arguments": arguments}
	call := map[string]any{"index": event.ToolIndex, "function": function}
	if includeIdentity && event.Item != nil {
		callID := event.Item.CallID
		if callID == "" {
			callID = event.Item.ID
		}
		if callID != "" {
			call["id"] = callID
			call["type"] = "function"
		}
		if event.Item.Name != "" {
			function["name"] = event.Item.Name
		}
	}
	return call
}
func chatFinishReason(source string) string {
	switch source {
	case "", "stop", "end_turn", "stop_sequence":
		return "stop"
	case "tool_use", "tool_calls":
		return "tool_calls"
	case "max_tokens", "model_context_window_exceeded", "pause_turn", "incomplete":
		return "length"
	case "refusal", "failed":
		return "content_filter"
	default:
		return source
	}
}
func chatTerminalEvent(finishReason string) canonical.EventType {
	switch chatFinishReason(finishReason) {
	case "length":
		return canonical.EventIncomplete
	case "content_filter":
		return canonical.EventFailed
	default:
		return canonical.EventCompleted
	}
}
func chatResponseStatus(eventType canonical.EventType) string {
	switch eventType {
	case canonical.EventIncomplete:
		return "incomplete"
	case canonical.EventFailed:
		return "failed"
	default:
		return "completed"
	}
}
func compactMap(source map[string]any) map[string]any {
	for key, value := range source {
		if value == nil || value == "" {
			delete(source, key)
		}
	}
	return source
}
func jsonStringValue(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	return string(raw)
}
func rawString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}
func rawInt64(raw json.RawMessage) int64 {
	var value int64
	_ = json.Unmarshal(raw, &value)
	return value
}
func cloneRaw(source json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), source...)
}
func dataURL(mediaType, data string) string {
	if strings.HasPrefix(data, "data:") {
		return data
	}
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	return "data:" + mediaType + ";base64," + data
}
func stringValue(source any) string      { value, _ := source.(string); return value }
func mustRaw(source any) json.RawMessage { value, _ := json.Marshal(source); return value }

// Package openai_responses maps OpenAI Responses wire objects to canonical.
package openai_responses

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mirainya/Prism/internal/gateway/canonical"
	protocol "github.com/mirainya/Prism/internal/provider/responses"
)

const (
	extraRequest           = "openai_responses.request_extras"
	extraInputAudioOptions = "openai_responses.input_audio_options"
)

// DecodeRequest maps a parsed OpenAI Responses request into the Gateway V2
// contract. Unknown request fields remain available to native transports.
func DecodeRequest(source protocol.Request) (canonical.Request, error) {
	items, err := decodeInput(source.Input)
	if err != nil {
		return canonical.Request{}, err
	}
	tools, err := decodeTools(source.Tools)
	if err != nil {
		return canonical.Request{}, err
	}
	choice, err := decodeToolChoice(source.ToolChoice)
	if err != nil {
		return canonical.Request{}, err
	}
	format, err := decodeFormat(source.Text)
	if err != nil {
		return canonical.Request{}, err
	}
	reasoning := decodeReasoning(source.Reasoning)
	clientExtensions, err := requestExtras(source)
	if err != nil {
		return canonical.Request{}, err
	}
	volcUnknown := cloneRawMap(source.ExtraFields)
	return canonical.Request{
		Endpoint: canonical.EndpointOpenAIResponses, Model: source.Model, Items: items,
		Instructions: source.Instructions, Tools: tools, ToolChoice: choice,
		ParallelToolCalls: cloneBool(source.ParallelToolCalls), ResponseFormat: format, Reasoning: reasoning,
		Stream: source.Stream, Store: cloneBool(source.Store), Background: source.Background,
		PreviousResponseID: source.PreviousResponseID, MaxOutputTokens: intPointer(source.MaxOutputTokens),
		Temperature: cloneFloat(source.Temperature), TopP: cloneFloat(source.TopP), User: source.User,
		Metadata: cloneStrings(source.Metadata), Include: append([]string(nil), source.Include...),
		ProviderOptions:  canonical.ProviderOptions{Volcengine: volcengineOptions(source, volcUnknown)},
		ClientExtensions: clientExtensions,
	}, nil
}

// EncodeResponseJSON renders a canonical response as an OpenAI Responses JSON
// object. Provider extensions and usage extensions are preserved verbatim.
func EncodeResponseJSON(source canonical.Response) ([]byte, error) {
	output, err := encodeItems(source.Output)
	if err != nil {
		return nil, err
	}
	outputJSON, err := json.Marshal(output)
	if err != nil {
		return nil, err
	}
	response := protocol.Response{
		ID: source.ID, Object: "response", CreatedAt: source.CreatedAt, Status: source.Status,
		Model: source.Model, Output: outputJSON, IncompleteDetails: cloneRaw(source.IncompleteDetails),
		Metadata: cloneStrings(source.Metadata),
	}
	if response.ID == "" {
		response.ID = source.ProviderResponseID
	}
	if response.Status == "" {
		response.Status = "completed"
	}
	if source.Usage != nil {
		response.Usage = encodeUsage(source.Usage)
	}
	if source.Error != nil {
		response.Error = encodeError(source.Error)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}
	return mergeResponseExtensions(encoded, source.ProviderExtensions)
}

// EncodeSSEFrame renders one OpenAI Responses SSE frame. Raw extension events
// retain their payload while canonical events receive the standard event name.
func EncodeSSEFrame(event canonical.Event) ([]byte, error) {
	payload, eventName, err := encodeEvent(event)
	if err != nil {
		return nil, err
	}
	return []byte("event: " + eventName + "\ndata: " + string(payload) + "\n\n"), nil
}

func decodeInput(raw json.RawMessage) ([]canonical.Item, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, errors.New("Responses input is required")
	}
	var text string
	if json.Unmarshal(trimmed, &text) == nil {
		return []canonical.Item{{Type: "message", Role: canonical.RoleUser, Content: []canonical.Content{{Type: "input_text", Text: text}}}}, nil
	}
	var values []json.RawMessage
	if err := json.Unmarshal(trimmed, &values); err != nil {
		return nil, errors.New("Responses input must be a string or array")
	}
	items := make([]canonical.Item, 0, len(values))
	for _, value := range values {
		item, err := decodeItem(value)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func decodeItem(raw json.RawMessage) (canonical.Item, error) {
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil {
		return canonical.Item{}, err
	}
	typeName := rawString(value["type"])
	role := canonical.Role(rawString(value["role"]))
	if typeName == "" && role != "" {
		typeName = "message"
	}
	item := canonical.Item{
		ID: rawString(value["id"]), Type: typeName, Role: role, Name: rawString(value["name"]),
		CallID: firstString(value, "call_id", "tool_call_id"), Status: rawString(value["status"]),
		Extra: extraExcept(value, "id", "type", "role", "name", "call_id", "tool_call_id", "status", "content", "arguments", "output"),
	}
	if item.Type == "message" && item.Role == "" {
		item.Role = canonical.RoleUser
	}
	if content, ok := value["content"]; ok {
		parts, err := decodeContent(content)
		if err != nil {
			return canonical.Item{}, err
		}
		item.Content = parts
	}
	item.Arguments = decodeJSONString(value["arguments"])
	item.Output = cloneRaw(value["output"])
	if item.Type == "function_call" && item.CallID == "" {
		item.CallID = item.ID
	}
	return item, nil
}

func decodeContent(raw json.RawMessage) ([]canonical.Content, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return []canonical.Content{{Type: "input_text", Text: text}}, nil
	}
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, errors.New("Responses message content must be a string or array")
	}
	parts := make([]canonical.Content, 0, len(values))
	for _, value := range values {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(value, &object); err != nil {
			return nil, err
		}
		kind := rawString(object["type"])
		part := canonical.Content{
			Type: kind, Text: rawString(object["text"]),
			URL:  firstString(object, "image_url", "video_url", "audio_url", "file_url"),
			Data: rawString(object["file_data"]), FileID: rawString(object["file_id"]),
			Filename: rawString(object["filename"]), MediaType: rawString(object["content_type"]),
			Format: rawString(object["format"]), Detail: rawString(object["detail"]),
			Transcript: rawString(object["transcript"]),
			Extra:      extraExcept(object, "type", "text", "image_url", "video_url", "audio_url", "file_url", "file_data", "file_id", "filename", "content_type", "format", "detail", "transcript", "input_audio"),
		}
		if kind == "input_audio" {
			var audio map[string]json.RawMessage
			if err := json.Unmarshal(object["input_audio"], &audio); err == nil {
				if part.Data == "" {
					part.Data = rawString(audio["data"])
				}
				if part.Format == "" {
					part.Format = rawString(audio["format"])
				}
				if extras := extraExcept(audio, "data", "format"); len(extras) > 0 {
					if part.Extra == nil {
						part.Extra = map[string]json.RawMessage{}
					}
					part.Extra[extraInputAudioOptions] = mustJSON(extras)
				}
			}
		}
		parts = append(parts, part)
	}
	return parts, nil
}

func decodeTools(raw json.RawMessage) ([]canonical.Tool, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var values []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	tools := make([]canonical.Tool, 0, len(values))
	for _, value := range values {
		tool := canonical.Tool{Type: rawString(value["type"]), Name: rawString(value["name"]), Description: rawString(value["description"]), InputSchema: cloneRaw(value["parameters"]), Strict: rawBool(value["strict"]), Options: mustJSON(extraExcept(value, "type", "name", "description", "parameters", "strict"))}
		tools = append(tools, tool)
	}
	return tools, nil
}

func decodeToolChoice(raw json.RawMessage) (*canonical.ToolChoice, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	choice := &canonical.ToolChoice{Raw: cloneRaw(raw)}
	if json.Unmarshal(raw, &choice.Mode) == nil {
		return choice, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, err
	}
	choice.Type, choice.Name = rawString(object["type"]), rawString(object["name"])
	return choice, nil
}

func decodeFormat(raw json.RawMessage) (*canonical.ResponseFormat, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var text struct {
		Format struct {
			Type, Name, Description string
			Schema                  json.RawMessage `json:"schema"`
			Strict                  *bool           `json:"strict"`
		} `json:"format"`
	}
	if err := json.Unmarshal(raw, &text); err != nil {
		return nil, err
	}
	if text.Format.Type == "" {
		return nil, nil
	}
	return &canonical.ResponseFormat{Type: text.Format.Type, Name: text.Format.Name, Description: text.Format.Description, Schema: cloneRaw(text.Format.Schema), Strict: cloneBool(text.Format.Strict)}, nil
}

func decodeReasoning(raw json.RawMessage) *canonical.Reasoning {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	var value struct{ Effort, Summary string }
	_ = json.Unmarshal(raw, &value)
	return &canonical.Reasoning{Effort: value.Effort, Summary: value.Summary, Raw: cloneRaw(raw)}
}

func encodeItems(source []canonical.Item) ([]map[string]any, error) {
	items := make([]map[string]any, 0, len(source))
	for _, item := range source {
		value, err := encodeItem(item)
		if err != nil {
			return nil, err
		}
		items = append(items, value)
	}
	return items, nil
}

func encodeItem(source canonical.Item) (map[string]any, error) {
	value, err := rawMap(source.Extra)
	if err != nil {
		return nil, err
	}
	value["type"] = source.Type
	if source.ID != "" {
		value["id"] = source.ID
	}
	if source.Role != "" {
		value["role"] = source.Role
	}
	if source.Name != "" {
		value["name"] = source.Name
	}
	if source.CallID != "" {
		value["call_id"] = source.CallID
	} else if source.Type == "function_call" && source.ID != "" {
		value["call_id"] = source.ID
	}
	if source.Status != "" {
		value["status"] = source.Status
	}
	if len(source.Content) > 0 {
		content, err := encodeContent(source.Content)
		if err != nil {
			return nil, err
		}
		value["content"] = content
	}
	if len(source.Arguments) > 0 {
		value["arguments"] = jsonStringValue(source.Arguments)
	}
	if len(source.Output) > 0 {
		value["output"] = jsonStringValue(source.Output)
	}
	return value, nil
}

func encodeContent(source []canonical.Content) ([]map[string]any, error) {
	parts := make([]map[string]any, 0, len(source))
	for _, part := range source {
		value, err := rawMap(part.Extra)
		if err != nil {
			return nil, err
		}
		value["type"] = part.Type
		delete(value, extraInputAudioOptions)
		if part.Text != "" {
			value["text"] = part.Text
		}
		switch part.Type {
		case "input_image":
			if part.URL != "" {
				value["image_url"] = part.URL
			} else if part.Data != "" {
				value["image_url"] = dataURL(part.MediaType, part.Data)
			}
		case "input_video":
			if part.URL != "" {
				value["video_url"] = part.URL
			} else if part.Data != "" {
				value["video_url"] = dataURL(part.MediaType, part.Data)
			}
		case "input_audio":
			if part.URL != "" {
				value["audio_url"] = part.URL
			}
			if part.Data != "" || part.Format != "" {
				audio := map[string]any{"data": part.Data, "format": part.Format}
				if raw := part.Extra[extraInputAudioOptions]; len(raw) > 0 {
					var extras map[string]any
					if json.Unmarshal(raw, &extras) == nil {
						for key, extra := range extras {
							audio[key] = extra
						}
					}
				}
				value["input_audio"] = audio
			}
		case "input_file":
			if part.URL != "" {
				value["file_url"] = part.URL
			}
		}
		if part.FileID != "" {
			value["file_id"] = part.FileID
		}
		if part.Data != "" && (part.Type == "input_file" || part.Type == "file") {
			value["file_data"] = part.Data
		}
		if part.Filename != "" {
			value["filename"] = part.Filename
		}
		if part.MediaType != "" && part.Type != "input_image" && part.Type != "input_audio" {
			value["content_type"] = part.MediaType
		}
		if part.Format != "" && part.Type != "input_audio" {
			value["format"] = part.Format
		}
		if part.Detail != "" {
			value["detail"] = part.Detail
		}
		if part.Transcript != "" {
			value["transcript"] = part.Transcript
		}
		parts = append(parts, value)
	}
	return parts, nil
}

func encodeUsage(source *canonical.Usage) *protocol.Usage {
	if source == nil {
		return nil
	}
	usage := &protocol.Usage{InputTokens: source.InputTokens, OutputTokens: source.OutputTokens, TotalTokens: source.TotalTokens, ExtraFields: cloneRawMap(source.Extra)}
	if source.CachedInputTokens != 0 {
		usage.InputTokensDetails = &protocol.InputTokensDetails{CachedTokens: source.CachedInputTokens}
	}
	if source.ReasoningOutputTokens != 0 {
		usage.OutputTokensDetails = &protocol.OutputTokensDetails{ReasoningTokens: source.ReasoningOutputTokens}
	}
	return usage
}

func encodeError(source *canonical.Error) *protocol.Error {
	if source == nil {
		return nil
	}
	return &protocol.Error{Code: source.Code, Message: source.Message, Type: source.Type, Param: source.Param}
}

func encodeEvent(event canonical.Event) ([]byte, string, error) {
	if event.Type == canonical.EventRaw {
		if len(event.Raw) == 0 {
			return nil, "", errors.New("Responses raw event payload is required")
		}
		if !json.Valid(event.Raw) {
			return nil, "", errors.New("Responses event raw payload is not valid JSON")
		}
		name := event.RawType
		if name == "" {
			name = string(event.Type)
		}
		if name == "" {
			name = "raw"
		}
		return cloneRaw(event.Raw), name, nil
	}
	name := responseEventName(event)
	if name == "" {
		return nil, "", errors.New("Responses event type is required")
	}
	body := map[string]any{"type": name, "sequence_number": event.SequenceNumber}
	if event.Type == canonical.EventError {
		body["type"] = "error"
		for key, value := range errorMap(event.Error) {
			body[key] = value
		}
		encoded, err := json.Marshal(body)
		return encoded, "error", err
	}
	if isResponseEvent(event.Type) {
		encoded, err := EncodeResponseJSON(responseForEvent(event))
		if err != nil {
			return nil, "", err
		}
		var response any
		if err := json.Unmarshal(encoded, &response); err != nil {
			return nil, "", err
		}
		body["response"] = response
	}
	switch event.Type {
	case canonical.EventOutputItemAdded, canonical.EventOutputItemDone:
		body["output_index"] = event.OutputIndex
		if event.Item == nil {
			return nil, "", fmt.Errorf("%s requires an item", name)
		}
		item, err := encodeItem(*event.Item)
		if err != nil {
			return nil, "", err
		}
		body["item"] = item
	case canonical.EventContentPartAdded, canonical.EventContentPartDone:
		body["output_index"] = event.OutputIndex
		body["content_index"] = event.ContentIndex
		setEventItemID(body, event.Item)
		part, err := eventContentPart(event.Item)
		if err != nil {
			return nil, "", err
		}
		body["part"] = part
	case canonical.EventTextDelta:
		body["output_index"] = event.OutputIndex
		body["content_index"] = event.ContentIndex
		setEventItemID(body, event.Item)
		body["delta"] = event.Delta
	case canonical.EventTextDone:
		body["output_index"] = event.OutputIndex
		body["content_index"] = event.ContentIndex
		setEventItemID(body, event.Item)
		body["text"] = event.Delta
	case canonical.EventReasoningDelta:
		body["output_index"] = event.OutputIndex
		body["summary_index"] = event.ContentIndex
		setEventItemID(body, event.Item)
		body["delta"] = event.Delta
	case canonical.EventToolArgumentsDelta:
		body["output_index"] = event.OutputIndex
		setEventItemID(body, event.Item)
		body["delta"] = event.Delta
	case canonical.EventUsage:
		body["usage"] = encodeUsage(event.Usage)
	}
	encoded, err := json.Marshal(body)
	return encoded, name, err
}

func errorMap(source *canonical.Error) map[string]any {
	if source == nil {
		return map[string]any{"message": "upstream error"}
	}
	result := map[string]any{"message": source.Message}
	if source.Code != "" {
		result["code"] = source.Code
	}
	if source.Param != nil {
		result["param"] = source.Param
	}
	return result
}

func requestExtras(source protocol.Request) (map[string]json.RawMessage, error) {
	encoded, err := json.Marshal(source)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return nil, err
	}
	for _, key := range []string{
		"model", "input", "instructions", "stream", "store", "background", "previous_response_id",
		"tools", "tool_choice", "parallel_tool_calls", "max_output_tokens", "temperature", "top_p",
		"reasoning", "metadata", "include", "user", "thinking", "caching", "session",
		"context_management", "expire_at",
	} {
		delete(fields, key)
	}
	if len(fields) == 0 {
		return nil, nil
	}
	return map[string]json.RawMessage{extraRequest: mustJSON(fields)}, nil
}

func volcengineOptions(source protocol.Request, unknown map[string]json.RawMessage) *canonical.VolcengineOptions {
	if len(source.Thinking) == 0 && len(source.Caching) == 0 && len(source.Session) == 0 &&
		len(source.ContextManagement) == 0 && source.ExpireAt == nil && len(unknown) == 0 {
		return nil
	}
	return &canonical.VolcengineOptions{
		Thinking: cloneRaw(source.Thinking), Caching: cloneRaw(source.Caching), Session: cloneRaw(source.Session),
		ContextManagement: cloneRaw(source.ContextManagement), ExpireAt: cloneInt64(source.ExpireAt), Unknown: unknown,
	}
}

func mergeResponseExtensions(encoded []byte, extensions map[string]json.RawMessage) ([]byte, error) {
	if len(extensions) == 0 {
		return encoded, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return nil, err
	}
	protected := map[string]bool{
		"id": true, "object": true, "created_at": true, "status": true, "error": true,
		"incomplete_details": true, "model": true, "output": true, "usage": true, "metadata": true,
	}
	for key, raw := range extensions {
		if protected[key] || !json.Valid(raw) {
			continue
		}
		fields[key] = cloneRaw(raw)
	}
	return json.Marshal(fields)
}

func responseEventName(event canonical.Event) string {
	if strings.HasPrefix(event.RawType, "response.") || event.RawType == "error" {
		return event.RawType
	}
	if event.Type == canonical.EventReasoningDelta {
		return "response.reasoning_summary_text.delta"
	}
	return string(event.Type)
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

func isResponseEvent(eventType canonical.EventType) bool {
	switch eventType {
	case canonical.EventResponseCreated, canonical.EventResponseQueued, canonical.EventResponseInProgress,
		canonical.EventCompleted, canonical.EventIncomplete, canonical.EventFailed:
		return true
	default:
		return false
	}
}

func responseForEvent(event canonical.Event) canonical.Response {
	response := canonical.Response{}
	if event.Response != nil {
		response = *event.Response
	}
	if response.ID == "" {
		response.ID = event.ProviderResponseID
	}
	if response.Usage == nil {
		response.Usage = event.Usage
	}
	if response.Error == nil {
		response.Error = event.Error
	}
	if response.Status == "" {
		switch event.Type {
		case canonical.EventResponseCreated, canonical.EventResponseInProgress:
			response.Status = "in_progress"
		case canonical.EventResponseQueued:
			response.Status = "queued"
		case canonical.EventIncomplete:
			response.Status = "incomplete"
		case canonical.EventFailed:
			response.Status = "failed"
		default:
			response.Status = "completed"
		}
	}
	return response
}

func setEventItemID(body map[string]any, item *canonical.Item) {
	if item != nil && item.ID != "" {
		body["item_id"] = item.ID
	}
}

func eventContentPart(item *canonical.Item) (map[string]any, error) {
	if item == nil || len(item.Content) == 0 {
		return nil, errors.New("Responses content-part event requires content")
	}
	parts, err := encodeContent(item.Content[:1])
	if err != nil {
		return nil, err
	}
	return parts[0], nil
}

func decodeJSONString(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return json.RawMessage(value)
	}
	return cloneRaw(raw)
}

func jsonStringValue(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	return string(raw)
}

func rawMap(source map[string]json.RawMessage) (map[string]any, error) {
	result := map[string]any{}
	for key, raw := range source {
		var value any
		if !json.Valid(raw) {
			return nil, fmt.Errorf("invalid extension %q", key)
		}
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		result[key] = value
	}
	return result, nil
}
func extraExcept(source map[string]json.RawMessage, names ...string) map[string]json.RawMessage {
	result := cloneRawMap(source)
	for _, name := range names {
		delete(result, name)
	}
	return result
}
func rawString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}
func firstString(source map[string]json.RawMessage, names ...string) string {
	for _, name := range names {
		if value := rawString(source[name]); value != "" {
			return value
		}
	}
	return ""
}
func rawBool(raw json.RawMessage) *bool {
	var value bool
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	return &value
}
func mustJSON(source any) json.RawMessage {
	if source == nil {
		return nil
	}
	value, _ := json.Marshal(source)
	if bytes.Equal(value, []byte("{}")) {
		return nil
	}
	return value
}
func intPointer(value int) *int {
	if value == 0 {
		return nil
	}
	result := value
	return &result
}
func cloneRaw(source json.RawMessage) json.RawMessage { return append(json.RawMessage(nil), source...) }
func cloneRawMap(source map[string]json.RawMessage) map[string]json.RawMessage {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]json.RawMessage, len(source))
	for key, value := range source {
		result[key] = cloneRaw(value)
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
func cloneFloat(source *float64) *float64 {
	if source == nil {
		return nil
	}
	value := *source
	return &value
}
func cloneInt64(source *int64) *int64 {
	if source == nil {
		return nil
	}
	value := *source
	return &value
}
func cloneStrings(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

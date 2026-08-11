// Package anthropic maps Anthropic Messages wire objects to canonical.
package anthropic

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mirainya/Prism/internal/gateway/canonical"
)

const (
	extraRequest  = "anthropic.messages.request_extras"
	extraRawBlock = "anthropic.raw_block"
	// extraChatFinishReason 是 openai_chat 上游在输出 Item.Extra 里携带的 choice 级 finish_reason。
	extraChatFinishReason = "openai_chat.finish_reason"
)

type requestWire struct {
	Model       string            `json:"model"`
	MaxTokens   int               `json:"max_tokens"`
	Messages    []messageWire     `json:"messages"`
	System      json.RawMessage   `json:"system"`
	Tools       []json.RawMessage `json:"tools"`
	ToolChoice  json.RawMessage   `json:"tool_choice"`
	Temperature *float64          `json:"temperature"`
	TopP        *float64          `json:"top_p"`
	Stop        []string          `json:"stop_sequences"`
	Stream      bool              `json:"stream"`
	Metadata    json.RawMessage   `json:"metadata"`
	Thinking    json.RawMessage   `json:"thinking"`
}

type messageWire struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type responseWire struct {
	ID           string            `json:"id"`
	Type         string            `json:"type"`
	Role         string            `json:"role"`
	Model        string            `json:"model"`
	Content      []json.RawMessage `json:"content"`
	StopReason   string            `json:"stop_reason"`
	StopSequence *string           `json:"stop_sequence"`
	Usage        usageWire         `json:"usage"`
	Error        *errorWire        `json:"error"`
}

type usageWire struct {
	InputTokens              int                        `json:"input_tokens"`
	OutputTokens             int                        `json:"output_tokens"`
	CacheCreationInputTokens int                        `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int                        `json:"cache_read_input_tokens"`
	Extra                    map[string]json.RawMessage `json:"-"`
}

type errorWire struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type sourceWire struct {
	Type      string          `json:"type"`
	URL       string          `json:"url"`
	MediaType string          `json:"media_type"`
	Data      string          `json:"data"`
	FileID    string          `json:"file_id"`
	Content   json.RawMessage `json:"content"`
}

// DecodeRequestJSON decodes the public Anthropic Messages wire format.
func DecodeRequestJSON(data []byte) (canonical.Request, error) {
	var wire requestWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return canonical.Request{}, err
	}
	if wire.Model == "" || wire.MaxTokens <= 0 {
		return canonical.Request{}, errors.New("model and max_tokens are required")
	}
	if len(wire.Messages) == 0 {
		return canonical.Request{}, errors.New("messages are required")
	}

	items, err := decodeMessages(wire.Messages)
	if err != nil {
		return canonical.Request{}, err
	}
	if presentJSON(wire.System) {
		contents, err := decodeSystem(wire.System)
		if err != nil {
			return canonical.Request{}, fmt.Errorf("system: %w", err)
		}
		items = append([]canonical.Item{{Type: "message", Role: canonical.RoleSystem, Content: contents}}, items...)
	}
	tools, err := decodeTools(wire.Tools)
	if err != nil {
		return canonical.Request{}, err
	}
	choice, parallel, err := decodeToolChoice(wire.ToolChoice)
	if err != nil {
		return canonical.Request{}, err
	}
	extras, err := requestExtras(data)
	if err != nil {
		return canonical.Request{}, err
	}

	request := canonical.Request{
		Endpoint: canonical.EndpointAnthropic, Model: wire.Model, Items: items,
		Tools: tools, ToolChoice: choice, ParallelToolCalls: parallel,
		Stream: wire.Stream, Temperature: cloneFloat(wire.Temperature), TopP: cloneFloat(wire.TopP),
		Stop: append([]string(nil), wire.Stop...), ClientExtensions: extras,
	}
	request.MaxOutputTokens = intPointer(wire.MaxTokens)
	request.User, request.Metadata = decodeMetadata(wire.Metadata)
	if presentJSON(wire.Thinking) {
		request.Reasoning = &canonical.Reasoning{Raw: cloneRaw(wire.Thinking)}
	}
	return request, nil
}

func decodeMessages(messages []messageWire) ([]canonical.Item, error) {
	result := make([]canonical.Item, 0, len(messages))
	for messageIndex, message := range messages {
		if message.Role != "user" && message.Role != "assistant" {
			// OpenAI-compatible clients may place system prompts in the messages array.
			// Treat "system" role as canonical RoleSystem to avoid hard failure.
			if message.Role == "system" {
				blocks, err := contentBlocks(message.Content)
				if err != nil {
					return nil, fmt.Errorf("messages[%d].content: %w", messageIndex, err)
				}
				parts := make([]canonical.Content, 0, len(blocks))
				for _, raw := range blocks {
					part, err := decodeContentBlock(raw, false)
					if err != nil {
						return nil, err
					}
					parts = append(parts, part)
				}
				result = append(result, canonical.Item{Type: "message", Role: canonical.RoleSystem, Content: parts})
				continue
			}
			return nil, fmt.Errorf("unsupported Anthropic role %q", message.Role)
		}
		blocks, err := contentBlocks(message.Content)
		if err != nil {
			return nil, fmt.Errorf("messages[%d].content: %w", messageIndex, err)
		}
		parts := make([]canonical.Content, 0, len(blocks))
		// canonical 将工具调用和 reasoning 建模为独立 Item，遇到这些 block 时先结束相邻文本消息。
		flushParts := func() {
			if len(parts) == 0 {
				return
			}
			result = append(result, canonical.Item{Type: "message", Role: canonical.Role(message.Role), Content: parts})
			parts = nil
		}
		for _, raw := range blocks {
			var block map[string]json.RawMessage
			if err := json.Unmarshal(raw, &block); err != nil {
				return nil, err
			}
			kind := rawString(block["type"])
			switch kind {
			case "tool_use", "server_tool_use":
				flushParts()
				id := rawString(block["id"])
				result = append(result, canonical.Item{
					ID: id, Type: "function_call", Role: canonical.RoleAssistant, CallID: id,
					Name: rawString(block["name"]), Arguments: normalizeArguments(block["input"]), Extra: rawBlockExtra(raw),
				})
			case "tool_result", "web_search_tool_result", "web_fetch_tool_result", "code_execution_tool_result", "bash_code_execution_tool_result", "text_editor_code_execution_tool_result":
				flushParts()
				result = append(result, canonical.Item{
					Type: "function_call_output", Role: canonical.RoleTool,
					CallID: rawString(block["tool_use_id"]), Output: cloneRaw(block["content"]), Extra: rawBlockExtra(raw),
				})
			case "thinking", "redacted_thinking":
				flushParts()
				text := rawString(block["thinking"])
				result = append(result, canonical.Item{
					Type: "reasoning", Role: canonical.RoleAssistant,
					Content: []canonical.Content{{Type: "reasoning_text", Text: text, Extra: rawBlockExtra(raw)}},
					Proof:   anthropicProof(block), Extra: rawBlockExtra(raw),
				})
			default:
				part, err := decodeContentBlock(raw, false)
				if err != nil {
					return nil, err
				}
				parts = append(parts, part)
			}
		}
		flushParts()
		if len(blocks) == 0 {
			result = append(result, canonical.Item{Type: "message", Role: canonical.Role(message.Role)})
		}
	}
	return result, nil
}

func decodeSystem(raw json.RawMessage) ([]canonical.Content, error) {
	blocks, err := contentBlocks(raw)
	if err != nil {
		return nil, err
	}
	result := make([]canonical.Content, 0, len(blocks))
	for _, block := range blocks {
		part, err := decodeContentBlock(block, false)
		if err != nil {
			return nil, err
		}
		if part.Type != "input_text" && part.Type != "text" {
			return nil, fmt.Errorf("unsupported Anthropic system block %q", part.Type)
		}
		result = append(result, part)
	}
	return result, nil
}

func contentBlocks(raw json.RawMessage) ([]json.RawMessage, error) {
	if !presentJSON(raw) {
		return nil, nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		block, _ := json.Marshal(map[string]any{"type": "text", "text": text})
		return []json.RawMessage{block}, nil
	}
	var blocks []json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, errors.New("content must be a string or array")
	}
	return blocks, nil
}

func decodeContentBlock(raw json.RawMessage, output bool) (canonical.Content, error) {
	var block map[string]json.RawMessage
	if err := json.Unmarshal(raw, &block); err != nil {
		return canonical.Content{}, err
	}
	kind := rawString(block["type"])
	switch kind {
	case "text":
		contentType := "input_text"
		if output {
			contentType = "output_text"
		}
		return canonical.Content{Type: contentType, Text: rawString(block["text"]), Extra: rawBlockExtra(raw)}, nil
	case "thinking", "redacted_thinking":
		return canonical.Content{Type: "reasoning_text", Text: rawString(block["thinking"]), Extra: rawBlockExtra(raw)}, nil
	case "image", "document":
		var source sourceWire
		if err := json.Unmarshal(block["source"], &source); err != nil {
			return canonical.Content{}, fmt.Errorf("%s source: %w", kind, err)
		}
		contentType := "input_image"
		if kind == "document" {
			contentType = "input_file"
		}
		return canonical.Content{
			Type: contentType, URL: source.URL, Data: source.Data, FileID: source.FileID,
			Filename: rawString(block["title"]), MediaType: source.MediaType, Extra: rawBlockExtra(raw),
		}, nil
	default:
		if kind == "" {
			return canonical.Content{}, errors.New("Anthropic content block type is required")
		}
		return canonical.Content{Type: kind, Extra: rawBlockExtra(raw)}, nil
	}
}

func decodeTools(rawTools []json.RawMessage) ([]canonical.Tool, error) {
	tools := make([]canonical.Tool, 0, len(rawTools))
	for index, raw := range rawTools {
		var value map[string]json.RawMessage
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, fmt.Errorf("tools[%d]: %w", index, err)
		}
		typeName := rawString(value["type"])
		canonicalType := typeName
		if canonicalType == "" || canonicalType == "custom" {
			canonicalType = "function"
		}
		tools = append(tools, canonical.Tool{
			Type: canonicalType, Name: rawString(value["name"]), Description: rawString(value["description"]),
			InputSchema: cloneRaw(value["input_schema"]), Strict: rawBool(value["strict"]),
			Options: mustJSON(extraExcept(value, "type", "name", "description", "input_schema", "strict")),
		})
	}
	return tools, nil
}

func decodeToolChoice(raw json.RawMessage) (*canonical.ToolChoice, *bool, error) {
	if !presentJSON(raw) {
		return nil, nil, nil
	}
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, nil, err
	}
	typeName := rawString(value["type"])
	choice := &canonical.ToolChoice{Mode: typeName, Type: typeName, Name: rawString(value["name"]), Raw: cloneRaw(raw)}
	var parallel *bool
	if disabled := rawBool(value["disable_parallel_tool_use"]); disabled != nil {
		enabled := !*disabled
		parallel = &enabled
	}
	return choice, parallel, nil
}

// EncodeRequest renders canonical input as an Anthropic Messages request.
func EncodeRequest(request canonical.Request) (map[string]any, error) {
	body := map[string]any{"model": request.Model, "max_tokens": 4096}
	if request.MaxOutputTokens != nil {
		body["max_tokens"] = *request.MaxOutputTokens
	}
	if request.Stream {
		body["stream"] = true
	}
	if request.Temperature != nil {
		body["temperature"] = *request.Temperature
	}
	if request.TopP != nil {
		body["top_p"] = *request.TopP
	}
	if len(request.Stop) > 0 {
		body["stop_sequences"] = append([]string(nil), request.Stop...)
	}

	system := make([]any, 0)
	messages := make([]any, 0)
	currentRole := ""
	currentContent := make([]any, 0)
	// Anthropic 要求相邻 block 按 user/assistant 消息分组；canonical Item 边界不等同于消息边界。
	flush := func() {
		if currentRole == "" || len(currentContent) == 0 {
			return
		}
		messages = append(messages, map[string]any{"role": currentRole, "content": currentContent})
		currentRole, currentContent = "", nil
	}
	appendBlock := func(role string, block any) {
		if currentRole != "" && currentRole != role {
			flush()
		}
		currentRole = role
		currentContent = append(currentContent, block)
	}

	for _, item := range request.Items {
		switch item.Type {
		case "function_call":
			block, err := encodeToolUse(item)
			if err != nil {
				return nil, err
			}
			appendBlock("assistant", block)
		case "function_call_output":
			block, err := encodeToolResult(item)
			if err != nil {
				return nil, err
			}
			appendBlock("user", block)
		case "reasoning":
			for _, part := range anthropicReasoningContent(item) {
				block, err := encodeAnthropicReasoning(item, part)
				if err != nil {
					return nil, err
				}
				appendBlock("assistant", block)
			}
		default:
			if item.Role == canonical.RoleSystem || item.Role == canonical.RoleDeveloper {
				for _, part := range item.Content {
					if part.Type != "" && part.Type != "input_text" && part.Type != "text" {
						return nil, fmt.Errorf("Anthropic cannot encode system content %q", part.Type)
					}
					block, err := encodeAnthropicContent(part)
					if err != nil {
						return nil, err
					}
					system = append(system, block)
				}
				continue
			}
			role := string(item.Role)
			if role != "user" && role != "assistant" {
				return nil, fmt.Errorf("Anthropic cannot encode role %q", role)
			}
			for _, part := range item.Content {
				block, err := encodeAnthropicContent(part)
				if err != nil {
					return nil, err
				}
				appendBlock(role, block)
			}
		}
	}
	flush()
	if request.Instructions != "" {
		system = append(system, map[string]any{"type": "text", "text": request.Instructions})
	}
	if len(system) > 0 {
		body["system"] = system
	}
	if len(messages) == 0 {
		return nil, errors.New("Anthropic messages are required")
	}
	body["messages"] = messages

	if len(request.Tools) > 0 {
		tools := make([]any, 0, len(request.Tools))
		for _, tool := range request.Tools {
			encoded, err := encodeTool(tool)
			if err != nil {
				return nil, err
			}
			tools = append(tools, encoded)
		}
		body["tools"] = tools
	}
	if request.ToolChoice != nil || request.ParallelToolCalls != nil {
		choice, err := encodeToolChoice(request.ToolChoice, request.ParallelToolCalls)
		if err != nil {
			return nil, err
		}
		body["tool_choice"] = choice
	}
	if request.User != "" || len(request.Metadata) > 0 {
		metadata := map[string]any{}
		for key, value := range request.Metadata {
			metadata[key] = value
		}
		if request.User != "" {
			metadata["user_id"] = request.User
		}
		body["metadata"] = metadata
	}
	if request.Reasoning != nil && presentJSON(request.Reasoning.Raw) {
		var thinking any
		if err := json.Unmarshal(request.Reasoning.Raw, &thinking); err != nil {
			return nil, fmt.Errorf("Anthropic thinking: %w", err)
		}
		body["thinking"] = thinking
	}
	if err := applyRequestExtras(body, request.ClientExtensions[extraRequest]); err != nil {
		return nil, err
	}
	return body, nil
}

func encodeAnthropicContent(content canonical.Content) (map[string]any, error) {
	value, err := rawBlockMap(content.Extra)
	if err != nil {
		return nil, err
	}
	if content.Type == "" && content.Text != "" {
		content.Type = "text"
	}
	switch content.Type {
	case "input_text", "output_text", "text":
		value["type"], value["text"] = "text", content.Text
	case "thinking", "reasoning_text":
		delete(value, "signature")
		if value["type"] == "redacted_thinking" {
			delete(value, "thinking")
		} else {
			value["type"], value["thinking"] = "thinking", content.Text
		}
	case "input_image", "image", "image_url":
		value["type"] = "image"
		if _, exists := value["source"]; !exists {
			value["source"] = encodeSource(content)
		}
	case "input_file", "file", "document":
		value["type"] = "document"
		if _, exists := value["source"]; !exists {
			value["source"] = encodeSource(content)
		}
		if content.Filename != "" {
			value["title"] = content.Filename
		}
	default:
		if len(value) == 0 {
			return nil, fmt.Errorf("Anthropic cannot encode content %q", content.Type)
		}
	}
	return value, nil
}

func encodeAnthropicReasoning(item canonical.Item, content canonical.Content) (map[string]any, error) {
	if !presentJSON(content.Extra[extraRawBlock]) && presentJSON(item.Extra[extraRawBlock]) {
		content.Extra = map[string]json.RawMessage{extraRawBlock: cloneRaw(item.Extra[extraRawBlock])}
	}
	block, err := encodeAnthropicContent(content)
	if err != nil {
		return nil, err
	}
	delete(block, "signature")
	if signature, ok := canonical.NativeProviderProofValue(item.Proof, canonical.ProofProviderAnthropic); ok {
		switch item.Proof.Subject {
		case canonical.ProofSubjectAnthropicRedacted:
			block["type"] = "redacted_thinking"
			block["data"] = signature
			delete(block, "thinking")
		case "":
			block["signature"] = signature
		}
	}
	return block, nil
}

func anthropicReasoningContent(item canonical.Item) []canonical.Content {
	if len(item.Content) > 0 {
		return item.Content
	}
	if item.Proof != nil || presentJSON(item.Extra[extraRawBlock]) {
		return []canonical.Content{{Type: "reasoning_text"}}
	}
	return nil
}

func anthropicProof(block map[string]json.RawMessage) *canonical.ProviderProof {
	switch rawString(block["type"]) {
	case "thinking":
		signature := rawString(block["signature"])
		if signature != "" {
			return &canonical.ProviderProof{Provider: canonical.ProofProviderAnthropic, Value: signature}
		}
	case "redacted_thinking":
		data := rawString(block["data"])
		if data != "" {
			return &canonical.ProviderProof{Provider: canonical.ProofProviderAnthropic, Value: data, Subject: canonical.ProofSubjectAnthropicRedacted}
		}
	}
	return nil
}

func encodeSource(content canonical.Content) map[string]any {
	switch {
	case content.URL != "":
		return map[string]any{"type": "url", "url": content.URL}
	case content.FileID != "":
		return map[string]any{"type": "file", "file_id": content.FileID}
	default:
		return compactMap(map[string]any{"type": "base64", "media_type": content.MediaType, "data": content.Data})
	}
}

func encodeToolUse(item canonical.Item) (map[string]any, error) {
	value, err := rawBlockMap(item.Extra)
	if err != nil {
		return nil, err
	}
	arguments, err := argumentObject(item.Arguments)
	if err != nil {
		return nil, fmt.Errorf("Anthropic tool %q arguments: %w", item.Name, err)
	}
	callID := item.CallID
	if callID == "" {
		callID = item.ID
	}
	typeName, _ := value["type"].(string)
	if typeName != "server_tool_use" {
		typeName = "tool_use"
	}
	value["type"], value["id"], value["name"], value["input"] = typeName, callID, item.Name, arguments
	return value, nil
}

func encodeToolResult(item canonical.Item) (map[string]any, error) {
	value, err := rawBlockMap(item.Extra)
	if err != nil {
		return nil, err
	}
	output, err := rawValue(item.Output)
	if err != nil {
		return nil, fmt.Errorf("Anthropic tool result %q: %w", item.CallID, err)
	}
	typeName, _ := value["type"].(string)
	switch typeName {
	case "web_search_tool_result", "web_fetch_tool_result", "code_execution_tool_result", "bash_code_execution_tool_result", "text_editor_code_execution_tool_result":
	default:
		typeName = "tool_result"
	}
	value["type"], value["tool_use_id"], value["content"] = typeName, item.CallID, output
	return value, nil
}

func encodeTool(tool canonical.Tool) (map[string]any, error) {
	value, err := rawMap(tool.Options)
	if err != nil {
		return nil, err
	}
	if tool.Type != "" && tool.Type != "function" {
		value["type"] = tool.Type
	}
	if tool.Name != "" {
		value["name"] = tool.Name
	}
	if tool.Description != "" {
		value["description"] = tool.Description
	}
	if len(tool.InputSchema) > 0 {
		value["input_schema"] = json.RawMessage(tool.InputSchema)
	} else if tool.Type == "" || tool.Type == "function" {
		value["input_schema"] = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	if tool.Strict != nil {
		value["strict"] = *tool.Strict
	}
	return value, nil
}

func encodeToolChoice(choice *canonical.ToolChoice, parallel *bool) (map[string]any, error) {
	value := map[string]any{}
	if choice != nil && presentJSON(choice.Raw) {
		if err := json.Unmarshal(choice.Raw, &value); err != nil {
			return nil, err
		}
	}
	typeName := "auto"
	if choice != nil {
		typeName = choice.Mode
		if typeName == "" {
			typeName = choice.Type
		}
	}
	switch typeName {
	case "", "auto":
		value["type"] = "auto"
	case "required", "any":
		value["type"] = "any"
	case "none":
		value["type"] = "none"
	case "function", "tool":
		value["type"] = "tool"
		value["name"] = choice.Name
	default:
		return nil, fmt.Errorf("Anthropic does not support tool choice %q", typeName)
	}
	if parallel != nil {
		value["disable_parallel_tool_use"] = !*parallel
	}
	return value, nil
}

// DecodeResponse maps an Anthropic Message or error object to canonical.
func DecodeResponse(raw []byte, publicModel string) (canonical.Response, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return canonical.Response{}, err
	}
	if rawString(root["type"]) == "error" || presentJSON(root["error"]) {
		canonicalError := decodeErrorObject(root, 0)
		return canonical.Response{Model: publicModel, Status: "failed", Error: canonicalError}, nil
	}
	var wire responseWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return canonical.Response{}, err
	}
	model := publicModel
	if model == "" {
		model = wire.Model
	}
	output, err := decodeResponseContent(wire.Content)
	if err != nil {
		return canonical.Response{}, err
	}
	usage, err := decodeUsage(root["usage"])
	if err != nil {
		return canonical.Response{}, err
	}
	status := anthropicResponseStatus(wire.StopReason)
	return canonical.Response{
		ID: wire.ID, ProviderResponseID: wire.ID, Model: model, Status: status,
		Output: output, Usage: usage, FinishReason: wire.StopReason,
		ProviderExtensions: responseExtras(root),
	}, nil
}

func decodeResponseContent(blocks []json.RawMessage) ([]canonical.Item, error) {
	result := make([]canonical.Item, 0, len(blocks))
	message := canonical.Item{Type: "message", Role: canonical.RoleAssistant, Status: "completed"}
	flushMessage := func() {
		if len(message.Content) == 0 {
			return
		}
		result = append(result, message)
		message = canonical.Item{Type: "message", Role: canonical.RoleAssistant, Status: "completed"}
	}
	for _, raw := range blocks {
		var block map[string]json.RawMessage
		if err := json.Unmarshal(raw, &block); err != nil {
			return nil, err
		}
		switch kind := rawString(block["type"]); kind {
		case "text":
			part, err := decodeContentBlock(raw, true)
			if err != nil {
				return nil, err
			}
			message.Content = append(message.Content, part)
		case "thinking", "redacted_thinking":
			flushMessage()
			result = append(result, canonical.Item{
				Type: "reasoning", Role: canonical.RoleAssistant, Status: "completed",
				Content: []canonical.Content{{Type: "reasoning_text", Text: rawString(block["thinking"]), Extra: rawBlockExtra(raw)}},
				Proof:   anthropicProof(block), Extra: rawBlockExtra(raw),
			})
		case "tool_use", "server_tool_use":
			flushMessage()
			id := rawString(block["id"])
			result = append(result, canonical.Item{
				ID: id, Type: "function_call", Role: canonical.RoleAssistant, Status: "completed",
				CallID: id, Name: rawString(block["name"]), Arguments: normalizeArguments(block["input"]), Extra: rawBlockExtra(raw),
			})
		default:
			part, err := decodeContentBlock(raw, true)
			if err != nil {
				return nil, err
			}
			message.Content = append(message.Content, part)
		}
	}
	flushMessage()
	return result, nil
}

// DecodeEvent maps one named Anthropic SSE event to canonical.
func DecodeEvent(name string, raw []byte) (canonical.Event, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return canonical.Event{}, err
	}
	if name == "" {
		name = rawString(root["type"])
	}
	event := canonical.Event{RawType: name, Raw: cloneRaw(raw)}
	switch name {
	case "message_start":
		messageRaw := root["message"]
		response, err := DecodeResponse(messageRaw, "")
		if err != nil {
			return canonical.Event{}, err
		}
		response.Status = "in_progress"
		event.Type, event.Response, event.Usage = canonical.EventResponseCreated, &response, response.Usage
		event.ProviderResponseID = response.ProviderResponseID
	case "content_block_start":
		index := rawInt(root["index"])
		var block map[string]json.RawMessage
		if err := json.Unmarshal(root["content_block"], &block); err != nil {
			return canonical.Event{}, err
		}
		kind := rawString(block["type"])
		event.ContentIndex, event.ToolIndex = index, index
		if kind == "tool_use" || kind == "server_tool_use" {
			id := rawString(block["id"])
			event.Type = canonical.EventOutputItemAdded
			event.Item = &canonical.Item{ID: id, Type: "function_call", CallID: id, Name: rawString(block["name"]), Arguments: normalizeArguments(block["input"]), Status: "in_progress"}
		} else {
			part, err := decodeContentBlock(root["content_block"], true)
			if err != nil {
				return canonical.Event{}, err
			}
			event.Type = canonical.EventContentPartAdded
			itemType := "message"
			if kind == "thinking" || kind == "redacted_thinking" {
				itemType = "reasoning"
			}
			event.Item = &canonical.Item{
				Type: itemType, Role: canonical.RoleAssistant, Content: []canonical.Content{part},
				Status: "in_progress", Proof: anthropicProof(block), Extra: rawBlockExtra(root["content_block"]),
			}
		}
	case "content_block_delta":
		index := rawInt(root["index"])
		var delta struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			Thinking    string `json:"thinking"`
			Signature   string `json:"signature"`
			PartialJSON string `json:"partial_json"`
		}
		if err := json.Unmarshal(root["delta"], &delta); err != nil {
			return canonical.Event{}, err
		}
		event.ContentIndex, event.ToolIndex = index, index
		switch delta.Type {
		case "text_delta":
			event.Type, event.Delta = canonical.EventTextDelta, delta.Text
		case "thinking_delta":
			event.Type, event.Delta = canonical.EventReasoningDelta, delta.Thinking
		case "signature_delta":
			event.Type, event.Delta = canonical.EventProviderProof, delta.Signature
			event.Item = &canonical.Item{
				Type: "reasoning", Role: canonical.RoleAssistant, Status: "in_progress",
				Content: []canonical.Content{{Type: "reasoning_text"}},
				Proof:   &canonical.ProviderProof{Provider: canonical.ProofProviderAnthropic, Value: delta.Signature},
			}
		case "input_json_delta":
			event.Type, event.Delta = canonical.EventToolArgumentsDelta, delta.PartialJSON
		default:
			event.Type = canonical.EventRaw
		}
	case "content_block_stop":
		event.Type, event.ContentIndex = canonical.EventContentPartDone, rawInt(root["index"])
	case "message_delta":
		var delta struct {
			StopReason   string  `json:"stop_reason"`
			StopSequence *string `json:"stop_sequence"`
		}
		if err := json.Unmarshal(root["delta"], &delta); err != nil {
			return canonical.Event{}, err
		}
		usage, err := decodeUsage(root["usage"])
		if err != nil {
			return canonical.Event{}, err
		}
		event.Type, event.Usage = canonical.EventUsage, usage
		event.Response = &canonical.Response{Status: anthropicResponseStatus(delta.StopReason), FinishReason: delta.StopReason, Usage: usage}
	case "message_stop":
		event.Type = canonical.EventCompleted
	case "error":
		event.Type, event.Error = canonical.EventError, decodeErrorObject(root, 0)
	default:
		event.Type = canonical.EventRaw
	}
	return event, nil
}

// EncodeResponseJSON encodes a canonical response as an Anthropic Message.
func EncodeResponseJSON(response canonical.Response) ([]byte, error) {
	if response.Error != nil && (response.Status == "failed" || len(response.Output) == 0) {
		return json.Marshal(map[string]any{"type": "error", "error": encodeError(response.Error)})
	}
	content := make([]any, 0, len(response.Output))
	for _, item := range response.Output {
		switch item.Type {
		case "function_call":
			block, err := encodeToolUse(item)
			if err != nil {
				return nil, err
			}
			content = append(content, block)
		case "reasoning":
			for _, part := range anthropicReasoningContent(item) {
				block, err := encodeAnthropicReasoning(item, part)
				if err != nil {
					return nil, err
				}
				content = append(content, block)
			}
		default:
			for _, part := range item.Content {
				block, err := encodeAnthropicContent(part)
				if err != nil {
					return nil, err
				}
				content = append(content, block)
			}
		}
	}
	responseID := response.ID
	if responseID == "" {
		responseID = response.ProviderResponseID
	}
	body := map[string]any{
		"id": responseID, "type": "message", "role": "assistant", "model": response.Model,
		"content": content, "stop_reason": anthropicStopReason(response), "stop_sequence": nil,
		"usage": encodeUsage(response.Usage),
	}
	for key, raw := range response.ProviderExtensions {
		if isProtectedResponseField(key) || !json.Valid(raw) {
			continue
		}
		var value any
		if json.Unmarshal(raw, &value) == nil {
			body[key] = value
		}
	}
	return json.Marshal(body)
}

// EncodeSSEFrame maps a canonical event to Anthropic's named SSE wire format.
func EncodeSSEFrame(event canonical.Event) ([]byte, error) {
	if event.Type == canonical.EventRaw {
		if event.RawType == "" || !json.Valid(event.Raw) {
			return nil, errors.New("Anthropic raw event requires a name and valid JSON")
		}
		return sseFrame(event.RawType, event.Raw), nil
	}
	switch event.Type {
	case canonical.EventResponseCreated:
		message, err := messageStart(event)
		if err != nil {
			return nil, err
		}
		return sseJSON("message_start", map[string]any{"type": "message_start", "message": message})
	case canonical.EventOutputItemAdded, canonical.EventContentPartAdded:
		block, err := contentBlockStart(event.Item)
		if err != nil {
			return nil, err
		}
		return sseJSON("content_block_start", map[string]any{"type": "content_block_start", "index": blockIndex(event), "content_block": block})
	case canonical.EventTextDelta:
		return sseJSON("content_block_delta", map[string]any{"type": "content_block_delta", "index": blockIndex(event), "delta": map[string]any{"type": "text_delta", "text": event.Delta}})
	case canonical.EventReasoningDelta:
		return sseJSON("content_block_delta", map[string]any{"type": "content_block_delta", "index": blockIndex(event), "delta": map[string]any{"type": "thinking_delta", "thinking": event.Delta}})
	case canonical.EventProviderProof:
		if event.Item == nil {
			return nil, nil
		}
		signature, ok := canonical.NativeProviderProofValue(event.Item.Proof, canonical.ProofProviderAnthropic)
		if !ok || event.Item.Proof.Subject != "" {
			return nil, nil
		}
		if event.Delta != "" {
			signature = event.Delta
		}
		return sseJSON("content_block_delta", map[string]any{"type": "content_block_delta", "index": blockIndex(event), "delta": map[string]any{"type": "signature_delta", "signature": signature}})
	case canonical.EventToolArgumentsDelta:
		return sseJSON("content_block_delta", map[string]any{"type": "content_block_delta", "index": blockIndex(event), "delta": map[string]any{"type": "input_json_delta", "partial_json": event.Delta}})
	case canonical.EventOutputItemDone, canonical.EventContentPartDone, canonical.EventTextDone:
		return sseJSON("content_block_stop", map[string]any{"type": "content_block_stop", "index": blockIndex(event)})
	case canonical.EventUsage:
		return messageDeltaFrame("", event.Usage)
	case canonical.EventCompleted, canonical.EventIncomplete:
		finish := "end_turn"
		if event.Type == canonical.EventIncomplete {
			finish = "max_tokens"
		}
		if event.Response != nil && event.Response.FinishReason != "" {
			finish = anthropicFinishReason(event.Response.FinishReason)
		}
		delta, err := messageDeltaFrame(finish, eventUsage(event))
		if err != nil {
			return nil, err
		}
		stop, err := sseJSON("message_stop", map[string]any{"type": "message_stop"})
		if err != nil {
			return nil, err
		}
		return append(delta, stop...), nil
	case canonical.EventError, canonical.EventFailed:
		return sseJSON("error", map[string]any{"type": "error", "error": encodeError(event.Error)})
	default:
		return nil, nil
	}
}

const (
	streamBlockText      = "text"
	streamBlockReasoning = "reasoning"
	streamBlockTool      = "tool"
)

type streamBlockKey struct {
	kind         string
	choiceIndex  int
	outputIndex  int
	contentIndex int
	toolIndex    int
}

type streamBlock struct {
	index int
	open  bool
}

// SSEEncoder converts canonical streams with different event granularity into
// a valid Anthropic Messages stream. One encoder must be used per response.
type SSEEncoder struct {
	publicModel    string
	messageStarted bool
	terminal       bool
	nextBlockIndex int
	sawTool        bool
	// chatFinish 记录流内 Item.Extra 携带的 openai_chat.finish_reason，终态缺 FinishReason 时兜底。
	chatFinish string
	// pendingUsage 暂存 EventUsage，anthropic 协议要求 usage 随带 stop_reason 的终态 message_delta 一起下发。
	pendingUsage *canonical.Usage
	blocks       map[streamBlockKey]streamBlock
	blockOrder   []streamBlockKey
}

func NewSSEEncoder(publicModel string) *SSEEncoder {
	return &SSEEncoder{publicModel: publicModel, blocks: map[streamBlockKey]streamBlock{}}
}

func (e *SSEEncoder) Encode(event canonical.Event) ([]byte, error) {
	if e == nil {
		return EncodeSSEFrame(event)
	}
	if e.terminal {
		return nil, nil
	}
	if event.Type == canonical.EventRaw {
		return EncodeSSEFrame(event)
	}
	if event.Item != nil {
		if finish := rawString(event.Item.Extra[extraChatFinishReason]); finish != "" {
			e.chatFinish = finish
		}
	}

	// Provider 的 canonical 事件粒度并不统一，编码器会补 message_start、block_start 和 block_stop。
	var encoded []byte
	appendEncoded := func(frame []byte, err error) error {
		if err != nil {
			return err
		}
		encoded = append(encoded, frame...)
		return nil
	}

	switch event.Type {
	case canonical.EventResponseCreated:
		if err := appendEncoded(e.ensureMessageStart(event)); err != nil {
			return nil, err
		}
	case canonical.EventOutputItemAdded, canonical.EventContentPartAdded:
		if err := appendEncoded(e.ensureMessageStart(event)); err != nil {
			return nil, err
		}
		kind, ok := addedBlockKind(event)
		if !ok {
			return encoded, nil
		}
		_, frame, err := e.ensureBlock(event, kind)
		if err := appendEncoded(frame, err); err != nil {
			return nil, err
		}
	case canonical.EventTextDelta, canonical.EventReasoningDelta, canonical.EventToolArgumentsDelta:
		if err := appendEncoded(e.ensureMessageStart(event)); err != nil {
			return nil, err
		}
		kind := deltaBlockKind(event.Type)
		block, frame, err := e.ensureBlock(event, kind)
		if err := appendEncoded(frame, err); err != nil {
			return nil, err
		}
		deltaEvent := event
		setBlockIndex(&deltaEvent, block.index)
		if err := appendEncoded(EncodeSSEFrame(deltaEvent)); err != nil {
			return nil, err
		}
	case canonical.EventProviderProof:
		if event.Item == nil {
			return encoded, nil
		}
		if _, ok := canonical.NativeProviderProofValue(event.Item.Proof, canonical.ProofProviderAnthropic); !ok {
			return encoded, nil
		}
		if err := appendEncoded(e.ensureMessageStart(event)); err != nil {
			return nil, err
		}
		block, frame, err := e.ensureBlock(event, streamBlockReasoning)
		if err := appendEncoded(frame, err); err != nil {
			return nil, err
		}
		proofEvent := event
		setBlockIndex(&proofEvent, block.index)
		if err := appendEncoded(EncodeSSEFrame(proofEvent)); err != nil {
			return nil, err
		}
	case canonical.EventTextDone, canonical.EventContentPartDone, canonical.EventOutputItemDone:
		if err := appendEncoded(e.stopForEvent(event)); err != nil {
			return nil, err
		}
	case canonical.EventUsage:
		if err := appendEncoded(e.ensureMessageStart(event)); err != nil {
			return nil, err
		}
		// 不单独发 stop_reason:null 的 message_delta，usage 暂存到终态与 stop_reason 合并下发。
		if usage := eventUsage(event); usage != nil {
			e.pendingUsage = usage
		}
	case canonical.EventCompleted, canonical.EventIncomplete:
		if eventUsage(event) == nil {
			event.Usage = e.pendingUsage
		}
		if err := appendEncoded(e.ensureMessageStart(event)); err != nil {
			return nil, err
		}
		if err := appendEncoded(e.stopAll(event)); err != nil {
			return nil, err
		}
		terminal := e.withTerminalFinish(event)
		if err := appendEncoded(EncodeSSEFrame(terminal)); err != nil {
			return nil, err
		}
		e.terminal = true
	case canonical.EventError, canonical.EventFailed:
		if err := appendEncoded(e.stopAll(event)); err != nil {
			return nil, err
		}
		if err := appendEncoded(EncodeSSEFrame(event)); err != nil {
			return nil, err
		}
		e.terminal = true
	default:
		frame, err := EncodeSSEFrame(event)
		if err := appendEncoded(frame, err); err != nil {
			return nil, err
		}
	}
	return encoded, nil
}

func (e *SSEEncoder) ensureMessageStart(event canonical.Event) ([]byte, error) {
	if e.messageStarted {
		return nil, nil
	}
	start := event
	start.Type = canonical.EventResponseCreated
	response := canonical.Response{ID: event.ProviderResponseID, ProviderResponseID: event.ProviderResponseID, Model: e.publicModel, Status: "in_progress", Usage: eventUsage(event)}
	if event.Response != nil {
		response = *event.Response
		if response.ProviderResponseID == "" {
			response.ProviderResponseID = event.ProviderResponseID
		}
		if response.ID == "" {
			response.ID = event.ProviderResponseID
		}
		if response.Model == "" {
			response.Model = e.publicModel
		}
	}
	start.Response = &response
	frame, err := EncodeSSEFrame(start)
	if err != nil {
		return nil, err
	}
	e.messageStarted = true
	return frame, nil
}

func (e *SSEEncoder) ensureBlock(event canonical.Event, kind string) (streamBlock, []byte, error) {
	key := blockKey(event, kind)
	if block, ok := e.blocks[key]; ok && block.open {
		return block, nil, nil
	}
	block := streamBlock{index: e.nextBlockIndex, open: true}
	e.nextBlockIndex++
	e.blocks[key] = block
	e.blockOrder = append(e.blockOrder, key)
	if kind == streamBlockTool {
		e.sawTool = true
	}

	start := event
	start.Type = canonical.EventContentPartAdded
	if kind == streamBlockTool {
		start.Type = canonical.EventOutputItemAdded
	}
	item := blockStartItem(event.Item, kind, block.index)
	start.Item = &item
	setBlockIndex(&start, block.index)
	frame, err := EncodeSSEFrame(start)
	return block, frame, err
}

func (e *SSEEncoder) stopForEvent(event canonical.Event) ([]byte, error) {
	kind, ok := doneBlockKind(event)
	if ok {
		return e.stopBlock(blockKey(event, kind), event)
	}
	var encoded []byte
	for _, key := range e.blockOrder {
		if key.choiceIndex != event.ChoiceIndex || key.outputIndex != event.OutputIndex {
			continue
		}
		frame, err := e.stopBlock(key, event)
		if err != nil {
			return nil, err
		}
		encoded = append(encoded, frame...)
	}
	return encoded, nil
}

func (e *SSEEncoder) stopAll(event canonical.Event) ([]byte, error) {
	var encoded []byte
	for _, key := range e.blockOrder {
		frame, err := e.stopBlock(key, event)
		if err != nil {
			return nil, err
		}
		encoded = append(encoded, frame...)
	}
	return encoded, nil
}

func (e *SSEEncoder) stopBlock(key streamBlockKey, event canonical.Event) ([]byte, error) {
	block, ok := e.blocks[key]
	if !ok || !block.open {
		return nil, nil
	}
	block.open = false
	e.blocks[key] = block
	stop := event
	stop.Type = canonical.EventContentPartDone
	setBlockIndex(&stop, block.index)
	return EncodeSSEFrame(stop)
}

func (e *SSEEncoder) withTerminalFinish(event canonical.Event) canonical.Event {
	if event.Response != nil && event.Response.FinishReason != "" {
		return event
	}
	// 终态缺 FinishReason 时优先用流内的 openai_chat.finish_reason 扩展，sawTool 启发式仅作最后兜底。
	finish := e.chatFinish
	if finish == "" && e.sawTool && event.Type == canonical.EventCompleted {
		finish = "tool_use"
	}
	if finish == "" {
		return event
	}
	response := canonical.Response{Model: e.publicModel, FinishReason: finish, Usage: eventUsage(event)}
	if event.Response != nil {
		response = *event.Response
		response.FinishReason = finish
	}
	event.Response = &response
	return event
}

func addedBlockKind(event canonical.Event) (string, bool) {
	if event.Item != nil {
		switch event.Item.Type {
		case "function_call":
			return streamBlockTool, true
		case "reasoning":
			if len(event.Item.Content) > 0 || (event.Item.Proof != nil && event.Item.Proof.Subject == canonical.ProofSubjectAnthropicRedacted) {
				return streamBlockReasoning, true
			}
			return "", false
		}
		if kind, ok := contentBlockKind(event.Item.Content); ok {
			return kind, true
		}
	}
	if event.Type == canonical.EventContentPartAdded {
		return streamBlockText, true
	}
	return "", false
}

func doneBlockKind(event canonical.Event) (string, bool) {
	switch event.Type {
	case canonical.EventTextDone:
		return streamBlockText, true
	}
	if event.Item != nil {
		switch event.Item.Type {
		case "function_call":
			return streamBlockTool, true
		case "reasoning":
			return streamBlockReasoning, true
		case "message":
			if kind, ok := contentBlockKind(event.Item.Content); ok {
				return kind, true
			}
			return streamBlockText, true
		}
	}
	if event.Type == canonical.EventContentPartDone {
		return streamBlockText, true
	}
	return "", false
}

func deltaBlockKind(eventType canonical.EventType) string {
	switch eventType {
	case canonical.EventReasoningDelta:
		return streamBlockReasoning
	case canonical.EventToolArgumentsDelta:
		return streamBlockTool
	default:
		return streamBlockText
	}
}

func contentBlockKind(content []canonical.Content) (string, bool) {
	if len(content) == 0 {
		return "", false
	}
	switch content[0].Type {
	case "thinking", "reasoning_text":
		return streamBlockReasoning, true
	case "input_text", "output_text", "text", "refusal", "":
		return streamBlockText, true
	default:
		return "", false
	}
}

func blockKey(event canonical.Event, kind string) streamBlockKey {
	return streamBlockKey{
		kind: kind, choiceIndex: event.ChoiceIndex, outputIndex: event.OutputIndex,
		contentIndex: event.ContentIndex, toolIndex: event.ToolIndex,
	}
}

func blockStartItem(source *canonical.Item, kind string, index int) canonical.Item {
	var item canonical.Item
	if source != nil {
		item = *source
	}
	switch kind {
	case streamBlockTool:
		item.Type = "function_call"
		if item.CallID == "" {
			item.CallID = item.ID
		}
		if item.CallID == "" {
			item.CallID = fmt.Sprintf("toolu_prism_%d", index)
		}
		if item.ID == "" {
			item.ID = item.CallID
		}
		if item.Name == "" {
			item.Name = "tool"
		}
	case streamBlockReasoning:
		item.Type, item.Role = "reasoning", canonical.RoleAssistant
		content := canonical.Content{Type: "reasoning_text"}
		if len(item.Content) > 0 {
			content = item.Content[0]
			content.Type = "reasoning_text"
			content.Text = ""
		}
		item.Content = []canonical.Content{content}
	default:
		item.Type, item.Role = "message", canonical.RoleAssistant
		item.Content = []canonical.Content{{Type: "output_text"}}
	}
	return item
}

func setBlockIndex(event *canonical.Event, index int) {
	event.OutputIndex = 0
	event.ToolIndex = 0
	event.ContentIndex = index
}

func requestExtras(data []byte) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	for _, key := range []string{
		"model", "max_tokens", "messages", "system", "tools", "tool_choice", "temperature", "top_p",
		"stop_sequences", "stream", "thinking", "metadata",
	} {
		delete(fields, key)
	}
	if len(fields) == 0 {
		return nil, nil
	}
	return map[string]json.RawMessage{extraRequest: mustJSON(fields)}, nil
}

func decodeMetadata(raw json.RawMessage) (string, map[string]string) {
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil {
		return "", nil
	}
	metadata := map[string]string{}
	for key, rawValue := range value {
		if text := rawString(rawValue); text != "" {
			metadata[key] = text
		}
	}
	user := metadata["user_id"]
	if len(metadata) == 0 {
		metadata = nil
	}
	return user, metadata
}

func applyRequestExtras(body map[string]any, raw json.RawMessage) error {
	if !presentJSON(raw) {
		return nil
	}
	var extras map[string]any
	if err := json.Unmarshal(raw, &extras); err != nil {
		return fmt.Errorf("%s: %w", extraRequest, err)
	}
	for key, value := range extras {
		if key == "metadata" {
			incoming, _ := value.(map[string]any)
			current, _ := body[key].(map[string]any)
			if current == nil {
				current = map[string]any{}
			}
			for metadataKey, metadataValue := range incoming {
				if _, exists := current[metadataKey]; !exists {
					current[metadataKey] = metadataValue
				}
			}
			body[key] = current
			continue
		}
		if _, exists := body[key]; !exists {
			body[key] = value
		}
	}
	return nil
}

func responseExtras(root map[string]json.RawMessage) map[string]json.RawMessage {
	return extraExcept(root, "id", "type", "role", "model", "content", "stop_reason", "usage", "error")
}

func decodeUsage(raw json.RawMessage) (*canonical.Usage, error) {
	if !presentJSON(raw) {
		return nil, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	cacheRead, cacheCreation := rawInt(fields["cache_read_input_tokens"]), rawInt(fields["cache_creation_input_tokens"])
	// anthropic 的 input_tokens 不含缓存读写；canonical 按 OpenAI 口径归一：InputTokens=全部输入，CachedInputTokens 为缓存读子集。
	usage := &canonical.Usage{
		InputTokens: rawInt(fields["input_tokens"]) + cacheRead + cacheCreation, OutputTokens: rawInt(fields["output_tokens"]),
		CachedInputTokens: cacheRead,
		Extra:             extraExcept(fields, "input_tokens", "output_tokens", "cache_read_input_tokens"),
	}
	usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	return usage, nil
}

func encodeUsage(usage *canonical.Usage) map[string]any {
	result := map[string]any{"input_tokens": 0, "output_tokens": 0}
	if usage == nil {
		return result
	}
	// canonical InputTokens 为 OpenAI 口径（含缓存读写），回写 anthropic wire 时扣除缓存部分还原 input_tokens。
	input := usage.InputTokens - usage.CachedInputTokens - rawInt(usage.Extra["cache_creation_input_tokens"])
	if input < 0 {
		input = 0
	}
	result["input_tokens"], result["output_tokens"] = input, usage.OutputTokens
	for key, raw := range usage.Extra {
		if key == "input_tokens" || key == "output_tokens" || !json.Valid(raw) {
			continue
		}
		var value any
		if json.Unmarshal(raw, &value) == nil {
			result[key] = value
		}
	}
	if usage.CachedInputTokens != 0 {
		result["cache_read_input_tokens"] = usage.CachedInputTokens
	}
	return result
}

func decodeErrorObject(root map[string]json.RawMessage, status int) *canonical.Error {
	if nested := root["error"]; presentJSON(nested) {
		var value map[string]json.RawMessage
		if json.Unmarshal(nested, &value) == nil {
			root = value
		}
	}
	typeName := rawString(root["type"])
	return &canonical.Error{
		Status: status, Type: typeName, Code: typeName, Message: rawString(root["message"]),
		Retryable: typeName == "overloaded_error" || typeName == "rate_limit_error" || status >= 500,
		Raw:       mustJSON(root),
	}
}

func encodeError(source *canonical.Error) map[string]any {
	if source == nil {
		return map[string]any{"type": "api_error", "message": "stream failed"}
	}
	typeName := source.Type
	if typeName == "" {
		typeName = source.Code
	}
	typeName = anthropicErrorType(typeName)
	message := source.Message
	if message == "" {
		message = "stream failed"
	}
	return map[string]any{"type": typeName, "message": message}
}

func anthropicErrorType(source string) string {
	switch source {
	case "invalid_request_error", "authentication_error", "billing_error", "permission_error", "not_found_error", "request_too_large", "rate_limit_error", "gateway_timeout", "api_error", "overloaded_error":
		return source
	default:
		return "api_error"
	}
}

func anthropicResponseStatus(stopReason string) string {
	switch stopReason {
	case "max_tokens", "model_context_window_exceeded", "pause_turn":
		return "incomplete"
	default:
		return "completed"
	}
}

func anthropicStopReason(response canonical.Response) any {
	finish := response.FinishReason
	if finish == "" && response.Status == "incomplete" {
		finish = "max_tokens"
	}
	if finish == "" {
		return nil
	}
	return anthropicFinishReason(finish)
}

func anthropicFinishReason(source string) string {
	switch source {
	case "", "stop", "end_turn":
		return "end_turn"
	case "tool_calls", "tool_use":
		return "tool_use"
	case "length", "incomplete", "max_tokens":
		return "max_tokens"
	case "content_filter", "refusal":
		return "refusal"
	default:
		return source
	}
}

func messageStart(event canonical.Event) (map[string]any, error) {
	response := canonical.Response{ID: event.ProviderResponseID, Status: "in_progress"}
	if event.Response != nil {
		response = *event.Response
	}
	if response.ID == "" {
		response.ID = "msg_" + fmt.Sprint(time.Now().UnixNano())
	}
	return map[string]any{
		"id": response.ID, "type": "message", "role": "assistant", "model": response.Model,
		"content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": encodeUsage(response.Usage),
	}, nil
}

func contentBlockStart(item *canonical.Item) (map[string]any, error) {
	if item == nil {
		return nil, errors.New("Anthropic content block start requires an item")
	}
	if item.Type == "function_call" {
		block, err := encodeToolUse(*item)
		if err != nil {
			return nil, err
		}
		block["input"] = map[string]any{}
		return block, nil
	}
	if len(item.Content) == 0 {
		return nil, errors.New("Anthropic content block start requires content")
	}
	var block map[string]any
	var err error
	if item.Type == "reasoning" {
		block, err = encodeAnthropicReasoning(*item, item.Content[0])
	} else {
		block, err = encodeAnthropicContent(item.Content[0])
	}
	if err != nil {
		return nil, err
	}
	if block["type"] == "text" {
		block["text"] = ""
	}
	if block["type"] == "thinking" {
		block["thinking"] = ""
	}
	return block, nil
}

func messageDeltaFrame(stopReason string, usage *canonical.Usage) ([]byte, error) {
	delta := map[string]any{"stop_reason": nil, "stop_sequence": nil}
	if stopReason != "" {
		delta["stop_reason"] = anthropicFinishReason(stopReason)
	}
	body := map[string]any{"type": "message_delta", "delta": delta}
	if usage != nil {
		body["usage"] = map[string]any{"output_tokens": usage.OutputTokens}
	}
	return sseJSON("message_delta", body)
}

func eventUsage(event canonical.Event) *canonical.Usage {
	if event.Usage != nil {
		return event.Usage
	}
	if event.Response != nil {
		return event.Response.Usage
	}
	return nil
}

func blockIndex(event canonical.Event) int {
	if event.ContentIndex != 0 {
		return event.ContentIndex
	}
	if event.ToolIndex != 0 {
		return event.ToolIndex
	}
	return event.OutputIndex
}

func sseJSON(name string, value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return sseFrame(name, raw), nil
}

func sseFrame(name string, raw []byte) []byte {
	return []byte("event: " + name + "\ndata: " + string(raw) + "\n\n")
}

func isProtectedResponseField(key string) bool {
	switch key {
	case "id", "type", "role", "model", "content", "stop_reason", "usage", "error":
		return true
	default:
		return false
	}
}

func normalizeArguments(raw json.RawMessage) json.RawMessage {
	if !presentJSON(raw) {
		return json.RawMessage(`{}`)
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return json.RawMessage(text)
	}
	return cloneRaw(raw)
}

func argumentObject(raw json.RawMessage) (map[string]any, error) {
	normalized := normalizeArguments(raw)
	var value map[string]any
	if err := json.Unmarshal(normalized, &value); err != nil {
		return nil, err
	}
	if value == nil {
		value = map[string]any{}
	}
	return value, nil
}

func rawValue(raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var value any
	if json.Unmarshal(raw, &value) == nil {
		return value, nil
	}
	return string(raw), nil
}

func rawBlockExtra(raw json.RawMessage) map[string]json.RawMessage {
	if !json.Valid(raw) {
		return nil
	}
	return map[string]json.RawMessage{extraRawBlock: cloneRaw(raw)}
}

func rawBlockMap(extra map[string]json.RawMessage) (map[string]any, error) {
	if len(extra) == 0 || !presentJSON(extra[extraRawBlock]) {
		return map[string]any{}, nil
	}
	var value map[string]any
	if err := json.Unmarshal(extra[extraRawBlock], &value); err != nil {
		return nil, err
	}
	return value, nil
}

func rawMap(raw json.RawMessage) (map[string]any, error) {
	if !presentJSON(raw) {
		return map[string]any{}, nil
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func extraExcept(source map[string]json.RawMessage, names ...string) map[string]json.RawMessage {
	result := cloneRawMap(source)
	for _, name := range names {
		delete(result, name)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func compactMap(source map[string]any) map[string]any {
	for key, value := range source {
		if value == nil || value == "" {
			delete(source, key)
		}
	}
	return source
}

func presentJSON(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

func rawString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}

func rawInt(raw json.RawMessage) int {
	var value int
	_ = json.Unmarshal(raw, &value)
	return value
}

func rawBool(raw json.RawMessage) *bool {
	var value bool
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	return &value
}

func intPointer(value int) *int {
	result := value
	return &result
}

func cloneFloat(source *float64) *float64 {
	if source == nil {
		return nil
	}
	value := *source
	return &value
}

func cloneRaw(source json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), source...)
}

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

func mustJSON(source any) json.RawMessage {
	if source == nil {
		return nil
	}
	raw, _ := json.Marshal(source)
	if bytes.Equal(raw, []byte("{}")) {
		return nil
	}
	return raw
}

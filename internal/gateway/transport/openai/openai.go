package openai

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/gateway/transport"
)

const (
	chatRequestExtras      = "openai_chat.request_extras"
	chatRawContent         = "openai_chat.raw"
	chatReasoningContent   = "openai_chat.reasoning_content"
	chatToolCalls          = "openai_chat.tool_calls"
	chatMessageName        = "openai_chat.message_name"
	chatRefusal            = "openai_chat.refusal"
	chatAnnotations        = "openai_chat.annotations"
	chatAudio              = "openai_chat.audio"
	chatChoiceIndex        = "openai_chat.choice_index"
	chatFinishReason       = "openai_chat.finish_reason"
	responsesRequestExtras = "openai_responses.request_extras"
)

func NewChat(client *http.Client) transport.Transport {
	return &base{client: client, id: transport.OpenAIChat, path: "/v1/chat/completions", plan: chatPlan, decode: decodeChat, event: decodeChatEvents}
}

func NewResponses(client *http.Client) transport.Transport {
	return &base{client: client, id: transport.OpenAIResponses, path: "/v1/responses", plan: responsesPlan, decode: decodeResponses, event: decodeResponseEvents}
}

func chatPlan(operation transport.Operation, request canonical.Request, features canonical.FeatureSet) transport.Plan {
	if operation != transport.OperationChat && operation != transport.OperationResponses && operation != transport.OperationMessages {
		return transport.Unsupported(operation, "unsupported OpenAI Chat operation")
	}
	if request.Background || request.PreviousResponseID != "" || len(request.Include) > 0 || hasVolcengineOptions(request.ProviderOptions.Volcengine) {
		return transport.Unsupported(operation, "OpenAI Chat cannot express Responses persistence or provider options")
	}
	if features.Has(canonical.FeatureVideo) {
		return transport.Unsupported(operation, "OpenAI Chat does not support video input")
	}
	for _, tool := range request.Tools {
		if tool.Type != "" && tool.Type != "function" {
			return transport.Unsupported(operation, "OpenAI Chat only supports function tools")
		}
	}
	if request.Reasoning != nil && (request.Reasoning.Summary != "" || !transport.RawObjectHasOnlyFields(request.Reasoning.Raw, "effort")) {
		return transport.Unsupported(operation, "OpenAI Chat cannot preserve these reasoning controls")
	}
	policy := transport.ExtensionPolicy{
		Client: map[string]bool{chatRequestExtras: true},
		Item: map[string]bool{
			transport.ExtensionChatChoiceIndex: true, transport.ExtensionChatContentMode: true,
			transport.ExtensionChatFinishReason: true, transport.ExtensionChatReasoningContent: true,
			transport.ExtensionChatToolCalls: true, chatMessageName: true, chatRefusal: true,
			chatAnnotations: true, chatAudio: true,
		},
		Content: map[string]bool{transport.ExtensionChatRawContent: true},
	}
	if extension := transport.UnsupportedRequestExtension(request, policy); extension != "" {
		return transport.Unsupported(operation, "OpenAI Chat cannot preserve "+extension)
	}
	if operation != transport.OperationChat && transport.ToolChoiceRawHasExtensions(request.ToolChoice, operation) {
		return transport.Unsupported(operation, "OpenAI Chat cannot preserve tool_choice extensions during conversion")
	}
	if operation == transport.OperationChat {
		return transport.Exact(operation, features)
	}
	return transport.Converted(operation, transport.OperationChat, features)
}

func responsesPlan(operation transport.Operation, request canonical.Request, features canonical.FeatureSet) transport.Plan {
	if operation != transport.OperationResponses && operation != transport.OperationChat && operation != transport.OperationMessages {
		return transport.Unsupported(operation, "unsupported OpenAI Responses operation")
	}
	if hasVolcengineOptions(request.ProviderOptions.Volcengine) {
		return transport.Unsupported(operation, "OpenAI Responses cannot preserve Volcengine provider options")
	}
	if len(request.Stop) > 0 {
		return transport.Unsupported(operation, "OpenAI Responses cannot preserve stop sequences")
	}
	clientExtensions := map[string]bool{}
	if operation == transport.OperationResponses {
		clientExtensions[responsesRequestExtras] = true
	}
	policy := transport.ExtensionPolicy{
		Client: clientExtensions,
		Item: map[string]bool{
			transport.ExtensionChatChoiceIndex: true, transport.ExtensionChatContentMode: true,
			transport.ExtensionChatFinishReason: true, transport.ExtensionChatToolCalls: true,
		},
		Content:                   map[string]bool{transport.ExtensionResponsesAudioOptions: true},
		PreserveGenericItemFields: true, PreserveGenericContentFields: true, PreserveToolOptions: true,
	}
	if extension := transport.UnsupportedRequestExtension(request, policy); extension != "" {
		return transport.Unsupported(operation, "OpenAI Responses cannot preserve "+extension)
	}
	if operation != transport.OperationResponses && transport.ToolChoiceRawHasExtensions(request.ToolChoice, operation) {
		return transport.Unsupported(operation, "OpenAI Responses cannot preserve tool_choice extensions during conversion")
	}
	if operation != transport.OperationResponses && request.Reasoning != nil && !transport.RawObjectHasOnlyFields(request.Reasoning.Raw, "effort", "summary") {
		return transport.Unsupported(operation, "OpenAI Responses cannot preserve foreign reasoning extensions")
	}
	if operation == transport.OperationChat || operation == transport.OperationMessages {
		return transport.Converted(operation, transport.OperationResponses, features)
	}
	return transport.Exact(operation, features)
}

func hasVolcengineOptions(options *canonical.VolcengineOptions) bool {
	return options != nil && (len(options.Thinking) > 0 || len(options.Caching) > 0 || len(options.Session) > 0 || len(options.ContextManagement) > 0 || options.ExpireAt != nil || len(options.Unknown) > 0)
}

func encode(invocation transport.Invocation, responses bool) ([]byte, error) {
	request := invocation.Request
	targetOperation := transport.OperationChat
	if responses {
		targetOperation = transport.OperationResponses
	}
	if request.ToolChoice != nil && invocation.Operation != targetOperation {
		choice := *request.ToolChoice
		choice.Raw = nil
		request.ToolChoice = &choice
	}
	body := make(map[string]any)
	if responses {
		items, err := responseItems(request.Items)
		if err != nil {
			return nil, err
		}
		body["input"] = items
		if request.Instructions != "" {
			body["instructions"] = request.Instructions
		}
		if request.MaxOutputTokens != nil {
			body["max_output_tokens"] = *request.MaxOutputTokens
		}
		if request.PreviousResponseID != "" {
			body["previous_response_id"] = request.PreviousResponseID
		}
		if request.Store != nil {
			body["store"] = *request.Store
		}
		if request.Background {
			body["background"] = true
		}
		if len(request.Include) > 0 {
			body["include"] = request.Include
		}
		if len(request.Tools) > 0 {
			tools, err := responseTools(request.Tools)
			if err != nil {
				return nil, err
			}
			body["tools"] = tools
		}
		if request.ResponseFormat != nil {
			format, err := responseFormat(request.ResponseFormat)
			if err != nil {
				return nil, err
			}
			body["text"] = map[string]any{"format": format}
		}
		if request.Reasoning != nil {
			reasoning, err := reasoningObject(request.Reasoning)
			if err != nil {
				return nil, err
			}
			if len(reasoning) > 0 {
				body["reasoning"] = reasoning
			}
		}
		if err := mergeRequestExtras(body, request.ClientExtensions[responsesRequestExtras], responsesRequestExtras); err != nil {
			return nil, err
		}
	} else {
		messages, err := chatMessages(request)
		if err != nil {
			return nil, err
		}
		body["messages"] = messages
		if request.MaxOutputTokens != nil {
			body["max_completion_tokens"] = *request.MaxOutputTokens
		}
		if request.Store != nil {
			body["store"] = *request.Store
		}
		if len(request.Tools) > 0 {
			tools, err := chatTools(request.Tools)
			if err != nil {
				return nil, err
			}
			body["tools"] = tools
		}
		if request.ResponseFormat != nil {
			format, err := chatResponseFormat(request.ResponseFormat)
			if err != nil {
				return nil, err
			}
			body["response_format"] = format
		}
		if request.Reasoning != nil && request.Reasoning.Effort != "" {
			body["reasoning_effort"] = request.Reasoning.Effort
		}
		if request.Stream {
			body["stream_options"] = map[string]any{"include_usage": true}
		}
		if err := mergeRequestExtras(body, request.ClientExtensions[chatRequestExtras], chatRequestExtras); err != nil {
			return nil, err
		}
	}

	if request.Temperature != nil {
		body["temperature"] = *request.Temperature
	}
	if request.TopP != nil {
		body["top_p"] = *request.TopP
	}
	if !responses && len(request.Stop) > 0 {
		body["stop"] = request.Stop
	}
	if request.ToolChoice != nil {
		choice, err := toolChoice(request.ToolChoice, responses)
		if err != nil {
			return nil, err
		}
		if choice != nil {
			body["tool_choice"] = choice
		}
	}
	if request.ParallelToolCalls != nil {
		body["parallel_tool_calls"] = *request.ParallelToolCalls
	}
	if len(request.Metadata) > 0 {
		body["metadata"] = request.Metadata
	}
	if request.User != "" {
		body["user"] = request.User
	}
	if len(request.Modalities) > 0 {
		body["modalities"] = request.Modalities
	}
	return json.Marshal(body)
}

func mergeRequestExtras(body map[string]any, raw json.RawMessage, name string) error {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	var extras map[string]any
	if err := json.Unmarshal(raw, &extras); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	for key, value := range extras {
		body[key] = value
	}
	return nil
}

func chatMessages(request canonical.Request) ([]map[string]any, error) {
	result := make([]map[string]any, 0, len(request.Items)+1)
	if request.Instructions != "" {
		result = append(result, map[string]any{"role": "developer", "content": request.Instructions})
	}
	for _, item := range request.Items {
		switch item.Type {
		case "function_call_output":
			result = append(result, map[string]any{"role": "tool", "tool_call_id": item.CallID, "content": rawText(item.Output)})
			continue
		case "function_call":
			callID := item.CallID
			if callID == "" {
				callID = item.ID
			}
			call := map[string]any{"id": callID, "type": "function", "function": map[string]any{"name": item.Name, "arguments": rawText(item.Arguments)}}
			if len(result) > 0 && result[len(result)-1]["role"] == canonical.RoleAssistant {
				calls, _ := result[len(result)-1]["tool_calls"].([]any)
				result[len(result)-1]["tool_calls"] = append(calls, call)
				result[len(result)-1]["content"] = nil
			} else {
				result = append(result, map[string]any{"role": canonical.RoleAssistant, "content": nil, "tool_calls": []any{call}})
			}
			continue
		case "reasoning":
			parts := make([]string, 0, len(item.Content))
			for _, part := range item.Content {
				if part.Type != "" && part.Type != "reasoning_text" && part.Type != "text" && part.Type != "output_text" {
					return nil, fmt.Errorf("OpenAI Chat cannot encode reasoning content %q", part.Type)
				}
				parts = append(parts, part.Text)
			}
			reasoning := strings.Join(parts, "")
			if len(result) > 0 && result[len(result)-1]["role"] == canonical.RoleAssistant {
				result[len(result)-1]["reasoning_content"] = rawTextAppend(result[len(result)-1]["reasoning_content"], reasoning)
			} else {
				result = append(result, map[string]any{"role": canonical.RoleAssistant, "content": nil, "reasoning_content": reasoning})
			}
			continue
		}

		content, err := chatContent(item.Content)
		if err != nil {
			return nil, err
		}
		switch rawString(item.Extra[transport.ExtensionChatContentMode]) {
		case "array":
			if text, ok := content.(string); ok {
				content = []any{map[string]any{"type": "text", "text": text}}
			}
		case "null":
			content = nil
		}
		message := map[string]any{"role": item.Role, "content": content}
		if item.Role == "" {
			message["role"] = canonical.RoleUser
		}
		if reasoning := rawString(item.Extra[chatReasoningContent]); reasoning != "" {
			message["reasoning_content"] = reasoning
		}
		if name := rawString(item.Extra[chatMessageName]); name != "" {
			message["name"] = name
		} else if item.Name != "" {
			message["name"] = item.Name
		}
		for key, field := range map[string]string{chatRefusal: "refusal", chatAnnotations: "annotations", chatAudio: "audio"} {
			if raw := item.Extra[key]; transport.HasJSONValue(raw) {
				var value any
				if err := json.Unmarshal(raw, &value); err != nil {
					return nil, fmt.Errorf("chat message %s: %w", field, err)
				}
				message[field] = value
			}
		}
		if raw := item.Extra[chatToolCalls]; len(raw) > 0 {
			var calls any
			if err := json.Unmarshal(raw, &calls); err != nil {
				return nil, fmt.Errorf("chat tool calls: %w", err)
			}
			message["tool_calls"] = calls
			if len(item.Content) == 0 {
				message["content"] = nil
			}
		}
		result = append(result, message)
	}
	return result, nil
}

func responseItems(items []canonical.Item) ([]any, error) {
	result := make([]any, 0, len(items))
	for _, item := range items {
		value := nativeExtras(item.Extra)
		typeName := item.Type
		if typeName == "" {
			typeName = "message"
		}
		value["type"] = typeName
		if item.ID != "" {
			value["id"] = item.ID
		}
		if item.Status != "" {
			value["status"] = item.Status
		}
		if item.Name != "" {
			value["name"] = item.Name
		}
		if item.CallID != "" {
			value["call_id"] = item.CallID
		}
		if len(item.Arguments) > 0 {
			value["arguments"] = rawText(item.Arguments)
		}
		if len(item.Output) > 0 {
			value["output"] = responseOutput(item.Output)
		}
		switch typeName {
		case "function_call":
			callID := item.CallID
			if callID == "" {
				callID = item.ID
			}
			value["call_id"] = callID
			value["name"] = item.Name
			value["arguments"] = rawText(item.Arguments)
		case "function_call_output":
			callID := item.CallID
			if callID == "" {
				callID = item.ID
			}
			value["call_id"] = callID
			value["output"] = responseOutput(item.Output)
		default:
			if item.Role != "" {
				value["role"] = item.Role
			}
			content, err := responseContent(item.Content)
			if err != nil {
				return nil, err
			}
			if len(content) > 0 {
				value["content"] = content
			}
		}
		result = append(result, value)

		if rawCalls := item.Extra[chatToolCalls]; typeName == "message" && len(rawCalls) > 0 {
			var calls []struct {
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			}
			if err := json.Unmarshal(rawCalls, &calls); err != nil {
				return nil, err
			}
			for _, call := range calls {
				result = append(result, map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Function.Name, "arguments": call.Function.Arguments})
			}
		}
	}
	return result, nil
}

func chatContent(content []canonical.Content) (any, error) {
	if len(content) == 0 {
		return nil, nil
	}
	if len(content) == 1 && (content[0].Type == "" || content[0].Type == "input_text" || content[0].Type == "output_text" || content[0].Type == "text") {
		return content[0].Text, nil
	}
	result := make([]any, 0, len(content))
	for _, part := range content {
		switch part.Type {
		case "", "input_text", "output_text", "text":
			result = append(result, map[string]any{"type": "text", "text": part.Text})
		case "input_image", "image", "image_url":
			url := part.URL
			if url == "" && part.Data != "" {
				url = makeDataURL(part.Data, part.MediaType, "image/png")
			}
			image := map[string]any{"url": url}
			if part.Detail != "" {
				image["detail"] = part.Detail
			}
			result = append(result, map[string]any{"type": "image_url", "image_url": image})
		case "input_file", "file":
			if part.URL != "" {
				file := map[string]any{"url": part.URL}
				if part.Filename != "" {
					file["filename"] = part.Filename
				}
				if part.MediaType != "" {
					file["content_type"] = part.MediaType
				}
				result = append(result, map[string]any{"type": "file_url", "file_url": file})
				continue
			}
			file := map[string]any{}
			if part.FileID != "" {
				file["file_id"] = part.FileID
			}
			if part.Data != "" {
				file["file_data"] = part.Data
			}
			if part.Filename != "" {
				file["filename"] = part.Filename
			}
			if part.MediaType != "" {
				file["content_type"] = part.MediaType
			}
			result = append(result, map[string]any{"type": "file", "file": file})
		case "input_audio", "audio":
			format := part.Format
			if format == "" {
				format = audioFormat(part.MediaType)
			}
			result = append(result, map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": stripDataURL(part.Data), "format": format}})
		default:
			raw := part.Extra[chatRawContent]
			if len(raw) == 0 || !json.Valid(raw) {
				return nil, fmt.Errorf("OpenAI Chat cannot encode content type %q", part.Type)
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

func responseContent(content []canonical.Content) ([]any, error) {
	result := make([]any, 0, len(content))
	for _, part := range content {
		value := nativeExtras(part.Extra)
		typeName := part.Type
		if typeName == "" || typeName == "text" {
			typeName = "input_text"
		}
		value["type"] = typeName
		switch typeName {
		case "input_text", "output_text", "reasoning_text":
			value["text"] = part.Text
		case "input_image", "image":
			if part.URL != "" {
				value["image_url"] = part.URL
			} else if part.Data != "" {
				value["image_url"] = makeDataURL(part.Data, part.MediaType, "image/png")
			} else if part.FileID != "" {
				value["file_id"] = part.FileID
			}
			if part.Detail != "" {
				value["detail"] = part.Detail
			}
		case "input_file", "file":
			if part.FileID != "" {
				value["file_id"] = part.FileID
			}
			if part.URL != "" {
				value["file_url"] = part.URL
			}
			if part.Data != "" {
				value["file_data"] = part.Data
			}
			if part.Filename != "" {
				value["filename"] = part.Filename
			}
			if part.MediaType != "" {
				value["content_type"] = part.MediaType
			}
		case "input_audio", "audio":
			format := part.Format
			if format == "" {
				format = audioFormat(part.MediaType)
			}
			audio := map[string]any{"data": stripDataURL(part.Data), "format": format}
			if raw := part.Extra[transport.ExtensionResponsesAudioOptions]; transport.HasJSONValue(raw) {
				var extras map[string]any
				if err := json.Unmarshal(raw, &extras); err != nil {
					return nil, fmt.Errorf("OpenAI Responses input audio options: %w", err)
				}
				for key, extra := range extras {
					audio[key] = extra
				}
			}
			value["input_audio"] = audio
		case "input_video", "video":
			if part.URL != "" {
				value["video_url"] = part.URL
			} else if part.Data != "" {
				value["video_url"] = makeDataURL(part.Data, part.MediaType, "video/mp4")
			} else if part.FileID != "" {
				value["file_id"] = part.FileID
			}
		default:
			if part.Text != "" {
				value["text"] = part.Text
			}
		}
		if part.Transcript != "" {
			value["transcript"] = part.Transcript
		}
		result = append(result, value)
	}
	return result, nil
}

func chatTools(tools []canonical.Tool) ([]any, error) {
	result := make([]any, 0, len(tools))
	for _, tool := range tools {
		if tool.Type != "" && tool.Type != "function" {
			return nil, fmt.Errorf("OpenAI Chat cannot encode tool %q", tool.Type)
		}
		function := map[string]any{"name": tool.Name}
		if tool.Description != "" {
			function["description"] = tool.Description
		}
		if len(tool.InputSchema) > 0 {
			function["parameters"] = json.RawMessage(tool.InputSchema)
		}
		if tool.Strict != nil {
			function["strict"] = *tool.Strict
		}
		result = append(result, map[string]any{"type": "function", "function": function})
	}
	return result, nil
}

func responseTools(tools []canonical.Tool) ([]any, error) {
	result := make([]any, 0, len(tools))
	for _, tool := range tools {
		value := map[string]any{}
		if len(tool.Options) > 0 {
			if err := json.Unmarshal(tool.Options, &value); err != nil {
				return nil, fmt.Errorf("tool %q options: %w", tool.Type, err)
			}
		}
		typeName := tool.Type
		if typeName == "" {
			typeName = "function"
		}
		value["type"] = typeName
		if tool.Name != "" {
			value["name"] = tool.Name
		}
		if tool.Description != "" {
			value["description"] = tool.Description
		}
		if len(tool.InputSchema) > 0 {
			value["parameters"] = json.RawMessage(tool.InputSchema)
		}
		if tool.Strict != nil {
			value["strict"] = *tool.Strict
		}
		result = append(result, value)
	}
	return result, nil
}

func toolChoice(choice *canonical.ToolChoice, responses bool) (any, error) {
	if len(bytes.TrimSpace(choice.Raw)) > 0 && !bytes.Equal(bytes.TrimSpace(choice.Raw), []byte("null")) {
		var value any
		if err := json.Unmarshal(choice.Raw, &value); err != nil {
			return nil, fmt.Errorf("tool_choice: %w", err)
		}
		return value, nil
	}
	mode := choice.Mode
	if mode == "" {
		mode = choice.Type
	}
	switch mode {
	case "", "auto":
		return "auto", nil
	case "none":
		return "none", nil
	case "required", "any":
		return "required", nil
	}
	typeName := choice.Type
	if typeName == "" || typeName == "required" || typeName == "any" || typeName == "tool" {
		typeName = "function"
	}
	if responses {
		value := map[string]any{"type": typeName}
		if choice.Name != "" {
			value["name"] = choice.Name
		}
		return value, nil
	}
	if typeName != "function" {
		return nil, fmt.Errorf("OpenAI Chat cannot encode tool choice %q", typeName)
	}
	return map[string]any{"type": "function", "function": map[string]any{"name": choice.Name}}, nil
}

func chatResponseFormat(format *canonical.ResponseFormat) (map[string]any, error) {
	if format.Type != "json_schema" {
		return map[string]any{"type": format.Type}, nil
	}
	schema, err := responseFormat(format)
	if err != nil {
		return nil, err
	}
	delete(schema, "type")
	return map[string]any{"type": "json_schema", "json_schema": schema}, nil
}

func responseFormat(format *canonical.ResponseFormat) (map[string]any, error) {
	value := map[string]any{"type": format.Type}
	if format.Name != "" {
		value["name"] = format.Name
	}
	if format.Description != "" {
		value["description"] = format.Description
	}
	if len(format.Schema) > 0 {
		if !json.Valid(format.Schema) {
			return nil, fmt.Errorf("response format schema is invalid JSON")
		}
		value["schema"] = json.RawMessage(format.Schema)
	}
	if format.Strict != nil {
		value["strict"] = *format.Strict
	}
	return value, nil
}

func reasoningObject(reasoning *canonical.Reasoning) (map[string]any, error) {
	value := map[string]any{}
	if len(bytes.TrimSpace(reasoning.Raw)) > 0 && !bytes.Equal(bytes.TrimSpace(reasoning.Raw), []byte("null")) {
		if err := json.Unmarshal(reasoning.Raw, &value); err != nil {
			return nil, fmt.Errorf("reasoning: %w", err)
		}
	}
	if reasoning.Effort != "" {
		value["effort"] = reasoning.Effort
	}
	if reasoning.Summary != "" {
		value["summary"] = reasoning.Summary
	}
	return value, nil
}

type chatResponseWire struct {
	ID      string           `json:"id"`
	Object  string           `json:"object"`
	Created int64            `json:"created"`
	Model   string           `json:"model"`
	Choices []chatChoiceWire `json:"choices"`
	Usage   *chatUsageWire   `json:"usage"`
}

type chatChoiceWire struct {
	Index        int             `json:"index"`
	Message      chatMessageWire `json:"message"`
	FinishReason string          `json:"finish_reason"`
}

type chatMessageWire struct {
	Role             string          `json:"role"`
	Content          json.RawMessage `json:"content"`
	ReasoningContent string          `json:"reasoning_content"`
	Refusal          string          `json:"refusal"`
	ToolCalls        []chatCallWire  `json:"tool_calls"`
	Audio            *chatAudioWire  `json:"audio"`
}

type chatCallWire struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type chatAudioWire struct {
	ID         string `json:"id"`
	Data       string `json:"data"`
	ExpiresAt  int64  `json:"expires_at"`
	Transcript string `json:"transcript"`
}

type chatUsageWire struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	TotalTokens         int `json:"total_tokens"`
	PromptTokensDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

func decodeChat(raw []byte, route transport.Route) (canonical.Response, error) {
	var wire chatResponseWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return canonical.Response{}, err
	}
	model := route.PublicModel
	if model == "" {
		model = wire.Model
	}
	response := canonical.Response{ID: wire.ID, ProviderResponseID: wire.ID, Model: model, CreatedAt: wire.Created, Status: "completed", ProviderExtensions: topLevelExtras(raw, "id", "object", "created", "model", "choices", "usage")}
	for _, choice := range wire.Choices {
		extra := map[string]json.RawMessage{chatChoiceIndex: must(choice.Index), chatFinishReason: must(choice.FinishReason)}
		content, err := decodeChatContent(choice.Message.Content)
		if err != nil {
			return canonical.Response{}, err
		}
		if choice.Message.Refusal != "" {
			content = append(content, canonical.Content{Type: "refusal", Text: choice.Message.Refusal})
		}
		if choice.Message.Audio != nil {
			content = append(content, canonical.Content{Type: "output_audio", Data: choice.Message.Audio.Data, Transcript: choice.Message.Audio.Transcript, Extra: map[string]json.RawMessage{"id": must(choice.Message.Audio.ID), "expires_at": must(choice.Message.Audio.ExpiresAt)}})
		}
		if len(content) > 0 || len(choice.Message.ToolCalls) == 0 {
			role := canonical.Role(choice.Message.Role)
			if role == "" {
				role = canonical.RoleAssistant
			}
			response.Output = append(response.Output, canonical.Item{Type: "message", Role: role, Status: "completed", Content: content, Extra: extra})
		}
		if choice.Message.ReasoningContent != "" {
			response.Output = append(response.Output, canonical.Item{Type: "reasoning", Role: canonical.RoleAssistant, Status: "completed", Content: []canonical.Content{{Type: "reasoning_text", Text: choice.Message.ReasoningContent}}, Extra: map[string]json.RawMessage{chatChoiceIndex: must(choice.Index)}})
		}
		for _, call := range choice.Message.ToolCalls {
			response.Output = append(response.Output, canonical.Item{Type: "function_call", Role: canonical.RoleAssistant, Status: "completed", ID: call.ID, CallID: call.ID, Name: call.Function.Name, Arguments: normalizeArguments([]byte(call.Function.Arguments)), Extra: map[string]json.RawMessage{chatChoiceIndex: must(choice.Index)}})
		}
		response.FinishReason = choice.FinishReason
	}
	response.Usage = chatUsage(wire.Usage)
	return response, nil
}

type responsesWire struct {
	ID                string            `json:"id"`
	Model             string            `json:"model"`
	Status            string            `json:"status"`
	CreatedAt         int64             `json:"created_at"`
	Output            []json.RawMessage `json:"output"`
	Usage             *responsesUsage   `json:"usage"`
	Error             *responseError    `json:"error"`
	IncompleteDetails json.RawMessage   `json:"incomplete_details"`
	Metadata          map[string]string `json:"metadata"`
}

type responsesUsage struct {
	InputTokens       int `json:"input_tokens"`
	OutputTokens      int `json:"output_tokens"`
	TotalTokens       int `json:"total_tokens"`
	InputTokenDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	OutputTokenDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}

type responseError struct {
	Status  int    `json:"status"`
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Param   any    `json:"param"`
}

func decodeResponses(raw []byte, route transport.Route) (canonical.Response, error) {
	var wire responsesWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return canonical.Response{}, err
	}
	model := route.PublicModel
	if model == "" {
		model = wire.Model
	}
	response := canonical.Response{ID: wire.ID, ProviderResponseID: wire.ID, Model: model, Status: wire.Status, CreatedAt: wire.CreatedAt, Usage: responsesUsageValue(wire.Usage), IncompleteDetails: append(json.RawMessage(nil), wire.IncompleteDetails...), Metadata: cloneStringMap(wire.Metadata), ProviderExtensions: topLevelExtras(raw, "id", "object", "model", "status", "created_at", "output", "usage", "error", "incomplete_details", "metadata")}
	if response.Status == "" {
		response.Status = "completed"
	}
	for _, rawItem := range wire.Output {
		item, err := decodeResponseItem(rawItem)
		if err != nil {
			return canonical.Response{}, err
		}
		response.Output = append(response.Output, item)
	}
	if wire.Error != nil {
		response.Error = canonicalResponseError(wire.Error, raw)
	}
	return response, nil
}

func decodeResponseItem(raw json.RawMessage) (canonical.Item, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return canonical.Item{}, err
	}
	item := canonical.Item{ID: rawString(fields["id"]), Type: rawString(fields["type"]), Role: canonical.Role(rawString(fields["role"])), Name: rawString(fields["name"]), CallID: rawString(fields["call_id"]), Status: rawString(fields["status"]), Extra: exceptRaw(fields, "id", "type", "role", "name", "call_id", "status", "arguments", "output", "content")}
	if item.Type == "" && item.Role != "" {
		item.Type = "message"
	}
	if rawArguments := fields["arguments"]; len(rawArguments) > 0 {
		item.Arguments = normalizeArguments(rawArguments)
	}
	item.Output = append(json.RawMessage(nil), fields["output"]...)
	if rawContent := fields["content"]; len(rawContent) > 0 {
		content, err := decodeResponseContent(rawContent)
		if err != nil {
			return canonical.Item{}, err
		}
		item.Content = content
	}
	return item, nil
}

func decodeResponseContent(raw json.RawMessage) ([]canonical.Content, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return []canonical.Content{{Type: "output_text", Text: text}}, nil
	}
	var values []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	result := make([]canonical.Content, 0, len(values))
	for _, fields := range values {
		typeName := rawString(fields["type"])
		part := canonical.Content{Type: typeName, Text: firstRawString(fields, "text", "refusal"), URL: firstRawString(fields, "image_url", "video_url", "audio_url", "file_url"), FileID: rawString(fields["file_id"]), Filename: rawString(fields["filename"]), Data: firstRawString(fields, "file_data", "data", "audio"), MediaType: firstRawString(fields, "media_type", "content_type"), Format: rawString(fields["format"]), Detail: rawString(fields["detail"]), Transcript: rawString(fields["transcript"]), Extra: exceptRaw(fields, "type", "text", "refusal", "image_url", "video_url", "audio_url", "file_url", "file_id", "filename", "file_data", "data", "audio", "media_type", "content_type", "format", "detail", "transcript")}
		if nested := fields["input_audio"]; len(nested) > 0 {
			var audio map[string]json.RawMessage
			if err := json.Unmarshal(nested, &audio); err != nil {
				return nil, err
			}
			part.Data, part.Format = rawString(audio["data"]), rawString(audio["format"])
		}
		result = append(result, part)
	}
	return result, nil
}

func decodeChatContent(raw json.RawMessage) ([]canonical.Content, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	var text string
	if json.Unmarshal(trimmed, &text) == nil {
		return []canonical.Content{{Type: "output_text", Text: text}}, nil
	}
	var parts []map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &parts); err != nil {
		return nil, err
	}
	result := make([]canonical.Content, 0, len(parts))
	for _, part := range parts {
		typeName := rawString(part["type"])
		content := canonical.Content{Type: typeName, Text: firstRawString(part, "text", "refusal"), Extra: exceptRaw(part, "type", "text", "refusal", "image_url", "file", "input_audio")}
		if typeName == "text" {
			content.Type = "output_text"
		}
		result = append(result, content)
	}
	return result, nil
}

func decodeChatEvents(_ string, raw []byte, _ transport.Route) ([]canonical.Event, error) {
	var wire struct {
		Error   json.RawMessage `json:"error"`
		Choices []struct {
			Index int `json:"index"`
			Delta struct {
				Role             string         `json:"role"`
				Content          *string        `json:"content"`
				ReasoningContent string         `json:"reasoning_content"`
				Refusal          string         `json:"refusal"`
				ToolCalls        []chatCallWire `json:"tool_calls"`
				Audio            *chatAudioWire `json:"audio"`
			} `json:"delta"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage *chatUsageWire `json:"usage"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, err
	}
	if len(wire.Error) > 0 && !bytes.Equal(bytes.TrimSpace(wire.Error), []byte("null")) {
		return []canonical.Event{{Type: canonical.EventError, Raw: append(json.RawMessage(nil), raw...), Error: decodeErrorValue(wire.Error, 0)}}, nil
	}
	usage := chatUsage(wire.Usage)
	events := make([]canonical.Event, 0)
	for _, choice := range wire.Choices {
		base := canonical.Event{ChoiceIndex: choice.Index, Raw: append(json.RawMessage(nil), raw...)}
		if choice.Delta.Role != "" {
			event := base
			event.Type = canonical.EventOutputItemAdded
			event.Item = &canonical.Item{Type: "message", Role: canonical.Role(choice.Delta.Role)}
			events = append(events, event)
		}
		if choice.Delta.Content != nil {
			event := base
			event.Type, event.Delta = canonical.EventTextDelta, *choice.Delta.Content
			events = append(events, event)
		}
		if choice.Delta.ReasoningContent != "" {
			event := base
			event.Type, event.Delta = canonical.EventReasoningDelta, choice.Delta.ReasoningContent
			events = append(events, event)
		}
		if choice.Delta.Refusal != "" {
			event := base
			event.Type, event.Delta = canonical.EventTextDelta, choice.Delta.Refusal
			event.Item = &canonical.Item{Type: "message", Content: []canonical.Content{{Type: "refusal"}}}
			events = append(events, event)
		}
		if choice.Delta.Audio != nil {
			event := base
			event.Type = canonical.EventOutputItemAdded
			event.Item = &canonical.Item{
				Type: "message",
				Content: []canonical.Content{{
					Type:       "output_audio",
					Data:       choice.Delta.Audio.Data,
					Transcript: choice.Delta.Audio.Transcript,
					Extra:      map[string]json.RawMessage{"id": must(choice.Delta.Audio.ID), "expires_at": must(choice.Delta.Audio.ExpiresAt)},
				}},
			}
			events = append(events, event)
		}
		for _, call := range choice.Delta.ToolCalls {
			event := base
			event.ToolIndex = call.Index
			event.Item = &canonical.Item{Type: "function_call", ID: call.ID, CallID: call.ID, Name: call.Function.Name}
			if call.Function.Arguments != "" {
				event.Type, event.Delta = canonical.EventToolArgumentsDelta, call.Function.Arguments
			} else {
				event.Type = canonical.EventOutputItemAdded
			}
			events = append(events, event)
		}
		if choice.FinishReason != "" {
			event := base
			event.Type = canonical.EventCompleted
			if choice.FinishReason == "length" {
				event.Type = canonical.EventIncomplete
			}
			event.Usage = usage
			event.Item = &canonical.Item{Extra: map[string]json.RawMessage{chatFinishReason: must(choice.FinishReason)}}
			events = append(events, event)
		}
	}
	if len(wire.Choices) == 0 && usage != nil {
		events = append(events, canonical.Event{Type: canonical.EventUsage, Usage: usage, Raw: append(json.RawMessage(nil), raw...)})
	}
	if len(events) == 0 {
		events = append(events, canonical.Event{Type: canonical.EventRaw, RawType: "openai.chat.chunk", Raw: append(json.RawMessage(nil), raw...)})
	}
	return events, nil
}

func decodeChatEvent(name string, raw []byte, route transport.Route) (canonical.Event, error) {
	events, err := decodeChatEvents(name, raw, route)
	if err != nil || len(events) == 0 {
		return canonical.Event{}, err
	}
	return events[0], nil
}

func decodeResponseEvents(name string, raw []byte, route transport.Route) ([]canonical.Event, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	if name == "" {
		name = rawString(fields["type"])
	}
	eventType := responseEventType(name)
	event := canonical.Event{Type: eventType, RawType: name, Raw: append(json.RawMessage(nil), raw...), SequenceNumber: rawInt64(fields["sequence_number"]), OutputIndex: rawInt(fields["output_index"]), ContentIndex: rawInt(fields["content_index"]), ToolIndex: rawInt(fields["tool_index"]), Delta: firstRawString(fields, "delta", "text", "arguments"), ProviderResponseID: firstRawString(fields, "response_id")}
	if responseRaw := fields["response"]; len(responseRaw) > 0 {
		response, err := decodeResponses(responseRaw, route)
		if err != nil {
			return nil, err
		}
		event.Response = &response
		event.Usage = response.Usage
		event.ProviderResponseID = response.ProviderResponseID
		if response.Error != nil {
			event.Error = response.Error
		}
	}
	if itemRaw := fields["item"]; len(itemRaw) > 0 {
		item, err := decodeResponseItem(itemRaw)
		if err != nil {
			return nil, err
		}
		event.Item = &item
	}
	if event.Item == nil {
		itemID := rawString(fields["item_id"])
		callID := rawString(fields["call_id"])
		if itemID != "" || callID != "" {
			event.Item = &canonical.Item{ID: itemID, CallID: callID}
			if eventType == canonical.EventToolArgumentsDelta {
				event.Item.Type = "function_call"
			}
		}
	}
	if usageRaw := fields["usage"]; len(usageRaw) > 0 {
		var usage responsesUsage
		if err := json.Unmarshal(usageRaw, &usage); err != nil {
			return nil, err
		}
		event.Usage = responsesUsageValue(&usage)
	}
	if eventType == canonical.EventError {
		errorRaw := fields["error"]
		if len(errorRaw) == 0 {
			errorRaw = raw
		}
		event.Error = decodeErrorValue(errorRaw, 0)
	}
	return []canonical.Event{event}, nil
}

func decodeResponseEvent(name string, raw []byte, route transport.Route) (canonical.Event, error) {
	events, err := decodeResponseEvents(name, raw, route)
	if err != nil || len(events) == 0 {
		return canonical.Event{}, err
	}
	return events[0], nil
}

func responseEventType(name string) canonical.EventType {
	switch name {
	case string(canonical.EventResponseCreated), string(canonical.EventResponseQueued), string(canonical.EventResponseInProgress), string(canonical.EventOutputItemAdded), string(canonical.EventOutputItemDone), string(canonical.EventContentPartAdded), string(canonical.EventContentPartDone), string(canonical.EventTextDelta), string(canonical.EventTextDone), string(canonical.EventToolArgumentsDelta), string(canonical.EventUsage), string(canonical.EventCompleted), string(canonical.EventIncomplete), string(canonical.EventFailed), string(canonical.EventError):
		return canonical.EventType(name)
	case "response.reasoning.delta", "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
		return canonical.EventReasoningDelta
	default:
		return canonical.EventRaw
	}
}

func chatUsage(source *chatUsageWire) *canonical.Usage {
	if source == nil {
		return nil
	}
	usage := &canonical.Usage{InputTokens: source.PromptTokens, OutputTokens: source.CompletionTokens, TotalTokens: source.TotalTokens}
	if source.PromptTokensDetails != nil {
		usage.CachedInputTokens = source.PromptTokensDetails.CachedTokens
	}
	if source.CompletionTokensDetails != nil {
		usage.ReasoningOutputTokens = source.CompletionTokensDetails.ReasoningTokens
	}
	return usage
}

func responsesUsageValue(source *responsesUsage) *canonical.Usage {
	if source == nil {
		return nil
	}
	usage := &canonical.Usage{InputTokens: source.InputTokens, OutputTokens: source.OutputTokens, TotalTokens: source.TotalTokens}
	if source.InputTokenDetails != nil {
		usage.CachedInputTokens = source.InputTokenDetails.CachedTokens
	}
	if source.OutputTokenDetails != nil {
		usage.ReasoningOutputTokens = source.OutputTokenDetails.ReasoningTokens
	}
	return usage
}

func canonicalResponseError(source *responseError, raw []byte) *canonical.Error {
	if source == nil {
		return nil
	}
	return &canonical.Error{Status: source.Status, Type: source.Type, Code: source.Code, Message: source.Message, Param: source.Param, Retryable: source.Status == http.StatusTooManyRequests || source.Status >= http.StatusInternalServerError, Raw: append(json.RawMessage(nil), raw...)}
}

func decodeErrorValue(raw json.RawMessage, status int) *canonical.Error {
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	if nested := fields["error"]; len(nested) > 0 && !bytes.Equal(bytes.TrimSpace(nested), []byte("null")) {
		return decodeErrorValue(nested, status)
	}
	code := rawString(fields["code"])
	if code == "" && len(fields["code"]) > 0 {
		code = strings.Trim(string(fields["code"]), `"`)
	}
	return &canonical.Error{Status: status, Type: firstRawString(fields, "type", "error_type"), Code: code, Message: rawString(fields["message"]), Param: rawAny(fields["param"]), Retryable: status == http.StatusTooManyRequests || status >= http.StatusInternalServerError, Raw: append(json.RawMessage(nil), raw...)}
}

func normalizeArguments(raw []byte) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	var text string
	if json.Unmarshal(trimmed, &text) == nil && json.Valid([]byte(text)) {
		return json.RawMessage(text)
	}
	return append(json.RawMessage(nil), trimmed...)
}

func responseOutput(raw json.RawMessage) any {
	if len(raw) == 0 {
		return ""
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return string(raw)
	}
	switch value.(type) {
	case string, []any:
		return value
	default:
		return string(raw)
	}
}

func rawText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	return string(raw)
}

func rawTextAppend(value any, suffix string) string {
	prefix, _ := value.(string)
	return prefix + suffix
}

func makeDataURL(data, mediaType, fallback string) string {
	if strings.HasPrefix(data, "data:") {
		return data
	}
	if mediaType == "" {
		mediaType = fallback
	}
	if _, err := base64.StdEncoding.DecodeString(data); err != nil {
		data = base64.StdEncoding.EncodeToString([]byte(data))
	}
	return "data:" + mediaType + ";base64," + data
}

func stripDataURL(data string) string {
	if comma := strings.IndexByte(data, ','); strings.HasPrefix(data, "data:") && comma >= 0 {
		return data[comma+1:]
	}
	return data
}

func audioFormat(mediaType string) string {
	mediaType = strings.TrimSpace(strings.ToLower(mediaType))
	if slash := strings.LastIndexByte(mediaType, '/'); slash >= 0 {
		mediaType = mediaType[slash+1:]
	}
	if mediaType == "mpeg" {
		return "mp3"
	}
	return mediaType
}

func nativeExtras(source map[string]json.RawMessage) map[string]any {
	result := map[string]any{}
	for key, raw := range source {
		if strings.Contains(key, ".") || !json.Valid(raw) {
			continue
		}
		var value any
		if json.Unmarshal(raw, &value) == nil {
			result[key] = value
		}
	}
	return result
}

func topLevelExtras(raw []byte, known ...string) map[string]json.RawMessage {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil
	}
	return exceptRaw(fields, known...)
}

func exceptRaw(source map[string]json.RawMessage, known ...string) map[string]json.RawMessage {
	result := make(map[string]json.RawMessage, len(source))
	for key, raw := range source {
		result[key] = append(json.RawMessage(nil), raw...)
	}
	for _, key := range known {
		delete(result, key)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func firstRawString(source map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		if value := rawString(source[key]); value != "" {
			return value
		}
	}
	return ""
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

func rawInt64(raw json.RawMessage) int64 {
	var value int64
	_ = json.Unmarshal(raw, &value)
	return value
}

func rawAny(raw json.RawMessage) any {
	var value any
	_ = json.Unmarshal(raw, &value)
	return value
}

func cloneStringMap(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func must(value any) json.RawMessage {
	raw, _ := json.Marshal(value)
	return raw
}

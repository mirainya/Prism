// Package volcengine implements the Ark Responses v3 upstream transport.
package volcengine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mirainya/Prism/internal/gateway/canonical"
	gatewaytransport "github.com/mirainya/Prism/internal/gateway/transport"
	protocol "github.com/mirainya/Prism/internal/provider/responses"
)

const (
	responsesPath             = "/api/v3/responses"
	maxOutputTokensUpperBound = 131072
)

type Transport struct{ Client gatewaytransport.HTTPClient }

func New(client gatewaytransport.HTTPClient) *Transport { return &Transport{Client: client} }
func (*Transport) ID() gatewaytransport.ID              { return gatewaytransport.VolcengineResponsesV3 }

func (*Transport) Plan(operation gatewaytransport.Operation, request canonical.Request, features canonical.FeatureSet) gatewaytransport.Plan {
	if operation != gatewaytransport.OperationResponses && operation != gatewaytransport.OperationChat && operation != gatewaytransport.OperationMessages {
		return gatewaytransport.Unsupported(operation, "unsupported Volcengine operation")
	}
	if len(request.Stop) > 0 {
		return gatewaytransport.Unsupported(operation, "Volcengine Responses v3 cannot preserve stop sequences")
	}
	if len(request.Metadata) > 0 {
		return gatewaytransport.Unsupported(operation, "Volcengine Responses v3 does not support metadata")
	}
	if request.User != "" {
		return gatewaytransport.Unsupported(operation, "Volcengine Responses v3 does not support user")
	}
	for index, item := range request.Items {
		if item.Proof != nil {
			return gatewaytransport.Unsupported(operation, fmt.Sprintf("Volcengine Responses v3 cannot replay provider proof on item %d", index))
		}
	}
	if field := gatewaytransport.UnsupportedNamespace(request); field != "" {
		return gatewaytransport.Unsupported(operation, "Volcengine Responses v3 cannot preserve "+field)
	}
	if field := gatewaytransport.UnsupportedProviderCallIDState(request); field != "" {
		return gatewaytransport.Unsupported(operation, "Volcengine Responses v3 cannot preserve "+field)
	}
	for _, tool := range request.Tools {
		if tool.Type == "file_search" {
			return gatewaytransport.Unsupported(operation, "Volcengine Responses v3 does not support file_search")
		}
	}
	clientExtensions := map[string]bool{}
	if operation == gatewaytransport.OperationResponses {
		clientExtensions["openai_responses.request_extras"] = true
	}
	policy := gatewaytransport.ExtensionPolicy{
		Client: clientExtensions,
		Item: map[string]bool{
			gatewaytransport.ExtensionChatChoiceIndex: true, gatewaytransport.ExtensionChatContentMode: true,
			gatewaytransport.ExtensionChatFinishReason: true,
		},
		PreserveGenericItemFields: true, PreserveGenericContentFields: true, PreserveToolOptions: true,
	}
	if extension := gatewaytransport.UnsupportedRequestExtension(request, policy); extension != "" {
		return gatewaytransport.Unsupported(operation, "Volcengine Responses v3 cannot preserve "+extension)
	}
	if field := unsupportedRequestExtra(request); field != "" {
		return gatewaytransport.Unsupported(operation, "Volcengine Responses v3 does not support request field "+field)
	}
	if operation != gatewaytransport.OperationResponses && gatewaytransport.ToolChoiceRawHasExtensions(request.ToolChoice, operation) {
		return gatewaytransport.Unsupported(operation, "Volcengine Responses v3 cannot preserve tool_choice extensions during conversion")
	}
	if operation != gatewaytransport.OperationResponses && request.Reasoning != nil && !gatewaytransport.RawObjectHasOnlyFields(request.Reasoning.Raw, "effort", "summary") {
		return gatewaytransport.Unsupported(operation, "Volcengine Responses v3 cannot preserve foreign reasoning extensions")
	}
	required := request.RequiredFeatures()
	if !features.Contains(required) {
		return gatewaytransport.Unsupported(operation, "route does not support all requested features")
	}
	if operation == gatewaytransport.OperationResponses {
		return gatewaytransport.Exact(operation, required)
	}
	return gatewaytransport.Converted(operation, gatewaytransport.OperationResponses, required)
}

func unsupportedRequestExtra(request canonical.Request) string {
	unsupported := []string{
		"conversation", "prompt", "stream_options", "top_logprobs", "metadata",
		"truncation", "prompt_cache_retention", "user",
	}
	if options := request.ProviderOptions.Volcengine; options != nil {
		for _, field := range unsupported {
			if gatewaytransport.HasJSONValue(options.Unknown[field]) {
				return field
			}
		}
	}
	raw := request.ClientExtensions["openai_responses.request_extras"]
	if !gatewaytransport.HasJSONValue(raw) {
		return ""
	}
	var extras map[string]json.RawMessage
	if json.Unmarshal(raw, &extras) != nil {
		return "openai_responses.request_extras"
	}
	for _, field := range unsupported {
		if gatewaytransport.HasJSONValue(extras[field]) {
			return field
		}
	}
	return ""
}

func (t *Transport) Prepare(_ context.Context, invocation gatewaytransport.Invocation) (gatewaytransport.PreparedRequest, error) {
	if strings.TrimSpace(invocation.Route.BaseURL) == "" {
		return gatewaytransport.PreparedRequest{}, errors.New("Volcengine base URL is required")
	}
	body, err := encodeRequest(invocation.Request, invocation.Route.VendorModel, invocation.Operation == gatewaytransport.OperationResponses)
	if err != nil {
		return gatewaytransport.PreparedRequest{}, err
	}
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	headers.Set("Authorization", "Bearer "+invocation.Route.APIKey)
	for key, value := range invocation.Route.ExtraHeaders {
		headers.Set(key, value)
	}
	for _, tool := range invocation.Request.Tools {
		switch tool.Type {
		case "image_process":
			headers.Set("ark-beta-image-process", "true")
		case "mcp":
			headers.Set("ark-beta-mcp", "true")
		case "knowledge_search":
			headers.Set("ark-beta-knowledge-search", "true")
		case "doubao_app":
			headers.Set("ark-beta-doubao-app", "true")
		}
	}
	return gatewaytransport.PreparedRequest{Method: http.MethodPost, URL: strings.TrimRight(invocation.Route.BaseURL, "/") + responsesPath, Headers: headers, Body: body, Stream: invocation.Request.Stream}, nil
}

func (t *Transport) Execute(ctx context.Context, invocation gatewaytransport.Invocation) (canonical.Response, gatewaytransport.PreparedRequest, error) {
	prepared, err := t.Prepare(ctx, invocation)
	if err != nil {
		return canonical.Response{}, prepared, err
	}
	response, err := t.ExecutePrepared(ctx, invocation, prepared)
	return response, prepared, err
}

func (t *Transport) ExecutePrepared(ctx context.Context, invocation gatewaytransport.Invocation, input gatewaytransport.PreparedRequest) (canonical.Response, error) {
	prepared := input.Clone()
	response, err := t.Client.Do(ctx, prepared)
	if err != nil {
		return canonical.Response{}, err
	}
	body, err := t.Client.ReadBody(response)
	if err != nil {
		return canonical.Response{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return canonical.Response{}, newHTTPError(response.StatusCode, body)
	}
	decoded, err := decodeResponse(body, invocation.Route.PublicModel)
	return decoded, err
}

func (t *Transport) Stream(ctx context.Context, invocation gatewaytransport.Invocation) (gatewaytransport.EventStream, gatewaytransport.PreparedRequest, error) {
	invocation.Request.Stream = true
	prepared, err := t.Prepare(ctx, invocation)
	if err != nil {
		return nil, prepared, err
	}
	stream, err := t.StreamPrepared(ctx, invocation, prepared)
	return stream, prepared, err
}

func (t *Transport) StreamPrepared(ctx context.Context, invocation gatewaytransport.Invocation, input gatewaytransport.PreparedRequest) (gatewaytransport.EventStream, error) {
	if !invocation.Request.Stream {
		return nil, errors.New("Volcengine stream requires stream=true")
	}
	prepared := input.Clone()
	response, err := t.Client.Do(ctx, prepared)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, readErr := t.Client.ReadBody(response)
		if readErr != nil {
			return nil, readErr
		}
		return nil, newHTTPError(response.StatusCode, body)
	}
	return &eventStream{body: response.Body, reader: gatewaytransport.NewSSEReader(response.Body), publicModel: invocation.Route.PublicModel}, nil
}

func encodeRequest(request canonical.Request, vendorModel string, preserveResponsesExtensions bool) ([]byte, error) {
	model := vendorModel
	if model == "" {
		model = request.Model
	}
	input, err := encodeItems(request.Items)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"model": model, "input": input}
	if request.Instructions != "" {
		body["instructions"] = request.Instructions
	}
	if request.Stream {
		body["stream"] = true
	}
	if request.Background {
		body["background"] = true
	}
	if request.Store != nil {
		body["store"] = *request.Store
	}
	if request.PreviousResponseID != "" {
		body["previous_response_id"] = request.PreviousResponseID
	}
	if request.MaxOutputTokens != nil {
		value := *request.MaxOutputTokens
		if value > maxOutputTokensUpperBound {
			value = maxOutputTokensUpperBound
		}
		body["max_output_tokens"] = value
	}
	if request.Temperature != nil {
		body["temperature"] = *request.Temperature
	}
	if request.TopP != nil {
		body["top_p"] = *request.TopP
	}
	if request.ParallelToolCalls != nil {
		body["parallel_tool_calls"] = *request.ParallelToolCalls
	}
	if len(request.Include) > 0 {
		body["include"] = request.Include
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
	if request.Reasoning != nil {
		reasoning, err := encodeReasoning(request.Reasoning)
		if err != nil {
			return nil, err
		}
		if len(reasoning) > 0 {
			body["reasoning"] = reasoning
		}
	}
	if request.ResponseFormat != nil {
		format := map[string]any{"type": request.ResponseFormat.Type}
		if request.ResponseFormat.Name != "" {
			format["name"] = request.ResponseFormat.Name
		}
		if request.ResponseFormat.Description != "" {
			format["description"] = request.ResponseFormat.Description
		}
		if len(request.ResponseFormat.Schema) > 0 {
			format["schema"] = json.RawMessage(request.ResponseFormat.Schema)
		}
		if request.ResponseFormat.Strict != nil {
			format["strict"] = *request.ResponseFormat.Strict
		}
		body["text"] = map[string]any{"format": format}
	}
	if len(request.Tools) > 0 {
		tools, err := encodeTools(request.Tools)
		if err != nil {
			return nil, err
		}
		body["tools"] = tools
	}
	if request.ToolChoice != nil {
		choiceValue := request.ToolChoice
		if !preserveResponsesExtensions {
			choice := *request.ToolChoice
			choice.Raw = nil
			choiceValue = &choice
		}
		choice, err := encodeToolChoice(choiceValue)
		if err != nil {
			return nil, err
		}
		body["tool_choice"] = choice
	}
	if options := request.ProviderOptions.Volcengine; options != nil {
		putRaw(body, "thinking", options.Thinking)
		putRaw(body, "caching", options.Caching)
		putRaw(body, "session", options.Session)
		putRaw(body, "context_management", options.ContextManagement)
		if options.ExpireAt != nil {
			body["expire_at"] = *options.ExpireAt
		}
		for key, raw := range options.Unknown {
			putRaw(body, key, raw)
		}
	}
	if preserveResponsesExtensions {
		if err := mergeRequestExtras(body, request.ClientExtensions["openai_responses.request_extras"]); err != nil {
			return nil, err
		}
	}
	return json.Marshal(body)
}

func encodeReasoning(reasoning *canonical.Reasoning) (map[string]any, error) {
	value := map[string]any{}
	if gatewaytransport.HasJSONValue(reasoning.Raw) {
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

func mergeRequestExtras(body map[string]any, raw json.RawMessage) error {
	if !gatewaytransport.HasJSONValue(raw) {
		return nil
	}
	var extras map[string]json.RawMessage
	if err := json.Unmarshal(raw, &extras); err != nil {
		return fmt.Errorf("openai_responses.request_extras: %w", err)
	}
	for key, value := range extras {
		if !json.Valid(value) {
			return fmt.Errorf("openai_responses.request_extras field %q is invalid JSON", key)
		}
		body[key] = json.RawMessage(value)
	}
	return nil
}

func upstreamItemExtras(source map[string]json.RawMessage) map[string]json.RawMessage {
	result := cloneRawMap(source)
	delete(result, gatewaytransport.ExtensionAnthropicRawBlock)
	delete(result, gatewaytransport.ExtensionChatChoiceIndex)
	delete(result, gatewaytransport.ExtensionChatContentMode)
	delete(result, gatewaytransport.ExtensionChatFinishReason)
	return result
}

func upstreamContentExtras(source map[string]json.RawMessage) map[string]json.RawMessage {
	result := cloneRawMap(source)
	delete(result, gatewaytransport.ExtensionAnthropicRawBlock)
	return result
}

func encodeItems(items []canonical.Item) ([]map[string]any, error) {
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		value, err := rawObject(upstreamItemExtras(item.Extra))
		if err != nil {
			return nil, err
		}
		typeName := item.Type
		if typeName == "" {
			typeName = "message"
		}
		if isArkInputItem(typeName) {
			clearCanonicalItemFields(value)
		}
		value["type"] = typeName
		switch typeName {
		case "message":
			if len(item.Content) == 0 {
				continue
			}
			if item.Role != "" {
				value["role"] = item.Role
			}
			content, err := encodeContent(item.Content)
			if err != nil {
				return nil, err
			}
			if len(content) == 0 {
				continue
			}
			value["content"] = content
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
			value["output"] = rawText(item.Output)
		case "reasoning":
			if len(item.Content) > 0 {
				summary, err := encodeReasoningSummary(item.Content)
				if err != nil {
					return nil, err
				}
				value["summary"] = summary
			}
		default:
			if item.Role != "" {
				value["role"] = item.Role
			}
			if item.ID != "" {
				value["id"] = item.ID
			}
			if item.Name != "" {
				value["name"] = item.Name
			}
			if item.CallID != "" {
				value["call_id"] = item.CallID
			}
			if item.Status != "" {
				value["status"] = item.Status
			}
			if len(item.Content) > 0 {
				content, err := encodeContent(item.Content)
				if err != nil {
					return nil, err
				}
				value["content"] = content
			}
			if len(item.Arguments) > 0 {
				value["arguments"] = rawText(item.Arguments)
			}
			if len(item.Output) > 0 {
				value["output"] = json.RawMessage(item.Output)
			}
		}
		result = append(result, value)
	}
	return result, nil
}

func isArkInputItem(typeName string) bool {
	switch typeName {
	case "message", "function_call", "function_call_output", "reasoning":
		return true
	default:
		return false
	}
}

func clearCanonicalItemFields(value map[string]any) {
	for _, key := range []string{"id", "role", "name", "call_id", "status", "content", "arguments", "output"} {
		delete(value, key)
	}
}

func encodeReasoningSummary(parts []canonical.Content) ([]map[string]any, error) {
	result := make([]map[string]any, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "", "reasoning_text", "summary_text", "text", "output_text":
			result = append(result, map[string]any{"type": "summary_text", "text": part.Text})
		default:
			return nil, fmt.Errorf("Volcengine reasoning cannot encode content %q", part.Type)
		}
	}
	return result, nil
}

func encodeContent(parts []canonical.Content) ([]map[string]any, error) {
	result := make([]map[string]any, 0, len(parts))
	for _, part := range parts {
		typeName := part.Type
		if typeName == "output_text" || typeName == "text" {
			typeName = "input_text"
		}
		if typeName == "input_text" && part.Text == "" {
			continue
		}
		value, err := rawObject(upstreamContentExtras(part.Extra))
		if err != nil {
			return nil, err
		}
		if isArkInputContent(typeName) {
			clearCanonicalContentFields(value)
		}
		if part.Type == "output_text" {
			delete(value, "annotations")
			delete(value, "logprobs")
		}
		value["type"] = typeName
		switch typeName {
		case "input_text":
			value["text"] = part.Text
		case "input_image":
			if part.FileID != "" {
				value["file_id"] = part.FileID
			} else if url := arkMediaURL(part.URL, part.Data, part.MediaType); url != "" {
				value["image_url"] = url
			}
			if part.Detail != "" {
				value["detail"] = part.Detail
			}
		case "input_video":
			if part.FileID != "" {
				value["file_id"] = part.FileID
			} else if url := arkMediaURL(part.URL, part.Data, part.MediaType); url != "" {
				value["video_url"] = url
			}
		case "input_audio":
			if part.FileID != "" {
				value["file_id"] = part.FileID
			} else {
				mediaType := part.MediaType
				if mediaType == "" && part.Format != "" {
					mediaType = "audio/" + strings.TrimPrefix(part.Format, ".")
				}
				if url := arkMediaURL(part.URL, part.Data, mediaType); url != "" {
					value["audio_url"] = url
				}
			}
		case "input_file":
			switch {
			case part.FileID != "":
				value["file_id"] = part.FileID
			case part.Data != "":
				value["file_data"] = part.Data
				filename := part.Filename
				if filename == "" {
					filename = "document.pdf"
				}
				value["filename"] = filename
			case part.URL != "":
				value["file_url"] = part.URL
			}
			if part.Data == "" && part.Filename != "" {
				value["filename"] = part.Filename
			}
		default:
			if part.Text != "" {
				value["text"] = part.Text
			}
			if part.FileID != "" {
				value["file_id"] = part.FileID
			}
			if part.Filename != "" {
				value["filename"] = part.Filename
			}
			if part.Data != "" {
				value["file_data"] = part.Data
			}
			if part.MediaType != "" {
				value["content_type"] = part.MediaType
			}
			if part.Format != "" {
				value["format"] = part.Format
			}
			if part.Detail != "" {
				value["detail"] = part.Detail
			}
			if part.Transcript != "" {
				value["transcript"] = part.Transcript
			}
		}
		result = append(result, value)
	}
	return result, nil
}

func isArkInputContent(typeName string) bool {
	switch typeName {
	case "input_text", "input_image", "input_video", "input_audio", "input_file":
		return true
	default:
		return false
	}
}

func clearCanonicalContentFields(value map[string]any) {
	for _, key := range []string{
		"type", "text", "image_url", "video_url", "audio_url", "file_url", "file_id", "file_data",
		"filename", "content_type", "format", "detail", "transcript",
	} {
		delete(value, key)
	}
}

func arkMediaURL(url, data, mediaType string) string {
	if url != "" {
		return url
	}
	if data == "" || strings.HasPrefix(data, "data:") {
		return data
	}
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	return "data:" + mediaType + ";base64," + data
}

func encodeTools(tools []canonical.Tool) ([]map[string]any, error) {
	result := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		value, err := rawObjectRaw(tool.Options)
		if err != nil {
			return nil, fmt.Errorf("tool %q options: %w", tool.Type, err)
		}
		value["type"] = tool.Type
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

func encodeToolChoice(choice *canonical.ToolChoice) (any, error) {
	if len(choice.Raw) > 0 {
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
	value := map[string]any{"type": typeName}
	if choice.Name != "" {
		value["name"] = choice.Name
	}
	return value, nil
}

type eventStream struct {
	body        io.ReadCloser
	reader      *gatewaytransport.SSEReader
	publicModel string
}

func (s *eventStream) Next(ctx context.Context) (canonical.Event, error) {
	frame, err := s.reader.Next(ctx)
	if err != nil {
		return canonical.Event{}, err
	}
	if bytes.Equal(bytes.TrimSpace(frame.Data), []byte("[DONE]")) {
		return canonical.Event{}, io.EOF
	}
	return decodeEvent(frame.Data, frame.Event, s.publicModel)
}
func (s *eventStream) Close() error {
	if s.body == nil {
		return nil
	}
	return s.body.Close()
}
func decodeEvent(raw []byte, eventName, publicModel string) (canonical.Event, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return canonical.Event{}, err
	}
	typeName := rawString(root["type"])
	if typeName == "" {
		typeName = eventName
	}
	eventType := canonical.EventType(typeName)
	if typeName == "response.reasoning_text.delta" || typeName == "response.reasoning_summary_text.delta" {
		eventType = canonical.EventReasoningDelta
	} else if typeName == "response.reasoning_text.done" {
		eventType = canonical.EventReasoningTextDone
	}
	contentIndex := rawInt(root["content_index"])
	if summaryIndex, ok := root["summary_index"]; ok {
		contentIndex = rawInt(summaryIndex)
	}
	event := canonical.Event{Type: eventType, RawType: eventName, Raw: append(json.RawMessage(nil), raw...), SequenceNumber: rawInt64(root["sequence_number"]), OutputIndex: rawInt(root["output_index"]), ContentIndex: contentIndex, ToolIndex: rawInt(root["tool_index"]), Delta: first(root, "delta", "text", "arguments"), ProviderResponseID: rawString(root["response_id"])}
	if typeName == "error" {
		event.Error = decodeError(root)
		return event, nil
	}
	if value := root["response"]; len(value) > 0 {
		response, err := decodeResponse(value, publicModel)
		if err != nil {
			return canonical.Event{}, err
		}
		event.Response = &response
		event.Usage = response.Usage
		event.ProviderResponseID = response.ProviderResponseID
		if response.Error != nil {
			event.Error = response.Error
		}
	}
	if value := root["item"]; len(value) > 0 {
		item, err := decodeItem(value)
		if err != nil {
			return canonical.Event{}, err
		}
		event.Item = &item
	}
	if event.Item == nil {
		itemID, callID := rawString(root["item_id"]), rawString(root["call_id"])
		if itemID != "" || callID != "" {
			event.Item = &canonical.Item{ID: itemID, CallID: callID}
			if event.Type == canonical.EventToolArgumentsDelta {
				event.Item.Type = "function_call"
			}
		}
	}
	if isReasoningSummaryEvent(eventType) {
		if event.Item == nil {
			event.Item = &canonical.Item{ID: rawString(root["item_id"])}
		}
		event.Item.Type = "reasoning"
		if event.Item.Role == "" {
			event.Item.Role = canonical.RoleAssistant
		}
		if partRaw := root["part"]; len(partRaw) > 0 {
			part, err := decodeReasoningPart(partRaw)
			if err != nil {
				return canonical.Event{}, err
			}
			event.Item.Content = []canonical.Content{part}
		} else if eventType == canonical.EventReasoningTextDone {
			event.Item.Content = []canonical.Content{{Type: "reasoning_text", Text: event.Delta}}
		}
	}
	if value := root["usage"]; len(value) > 0 {
		event.Usage = decodeUsage(value)
	}
	return event, nil
}

func decodeResponse(raw []byte, publicModel string) (canonical.Response, error) {
	var response protocol.Response
	if err := json.Unmarshal(raw, &response); err != nil {
		return canonical.Response{}, err
	}
	var output []json.RawMessage
	if len(response.Output) > 0 {
		if err := json.Unmarshal(response.Output, &output); err != nil {
			return canonical.Response{}, err
		}
	}
	items := make([]canonical.Item, 0, len(output))
	for _, rawItem := range output {
		item, err := decodeItem(rawItem)
		if err != nil {
			return canonical.Response{}, err
		}
		items = append(items, item)
	}
	extensions := cloneRawMap(response.ExtraFields)
	if len(response.ServiceStatus) > 0 {
		if extensions == nil {
			extensions = map[string]json.RawMessage{}
		}
		extensions["service_status"] = append(json.RawMessage(nil), response.ServiceStatus...)
	}
	model := publicModel
	if model == "" {
		model = response.Model
	}
	result := canonical.Response{ID: response.ID, ProviderResponseID: response.ID, Model: model, Status: response.Status, CreatedAt: response.CreatedAt, Output: items, Usage: decodeUsageValue(response.Usage), IncompleteDetails: append(json.RawMessage(nil), response.IncompleteDetails...), Metadata: cloneStrings(response.Metadata), ProviderExtensions: extensions}
	if response.Error != nil {
		result.Error = &canonical.Error{Code: response.Error.Code, Message: response.Error.Message, Type: response.Error.Type, Param: response.Error.Param}
	}
	return result, nil
}
func decodeItem(raw []byte) (canonical.Item, error) {
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil {
		return canonical.Item{}, err
	}
	item := canonical.Item{ID: rawString(value["id"]), Type: rawString(value["type"]), Role: canonical.Role(rawString(value["role"])), Name: rawString(value["name"]), CallID: rawString(value["call_id"]), Arguments: normalizeArguments(value["arguments"]), Output: append(json.RawMessage(nil), value["output"]...), Status: rawString(value["status"]), Extra: except(value, "id", "type", "role", "name", "call_id", "arguments", "output", "status", "content", "summary")}
	if rawContent := value["content"]; len(rawContent) > 0 {
		var parts []map[string]json.RawMessage
		if err := json.Unmarshal(rawContent, &parts); err != nil {
			return canonical.Item{}, err
		}
		for _, part := range parts {
			item.Content = append(item.Content, canonical.Content{Type: rawString(part["type"]), Text: rawString(part["text"]), URL: first(part, "image_url", "video_url", "audio_url", "file_url"), FileID: rawString(part["file_id"]), Filename: rawString(part["filename"]), Data: rawString(part["file_data"]), MediaType: rawString(part["content_type"]), Format: rawString(part["format"]), Detail: rawString(part["detail"]), Transcript: rawString(part["transcript"]), Extra: except(part, "type", "text", "image_url", "video_url", "audio_url", "file_url", "file_id", "filename", "file_data", "content_type", "format", "detail", "transcript")})
		}
	}
	if item.Type == "reasoning" {
		parts, err := decodeReasoningSummary(value["summary"])
		if err != nil {
			return canonical.Item{}, err
		}
		item.Content = parts
	}
	return item, nil
}

func isReasoningSummaryEvent(eventType canonical.EventType) bool {
	switch eventType {
	case canonical.EventReasoningDelta, canonical.EventReasoningPartAdded, canonical.EventReasoningTextDone, canonical.EventReasoningPartDone:
		return true
	default:
		return false
	}
}

func decodeReasoningSummary(raw json.RawMessage) ([]canonical.Content, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var values []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	result := make([]canonical.Content, 0, len(values))
	for _, value := range values {
		result = append(result, reasoningContent(value))
	}
	return result, nil
}

func decodeReasoningPart(raw json.RawMessage) (canonical.Content, error) {
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil {
		return canonical.Content{}, err
	}
	return reasoningContent(value), nil
}

func reasoningContent(value map[string]json.RawMessage) canonical.Content {
	typeName := rawString(value["type"])
	if typeName == "summary_text" {
		typeName = "reasoning_text"
	}
	return canonical.Content{Type: typeName, Text: rawString(value["text"]), Extra: except(value, "type", "text")}
}

func decodeUsage(raw []byte) *canonical.Usage {
	var usage protocol.Usage
	if json.Unmarshal(raw, &usage) != nil {
		return nil
	}
	return decodeUsageValue(&usage)
}
func decodeUsageValue(source *protocol.Usage) *canonical.Usage {
	if source == nil {
		return nil
	}
	result := &canonical.Usage{InputTokens: source.InputTokens, OutputTokens: source.OutputTokens, TotalTokens: source.TotalTokens, Extra: cloneRawMap(source.ExtraFields)}
	if source.InputTokensDetails != nil {
		result.CachedInputTokens = source.InputTokensDetails.CachedTokens
	}
	if source.OutputTokensDetails != nil {
		result.ReasoningOutputTokens = source.OutputTokensDetails.ReasoningTokens
	}
	return result
}
func decodeError(root map[string]json.RawMessage) *canonical.Error {
	if nested := root["error"]; len(nested) > 0 && !bytes.Equal(bytes.TrimSpace(nested), []byte("null")) {
		var fields map[string]json.RawMessage
		if json.Unmarshal(nested, &fields) == nil {
			return decodeError(fields)
		}
	}
	typeName := first(root, "error_type", "type")
	return &canonical.Error{Status: rawInt(root["status"]), Code: rawString(root["code"]), Message: rawString(root["message"]), Type: typeName, Param: rawAny(root["param"]), Retryable: typeName == "rate_limit_error" || typeName == "server_error"}
}

type HTTPError struct {
	Status  int
	Body    []byte
	Details *canonical.Error
}

func (e *HTTPError) Error() string {
	if e.Details != nil && strings.TrimSpace(e.Details.Message) != "" {
		return fmt.Sprintf("Volcengine upstream HTTP %d: %s", e.Status, e.Details.Message)
	}
	return fmt.Sprintf("Volcengine upstream HTTP %d", e.Status)
}
func (e *HTTPError) HTTPStatus() int                { return e.Status }
func (e *HTTPError) ErrorDetails() *canonical.Error { return e.Details }

func newHTTPError(status int, body []byte) error {
	var root map[string]json.RawMessage
	_ = json.Unmarshal(body, &root)
	details := decodeError(root)
	details.Status = status
	details.Retryable = details.Retryable || status == http.StatusTooManyRequests || status >= http.StatusInternalServerError
	details.Raw = append(json.RawMessage(nil), body...)
	return &HTTPError{Status: status, Body: append([]byte(nil), body...), Details: details}
}
func putRaw(target map[string]any, key string, raw json.RawMessage) {
	if len(raw) > 0 && json.Valid(raw) {
		target[key] = json.RawMessage(raw)
	}
}
func rawObject(source map[string]json.RawMessage) (map[string]any, error) {
	return rawObjectRaw(mustMap(source))
}
func rawObjectRaw(source json.RawMessage) (map[string]any, error) {
	result := map[string]any{}
	if len(source) == 0 {
		return result, nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(source, &raw); err != nil {
		return nil, err
	}
	for key, value := range raw {
		var parsed any
		if err := json.Unmarshal(value, &parsed); err != nil {
			return nil, err
		}
		result[key] = parsed
	}
	return result, nil
}
func mustMap(source map[string]json.RawMessage) json.RawMessage {
	if len(source) == 0 {
		return nil
	}
	value, _ := json.Marshal(source)
	return value
}
func except(source map[string]json.RawMessage, names ...string) map[string]json.RawMessage {
	result := cloneRawMap(source)
	for _, name := range names {
		delete(result, name)
	}
	return result
}
func cloneRawMap(source map[string]json.RawMessage) map[string]json.RawMessage {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]json.RawMessage, len(source))
	for key, value := range source {
		result[key] = append(json.RawMessage(nil), value...)
	}
	return result
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
func rawString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}
func rawInt(raw json.RawMessage) int { var value int; _ = json.Unmarshal(raw, &value); return value }
func rawInt64(raw json.RawMessage) int64 {
	var value int64
	_ = json.Unmarshal(raw, &value)
	return value
}
func rawAny(raw json.RawMessage) any { var value any; _ = json.Unmarshal(raw, &value); return value }
func rawText(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	return string(raw)
}
func normalizeArguments(raw json.RawMessage) json.RawMessage {
	var value string
	if json.Unmarshal(raw, &value) == nil && json.Valid([]byte(value)) {
		return json.RawMessage(value)
	}
	return append(json.RawMessage(nil), raw...)
}
func first(source map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		if value := rawString(source[key]); value != "" {
			return value
		}
	}
	return ""
}

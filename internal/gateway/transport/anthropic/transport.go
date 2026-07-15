package anthropic

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
	codecanthropic "github.com/mirainya/Prism/internal/gateway/codec/anthropic"
	"github.com/mirainya/Prism/internal/gateway/transport"
)

const requestExtras = "anthropic.messages.request_extras"

type Transport struct{ Client *http.Client }

func New(client *http.Client) transport.Transport { return &Transport{Client: client} }
func (*Transport) ID() transport.ID               { return transport.AnthropicMessages }

func (*Transport) Plan(operation transport.Operation, request canonical.Request, features canonical.FeatureSet) transport.Plan {
	if operation != transport.OperationMessages && operation != transport.OperationChat && operation != transport.OperationResponses {
		return transport.Unsupported(operation, "unsupported Anthropic operation")
	}
	if request.Background || request.PreviousResponseID != "" || len(request.Include) > 0 || len(request.Modalities) > 0 || hasVolcengineOptions(request.ProviderOptions.Volcengine) {
		return transport.Unsupported(operation, "Anthropic Messages cannot express response persistence or provider options")
	}
	// Responses storage is owned by Prism; other downstream protocols would be
	// asking the upstream provider to persist the response.
	if operation != transport.OperationResponses && request.Store != nil && *request.Store {
		return transport.Unsupported(operation, "Anthropic Messages cannot provide upstream response storage")
	}
	if operation != transport.OperationMessages && len(request.Metadata) > 0 {
		return transport.Unsupported(operation, "converted Anthropic requests cannot preserve arbitrary downstream metadata")
	}
	if request.Reasoning != nil && !isNativeThinking(request.Reasoning.Raw) {
		return transport.Unsupported(operation, "Anthropic thinking requires native type and budget controls")
	}
	if request.ResponseFormat != nil || features.Has(canonical.FeatureAudio) || features.Has(canonical.FeatureVideo) {
		return transport.Unsupported(operation, "Anthropic Messages cannot express this structured or multimodal request")
	}
	for _, tool := range request.Tools {
		if tool.Type != "" && tool.Type != "function" {
			return transport.Unsupported(operation, "Anthropic Messages only supports function tools")
		}
	}
	clientExtensions := map[string]bool{}
	if operation == transport.OperationMessages {
		clientExtensions[requestExtras] = true
	}
	policy := transport.ExtensionPolicy{
		Client: clientExtensions,
		Item: map[string]bool{
			transport.ExtensionChatChoiceIndex: true, transport.ExtensionChatContentMode: true,
			transport.ExtensionChatFinishReason: true,
		},
		PreserveAnthropicRawBlocks: true,
		PreserveToolOptions:        true,
	}
	if extension := transport.UnsupportedRequestExtension(request, policy); extension != "" {
		return transport.Unsupported(operation, "Anthropic Messages cannot preserve "+extension)
	}
	if operation != transport.OperationMessages && transport.ToolChoiceRawHasExtensions(request.ToolChoice, operation) {
		return transport.Unsupported(operation, "Anthropic Messages cannot preserve tool_choice extensions during conversion")
	}
	if operation == transport.OperationMessages {
		return transport.Exact(operation, features)
	}
	return transport.Converted(operation, transport.OperationMessages, features)
}

func isNativeThinking(raw json.RawMessage) bool {
	var value map[string]json.RawMessage
	return json.Unmarshal(raw, &value) == nil && rawString(value["type"]) != ""
}

func hasVolcengineOptions(options *canonical.VolcengineOptions) bool {
	return options != nil && (len(options.Thinking) > 0 || len(options.Caching) > 0 || len(options.Session) > 0 || len(options.ContextManagement) > 0 || options.ExpireAt != nil || len(options.Unknown) > 0)
}

func (t *Transport) Prepare(_ context.Context, invocation transport.Invocation) (transport.PreparedRequest, error) {
	if strings.TrimSpace(invocation.Route.BaseURL) == "" {
		return transport.PreparedRequest{}, errors.New("Anthropic base URL is required")
	}
	request := invocation.Request
	request.Model = invocation.Route.VendorModel
	if invocation.Operation != transport.OperationMessages {
		request.Metadata = nil
		if request.ToolChoice != nil {
			choice := *request.ToolChoice
			choice.Raw = nil
			request.ToolChoice = &choice
		}
	}
	if request.Model == "" {
		request.Model = invocation.Request.Model
	}
	body, err := codecanthropic.EncodeRequest(request)
	if err != nil {
		return transport.PreparedRequest{}, err
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return transport.PreparedRequest{}, err
	}
	headers := http.Header{"Content-Type": []string{"application/json"}, "x-api-key": []string{invocation.Route.APIKey}, "anthropic-version": []string{"2023-06-01"}}
	for key, value := range invocation.Route.ExtraHeaders {
		headers.Set(key, value)
	}
	return transport.PreparedRequest{Method: http.MethodPost, URL: strings.TrimRight(invocation.Route.BaseURL, "/") + "/v1/messages", Headers: headers, Body: raw, Stream: invocation.Request.Stream}, nil
}

func (t *Transport) Execute(ctx context.Context, invocation transport.Invocation) (canonical.Response, transport.PreparedRequest, error) {
	prepared, err := t.Prepare(ctx, invocation)
	if err != nil {
		return canonical.Response{}, prepared, err
	}
	response, err := t.ExecutePrepared(ctx, invocation, prepared)
	return response, prepared, err
}

func (t *Transport) ExecutePrepared(ctx context.Context, invocation transport.Invocation, input transport.PreparedRequest) (canonical.Response, error) {
	prepared := input.Clone()
	response, err := t.do(ctx, prepared)
	if err != nil {
		return canonical.Response{}, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return canonical.Response{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return canonical.Response{}, newHTTPError(response.StatusCode, raw)
	}
	result, err := codecanthropic.DecodeResponse(raw, invocation.Route.PublicModel)
	if err == nil {
		augmentResponseUsage(&result, raw)
	}
	return result, err
}

func (t *Transport) Stream(ctx context.Context, invocation transport.Invocation) (transport.EventStream, transport.PreparedRequest, error) {
	invocation.Request.Stream = true
	prepared, err := t.Prepare(ctx, invocation)
	if err != nil {
		return nil, prepared, err
	}
	stream, err := t.StreamPrepared(ctx, invocation, prepared)
	return stream, prepared, err
}

func (t *Transport) StreamPrepared(ctx context.Context, invocation transport.Invocation, input transport.PreparedRequest) (transport.EventStream, error) {
	prepared := input.Clone()
	response, err := t.do(ctx, prepared)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		raw, _ := io.ReadAll(response.Body)
		response.Body.Close()
		return nil, newHTTPError(response.StatusCode, raw)
	}
	return &stream{
		body: response.Body, reader: transport.NewSSEReader(response.Body),
		publicModel: invocation.Route.PublicModel,
		blocks:      map[int]canonical.Item{},
		toolDeltas:  map[int]bool{},
	}, nil
}

func (t *Transport) do(ctx context.Context, prepared transport.PreparedRequest) (*http.Response, error) {
	client := t.Client
	if client == nil {
		client = http.DefaultClient
	}
	request, err := http.NewRequestWithContext(ctx, prepared.Method, prepared.URL, bytes.NewReader(prepared.Body))
	if err != nil {
		return nil, err
	}
	request.Header = prepared.Headers.Clone()
	return client.Do(request)
}

type HTTPError struct {
	Status  int
	Body    []byte
	Details *canonical.Error
}

func (e *HTTPError) Error() string                  { return fmt.Sprintf("Anthropic upstream HTTP %d", e.Status) }
func (e *HTTPError) HTTPStatus() int                { return e.Status }
func (e *HTTPError) ErrorDetails() *canonical.Error { return e.Details }

func newHTTPError(status int, body []byte) error {
	details := decodeAnthropicError(body, status)
	details.Raw = append(json.RawMessage(nil), body...)
	return &HTTPError{Status: status, Body: append([]byte(nil), body...), Details: details}
}

type stream struct {
	body        io.ReadCloser
	reader      *transport.SSEReader
	publicModel string
	responseID  string
	finish      string
	usage       canonical.Usage
	hasUsage    bool
	blocks      map[int]canonical.Item
	toolDeltas  map[int]bool
}

func (s *stream) Close() error { return s.body.Close() }

func (s *stream) Next(ctx context.Context) (canonical.Event, error) {
	for {
		frame, err := s.reader.Next(ctx)
		if err != nil {
			return canonical.Event{}, err
		}
		event, emit, err := s.decode(frame.Event, frame.Data)
		if err != nil {
			return canonical.Event{}, err
		}
		if emit {
			return event, nil
		}
	}
}

func (s *stream) decode(name string, raw []byte) (canonical.Event, bool, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return canonical.Event{}, false, err
	}
	if name == "" {
		name = rawString(root["type"])
	}
	base := canonical.Event{RawType: name, Raw: append(json.RawMessage(nil), raw...), ProviderResponseID: s.responseID}
	switch name {
	case "message_start":
		var message struct {
			ID    string `json:"id"`
			Model string `json:"model"`
			Usage struct {
				InputTokens              int `json:"input_tokens"`
				CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
				CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(root["message"], &message); err != nil {
			return canonical.Event{}, false, err
		}
		s.responseID = message.ID
		s.usage.InputTokens = message.Usage.InputTokens
		s.usage.CachedInputTokens = message.Usage.CacheReadInputTokens
		s.usage.Extra = map[string]json.RawMessage{"cache_creation_input_tokens": mustRaw(message.Usage.CacheCreationInputTokens)}
		s.hasUsage = true
		model := s.publicModel
		if model == "" {
			model = message.Model
		}
		response := canonical.Response{ID: message.ID, ProviderResponseID: message.ID, Model: model, Status: "in_progress", Usage: s.usageValue()}
		base.Type, base.Response, base.Usage, base.ProviderResponseID = canonical.EventResponseCreated, &response, response.Usage, message.ID
		return base, true, nil
	case "content_block_start":
		index := rawInt(root["index"])
		var block struct {
			Type     string          `json:"type"`
			ID       string          `json:"id"`
			Name     string          `json:"name"`
			Input    json.RawMessage `json:"input"`
			Text     string          `json:"text"`
			Thinking string          `json:"thinking"`
		}
		if err := json.Unmarshal(root["content_block"], &block); err != nil {
			return canonical.Event{}, false, err
		}
		item := canonical.Item{ID: block.ID, Status: "in_progress"}
		switch block.Type {
		case "tool_use":
			item.Type, item.CallID, item.Name, item.Arguments = "function_call", block.ID, block.Name, normalizeArguments(block.Input)
			base.Type = canonical.EventOutputItemAdded
		case "thinking":
			item.Type, item.Role = "reasoning", canonical.RoleAssistant
			item.Content = []canonical.Content{{Type: "reasoning_text", Text: block.Thinking}}
			base.Type = canonical.EventContentPartAdded
		default:
			item.Type, item.Role = "message", canonical.RoleAssistant
			item.Content = []canonical.Content{{Type: "output_text", Text: block.Text}}
			base.Type = canonical.EventContentPartAdded
		}
		s.blocks[index] = item
		delete(s.toolDeltas, index)
		base.ContentIndex, base.ToolIndex, base.Item = index, index, &item
		return base, true, nil
	case "content_block_delta":
		index := rawInt(root["index"])
		var delta struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			Thinking    string `json:"thinking"`
			PartialJSON string `json:"partial_json"`
		}
		if err := json.Unmarshal(root["delta"], &delta); err != nil {
			return canonical.Event{}, false, err
		}
		base.ContentIndex, base.ToolIndex, base.Delta = index, index, delta.Text
		switch delta.Type {
		case "thinking_delta":
			base.Type, base.Delta = canonical.EventReasoningDelta, delta.Thinking
		case "input_json_delta":
			base.Type, base.Delta = canonical.EventToolArgumentsDelta, delta.PartialJSON
			if s.toolDeltas == nil {
				s.toolDeltas = make(map[int]bool)
			}
			if item, ok := s.blocks[index]; ok {
				if !s.toolDeltas[index] {
					item.Arguments = nil
				}
				item.Arguments = append(item.Arguments, delta.PartialJSON...)
				s.blocks[index] = item
			}
			s.toolDeltas[index] = true
		case "text_delta":
			base.Type = canonical.EventTextDelta
		default:
			base.Type = canonical.EventRaw
		}
		if item, ok := s.blocks[index]; ok {
			copy := item
			base.Item = &copy
		}
		return base, true, nil
	case "content_block_stop":
		index := rawInt(root["index"])
		base.Type, base.ContentIndex = canonical.EventContentPartDone, index
		if item, ok := s.blocks[index]; ok {
			item.Status = "completed"
			if item.Type == "function_call" {
				base.Type = canonical.EventOutputItemDone
			}
			base.Item = &item
			delete(s.blocks, index)
			delete(s.toolDeltas, index)
		}
		return base, true, nil
	case "message_delta":
		var delta struct {
			StopReason string `json:"stop_reason"`
		}
		var usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		}
		_ = json.Unmarshal(root["delta"], &delta)
		_ = json.Unmarshal(root["usage"], &usage)
		s.finish = delta.StopReason
		if usage.InputTokens > 0 {
			s.usage.InputTokens = usage.InputTokens
		}
		s.usage.OutputTokens = usage.OutputTokens
		s.usage.TotalTokens = s.usage.InputTokens + s.usage.OutputTokens
		s.hasUsage = true
		base.Type, base.Usage = canonical.EventUsage, s.usageValue()
		return base, true, nil
	case "message_stop":
		base.Type = canonical.EventCompleted
		if s.finish == "max_tokens" {
			base.Type = canonical.EventIncomplete
		}
		base.ProviderResponseID, base.Usage = s.responseID, s.usageValue()
		return base, true, nil
	case "error":
		errorRaw := root["error"]
		if len(errorRaw) == 0 {
			errorRaw = raw
		}
		base.Type, base.Error = canonical.EventError, decodeAnthropicError(errorRaw, 0)
		return base, true, nil
	default:
		base.Type = canonical.EventRaw
		return base, true, nil
	}
}

func (s *stream) usageValue() *canonical.Usage {
	if !s.hasUsage {
		return nil
	}
	copy := s.usage
	return &copy
}

func decodeAnthropicError(raw []byte, status int) *canonical.Error {
	var root map[string]json.RawMessage
	_ = json.Unmarshal(raw, &root)
	if nested := root["error"]; len(nested) > 0 && !bytes.Equal(bytes.TrimSpace(nested), []byte("null")) {
		return decodeAnthropicError(nested, status)
	}
	typeName := rawString(root["type"])
	return &canonical.Error{Status: status, Type: typeName, Code: typeName, Message: rawString(root["message"]), Retryable: typeName == "overloaded_error" || status == http.StatusTooManyRequests || status >= 500, Raw: append(json.RawMessage(nil), raw...)}
}

func augmentResponseUsage(response *canonical.Response, raw []byte) {
	if response == nil || response.Usage == nil {
		return
	}
	var wire struct {
		Usage struct {
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(raw, &wire) != nil {
		return
	}
	response.Usage.CachedInputTokens = wire.Usage.CacheReadInputTokens
	if wire.Usage.CacheCreationInputTokens != 0 {
		response.Usage.Extra = map[string]json.RawMessage{"cache_creation_input_tokens": mustRaw(wire.Usage.CacheCreationInputTokens)}
	}
}

func normalizeArguments(raw json.RawMessage) json.RawMessage {
	var text string
	if json.Unmarshal(raw, &text) == nil && json.Valid([]byte(text)) {
		return json.RawMessage(text)
	}
	return append(json.RawMessage(nil), raw...)
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

func mustRaw(value any) json.RawMessage {
	raw, _ := json.Marshal(value)
	return raw
}

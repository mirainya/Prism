// Package anthropic implements the Anthropic Messages upstream transport.
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
	// 转换必须保留 thinking proof、工具结果和原始 block；任一形态不可重放时拒绝该方案。
	if operation != transport.OperationMessages && operation != transport.OperationChat && operation != transport.OperationResponses {
		return transport.Unsupported(operation, "unsupported Anthropic operation")
	}
	if request.Background || request.PreviousResponseID != "" || !transport.SupportsLocalResponsesInclude(operation, request.Include) || !textOnlyModalities(request.Modalities) || hasVolcengineOptions(request.ProviderOptions.Volcengine) {
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
	if operation == transport.OperationChat && request.Reasoning != nil && features.Has(canonical.FeatureTools) {
		return transport.Unsupported(operation, "OpenAI Chat cannot preserve Anthropic thinking proofs across tool calls")
	}
	if request.ResponseFormat != nil || features.Has(canonical.FeatureAudio) || features.Has(canonical.FeatureVideo) {
		return transport.Unsupported(operation, "Anthropic Messages cannot express this structured or multimodal request")
	}
	for index, item := range request.Items {
		if item.ProviderCallIDOmitted {
			return transport.Unsupported(operation, fmt.Sprintf("Anthropic Messages cannot preserve provider call-ID state on item %d", index))
		}
		if item.Proof != nil && item.Type != "reasoning" {
			return transport.Unsupported(operation, fmt.Sprintf("Anthropic Messages cannot preserve provider proof on item %d", index))
		}
		if item.Type == "reasoning" && !replayableAnthropicReasoning(item) {
			return transport.Unsupported(operation, fmt.Sprintf("Anthropic Messages requires a native proof on reasoning item %d", index))
		}
	}
	if field := transport.UnsupportedNamespace(request); field != "" {
		return transport.Unsupported(operation, "Anthropic Messages cannot preserve "+field)
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

func replayableAnthropicReasoning(item canonical.Item) bool {
	// redacted_thinking 与普通 thinking 的 proof 形态不同，二者都必须验证原始 block 与 canonical 一致。
	var block map[string]json.RawMessage
	raw := item.Extra[transport.ExtensionAnthropicRawBlock]
	if json.Unmarshal(raw, &block) == nil && rawString(block["type"]) == "redacted_thinking" {
		data := rawString(block["data"])
		if data == "" {
			return false
		}
		if item.Proof != nil && (item.Proof.Provider != canonical.ProofProviderAnthropic || item.Proof.Subject != canonical.ProofSubjectAnthropicRedacted || item.Proof.Value != data || item.Proof.TargetID != "") {
			return false
		}
		if len(item.Content) == 0 {
			return true
		}
		if len(item.Content) != 1 || !isAnthropicReasoningContent(item.Content[0]) || item.Content[0].Text != "" {
			return false
		}
		contentRaw := item.Content[0].Extra[transport.ExtensionAnthropicRawBlock]
		return !transport.HasJSONValue(contentRaw) || bytes.Equal(bytes.TrimSpace(raw), bytes.TrimSpace(contentRaw))
	}
	if item.Proof != nil && item.Proof.Provider == canonical.ProofProviderAnthropic && item.Proof.Subject == canonical.ProofSubjectAnthropicRedacted {
		if item.Proof.Value == "" || item.Proof.TargetID != "" || len(item.Content) > 1 {
			return false
		}
		return len(item.Content) == 0 || (isAnthropicReasoningContent(item.Content[0]) && item.Content[0].Text == "")
	}
	if _, ok := canonical.NativeProviderProofValue(item.Proof, canonical.ProofProviderAnthropic); !ok || len(item.Content) != 1 || !isAnthropicReasoningContent(item.Content[0]) {
		return false
	}
	if item.Proof.Subject != "" || item.Proof.TargetID != "" {
		return false
	}
	var contentBlock map[string]json.RawMessage
	contentRaw := item.Content[0].Extra[transport.ExtensionAnthropicRawBlock]
	return json.Unmarshal(contentRaw, &contentBlock) != nil || rawString(contentBlock["type"]) != "redacted_thinking"
}

func isAnthropicReasoningContent(content canonical.Content) bool {
	return content.Type == "reasoning_text" || content.Type == "thinking"
}

func textOnlyModalities(modalities []string) bool {
	// 显式 ["text"] 与缺省等价，可放行；含 audio 等其他形态仍拒绝。
	return len(modalities) == 0 || (len(modalities) == 1 && modalities[0] == "text")
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
	return codecanthropic.DecodeResponse(raw, invocation.Route.PublicModel)
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
	model       string
	finish      string
	usage       canonical.Usage
	hasUsage    bool
	// usage 的原始分量：anthropic 的 input_tokens 不含缓存读写，归一时需分开累计。
	inputTokens   int
	outputTokens  int
	cacheRead     int
	cacheCreation int
	blocks        map[int]canonical.Item
	toolDeltas    map[int]bool
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
	// Anthropic 将一个响应拆成 message 与 content block 事件；blocks 保存索引对应的 Item 身份和累计参数。
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
		s.inputTokens, s.cacheRead, s.cacheCreation = message.Usage.InputTokens, message.Usage.CacheReadInputTokens, message.Usage.CacheCreationInputTokens
		s.refreshUsage()
		model := s.publicModel
		if model == "" {
			model = message.Model
		}
		s.model = model
		response := canonical.Response{ID: message.ID, ProviderResponseID: message.ID, Model: model, Status: "in_progress", Usage: s.usageValue()}
		base.Type, base.Response, base.Usage, base.ProviderResponseID = canonical.EventResponseCreated, &response, response.Usage, message.ID
		return base, true, nil
	case "content_block_start":
		index := rawInt(root["index"])
		decoded, err := codecanthropic.DecodeEvent(name, raw)
		if err != nil {
			return canonical.Event{}, false, err
		}
		if decoded.Item == nil {
			return canonical.Event{}, false, errors.New("Anthropic content_block_start is missing content_block")
		}
		item := canonical.CloneItems([]canonical.Item{*decoded.Item})[0]
		s.blocks[index] = item
		delete(s.toolDeltas, index)
		base.Type, base.OutputIndex, base.ContentIndex, base.ToolIndex, base.Item = decoded.Type, index, 0, index, &item
		return base, true, nil
	case "content_block_delta":
		index := rawInt(root["index"])
		decoded, err := codecanthropic.DecodeEvent(name, raw)
		if err != nil {
			return canonical.Event{}, false, err
		}
		base.Type, base.ContentIndex, base.ToolIndex, base.Delta = decoded.Type, 0, index, decoded.Delta
		switch decoded.Type {
		case canonical.EventToolArgumentsDelta:
			if s.toolDeltas == nil {
				s.toolDeltas = make(map[int]bool)
			}
			if item, ok := s.blocks[index]; ok {
				if !s.toolDeltas[index] {
					item.Arguments = nil
				}
				item.Arguments = append(item.Arguments, decoded.Delta...)
				s.blocks[index] = item
			}
			s.toolDeltas[index] = true
		case canonical.EventProviderProof:
			if decoded.Item != nil && decoded.Item.Proof != nil {
				item := s.blocks[index]
				proof := *decoded.Item.Proof
				if item.Proof != nil && item.Proof.Provider == proof.Provider {
					proof.Value = item.Proof.Value + proof.Value
				}
				item.Proof = &proof
				if item.Type == "" {
					item.Type, item.Role, item.Status = "reasoning", canonical.RoleAssistant, "in_progress"
				}
				s.blocks[index] = item
			}
		}
		if item, ok := s.blocks[index]; ok {
			copy := canonical.CloneItems([]canonical.Item{item})[0]
			base.Item = &copy
		} else if decoded.Item != nil {
			copy := canonical.CloneItems([]canonical.Item{*decoded.Item})[0]
			base.Item = &copy
		}
		if base.Item != nil {
			base.OutputIndex = index
		}
		return base, true, nil
	case "content_block_stop":
		index := rawInt(root["index"])
		// ToolIndex 必须与 start/delta 保持同一 index，否则累积器与下游 SSE 编码器按键查块会漂移。
		base.Type, base.OutputIndex, base.ContentIndex, base.ToolIndex = canonical.EventContentPartDone, index, 0, index
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
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		}
		_ = json.Unmarshal(root["delta"], &delta)
		_ = json.Unmarshal(root["usage"], &usage)
		s.finish = delta.StopReason
		if usage.InputTokens > 0 {
			s.inputTokens = usage.InputTokens
		}
		if usage.CacheReadInputTokens > 0 {
			s.cacheRead = usage.CacheReadInputTokens
		}
		if usage.CacheCreationInputTokens > 0 {
			s.cacheCreation = usage.CacheCreationInputTokens
		}
		s.outputTokens = usage.OutputTokens
		s.refreshUsage()
		base.Type, base.Usage = canonical.EventUsage, s.usageValue()
		base.Response = &canonical.Response{Status: responseStatus(delta.StopReason), FinishReason: delta.StopReason, Usage: base.Usage}
		return base, true, nil
	case "message_stop":
		base.Type = canonical.EventCompleted
		if s.finish == "max_tokens" {
			base.Type = canonical.EventIncomplete
		}
		base.ProviderResponseID, base.Usage = s.responseID, s.usageValue()
		// 终态事件按约定携带上游原生 stop reason，供下游编码器映射自家 finish_reason 词表。
		base.Response = &canonical.Response{ID: s.responseID, ProviderResponseID: s.responseID, Model: s.model, Status: responseStatus(s.finish), FinishReason: s.finish, Usage: base.Usage}
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

// refreshUsage 按 OpenAI 口径归一：InputTokens=全部输入（含缓存读+写），CachedInputTokens 为其中缓存读子集。
func (s *stream) refreshUsage() {
	s.usage.InputTokens = s.inputTokens + s.cacheRead + s.cacheCreation
	s.usage.CachedInputTokens = s.cacheRead
	s.usage.OutputTokens = s.outputTokens
	s.usage.TotalTokens = s.usage.InputTokens + s.usage.OutputTokens
	s.usage.Extra = map[string]json.RawMessage{"cache_creation_input_tokens": mustRaw(s.cacheCreation)}
	s.hasUsage = true
}

func (s *stream) usageValue() *canonical.Usage {
	if !s.hasUsage {
		return nil
	}
	copy := s.usage
	return &copy
}

func responseStatus(stopReason string) string {
	switch stopReason {
	case "max_tokens", "model_context_window_exceeded", "pause_turn":
		return "incomplete"
	default:
		return "completed"
	}
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

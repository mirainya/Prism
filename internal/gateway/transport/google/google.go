// Package google implements the Gemini GenerateContent transport.
package google

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/gateway/transport"
)

type Transport struct {
	client *http.Client
}

func New(client *http.Client) transport.Transport { return &Transport{client: client} }

func NewGenerateContent(client *http.Client) transport.Transport { return New(client) }

func (*Transport) ID() transport.ID { return transport.GoogleGenerateContent }

func (*Transport) Plan(operation transport.Operation, request canonical.Request, features canonical.FeatureSet) transport.Plan {
	if operation != transport.OperationChat && operation != transport.OperationResponses && operation != transport.OperationMessages {
		return transport.Unsupported(operation, "unsupported Gemini operation")
	}
	if request.Background || request.PreviousResponseID != "" || len(request.Include) > 0 {
		return transport.Unsupported(operation, "Gemini GenerateContent does not support response persistence, background execution, or previous_response_id")
	}
	// Responses storage is owned by Prism; other downstream protocols would be
	// asking the upstream provider to persist the response.
	if operation != transport.OperationResponses && request.Store != nil && *request.Store {
		return transport.Unsupported(operation, "Gemini GenerateContent cannot provide upstream response storage")
	}
	if request.User != "" || len(request.Metadata) > 0 {
		return transport.Unsupported(operation, "Gemini GenerateContent cannot preserve user or metadata fields")
	}
	if hasVolcengineOptions(request.ProviderOptions.Volcengine) {
		return transport.Unsupported(operation, "Gemini GenerateContent cannot preserve Volcengine provider options")
	}
	if request.Reasoning != nil || request.ParallelToolCalls != nil {
		return transport.Unsupported(operation, "Gemini GenerateContent transport does not support reasoning or parallel tool controls")
	}
	if format := request.ResponseFormat; format != nil {
		if (format.Type != "json_object" && format.Type != "json_schema") || format.Name != "" || format.Description != "" || format.Strict != nil {
			return transport.Unsupported(operation, "Gemini GenerateContent cannot preserve this response format")
		}
	}
	for _, tool := range request.Tools {
		if tool.Type != "" && tool.Type != "function" {
			return transport.Unsupported(operation, "Gemini GenerateContent only supports function tools")
		}
		if tool.Strict != nil {
			return transport.Unsupported(operation, "Gemini GenerateContent does not support strict function tools")
		}
	}
	for _, feature := range []canonical.Feature{
		canonical.FeatureWebSearch,
		canonical.FeatureFileSearch,
		canonical.FeatureCodeInterpreter,
		canonical.FeatureComputerUse,
		canonical.FeatureImageGeneration,
	} {
		if features.Has(feature) {
			return transport.Unsupported(operation, "Gemini GenerateContent does not support this managed tool")
		}
	}
	policy := transport.ExtensionPolicy{
		Client: map[string]bool{"google.generate_content.request_extras": true},
		Item: map[string]bool{
			transport.ExtensionChatChoiceIndex: true, transport.ExtensionChatContentMode: true,
			transport.ExtensionChatFinishReason: true,
		},
	}
	if extension := transport.UnsupportedRequestExtension(request, policy); extension != "" {
		return transport.Unsupported(operation, "Gemini GenerateContent cannot preserve "+extension)
	}
	if transport.ToolChoiceRawHasExtensions(request.ToolChoice, operation) {
		return transport.Unsupported(operation, "Gemini GenerateContent cannot preserve tool_choice extensions")
	}
	switch operation {
	case transport.OperationChat:
		return transport.Converted(operation, transport.OperationChat, features)
	case transport.OperationResponses, transport.OperationMessages:
		return transport.Converted(operation, transport.OperationChat, features)
	default:
		return transport.Unsupported(operation, "unsupported Gemini operation")
	}
}

func hasVolcengineOptions(options *canonical.VolcengineOptions) bool {
	return options != nil && (len(options.Thinking) > 0 || len(options.Caching) > 0 || len(options.Session) > 0 || len(options.ContextManagement) > 0 || options.ExpireAt != nil || len(options.Unknown) > 0)
}

func (t *Transport) Prepare(_ context.Context, invocation transport.Invocation) (transport.PreparedRequest, error) {
	body, err := encode(invocation)
	if err != nil {
		return transport.PreparedRequest{}, err
	}
	model := invocation.Route.VendorModel
	if model == "" {
		model = invocation.Request.Model
	}
	if strings.TrimSpace(model) == "" {
		return transport.PreparedRequest{}, fmt.Errorf("Gemini model is required")
	}
	path := "/v1beta/models/" + url.PathEscape(model) + ":generateContent"
	query := url.Values{}
	if invocation.Route.APIKey != "" {
		query.Set("key", invocation.Route.APIKey)
	}
	if invocation.Request.Stream {
		path = "/v1beta/models/" + url.PathEscape(model) + ":streamGenerateContent"
		query.Set("alt", "sse")
	}
	endpoint := strings.TrimRight(invocation.Route.BaseURL, "/") + path
	if encoded := query.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}
	headers := http.Header{"Content-Type": []string{"application/json"}}
	for key, value := range invocation.Route.ExtraHeaders {
		headers.Set(key, value)
	}
	return transport.PreparedRequest{Method: http.MethodPost, URL: endpoint, Headers: headers, Body: body, Stream: invocation.Request.Stream}, nil
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
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		return canonical.Response{}, err
	}
	if response.StatusCode >= http.StatusBadRequest {
		return canonical.Response{}, newHTTPError(response.StatusCode, body)
	}
	result, err := decodeResponse(body, invocation.Route)
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
	if response.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		return nil, newHTTPError(response.StatusCode, body)
	}
	return &sse{body: response.Body, reader: transport.NewSSEReader(response.Body), route: invocation.Route}, nil
}

func (t *Transport) do(ctx context.Context, prepared transport.PreparedRequest) (*http.Response, error) {
	client := t.client
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, prepared.Method, prepared.URL, bytes.NewReader(prepared.Body))
	if err != nil {
		return nil, err
	}
	req.Header = prepared.Headers.Clone()
	return client.Do(req)
}

func encode(invocation transport.Invocation) ([]byte, error) {
	request := invocation.Request
	contents, systemInstruction, err := encodeItems(request.Items)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"contents": contents}
	if request.Instructions != "" {
		systemInstruction = append(systemInstruction, request.Instructions)
	}
	if len(systemInstruction) > 0 {
		body["systemInstruction"] = map[string]any{"parts": []any{map[string]any{"text": strings.Join(systemInstruction, "\n")}}}
	}
	if config, err := generationConfig(request); err != nil {
		return nil, err
	} else if len(config) > 0 {
		body["generationConfig"] = config
	}
	if tools, err := encodeTools(request.Tools); err != nil {
		return nil, err
	} else if len(tools) > 0 {
		body["tools"] = tools
	}
	if toolConfig, err := encodeToolChoice(request.ToolChoice); err != nil {
		return nil, err
	} else if toolConfig != nil {
		body["toolConfig"] = toolConfig
	}
	if raw, ok := request.ClientExtensions["google.generate_content.request_extras"]; ok {
		var extras map[string]any
		if err := json.Unmarshal(raw, &extras); err != nil {
			return nil, fmt.Errorf("google.generate_content.request_extras: %w", err)
		}
		for key, value := range extras {
			body[key] = value
		}
	}
	return json.Marshal(body)
}

func encodeItems(items []canonical.Item) ([]any, []string, error) {
	contents := make([]any, 0, len(items))
	system := make([]string, 0)
	callNames := make(map[string]string)
	for _, item := range items {
		if item.Type == "reasoning" {
			return nil, nil, fmt.Errorf("Gemini GenerateContent does not accept reasoning input")
		}
		if item.Type == "function_call_output" {
			name := callNames[item.CallID]
			if name == "" {
				return nil, nil, fmt.Errorf("function_call_output %q has no preceding function call", item.CallID)
			}
			response := any(map[string]any{})
			if len(item.Output) > 0 && json.Unmarshal(item.Output, &response) != nil {
				response = map[string]any{"result": string(item.Output)}
			}
			if _, ok := response.(map[string]any); !ok {
				response = map[string]any{"result": response}
			}
			functionResponse := map[string]any{"name": name, "response": response}
			if item.CallID != "" {
				functionResponse["id"] = item.CallID
			}
			contents = append(contents, map[string]any{"role": "user", "parts": []any{map[string]any{"functionResponse": functionResponse}}})
			continue
		}
		if item.Type == "function_call" {
			args := any(map[string]any{})
			if len(item.Arguments) > 0 && json.Unmarshal(item.Arguments, &args) != nil {
				return nil, nil, fmt.Errorf("function_call %q arguments: invalid JSON", item.Name)
			}
			callNames[item.CallID] = item.Name
			functionCall := map[string]any{"name": item.Name, "args": args}
			if item.CallID != "" {
				functionCall["id"] = item.CallID
			}
			contents = append(contents, map[string]any{"role": "model", "parts": []any{map[string]any{"functionCall": functionCall}}})
			continue
		}
		parts, err := encodeContent(item.Content)
		if err != nil {
			return nil, nil, err
		}
		switch item.Role {
		case canonical.RoleSystem, canonical.RoleDeveloper:
			for _, part := range parts {
				if text, ok := part.(map[string]any)["text"].(string); ok && text != "" {
					system = append(system, text)
				}
			}
		case canonical.RoleAssistant:
			contents = append(contents, map[string]any{"role": "model", "parts": parts})
		default:
			contents = append(contents, map[string]any{"role": "user", "parts": parts})
		}
	}
	return contents, system, nil
}

func encodeContent(content []canonical.Content) ([]any, error) {
	parts := make([]any, 0, len(content))
	for _, block := range content {
		switch block.Type {
		case "", "input_text", "output_text", "text":
			parts = append(parts, map[string]any{"text": block.Text})
		case "input_image", "image", "input_file", "file", "document", "input_audio", "audio", "input_video", "video":
			part, err := encodeBlob(block)
			if err != nil {
				return nil, err
			}
			parts = append(parts, part)
		default:
			return nil, fmt.Errorf("Gemini GenerateContent does not support content type %q", block.Type)
		}
	}
	return parts, nil
}

func encodeBlob(block canonical.Content) (map[string]any, error) {
	if block.Data != "" {
		return map[string]any{"inlineData": map[string]any{"mimeType": mediaType(block, "application/octet-stream"), "data": block.Data}}, nil
	}
	if mime, data, ok := parseDataURL(block.URL); ok {
		return map[string]any{"inlineData": map[string]any{"mimeType": mime, "data": data}}, nil
	}
	if block.URL != "" {
		file := map[string]any{"fileUri": block.URL}
		if block.MediaType != "" {
			file["mimeType"] = block.MediaType
		}
		return map[string]any{"fileData": file}, nil
	}
	return nil, fmt.Errorf("Gemini content requires data or URL; file_id %q is unresolved", block.FileID)
}

func mediaType(block canonical.Content, fallback string) string {
	if block.MediaType != "" {
		return block.MediaType
	}
	return fallback
}

func parseDataURL(value string) (string, string, bool) {
	if !strings.HasPrefix(value, "data:") {
		return "", "", false
	}
	comma := strings.IndexByte(value, ',')
	if comma < 0 {
		return "", "", false
	}
	header, data := value[5:comma], value[comma+1:]
	if !strings.HasSuffix(header, ";base64") {
		return "", "", false
	}
	mime := strings.TrimSuffix(header, ";base64")
	if mime == "" {
		mime = "application/octet-stream"
	}
	if _, err := base64.StdEncoding.DecodeString(data); err != nil {
		return "", "", false
	}
	return mime, data, true
}

func generationConfig(request canonical.Request) (map[string]any, error) {
	config := make(map[string]any)
	if request.MaxOutputTokens != nil {
		config["maxOutputTokens"] = *request.MaxOutputTokens
	}
	if request.Temperature != nil {
		config["temperature"] = *request.Temperature
	}
	if request.TopP != nil {
		config["topP"] = *request.TopP
	}
	if len(request.Stop) > 0 {
		config["stopSequences"] = request.Stop
	}
	if len(request.Modalities) > 0 {
		config["responseModalities"] = request.Modalities
	}
	if request.ResponseFormat != nil {
		switch request.ResponseFormat.Type {
		case "json_object":
			config["responseMimeType"] = "application/json"
		case "json_schema":
			config["responseMimeType"] = "application/json"
			if len(request.ResponseFormat.Schema) == 0 {
				return nil, fmt.Errorf("json_schema response format requires schema")
			}
			var schema any
			if err := json.Unmarshal(request.ResponseFormat.Schema, &schema); err != nil {
				return nil, fmt.Errorf("json_schema response format: %w", err)
			}
			config["responseJsonSchema"] = schema
		default:
			return nil, fmt.Errorf("Gemini GenerateContent does not support response format %q", request.ResponseFormat.Type)
		}
	}
	return config, nil
}

func encodeTools(tools []canonical.Tool) ([]any, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	declarations := make([]any, 0, len(tools))
	for _, tool := range tools {
		if tool.Type != "" && tool.Type != "function" {
			return nil, fmt.Errorf("Gemini GenerateContent does not support tool %q", tool.Type)
		}
		var parameters any = map[string]any{}
		if len(tool.InputSchema) > 0 && json.Unmarshal(tool.InputSchema, &parameters) != nil {
			return nil, fmt.Errorf("function %q parameters: invalid JSON", tool.Name)
		}
		declarations = append(declarations, map[string]any{"name": tool.Name, "description": tool.Description, "parametersJsonSchema": parameters})
	}
	return []any{map[string]any{"functionDeclarations": declarations}}, nil
}

func encodeToolChoice(choice *canonical.ToolChoice) (map[string]any, error) {
	if choice == nil {
		return nil, nil
	}
	mode := strings.ToUpper(choice.Mode)
	if mode == "" {
		mode = strings.ToUpper(choice.Type)
	}
	config := map[string]any{}
	switch mode {
	case "", "AUTO":
		config["mode"] = "AUTO"
	case "NONE":
		config["mode"] = "NONE"
	case "REQUIRED", "ANY", "FUNCTION", "TOOL":
		config["mode"] = "ANY"
		if choice.Name != "" {
			config["allowedFunctionNames"] = []string{choice.Name}
		}
	default:
		return nil, fmt.Errorf("Gemini GenerateContent does not support tool choice %q", choice.Mode)
	}
	return map[string]any{"functionCallingConfig": config}, nil
}

type geminiResponse struct {
	ResponseID   string `json:"responseId"`
	ModelVersion string `json:"modelVersion"`
	Candidates   []struct {
		Content struct {
			Role  string       `json:"role"`
			Parts []geminiPart `json:"parts"`
		} `json:"content"`
		FinishReason  string `json:"finishReason"`
		FinishMessage string `json:"finishMessage"`
	} `json:"candidates"`
	UsageMetadata  *geminiUsage `json:"usageMetadata"`
	PromptFeedback *struct {
		BlockReason        string `json:"blockReason"`
		BlockReasonMessage string `json:"blockReasonMessage"`
	} `json:"promptFeedback"`
}

type geminiPart struct {
	Text             string          `json:"text"`
	Thought          bool            `json:"thought"`
	ThoughtSignature string          `json:"thoughtSignature"`
	InlineData       *geminiBlob     `json:"inlineData"`
	FileData         *geminiFileData `json:"fileData"`
	FunctionCall     *struct {
		ID   string          `json:"id"`
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	} `json:"functionCall"`
}

type geminiBlob struct {
	MIMEType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiFileData struct {
	MIMEType string `json:"mimeType"`
	FileURI  string `json:"fileUri"`
}

type geminiUsage struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
	CachedContentTokens  int `json:"cachedContentTokenCount"`
	ThoughtsTokenCount   int `json:"thoughtsTokenCount"`
}

func decodeResponse(raw []byte, route transport.Route) (canonical.Response, error) {
	var upstream geminiResponse
	if err := json.Unmarshal(raw, &upstream); err != nil {
		return canonical.Response{}, err
	}
	model := route.PublicModel
	if model == "" {
		model = route.VendorModel
	}
	if upstream.ResponseID == "" {
		upstream.ResponseID = fmt.Sprintf("gemini_%d", time.Now().UnixNano())
	}
	response := canonical.Response{ID: upstream.ResponseID, ProviderResponseID: upstream.ResponseID, Model: model, CreatedAt: time.Now().Unix(), Status: "completed"}
	if upstream.ModelVersion != "" {
		response.ProviderExtensions = map[string]json.RawMessage{"model_version": mustRaw(upstream.ModelVersion)}
	}
	if upstream.PromptFeedback != nil && upstream.PromptFeedback.BlockReason != "" {
		response.Status = "failed"
		response.Error = &canonical.Error{Type: "invalid_request_error", Code: upstream.PromptFeedback.BlockReason, Message: upstream.PromptFeedback.BlockReasonMessage, Raw: append(json.RawMessage(nil), raw...)}
	}
	for candidateIndex, candidate := range upstream.Candidates {
		message := canonical.Item{Type: "message", Role: canonical.RoleAssistant, Status: "completed"}
		for partIndex, part := range candidate.Content.Parts {
			if part.Text != "" {
				if part.Thought {
					response.Output = append(response.Output, canonical.Item{ID: fmt.Sprintf("reasoning_%d_%d", candidateIndex, partIndex), Type: "reasoning", Status: "completed", Content: []canonical.Content{{Type: "output_text", Text: part.Text}}})
				} else {
					message.Content = append(message.Content, canonical.Content{Type: "output_text", Text: part.Text})
				}
			}
			if part.FunctionCall != nil {
				callID := part.FunctionCall.ID
				if callID == "" {
					callID = fmt.Sprintf("call_%d_%d", candidateIndex, partIndex)
				}
				response.Output = append(response.Output, canonical.Item{ID: callID, Type: "function_call", Status: "completed", CallID: callID, Name: part.FunctionCall.Name, Arguments: append(json.RawMessage(nil), part.FunctionCall.Args...)})
			}
			if part.InlineData != nil {
				message.Content = append(message.Content, contentFromBlob(part.InlineData.MIMEType, part.InlineData.Data, ""))
			}
			if part.FileData != nil {
				message.Content = append(message.Content, contentFromBlob(part.FileData.MIMEType, "", part.FileData.FileURI))
			}
		}
		if len(message.Content) > 0 {
			response.Output = append(response.Output, message)
		}
		if candidate.FinishReason != "" {
			response.FinishReason = candidate.FinishReason
			switch finishEventType(candidate.FinishReason) {
			case canonical.EventIncomplete:
				response.Status = "incomplete"
			case canonical.EventFailed:
				response.Status = "failed"
				response.Error = &canonical.Error{Type: "server_error", Code: candidate.FinishReason, Message: candidate.FinishMessage, Raw: append(json.RawMessage(nil), raw...)}
			}
		}
	}
	response.Usage = usageFromGemini(upstream.UsageMetadata)
	return response, nil
}

func usageFromGemini(usage *geminiUsage) *canonical.Usage {
	if usage == nil {
		return nil
	}
	return &canonical.Usage{InputTokens: usage.PromptTokenCount, OutputTokens: usage.CandidatesTokenCount, TotalTokens: usage.TotalTokenCount, CachedInputTokens: usage.CachedContentTokens, ReasoningOutputTokens: usage.ThoughtsTokenCount}
}

type HTTPError struct {
	Status  int
	Body    []byte
	Details *canonical.Error
}

func (e *HTTPError) Error() string                  { return fmt.Sprintf("Gemini upstream HTTP %d", e.Status) }
func (e *HTTPError) HTTPStatus() int                { return e.Status }
func (e *HTTPError) ErrorDetails() *canonical.Error { return e.Details }

func newHTTPError(status int, body []byte) error {
	var upstream struct {
		Error struct {
			Code    any    `json:"code"`
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &upstream)
	typeName := "server_error"
	if status >= 400 && status < 500 {
		typeName = "invalid_request_error"
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		typeName = "authentication_error"
	}
	if status == http.StatusTooManyRequests {
		typeName = "rate_limit_error"
	}
	details := &canonical.Error{Status: status, Type: typeName, Code: upstream.Error.Status, Message: upstream.Error.Message, Retryable: status == http.StatusTooManyRequests || status >= 500, Raw: append(json.RawMessage(nil), body...)}
	if details.Code == "" {
		details.Code = fmt.Sprint(upstream.Error.Code)
	}
	return &HTTPError{Status: status, Body: append([]byte(nil), body...), Details: details}
}

type sse struct {
	body     io.ReadCloser
	reader   *transport.SSEReader
	route    transport.Route
	sequence int64
	pending  []canonical.Event
}

func (s *sse) Close() error { return s.body.Close() }

func (s *sse) Next(ctx context.Context) (canonical.Event, error) {
	for {
		if len(s.pending) > 0 {
			event := s.pending[0]
			s.pending = s.pending[1:]
			return event, nil
		}
		if err := ctx.Err(); err != nil {
			return canonical.Event{}, err
		}
		frame, err := s.reader.Next(ctx)
		if err == io.EOF {
			return canonical.Event{}, err
		}
		if err != nil {
			return canonical.Event{}, err
		}
		data := bytes.TrimSpace(frame.Data)
		if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
			if bytes.Equal(data, []byte("[DONE]")) {
				return canonical.Event{}, io.EOF
			}
			continue
		}
		s.sequence++
		events, err := decodeStreamEvents(data, s.sequence)
		if err != nil {
			return canonical.Event{}, err
		}
		s.pending = append(s.pending, events...)
	}
}

func decodeStreamEvents(raw []byte, sequence int64) ([]canonical.Event, error) {
	var failure struct {
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(raw, &failure); err != nil {
		return nil, err
	}
	if len(failure.Error) > 0 && string(failure.Error) != "null" {
		event := canonical.Event{
			Type:           canonical.EventError,
			SequenceNumber: sequence,
			Raw:            append(json.RawMessage(nil), raw...),
			Error:          decodeGeminiError(failure.Error),
		}
		return []canonical.Event{event}, nil
	}
	var response geminiResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	events := make([]canonical.Event, 0)
	terminalType := canonical.EventType("")
	terminalChoice := 0
	var terminalError *canonical.Error
	if response.PromptFeedback != nil && response.PromptFeedback.BlockReason != "" {
		terminalType = canonical.EventFailed
		terminalError = &canonical.Error{Type: "invalid_request_error", Code: response.PromptFeedback.BlockReason, Message: response.PromptFeedback.BlockReasonMessage, Raw: append(json.RawMessage(nil), raw...)}
	}
	for candidateIndex, candidate := range response.Candidates {
		for partIndex, part := range candidate.Content.Parts {
			if part.Text != "" {
				typeName := canonical.EventTextDelta
				if part.Thought {
					typeName = canonical.EventReasoningDelta
				}
				events = append(events, canonical.Event{Type: typeName, SequenceNumber: sequence, ChoiceIndex: candidateIndex, ContentIndex: partIndex, Delta: part.Text, Raw: append(json.RawMessage(nil), raw...)})
			}
			if part.FunctionCall != nil {
				callID := part.FunctionCall.ID
				if callID == "" {
					callID = fmt.Sprintf("call_%d_%d", candidateIndex, partIndex)
				}
				events = append(events, canonical.Event{Type: canonical.EventToolArgumentsDelta, SequenceNumber: sequence, ChoiceIndex: candidateIndex, ToolIndex: partIndex, Delta: string(part.FunctionCall.Args), Item: &canonical.Item{ID: callID, Type: "function_call", CallID: callID, Name: part.FunctionCall.Name, Arguments: append(json.RawMessage(nil), part.FunctionCall.Args...)}, Raw: append(json.RawMessage(nil), raw...)})
			}
			if part.InlineData != nil {
				item := canonical.Item{Type: "message", Role: canonical.RoleAssistant, Content: []canonical.Content{contentFromBlob(part.InlineData.MIMEType, part.InlineData.Data, "")}}
				events = append(events, canonical.Event{Type: canonical.EventOutputItemAdded, SequenceNumber: sequence, ChoiceIndex: candidateIndex, ContentIndex: partIndex, Item: &item, Raw: append(json.RawMessage(nil), raw...)})
			}
			if part.FileData != nil {
				item := canonical.Item{Type: "message", Role: canonical.RoleAssistant, Content: []canonical.Content{contentFromBlob(part.FileData.MIMEType, "", part.FileData.FileURI)}}
				events = append(events, canonical.Event{Type: canonical.EventOutputItemAdded, SequenceNumber: sequence, ChoiceIndex: candidateIndex, ContentIndex: partIndex, Item: &item, Raw: append(json.RawMessage(nil), raw...)})
			}
		}
		if candidate.FinishReason != "" {
			terminalType = finishEventType(candidate.FinishReason)
			terminalChoice = candidateIndex
			if terminalType == canonical.EventFailed {
				terminalError = &canonical.Error{Type: "server_error", Code: candidate.FinishReason, Message: candidate.FinishMessage, Raw: append(json.RawMessage(nil), raw...)}
			}
		}
	}
	if terminalType != "" {
		events = append(events, canonical.Event{Type: terminalType, SequenceNumber: sequence, ChoiceIndex: terminalChoice, Usage: usageFromGemini(response.UsageMetadata), Error: terminalError, ProviderResponseID: response.ResponseID, Raw: append(json.RawMessage(nil), raw...)})
	} else if response.UsageMetadata != nil {
		events = append(events, canonical.Event{Type: canonical.EventUsage, SequenceNumber: sequence, Usage: usageFromGemini(response.UsageMetadata), Raw: append(json.RawMessage(nil), raw...)})
	}
	if len(events) == 0 {
		events = append(events, canonical.Event{Type: canonical.EventRaw, SequenceNumber: sequence, RawType: "gemini.generate_content", Raw: append(json.RawMessage(nil), raw...)})
	}
	return events, nil
}

func contentFromBlob(mediaType, data, url string) canonical.Content {
	typeName := "output_file"
	switch {
	case strings.HasPrefix(mediaType, "image/"):
		typeName = "output_image"
	case strings.HasPrefix(mediaType, "audio/"):
		typeName = "output_audio"
	case strings.HasPrefix(mediaType, "video/"):
		typeName = "output_video"
	}
	return canonical.Content{Type: typeName, Data: data, URL: url, MediaType: mediaType}
}

func finishEventType(reason string) canonical.EventType {
	switch reason {
	case "MAX_TOKENS":
		return canonical.EventIncomplete
	case "STOP":
		return canonical.EventCompleted
	default:
		return canonical.EventFailed
	}
}

func decodeGeminiError(raw json.RawMessage) *canonical.Error {
	var value struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	}
	_ = json.Unmarshal(raw, &value)
	typeName := "server_error"
	if value.Code >= 400 && value.Code < 500 {
		typeName = "invalid_request_error"
	}
	if value.Code == http.StatusTooManyRequests {
		typeName = "rate_limit_error"
	}
	return &canonical.Error{Status: value.Code, Type: typeName, Code: value.Status, Message: value.Message, Retryable: value.Code == http.StatusTooManyRequests || value.Code >= 500, Raw: append(json.RawMessage(nil), raw...)}
}

func mustRaw(value any) json.RawMessage {
	raw, _ := json.Marshal(value)
	return raw
}

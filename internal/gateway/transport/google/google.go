// Package google implements the Gemini GenerateContent transport.
package google

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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
	if operation != transport.OperationResponses {
		return transport.Unsupported(operation, "Gemini GenerateContent requires a Responses downstream to preserve provider proofs")
	}
	if request.Background || request.PreviousResponseID != "" || !transport.SupportsLocalResponsesInclude(operation, request.Include) {
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
	if googleRequestExtrasConflict(request.ClientExtensions["google.generate_content.request_extras"]) != "" {
		return transport.Unsupported(operation, "Gemini GenerateContent request extras cannot override canonical fields")
	}
	if operation != transport.OperationResponses && (features.Has(canonical.FeatureTools) || hasFunctionCallHistory(request.Items)) {
		return transport.Unsupported(operation, "this downstream protocol cannot preserve Gemini function-call proofs")
	}
	googlePartTargets := make(map[string]bool)
	if !validGoogleFunctionCallGroups(request.Items) {
		return transport.Unsupported(operation, "Gemini GenerateContent requires the original first-call proof for function-call history")
	}
	for index, item := range request.Items {
		if item.ProviderCallIDOmitted && item.Type != "function_call" {
			return transport.Unsupported(operation, "Gemini GenerateContent cannot preserve provider call-ID state on "+item.Type)
		}
		if item.Proof == nil {
			continue
		}
		if _, ok := canonical.NativeProviderProofValue(item.Proof, canonical.ProofProviderGoogle); !ok {
			return transport.Unsupported(operation, "Gemini GenerateContent cannot preserve a non-Google or empty provider proof")
		}
		if item.Proof.Subject == canonical.ProofSubjectGooglePart {
			if googlePartTargets[item.Proof.TargetID] || operation != transport.OperationResponses || !validGooglePartProofCarrier(request.Items, index) {
				return transport.Unsupported(operation, "Gemini GenerateContent cannot replay this Google text proof carrier")
			}
			googlePartTargets[item.Proof.TargetID] = true
			continue
		}
		if item.Proof.Subject != "" || item.Proof.TargetID != "" {
			return transport.Unsupported(operation, "Gemini GenerateContent cannot preserve this provider proof shape")
		}
		if item.Type != "reasoning" && item.Type != "function_call" {
			return transport.Unsupported(operation, "Gemini GenerateContent cannot preserve a provider proof on "+item.Type)
		}
	}
	if field := transport.UnsupportedNamespace(request); field != "" {
		return transport.Unsupported(operation, "Gemini GenerateContent cannot preserve "+field)
	}
	if request.Reasoning != nil {
		return transport.Unsupported(operation, "Gemini GenerateContent transport does not support reasoning controls")
	}
	if request.ParallelToolCalls != nil && !*request.ParallelToolCalls && googleMayCallTools(request) {
		return transport.Unsupported(operation, "Gemini GenerateContent cannot guarantee disabled parallel tool calls")
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

func googleMayCallTools(request canonical.Request) bool {
	if len(request.Tools) == 0 {
		return false
	}
	if request.ToolChoice == nil {
		return true
	}
	mode := strings.ToUpper(strings.TrimSpace(request.ToolChoice.Mode))
	if mode == "" {
		mode = strings.ToUpper(strings.TrimSpace(request.ToolChoice.Type))
	}
	return mode != "NONE"
}

func validGoogleFunctionCallGroups(items []canonical.Item) bool {
	currentRole := ""
	seenFunctionCall := false
	for _, item := range items {
		if item.Proof != nil && item.Proof.Subject == canonical.ProofSubjectGooglePart {
			continue
		}
		role, emitted := googleItemRole(item)
		if !emitted {
			continue
		}
		if role != currentRole {
			currentRole = role
			seenFunctionCall = false
		}
		if item.Type != "function_call" {
			continue
		}
		proof := item.Proof
		if !seenFunctionCall && proof == nil {
			return false
		}
		if proof != nil && (proof.Provider != canonical.ProofProviderGoogle || proof.Value == "" || proof.Subject != "" || proof.TargetID != "") {
			return false
		}
		seenFunctionCall = true
	}
	return true
}

func googleItemRole(item canonical.Item) (string, bool) {
	switch item.Type {
	case "reasoning", "function_call":
		return "model", true
	case "function_call_output":
		return "user", true
	}
	switch item.Role {
	case canonical.RoleSystem, canonical.RoleDeveloper:
		return "", false
	case canonical.RoleAssistant:
		return "model", true
	default:
		return "user", true
	}
}

func validGooglePartProofCarrier(items []canonical.Item, index int) bool {
	if index < 0 || index >= len(items) {
		return false
	}
	carrier := items[index]
	if carrier.Proof == nil || strings.TrimSpace(carrier.Proof.TargetID) == "" {
		return false
	}
	if carrier.Type != "reasoning" || (carrier.Role != "" && carrier.Role != canonical.RoleAssistant) || carrier.Name != "" || carrier.CallID != "" || carrier.ProviderCallIDOmitted ||
		len(carrier.Content) != 0 || len(carrier.Arguments) != 0 || len(carrier.Output) != 0 || len(carrier.Extra) != 0 {
		return false
	}
	targetIndex := -1
	for candidate := range items {
		if items[candidate].ID != carrier.Proof.TargetID {
			continue
		}
		if targetIndex >= 0 {
			return false
		}
		targetIndex = candidate
	}
	if targetIndex < 0 || targetIndex == index {
		return false
	}
	target := items[targetIndex]
	if target.Type != "message" || target.Role != canonical.RoleAssistant || target.ID == "" ||
		carrier.Proof.TargetID != target.ID || len(target.Content) != 1 || target.Proof != nil {
		return false
	}
	parts, err := encodeContent(target.Content)
	return err == nil && len(parts) == 1
}

func hasFunctionCallHistory(items []canonical.Item) bool {
	for _, item := range items {
		if item.Type == "function_call" || item.Type == "function_call_output" {
			return true
		}
	}
	return false
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
			if googleReservedRequestField(key) {
				return nil, fmt.Errorf("google.generate_content.request_extras cannot override %q", key)
			}
			body[key] = value
		}
	}
	return json.Marshal(body)
}

func googleRequestExtrasConflict(raw json.RawMessage) string {
	if !transport.HasJSONValue(raw) {
		return ""
	}
	var extras map[string]json.RawMessage
	if json.Unmarshal(raw, &extras) != nil {
		return "invalid"
	}
	for key := range extras {
		if googleReservedRequestField(key) {
			return key
		}
	}
	return ""
}

func googleReservedRequestField(key string) bool {
	switch key {
	case "contents", "systemInstruction", "generationConfig", "tools", "toolConfig":
		return true
	default:
		return false
	}
}

func encodeItems(items []canonical.Item) ([]any, []string, error) {
	contents := make([]any, 0, len(items))
	system := make([]string, 0)
	type callReplay struct {
		name   string
		omitID bool
	}
	callNames := make(map[string]callReplay)
	if !validGoogleFunctionCallGroups(items) {
		return nil, nil, errors.New("Gemini function-call history is missing its first-call Google proof")
	}
	partProofs := make(map[string]*canonical.ProviderProof)
	partCarriers := make(map[int]bool)
	for index, item := range items {
		if item.Proof == nil || item.Proof.Subject != canonical.ProofSubjectGooglePart {
			continue
		}
		if !validGooglePartProofCarrier(items, index) || partProofs[item.Proof.TargetID] != nil {
			return nil, nil, errors.New("Gemini Google part proof carrier has no unique target message")
		}
		proof := *item.Proof
		partProofs[item.Proof.TargetID] = &proof
		partCarriers[index] = true
	}
	appendContent := func(role string, parts []any) {
		if len(contents) > 0 {
			if previous, ok := contents[len(contents)-1].(map[string]any); ok && previous["role"] == role {
				previousParts, _ := previous["parts"].([]any)
				previous["parts"] = append(previousParts, parts...)
				return
			}
		}
		contents = append(contents, map[string]any{"role": role, "parts": parts})
	}
	for index := 0; index < len(items); index++ {
		item := items[index]
		if partCarriers[index] {
			continue
		}
		if proof := partProofs[item.ID]; proof != nil {
			parts, err := encodeContent(item.Content)
			if err != nil {
				return nil, nil, err
			}
			if err := attachGooglePartProof(parts, proof.Value); err != nil {
				return nil, nil, err
			}
			appendContent("model", parts)
			continue
		}
		if item.Type == "reasoning" {
			parts, err := encodeReasoningItem(item)
			if err != nil {
				return nil, nil, err
			}
			appendContent("model", parts)
			continue
		}
		if item.Type == "function_call_output" {
			call, ok := callNames[item.CallID]
			if !ok || call.name == "" {
				return nil, nil, fmt.Errorf("function_call_output %q has no preceding function call", item.CallID)
			}
			response := any(map[string]any{})
			if len(item.Output) > 0 && json.Unmarshal(item.Output, &response) != nil {
				response = map[string]any{"result": string(item.Output)}
			}
			if _, ok := response.(map[string]any); !ok {
				response = map[string]any{"result": response}
			}
			functionResponse := map[string]any{"name": call.name, "response": response}
			if item.CallID != "" && !call.omitID {
				functionResponse["id"] = item.CallID
			}
			appendContent("user", []any{map[string]any{"functionResponse": functionResponse}})
			continue
		}
		if item.Type == "function_call" {
			thoughtSignature, err := googleProofValue(item)
			if err != nil {
				return nil, nil, err
			}
			args := any(map[string]any{})
			if len(item.Arguments) > 0 && json.Unmarshal(item.Arguments, &args) != nil {
				return nil, nil, fmt.Errorf("function_call %q arguments: invalid JSON", item.Name)
			}
			callNames[item.CallID] = callReplay{name: item.Name, omitID: item.ProviderCallIDOmitted}
			functionCall := map[string]any{"name": item.Name, "args": args}
			if item.CallID != "" && !item.ProviderCallIDOmitted {
				functionCall["id"] = item.CallID
			}
			part := map[string]any{"functionCall": functionCall}
			if thoughtSignature != "" {
				part["thoughtSignature"] = thoughtSignature
			}
			appendContent("model", []any{part})
			continue
		}
		if item.Proof != nil {
			return nil, nil, fmt.Errorf("Gemini GenerateContent cannot preserve a provider proof on %q", item.Type)
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
			appendContent("model", parts)
		default:
			appendContent("user", parts)
		}
	}
	return contents, system, nil
}

func attachGooglePartProof(parts []any, proof string) error {
	if proof == "" {
		return errors.New("Gemini Google text proof is empty")
	}
	if len(parts) != 1 {
		return errors.New("Gemini Google part proof target must contain exactly one part")
	}
	part, ok := parts[0].(map[string]any)
	if !ok {
		return errors.New("Gemini Google part proof target is invalid")
	}
	part["thoughtSignature"] = proof
	return nil
}

func encodeReasoningItem(item canonical.Item) ([]any, error) {
	thoughtSignature, err := googleProofValue(item)
	if err != nil {
		return nil, err
	}
	parts := make([]any, 0, len(item.Content))
	for _, block := range item.Content {
		switch block.Type {
		case "", "reasoning_text", "output_text", "text":
			parts = append(parts, map[string]any{"text": block.Text, "thought": true})
		default:
			return nil, fmt.Errorf("Gemini reasoning does not support content type %q", block.Type)
		}
	}
	if len(parts) == 0 {
		if thoughtSignature == "" {
			return nil, fmt.Errorf("Gemini reasoning input requires text or a Google provider proof")
		}
		parts = append(parts, map[string]any{"text": "", "thought": true})
	}
	if thoughtSignature != "" {
		parts[len(parts)-1].(map[string]any)["thoughtSignature"] = thoughtSignature
	}
	return parts, nil
}

func googleProofValue(item canonical.Item) (string, error) {
	if item.Proof == nil {
		return "", nil
	}
	value, ok := canonical.NativeProviderProofValue(item.Proof, canonical.ProofProviderGoogle)
	if !ok {
		return "", fmt.Errorf("Gemini GenerateContent cannot preserve provider proof %q", item.Proof.Provider)
	}
	if item.Proof.Subject != "" || item.Proof.TargetID != "" {
		return "", errors.New("Gemini GenerateContent cannot preserve this provider proof shape")
	}
	return value, nil
}

func encodeContent(content []canonical.Content) ([]any, error) {
	parts := make([]any, 0, len(content))
	for _, block := range content {
		switch block.Type {
		case "", "input_text", "output_text", "text":
			parts = append(parts, map[string]any{"text": block.Text})
		case "input_image", "output_image", "image", "input_file", "output_file", "file", "document", "input_audio", "output_audio", "audio", "input_video", "output_video", "video":
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
		Index   *int `json:"index"`
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
	if len(upstream.Candidates) > 1 {
		return canonical.Response{}, errors.New("Gemini returned multiple candidates, which Responses cannot represent losslessly")
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
		choiceIndex := geminiCandidateIndex(candidateIndex, candidate.Index)
		message := canonical.Item{Type: "message", Role: canonical.RoleAssistant, Status: "completed"}
		activeReasoning := -1
		flushMessage := func() {
			if len(message.Content) == 0 {
				return
			}
			response.Output = append(response.Output, message)
			message = canonical.Item{Type: "message", Role: canonical.RoleAssistant, Status: "completed"}
		}
		for partIndex, part := range candidate.Content.Parts {
			proof := googleProof(part.ThoughtSignature)
			if proof != nil && !part.Thought && part.FunctionCall == nil {
				if part.Text == "" && part.InlineData == nil && part.FileData == nil && activeReasoning >= 0 {
					response.Output[activeReasoning].Proof = proof
					continue
				}
				activeReasoning = -1
				content, _ := googlePartContent(part, true)
				flushMessage()
				messageID := fmt.Sprintf("message_%d_%d", choiceIndex, partIndex)
				proof.Subject, proof.TargetID = canonical.ProofSubjectGooglePart, messageID
				response.Output = append(response.Output,
					canonical.Item{ID: fmt.Sprintf("proof_%d_%d", choiceIndex, partIndex), Type: "reasoning", Role: canonical.RoleAssistant, Status: "completed", Proof: proof},
					canonical.Item{ID: messageID, Type: "message", Role: canonical.RoleAssistant, Status: "completed", Content: []canonical.Content{content}},
				)
				continue
			}
			if part.Text != "" {
				if part.Thought {
					flushMessage()
					response.Output = append(response.Output, canonical.Item{ID: fmt.Sprintf("reasoning_%d_%d", choiceIndex, partIndex), Type: "reasoning", Role: canonical.RoleAssistant, Status: "completed", Content: []canonical.Content{{Type: "reasoning_text", Text: part.Text}}, Proof: proof})
					activeReasoning = len(response.Output) - 1
				} else {
					activeReasoning = -1
					message.Content = append(message.Content, canonical.Content{Type: "output_text", Text: part.Text})
				}
			}
			if part.FunctionCall != nil {
				activeReasoning = -1
				flushMessage()
				callID := part.FunctionCall.ID
				if callID == "" {
					callID = fmt.Sprintf("call_%d_%d", choiceIndex, partIndex)
				}
				response.Output = append(response.Output, canonical.Item{ID: callID, Type: "function_call", Status: "completed", CallID: callID, Name: part.FunctionCall.Name, Arguments: append(json.RawMessage(nil), part.FunctionCall.Args...), Proof: proof, ProviderCallIDOmitted: part.FunctionCall.ID == ""})
			} else if part.Thought && part.Text == "" && proof != nil {
				flushMessage()
				response.Output = append(response.Output, canonical.Item{ID: fmt.Sprintf("reasoning_%d_%d", choiceIndex, partIndex), Type: "reasoning", Role: canonical.RoleAssistant, Status: "completed", Proof: proof})
				activeReasoning = len(response.Output) - 1
			}
			if part.InlineData != nil {
				activeReasoning = -1
				message.Content = append(message.Content, contentFromBlob(part.InlineData.MIMEType, part.InlineData.Data, ""))
			}
			if part.FileData != nil {
				activeReasoning = -1
				message.Content = append(message.Content, contentFromBlob(part.FileData.MIMEType, "", part.FileData.FileURI))
			}
		}
		flushMessage()
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

func googleProof(value string) *canonical.ProviderProof {
	if value == "" {
		return nil
	}
	return &canonical.ProviderProof{Provider: canonical.ProofProviderGoogle, Value: value}
}

func googlePartContent(part geminiPart, allowEmptyText bool) (canonical.Content, bool) {
	if part.Text != "" {
		return canonical.Content{Type: "output_text", Text: part.Text}, true
	}
	if part.InlineData != nil {
		return contentFromBlob(part.InlineData.MIMEType, part.InlineData.Data, ""), true
	}
	if part.FileData != nil {
		return contentFromBlob(part.FileData.MIMEType, "", part.FileData.FileURI), true
	}
	if allowEmptyText {
		return canonical.Content{Type: "output_text"}, true
	}
	return canonical.Content{}, false
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
	body            io.ReadCloser
	reader          *transport.SSEReader
	route           transport.Route
	sequence        int64
	pending         []canonical.Event
	nextOutputIndex int
	nextToolIndex   int
	activeMessages  map[int]googleStreamMessage
	activeReasoning map[int]googleStreamReasoning
	toolOutputs     map[string]googleStreamTool
	candidateChoice int
	hasCandidate    bool
}

type googleStreamMessage struct {
	id    string
	index int
}

type googleStreamReasoning struct {
	id        string
	index     int
	partIndex int
}

type googleStreamTool struct {
	outputIndex int
	toolIndex   int
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
		if err := s.validateCandidate(events); err != nil {
			return canonical.Event{}, err
		}
		events = s.normalizeEvents(events)
		s.pending = append(s.pending, events...)
	}
}

func (s *sse) validateCandidate(events []canonical.Event) error {
	for _, event := range events {
		if !isGoogleCandidateEvent(event.Type) {
			continue
		}
		if !s.hasCandidate {
			s.candidateChoice = event.ChoiceIndex
			s.hasCandidate = true
			continue
		}
		if event.ChoiceIndex != s.candidateChoice {
			return errors.New("Gemini returned multiple candidates, which Responses cannot represent losslessly")
		}
	}
	return nil
}

func isGoogleCandidateEvent(eventType canonical.EventType) bool {
	switch eventType {
	case canonical.EventTextDelta, canonical.EventReasoningDelta, canonical.EventProviderProof,
		canonical.EventOutputItemAdded, canonical.EventToolArgumentsDelta,
		canonical.EventCompleted, canonical.EventIncomplete, canonical.EventFailed:
		return true
	default:
		return false
	}
}

func (s *sse) normalizeEvents(events []canonical.Event) []canonical.Event {
	if s.activeMessages == nil {
		s.activeMessages = make(map[int]googleStreamMessage)
		s.activeReasoning = make(map[int]googleStreamReasoning)
		s.toolOutputs = make(map[string]googleStreamTool)
	}
	signedTargets := make(map[string]bool)
	for _, event := range events {
		if event.Type == canonical.EventProviderProof && event.Item != nil && event.Item.Proof != nil && event.Item.Proof.Subject == canonical.ProofSubjectGooglePart {
			signedTargets[event.Item.Proof.TargetID] = true
		}
	}
	reasoningProofTargets := make(map[string]googleStreamReasoning)
	emptyTextTargets := make(map[string]bool)
	for _, event := range events {
		if event.Type == canonical.EventTextDelta && event.Delta == "" && event.Item != nil && event.Item.ID != "" {
			emptyTextTargets[googleStreamTargetKey(event.ChoiceIndex, event.Item.ID)] = true
		}
	}
	signatureOnlyTargets := make(map[string]bool)
	for _, event := range events {
		if event.Type != canonical.EventProviderProof || event.Item == nil || event.Item.Proof == nil || event.Item.Proof.Subject != canonical.ProofSubjectGooglePart {
			continue
		}
		key := googleStreamTargetKey(event.ChoiceIndex, event.Item.Proof.TargetID)
		if emptyTextTargets[key] {
			signatureOnlyTargets[key] = true
		}
	}
	targets := make(map[string]googleStreamMessage)
	partMessages := make(map[string]googleStreamMessage)
	textPartCounts := make(map[int]int)
	result := make([]canonical.Event, 0, len(events))
	for index := range events {
		event := &events[index]
		choice := event.ChoiceIndex
		if event.Type == canonical.EventTextDelta && event.Delta == "" && event.Item != nil {
			key := googleStreamTargetKey(choice, event.Item.ID)
			if signatureOnlyTargets[key] {
				if reasoning, active := s.activeReasoning[choice]; active {
					reasoningProofTargets[key] = reasoning
					continue
				}
			}
		}
		switch event.Type {
		case canonical.EventReasoningDelta, canonical.EventProviderProof:
			if event.Item != nil && event.Item.Proof != nil && event.Item.Proof.Subject == canonical.ProofSubjectGooglePart {
				key := googleStreamTargetKey(choice, event.Item.Proof.TargetID)
				if reasoning, ok := reasoningProofTargets[key]; ok {
					proof := *event.Item.Proof
					proof.Subject, proof.TargetID = "", ""
					event.Item = &canonical.Item{ID: reasoning.id, Type: "reasoning", Role: canonical.RoleAssistant, Proof: &proof}
					event.OutputIndex = reasoning.index
					event.ContentIndex = 0
					break
				}
				delete(s.activeReasoning, choice)
				if target, ok := targets[event.Item.Proof.TargetID]; ok {
					event.Item.Proof.TargetID = target.id
				}
				event.OutputIndex = s.allocateOutput()
				event.ContentIndex = 0
				break
			}
			delete(s.activeMessages, choice)
			sourcePartIndex := event.ContentIndex
			state, ok := s.activeReasoning[choice]
			if !ok || state.partIndex != sourcePartIndex {
				outputIndex := s.allocateOutput()
				state = googleStreamReasoning{
					id:        fmt.Sprintf("reasoning_%d_%d", choice, outputIndex),
					index:     outputIndex,
					partIndex: sourcePartIndex,
				}
				s.activeReasoning[choice] = state
			}
			if event.Item == nil {
				event.Item = &canonical.Item{Type: "reasoning", Role: canonical.RoleAssistant}
			}
			event.Item.ID = state.id
			event.OutputIndex = state.index
			event.ContentIndex = 0
		case canonical.EventTextDelta:
			delete(s.activeReasoning, choice)
			partIndex := event.ContentIndex
			originalID := ""
			if event.Item != nil {
				originalID = event.Item.ID
			}
			partKey := fmt.Sprintf("%d:%d", choice, partIndex)
			message, assigned := partMessages[partKey]
			reused := false
			if !assigned {
				if textPartCounts[choice] == 0 {
					message, reused = s.activeMessages[choice]
				}
				if !reused {
					outputIndex := s.allocateOutput()
					messageID := originalID
					if messageID == "" {
						messageID = fmt.Sprintf("message_%d_%d", choice, outputIndex)
					}
					message = googleStreamMessage{id: messageID, index: outputIndex}
				}
				partMessages[partKey] = message
				s.activeMessages[choice] = message
				textPartCounts[choice]++
			}
			if originalID != "" && signedTargets[originalID] {
				targets[originalID] = message
				event.Item.ID = message.id
				event.OutputIndex = message.index
				event.ContentIndex = 0
				if event.Delta == "" && reused {
					continue
				}
				break
			}
			if event.Item == nil {
				event.Item = &canonical.Item{Type: "message", Role: canonical.RoleAssistant}
			}
			event.Item.ID = message.id
			event.OutputIndex = message.index
			event.ContentIndex = 0
		case canonical.EventOutputItemAdded, canonical.EventToolArgumentsDelta:
			if event.Item != nil && event.Item.Type == "function_call" {
				delete(s.activeMessages, choice)
				delete(s.activeReasoning, choice)
				identity := event.Item.CallID
				if identity == "" {
					identity = event.Item.ID
				}
				key := fmt.Sprintf("%d:%s", choice, identity)
				tool, ok := s.toolOutputs[key]
				if !ok {
					tool = googleStreamTool{outputIndex: s.allocateOutput(), toolIndex: s.nextToolIndex}
					s.nextToolIndex++
					s.toolOutputs[key] = tool
				}
				event.OutputIndex, event.ToolIndex = tool.outputIndex, tool.toolIndex
				break
			}
			delete(s.activeMessages, choice)
			delete(s.activeReasoning, choice)
			event.OutputIndex = s.allocateOutput()
			if event.Item != nil && signedTargets[event.Item.ID] {
				targets[event.Item.ID] = googleStreamMessage{id: event.Item.ID, index: event.OutputIndex}
			}
		case canonical.EventCompleted, canonical.EventIncomplete, canonical.EventFailed:
			delete(s.activeMessages, choice)
			delete(s.activeReasoning, choice)
		}
		result = append(result, *event)
	}
	return result
}

func (s *sse) allocateOutput() int {
	index := s.nextOutputIndex
	s.nextOutputIndex++
	return index
}

func googleStreamTargetKey(choice int, target string) string {
	return fmt.Sprintf("%d:%s", choice, target)
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
	if len(response.Candidates) > 1 {
		return nil, errors.New("Gemini returned multiple candidates, which Responses cannot represent losslessly")
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
		choiceIndex := geminiCandidateIndex(candidateIndex, candidate.Index)
		for partIndex, part := range candidate.Content.Parts {
			proof := googleProof(part.ThoughtSignature)
			emitted := false
			if proof != nil && !part.Thought && part.FunctionCall == nil {
				content, _ := googlePartContent(part, true)
				messageID := fmt.Sprintf("message_%d_%d_%d", choiceIndex, sequence, partIndex)
				proof.Subject, proof.TargetID = canonical.ProofSubjectGooglePart, messageID
				carrier := canonical.Item{ID: fmt.Sprintf("proof_%d_%d_%d", choiceIndex, sequence, partIndex), Type: "reasoning", Role: canonical.RoleAssistant, Proof: proof}
				message := canonical.Item{ID: messageID, Type: "message", Role: canonical.RoleAssistant, Content: []canonical.Content{content}}
				if content.Type == "output_text" {
					message.Content = nil
					events = append(events, canonical.Event{Type: canonical.EventTextDelta, SequenceNumber: sequence, ChoiceIndex: choiceIndex, ContentIndex: partIndex, Delta: content.Text, Item: &message, Raw: append(json.RawMessage(nil), raw...)})
				} else {
					events = append(events, canonical.Event{Type: canonical.EventOutputItemAdded, SequenceNumber: sequence, ChoiceIndex: choiceIndex, ContentIndex: partIndex, Item: &message, Raw: append(json.RawMessage(nil), raw...)})
				}
				events = append(events, canonical.Event{Type: canonical.EventProviderProof, SequenceNumber: sequence, ChoiceIndex: choiceIndex, ContentIndex: partIndex, Item: &carrier, Raw: append(json.RawMessage(nil), raw...)})
				continue
			}
			if part.Text != "" {
				typeName := canonical.EventTextDelta
				var item *canonical.Item
				if part.Thought {
					typeName = canonical.EventReasoningDelta
					item = &canonical.Item{ID: fmt.Sprintf("reasoning_%d_%d", choiceIndex, partIndex), Type: "reasoning", Role: canonical.RoleAssistant, Proof: proof}
				}
				event := canonical.Event{Type: typeName, SequenceNumber: sequence, ChoiceIndex: choiceIndex, ContentIndex: partIndex, Delta: part.Text, Item: item, Raw: append(json.RawMessage(nil), raw...)}
				if typeName == canonical.EventReasoningDelta {
					event.OutputIndex = partIndex
				}
				events = append(events, event)
				emitted = true
			}
			if part.FunctionCall != nil {
				callID := part.FunctionCall.ID
				if callID == "" {
					callID = fmt.Sprintf("call_%d_%d_%d", choiceIndex, sequence, partIndex)
				}
				item := &canonical.Item{ID: callID, Type: "function_call", CallID: callID, Name: part.FunctionCall.Name, Proof: proof, ProviderCallIDOmitted: part.FunctionCall.ID == ""}
				events = append(events,
					canonical.Event{Type: canonical.EventOutputItemAdded, SequenceNumber: sequence, ChoiceIndex: choiceIndex, ToolIndex: partIndex, Item: item, Raw: append(json.RawMessage(nil), raw...)},
					canonical.Event{Type: canonical.EventToolArgumentsDelta, SequenceNumber: sequence, ChoiceIndex: choiceIndex, ToolIndex: partIndex, Delta: string(part.FunctionCall.Args), Item: item, Raw: append(json.RawMessage(nil), raw...)},
				)
				emitted = true
			}
			if part.InlineData != nil {
				item := canonical.Item{Type: "message", Role: canonical.RoleAssistant, Content: []canonical.Content{contentFromBlob(part.InlineData.MIMEType, part.InlineData.Data, "")}}
				events = append(events, canonical.Event{Type: canonical.EventOutputItemAdded, SequenceNumber: sequence, ChoiceIndex: choiceIndex, ContentIndex: partIndex, Item: &item, Raw: append(json.RawMessage(nil), raw...)})
				emitted = true
			}
			if part.FileData != nil {
				item := canonical.Item{Type: "message", Role: canonical.RoleAssistant, Content: []canonical.Content{contentFromBlob(part.FileData.MIMEType, "", part.FileData.FileURI)}}
				events = append(events, canonical.Event{Type: canonical.EventOutputItemAdded, SequenceNumber: sequence, ChoiceIndex: choiceIndex, ContentIndex: partIndex, Item: &item, Raw: append(json.RawMessage(nil), raw...)})
				emitted = true
			}
			if !emitted && proof != nil {
				item := canonical.Item{ID: fmt.Sprintf("reasoning_%d_%d", choiceIndex, partIndex), Type: "reasoning", Role: canonical.RoleAssistant, Proof: proof}
				events = append(events, canonical.Event{Type: canonical.EventProviderProof, SequenceNumber: sequence, ChoiceIndex: choiceIndex, OutputIndex: partIndex, ContentIndex: partIndex, Item: &item, Raw: append(json.RawMessage(nil), raw...)})
			}
		}
		if candidate.FinishReason != "" {
			terminalType = finishEventType(candidate.FinishReason)
			terminalChoice = choiceIndex
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
	format := ""
	switch {
	case strings.HasPrefix(mediaType, "image/"):
		typeName = "output_image"
	case strings.HasPrefix(mediaType, "audio/"):
		typeName = "output_audio"
		format = audioFormat(mediaType)
	case strings.HasPrefix(mediaType, "video/"):
		typeName = "output_video"
	}
	return canonical.Content{Type: typeName, Data: data, URL: url, MediaType: mediaType, Format: format}
}

func geminiCandidateIndex(fallback int, index *int) int {
	if index != nil && *index >= 0 {
		return *index
	}
	return fallback
}

func audioFormat(mediaType string) string {
	mediaType = strings.ToLower(strings.TrimSpace(strings.SplitN(mediaType, ";", 2)[0]))
	switch mediaType {
	case "audio/mpeg", "audio/mp3":
		return "mp3"
	case "audio/x-wav":
		return "wav"
	}
	return strings.TrimPrefix(mediaType, "audio/")
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

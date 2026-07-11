package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/pkg/httputil"
)

// GoogleProvider Gemini API
type GoogleProvider struct {
	config ProviderConfig
}

func NewGoogleProvider(config ProviderConfig) *GoogleProvider {
	return &GoogleProvider{config: config}
}

func (p *GoogleProvider) Name() string {
	return "google"
}

func (p *GoogleProvider) Complete(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	geminiReq := p.convertRequest(req)

	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s",
		p.config.BaseURL, p.config.VendorModel, p.config.APIKey)

	ctx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	defer cancel()

	resp, err := httputil.PostJSON(ctx, url, geminiReq, p.config.ExtraHeaders)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return p.convertResponse(resp)
}

func (p *GoogleProvider) StreamComplete(ctx context.Context, req *ChatRequest) (*http.Response, error) {
	return nil, fmt.Errorf("streaming not supported for provider: %s", p.Name())
}

func (p *GoogleProvider) ListModels(ctx context.Context) ([]domain.ModelInfo, error) {
	url := fmt.Sprintf("%s/v1beta/models?key=%s", p.config.BaseURL, p.config.APIKey)

	resp, err := httputil.Get(ctx, url, nil)
	if err != nil {
		return nil, fmt.Errorf("list models failed: %w", err)
	}

	var result struct {
		Models []struct {
			Name                       string   `json:"name"`
			DisplayName                string   `json:"displayName"`
			SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
			InputTokenLimit            int      `json:"inputTokenLimit"`
			OutputTokenLimit           int      `json:"outputTokenLimit"`
		} `json:"models"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("unmarshal models failed: %w", err)
	}

	models := make([]domain.ModelInfo, 0, len(result.Models))
	for _, m := range result.Models {
		// name 格式: "models/gemini-2.0-flash"
		id := strings.TrimPrefix(m.Name, "models/")
		models = append(models, domain.ModelInfo{
			ID:        id,
			Name:      m.DisplayName,
			Provider:  p.Name(),
			Type:      "chat",
			MaxTokens: m.OutputTokenLimit,
			Features:  m.SupportedGenerationMethods,
			RawMeta: map[string]any{
				"input_token_limit":  m.InputTokenLimit,
				"output_token_limit": m.OutputTokenLimit,
			},
		})
	}
	return models, nil
}

func (p *GoogleProvider) convertRequest(req *ChatRequest) map[string]any {
	var contents []map[string]any
	var systemInstructions []string

	for _, msg := range req.Messages {
		role := msg.Role
		if role == "assistant" {
			role = "model"
		}
		if role == "system" {
			if text := msg.ContentText(); text != "" {
				systemInstructions = append(systemInstructions, text)
			}
			continue
		}
		if role == "developer" || role == "tool" {
			role = "user"
		}

		parts := convertToGeminiParts(msg.Content)
		if msg.Role == "assistant" {
			for _, call := range msg.ToolCalls {
				var args any = map[string]any{}
				if call.Function.Arguments != "" {
					_ = json.Unmarshal([]byte(call.Function.Arguments), &args)
				}
				parts = append(parts, map[string]any{"functionCall": map[string]any{
					"name": call.Function.Name, "args": args,
				}})
			}
		}
		if msg.Role == "tool" {
			parts = []map[string]any{{"functionResponse": map[string]any{
				"name":     findToolCallName(req.Messages, msg.ToolCallID),
				"response": toolResponseValue(msg.ContentText()),
			}}}
		}
		entry := map[string]any{
			"role":  role,
			"parts": parts,
		}
		if msg.Role == "tool" && len(contents) > 0 && contents[len(contents)-1]["role"] == "user" {
			current, _ := contents[len(contents)-1]["parts"].([]map[string]any)
			contents[len(contents)-1]["parts"] = append(current, parts...)
		} else {
			contents = append(contents, entry)
		}
	}

	result := map[string]any{
		"contents": contents,
	}

	// Gemini 的 system instruction
	if len(systemInstructions) > 0 {
		result["systemInstruction"] = map[string]any{
			"parts": []map[string]any{
				{"text": strings.Join(systemInstructions, "\n")},
			},
		}
	}

	// generationConfig：补全所有参数
	generationConfig := map[string]any{}
	if req.MaxCompletionTokens != nil {
		generationConfig["maxOutputTokens"] = *req.MaxCompletionTokens
	} else if req.MaxTokens > 0 {
		generationConfig["maxOutputTokens"] = req.MaxTokens
	}
	if req.Temperature != nil {
		generationConfig["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		generationConfig["topP"] = *req.TopP
	}
	if req.N != nil {
		generationConfig["candidateCount"] = *req.N
	}
	if len(req.Stop) > 0 {
		generationConfig["stopSequences"] = req.Stop
	}
	if req.ResponseFormat != nil && req.ResponseFormat.Type == "json_object" {
		generationConfig["responseMimeType"] = "application/json"
	}
	if req.ResponseFormat != nil && req.ResponseFormat.Type == "json_schema" {
		generationConfig["responseMimeType"] = "application/json"
		var wrapper struct {
			Schema any `json:"schema"`
		}
		if json.Unmarshal(req.ResponseFormat.JSONSchema, &wrapper) == nil && wrapper.Schema != nil {
			generationConfig["responseJsonSchema"] = wrapper.Schema
		}
	}
	if len(generationConfig) > 0 {
		result["generationConfig"] = generationConfig
	}

	// Tool Use: function declarations
	if len(req.Tools) > 0 {
		var funcDecls []map[string]any
		for _, t := range req.Tools {
			decl := map[string]any{
				"name":        t.Function.Name,
				"description": t.Function.Description,
			}
			if t.Function.Parameters != nil {
				var schema any
				json.Unmarshal(t.Function.Parameters, &schema)
				decl["parameters"] = schema
			}
			funcDecls = append(funcDecls, decl)
		}
		result["tools"] = []map[string]any{
			{"functionDeclarations": funcDecls},
		}
	}
	if req.ToolChoice != nil {
		config := map[string]any{}
		switch choice := req.ToolChoice.(type) {
		case string:
			switch choice {
			case "auto":
				config["mode"] = "AUTO"
			case "none":
				config["mode"] = "NONE"
			case "required":
				config["mode"] = "ANY"
			}
		case map[string]any:
			if fn, ok := choice["function"].(map[string]any); ok {
				if name, ok := fn["name"].(string); ok {
					config["mode"] = "ANY"
					config["allowedFunctionNames"] = []string{name}
				}
			}
		}
		if len(config) > 0 {
			result["toolConfig"] = map[string]any{"functionCallingConfig": config}
		}
	}

	return result
}

func findToolCallName(messages []ChatMessage, callID string) string {
	for i := len(messages) - 1; i >= 0; i-- {
		for _, call := range messages[i].ToolCalls {
			if call.ID == callID {
				return call.Function.Name
			}
		}
	}
	return callID
}

func toolResponseValue(content string) any {
	var value any
	if content != "" && json.Unmarshal([]byte(content), &value) == nil {
		return value
	}
	return map[string]any{"content": content}
}

// convertToGeminiParts 将 Content (string 或 []ContentPart) 转为 Gemini parts
func convertToGeminiParts(content any) []map[string]any {
	if content == nil {
		return nil
	}
	switch v := content.(type) {
	case string:
		return []map[string]any{{"text": v}}
	case []any:
		var parts []map[string]any
		for _, part := range v {
			pm, ok := part.(map[string]any)
			if !ok {
				continue
			}
			switch pm["type"] {
			case "text":
				parts = append(parts, map[string]any{"text": pm["text"]})
			case "image_url":
				if imgURL, ok := pm["image_url"].(map[string]any); ok {
					url, _ := imgURL["url"].(string)
					// data: URL 走 inlineData(内联 base64); http(s) URL 才用 fileData.fileUri
					// Gemini 的 fileUri 只接受 File API/GCS URI,直接塞 data: URL 会失效
					if mime, b64, ok := parseDataURL(url); ok {
						parts = append(parts, map[string]any{
							"inlineData": map[string]any{
								"mimeType": mime,
								"data":     b64,
							},
						})
					} else {
						parts = append(parts, map[string]any{
							"fileData": map[string]any{
								"fileUri": url,
							},
						})
					}
				}
			case "file_url":
				if fileURL, ok := pm["file_url"].(map[string]any); ok {
					url, _ := fileURL["url"].(string)
					ct, _ := fileURL["content_type"].(string)
					if ct == "" {
						ct = "application/pdf"
					}
					if mediaType, data, ok := parseDataURL(url); ok {
						if ct == "" {
							ct = mediaType
						}
						parts = append(parts, map[string]any{"inlineData": map[string]any{"mimeType": ct, "data": data}})
						continue
					}
					parts = append(parts, map[string]any{
						"fileData": map[string]any{
							"mimeType": ct,
							"fileUri":  url,
						},
					})
				}
			case "file":
				if file, ok := pm["file"].(map[string]any); ok {
					if mediaType, data, ok := parseChatFileData(file); ok {
						parts = append(parts, map[string]any{
							"inlineData": map[string]any{
								"mimeType": mediaType,
								"data":     data,
							},
						})
					}
				}
			}
		}
		return parts
	default:
		return []map[string]any{{"text": fmt.Sprint(content)}}
	}
}

func (p *GoogleProvider) convertResponse(body []byte) (*ChatResponse, error) {
	var geminiResp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text         string `json:"text,omitempty"`
					Thought      bool   `json:"thought,omitempty"`
					FunctionCall *struct {
						Name string          `json:"name"`
						Args json.RawMessage `json:"args"`
					} `json:"functionCall,omitempty"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
			TotalTokenCount      int `json:"totalTokenCount"`
		} `json:"usageMetadata"`
	}

	if err := json.Unmarshal(body, &geminiResp); err != nil {
		return nil, fmt.Errorf("unmarshal response failed: %w", err)
	}

	choices := make([]ChatChoice, 0, len(geminiResp.Candidates))
	for candidateIndex, candidate := range geminiResp.Candidates {
		content := ""
		reasoning := ""
		finishReason := candidate.FinishReason
		var toolCalls []ToolCall
		for i, part := range candidate.Content.Parts {
			if part.Text != "" {
				if part.Thought {
					reasoning += part.Text
				} else {
					content += part.Text
				}
			}
			if part.FunctionCall != nil {
				toolCalls = append(toolCalls, ToolCall{
					ID:   fmt.Sprintf("call_%d_%d", candidateIndex, i),
					Type: "function",
					Function: FunctionCall{
						Name:      part.FunctionCall.Name,
						Arguments: string(part.FunctionCall.Args),
					},
				})
			}
		}
		msg := ChatMessage{Role: "assistant", Content: content, ReasoningContent: reasoning}
		if len(toolCalls) > 0 {
			msg.ToolCalls = toolCalls
			finishReason = "tool_calls"
		}
		choices = append(choices, ChatChoice{Index: candidateIndex, Message: msg, FinishReason: finishReason})
	}

	return &ChatResponse{
		ID:      fmt.Sprintf("gemini-%d", time.Now().UnixNano()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   p.config.VendorModel,
		Choices: choices,
		Usage: &ChatUsage{
			PromptTokens:     geminiResp.UsageMetadata.PromptTokenCount,
			CompletionTokens: geminiResp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      geminiResp.UsageMetadata.TotalTokenCount,
		},
	}, nil
}

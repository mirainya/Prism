package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

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

func (p *GoogleProvider) convertRequest(req *ChatRequest) map[string]any {
	var contents []map[string]any
	var systemInstruction string

	for _, msg := range req.Messages {
		role := msg.Role
		if role == "assistant" {
			role = "model"
		}
		if role == "system" {
			systemInstruction = msg.ContentText()
			continue
		}

		parts := convertToGeminiParts(msg.Content)
		contents = append(contents, map[string]any{
			"role":  role,
			"parts": parts,
		})
	}

	result := map[string]any{
		"contents": contents,
	}

	// Gemini 的 system instruction
	if systemInstruction != "" {
		result["systemInstruction"] = map[string]any{
			"parts": []map[string]any{
				{"text": systemInstruction},
			},
		}
	}

	// generationConfig：补全所有参数
	generationConfig := map[string]any{}
	if req.MaxTokens > 0 {
		generationConfig["maxOutputTokens"] = req.MaxTokens
	}
	if req.Temperature > 0 {
		generationConfig["temperature"] = req.Temperature
	}
	if req.TopP > 0 {
		generationConfig["topP"] = req.TopP
	}
	if len(req.Stop) > 0 {
		generationConfig["stopSequences"] = req.Stop
	}
	if req.ResponseFormat != nil && req.ResponseFormat.Type == "json_object" {
		generationConfig["responseMimeType"] = "application/json"
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

	return result
}

// convertToGeminiParts 将 Content (string 或 []ContentPart) 转为 Gemini parts
func convertToGeminiParts(content any) []map[string]any {
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
					parts = append(parts, map[string]any{
						"fileData": map[string]any{
							"fileUri": url,
						},
					})
				}
			case "file_url":
				if fileURL, ok := pm["file_url"].(map[string]any); ok {
					url, _ := fileURL["url"].(string)
					ct, _ := fileURL["content_type"].(string)
					if ct == "" {
						ct = "application/pdf"
					}
					parts = append(parts, map[string]any{
						"fileData": map[string]any{
							"mimeType": ct,
							"fileUri":  url,
						},
					})
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

	content := ""
	reasoning := ""
	finishReason := ""
	var toolCalls []ToolCall

	if len(geminiResp.Candidates) > 0 {
		candidate := geminiResp.Candidates[0]
		finishReason = candidate.FinishReason
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
					ID:   fmt.Sprintf("call_%d", i),
					Type: "function",
					Function: FunctionCall{
						Name:      part.FunctionCall.Name,
						Arguments: string(part.FunctionCall.Args),
					},
				})
			}
		}
	}

	msg := ChatMessage{
		Role:             "assistant",
		Content:          content,
		ReasoningContent: reasoning,
	}
	if len(toolCalls) > 0 {
		msg.ToolCalls = toolCalls
		finishReason = "tool_calls"
	}

	return &ChatResponse{
		ID:      fmt.Sprintf("gemini-%d", time.Now().UnixNano()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   p.config.VendorModel,
		Choices: []ChatChoice{{
			Index:        0,
			Message:      msg,
			FinishReason: finishReason,
		}},
		Usage: &ChatUsage{
			PromptTokens:     geminiResp.UsageMetadata.PromptTokenCount,
			CompletionTokens: geminiResp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      geminiResp.UsageMetadata.TotalTokenCount,
		},
	}, nil
}

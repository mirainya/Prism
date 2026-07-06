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

// AnthropicProvider Claude API
type AnthropicProvider struct {
	config ProviderConfig
}

func NewAnthropicProvider(config ProviderConfig) *AnthropicProvider {
	return &AnthropicProvider{config: config}
}

func (p *AnthropicProvider) Name() string {
	return "anthropic"
}

func (p *AnthropicProvider) Complete(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	anthropicReq := p.convertRequest(req)

	url := p.config.BaseURL + "/v1/messages"

	headers := map[string]string{
		"x-api-key":         p.config.APIKey,
		"anthropic-version": "2023-06-01",
	}
	for k, v := range p.config.ExtraHeaders {
		headers[k] = v
	}

	ctx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	defer cancel()

	resp, err := httputil.PostJSON(ctx, url, anthropicReq, headers)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return p.convertResponse(resp)
}

func (p *AnthropicProvider) StreamComplete(ctx context.Context, req *ChatRequest) (*http.Response, error) {
	return nil, fmt.Errorf("streaming not supported for provider: %s", p.Name())
}

func (p *AnthropicProvider) ListModels(ctx context.Context) ([]domain.ModelInfo, error) {
	// Anthropic 无公开 models API，返回已知模型列表
	knownModels := []domain.ModelInfo{
		{ID: "claude-sonnet-4-20250514", Name: "Claude Sonnet 4", Provider: "anthropic", Type: "chat", MaxTokens: 8192, Features: []string{"streaming", "tools", "vision"}},
		{ID: "claude-opus-4-20250514", Name: "Claude Opus 4", Provider: "anthropic", Type: "chat", MaxTokens: 8192, Features: []string{"streaming", "tools", "vision"}},
		{ID: "claude-3-7-sonnet-20250219", Name: "Claude 3.7 Sonnet", Provider: "anthropic", Type: "chat", MaxTokens: 8192, Features: []string{"streaming", "tools", "vision", "extended_thinking"}},
		{ID: "claude-3-5-sonnet-20241022", Name: "Claude 3.5 Sonnet", Provider: "anthropic", Type: "chat", MaxTokens: 8192, Features: []string{"streaming", "tools", "vision"}},
		{ID: "claude-3-5-haiku-20241022", Name: "Claude 3.5 Haiku", Provider: "anthropic", Type: "chat", MaxTokens: 8192, Features: []string{"streaming", "tools"}},
	}
	return knownModels, nil
}

// ConvertRequestToAnthropic 将 OpenAI 格式的 ChatRequest 转换为 Anthropic Messages API 格式
func ConvertRequestToAnthropic(req *ChatRequest) map[string]any {
	result := map[string]any{
		"model":      req.Model,
		"max_tokens": req.MaxTokens,
	}

	if req.MaxTokens == 0 {
		result["max_tokens"] = 4096
	}

	if req.Temperature != nil {
		result["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		result["top_p"] = *req.TopP
	}
	if len(req.Stop) > 0 {
		result["stop_sequences"] = req.Stop
	}

	var messages []map[string]any
	for _, msg := range req.Messages {
		if msg.Role == "system" {
			result["system"] = msg.ContentText()
			continue
		}

		m := map[string]any{
			"role": msg.Role,
		}

		switch v := msg.Content.(type) {
		case string:
			m["content"] = v
		case []any:
			m["content"] = ConvertToAnthropicContent(v)
		default:
			m["content"] = fmt.Sprint(msg.Content)
		}

		if msg.ToolCallID != "" {
			// Anthropic 只接受 user/assistant,tool_result 必须放在 user 消息里
			m["role"] = "user"
			m["content"] = []map[string]any{{
				"type":        "tool_result",
				"tool_use_id": msg.ToolCallID,
				"content":     msg.ContentText(),
			}}
		}

		messages = append(messages, m)
	}
	result["messages"] = messages

	if len(req.Tools) > 0 {
		var tools []map[string]any
		for _, t := range req.Tools {
			tool := map[string]any{
				"name":        t.Function.Name,
				"description": t.Function.Description,
			}
			if t.Function.Parameters != nil {
				var schema any
				json.Unmarshal(t.Function.Parameters, &schema)
				tool["input_schema"] = schema
			}
			tools = append(tools, tool)
		}
		result["tools"] = tools
	}

	return result
}

func (p *AnthropicProvider) convertRequest(req *ChatRequest) map[string]any {
	body := ConvertRequestToAnthropic(req)
	body["model"] = p.config.VendorModel
	return body
}

// ConvertToAnthropicContent 将 OpenAI 格式的 content parts 转为 Anthropic 格式
func ConvertToAnthropicContent(parts []any) []map[string]any {
	var blocks []map[string]any
	for _, part := range parts {
		pm, ok := part.(map[string]any)
		if !ok {
			continue
		}
		switch pm["type"] {
		case "text":
			blocks = append(blocks, map[string]any{
				"type": "text",
				"text": pm["text"],
			})
		case "image_url":
			if imgURL, ok := pm["image_url"].(map[string]any); ok {
				url, _ := imgURL["url"].(string)
				if strings.HasPrefix(url, "data:") {
					mediaType, data, ok := parseDataURL(url)
					if ok {
						blocks = append(blocks, map[string]any{
							"type": "image",
							"source": map[string]any{
								"type":       "base64",
								"media_type": mediaType,
								"data":       data,
							},
						})
						continue
					}
				}
				blocks = append(blocks, map[string]any{
					"type": "image",
					"source": map[string]any{
						"type": "url",
						"url":  url,
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
				blocks = append(blocks, map[string]any{
					"type": "document",
					"source": map[string]any{
						"type":       "url",
						"url":        url,
						"media_type": ct,
					},
				})
			}
		}
	}
	return blocks
}

// ParseAnthropicResponse 将 Anthropic Messages API 响应转换为 OpenAI 格式
func ParseAnthropicResponse(body []byte, modelName string) (*ChatResponse, error) {
	var anthropicResp struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type     string          `json:"type"`
			Text     string          `json:"text,omitempty"`
			Thinking string          `json:"thinking,omitempty"`
			ID       string          `json:"id,omitempty"`
			Name     string          `json:"name,omitempty"`
			Input    json.RawMessage `json:"input,omitempty"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(body, &anthropicResp); err != nil {
		return nil, fmt.Errorf("unmarshal response failed: %w", err)
	}

	content := ""
	reasoning := ""
	var toolCalls []ToolCall
	for _, block := range anthropicResp.Content {
		switch block.Type {
		case "thinking":
			reasoning += block.Thinking
		case "text":
			content += block.Text
		case "tool_use":
			toolCalls = append(toolCalls, ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: FunctionCall{
					Name:      block.Name,
					Arguments: string(block.Input),
				},
			})
		}
	}

	finishReason := anthropicResp.StopReason
	if finishReason == "tool_use" {
		finishReason = "tool_calls"
	} else if finishReason == "end_turn" {
		finishReason = "stop"
	}

	msg := ChatMessage{
		Role:             "assistant",
		Content:          content,
		ReasoningContent: reasoning,
	}
	if len(toolCalls) > 0 {
		msg.ToolCalls = toolCalls
	}

	return &ChatResponse{
		ID:      anthropicResp.ID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   modelName,
		Choices: []ChatChoice{{
			Index:        0,
			Message:      msg,
			FinishReason: finishReason,
		}},
		Usage: &ChatUsage{
			PromptTokens:     anthropicResp.Usage.InputTokens,
			CompletionTokens: anthropicResp.Usage.OutputTokens,
			TotalTokens:      anthropicResp.Usage.InputTokens + anthropicResp.Usage.OutputTokens,
		},
	}, nil
}

func (p *AnthropicProvider) convertResponse(body []byte) (*ChatResponse, error) {
	return ParseAnthropicResponse(body, p.config.VendorModel)
}

// parseDataURL 解析 data:image/jpeg;base64,... 格式
func parseDataURL(url string) (mediaType, data string, ok bool) {
	// data:<mediatype>;base64,<data>
	if !strings.HasPrefix(url, "data:") {
		return "", "", false
	}
	rest := url[5:]
	semicolon := strings.Index(rest, ";base64,")
	if semicolon < 0 {
		return "", "", false
	}
	return rest[:semicolon], rest[semicolon+8:], true
}

package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

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

func (p *AnthropicProvider) convertRequest(req *ChatRequest) map[string]any {
	result := map[string]any{
		"model":      p.config.VendorModel,
		"max_tokens": req.MaxTokens,
	}

	if req.MaxTokens == 0 {
		result["max_tokens"] = 4096
	}

	if req.Temperature > 0 {
		result["temperature"] = req.Temperature
	}
	if req.TopP > 0 {
		result["top_p"] = req.TopP
	}
	if len(req.Stop) > 0 {
		result["stop_sequences"] = req.Stop
	}

	// 消息转换：支持多模态 Content
	var messages []map[string]any
	for _, msg := range req.Messages {
		if msg.Role == "system" {
			// Anthropic 的 system 是顶层字段
			result["system"] = msg.ContentText()
			continue
		}

		m := map[string]any{
			"role": msg.Role,
		}

		// Content 可能是 string 或 []ContentPart
		switch v := msg.Content.(type) {
		case string:
			m["content"] = v
		case []any:
			// 转换为 Anthropic 格式的 content blocks
			m["content"] = convertToAnthropicContent(v)
		default:
			m["content"] = fmt.Sprint(msg.Content)
		}

		// tool_result 消息
		if msg.ToolCallID != "" {
			m["content"] = []map[string]any{{
				"type":        "tool_result",
				"tool_use_id": msg.ToolCallID,
				"content":     msg.ContentText(),
			}}
		}

		messages = append(messages, m)
	}
	result["messages"] = messages

	// Tool Use
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

// convertToAnthropicContent 将 OpenAI 格式的 content parts 转为 Anthropic 格式
func convertToAnthropicContent(parts []any) []map[string]any {
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

func (p *AnthropicProvider) convertResponse(body []byte) (*ChatResponse, error) {
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
		Model:   p.config.VendorModel,
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

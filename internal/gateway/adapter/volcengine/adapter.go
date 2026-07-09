// Package volcengine 实现火山方舟 Responses API 适配器 (/api/v3/responses)。
// 支持有状态对话 B 模式:previous_response_id 只发新消息;失效时上层触发 A 兜底自愈。
package volcengine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/gateway/adapter"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider/chat"
	"github.com/mirainya/Prism/pkg/httputil"
)

const defaultPath = "/api/v3/responses"

// Adapter 火山 Responses 协议适配器。无状态。
type Adapter struct{}

// New 构造适配器。
func New() adapter.UpstreamAdapter { return &Adapter{} }

func (a *Adapter) Protocol() model.Protocol { return model.ProtocolVolcengine }

func (a *Adapter) Complete(ctx context.Context, req *adapter.UpstreamRequest) (*adapter.UpstreamResponse, error) {
	req.Chat.Model = req.VendorModel
	req.Chat.Stream = false

	body := toRequestBody(req.Chat, req.PreviousResponseID, req.NewMessages)
	url := req.BaseURL + resolvePath(req.RequestPath)
	headers := buildHeaders(req)

	timeout := req.Timeout
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	respBody, err := httputil.PostJSON(ctx, url, body, headers)
	if err != nil {
		return nil, fmt.Errorf("volcengine request failed: %w", err)
	}

	chatResp, respID, err := parseNonStreamResponse(respBody, req.VendorModel)
	if err != nil {
		return nil, err
	}
	return &adapter.UpstreamResponse{Chat: chatResp, ProviderResponseID: respID}, nil
}

func (a *Adapter) StreamComplete(ctx context.Context, req *adapter.UpstreamRequest) (*http.Response, error) {
	req.Chat.Model = req.VendorModel
	req.Chat.Stream = true

	body := toRequestBody(req.Chat, req.PreviousResponseID, req.NewMessages)
	url := req.BaseURL + resolvePath(req.RequestPath)
	headers := buildHeaders(req)

	resp, err := httputil.PostJSONStream(ctx, url, body, headers)
	if err != nil {
		return nil, fmt.Errorf("volcengine stream request failed: %w", err)
	}
	resp.Body = newStreamAdapter(resp.Body, req.VendorModel)
	return resp, nil
}

// IsPreviousResponseNotFound 判断是否 previous_response_id 失效错误(触发 A 兜底自愈)。
func IsPreviousResponseNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "PreviousResponseNotFound")
}

// toRequestBody ChatRequest → Responses API 请求体。
// previousResponseID 非空=B 模式(只发 newMessages);为空=A 模式(全量 Messages)。
func toRequestBody(req *chat.ChatRequest, previousResponseID string, newMessages []chat.ChatMessage) map[string]any {
	body := map[string]any{"model": req.Model}

	if req.MaxTokens > 0 {
		body["max_output_tokens"] = req.MaxTokens
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		body["top_p"] = *req.TopP
	}
	if req.Stream {
		body["stream"] = true
	}

	msgs := req.Messages
	if previousResponseID != "" {
		body["previous_response_id"] = previousResponseID
		if len(newMessages) > 0 {
			msgs = newMessages
		}
	}

	var input []map[string]any
	for i := range msgs {
		msg := msgs[i]
		if msg.Role == "system" {
			body["instructions"] = msg.ContentText()
			continue
		}
		input = append(input, map[string]any{
			"role":    msg.Role,
			"content": toResponsesContent(&msg),
		})
	}
	body["input"] = input

	for k, v := range req.ExtraBody {
		body[k] = v
	}
	return body
}

// toResponsesContent OpenAI content → Responses content block 数组。
func toResponsesContent(msg *chat.ChatMessage) []map[string]any {
	switch v := msg.Content.(type) {
	case string:
		return []map[string]any{{"type": "input_text", "text": v}}
	case []any:
		var blocks []map[string]any
		for _, part := range v {
			pm, ok := part.(map[string]any)
			if !ok {
				continue
			}
			switch pm["type"] {
			case "text":
				text, _ := pm["text"].(string)
				blocks = append(blocks, map[string]any{"type": "input_text", "text": text})
			case "image_url":
				if imgURL, ok := pm["image_url"].(map[string]any); ok {
					if url, ok := imgURL["url"].(string); ok {
						blocks = append(blocks, map[string]any{"type": "input_image", "image_url": url})
					}
				}
			}
		}
		return blocks
	default:
		return []map[string]any{{"type": "input_text", "text": msg.ContentText()}}
	}
}

// parseNonStreamResponse 解析 Responses 非流式响应。第二返回值=火山 response_id(B 模式下轮用)。
func parseNonStreamResponse(body []byte, modelName string) (*chat.ChatResponse, string, error) {
	var resp struct {
		ID     string `json:"id"`
		Output []struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			Summary []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"summary"`
		} `json:"output"`
		Status string `json:"status"`
		Usage  struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, "", fmt.Errorf("volcengine unmarshal response failed: %w", err)
	}
	if resp.Error != nil {
		return nil, "", fmt.Errorf("volcengine error: %s", resp.Error.Message)
	}

	content := ""
	reasoning := ""
	for _, item := range resp.Output {
		switch item.Type {
		case "reasoning":
			for _, s := range item.Summary {
				reasoning += s.Text
			}
		case "message":
			for _, c := range item.Content {
				if c.Type == "output_text" {
					content += c.Text
				}
			}
		}
	}

	finishReason := "stop"
	if resp.Status == "incomplete" {
		finishReason = "length"
	}

	chatResp := &chat.ChatResponse{
		ID:      resp.ID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   modelName,
		Choices: []chat.ChatChoice{{
			Index: 0,
			Message: chat.ChatMessage{
				Role:             "assistant",
				Content:          content,
				ReasoningContent: reasoning,
			},
			FinishReason: finishReason,
		}},
		Usage: &chat.ChatUsage{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		},
	}
	return chatResp, resp.ID, nil
}

func resolvePath(p string) string {
	if p == "" || p == "/v1/chat/completions" {
		return defaultPath
	}
	return p
}

func buildHeaders(req *adapter.UpstreamRequest) map[string]string {
	headers := map[string]string{"Authorization": "Bearer " + req.APIKey}
	for k, v := range req.ExtraHeaders {
		headers[k] = v
	}
	return headers
}

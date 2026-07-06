package service

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/provider/chat"
	"github.com/mirainya/Prism/pkg/logger"
	"go.uber.org/zap"
)

// convertImageURLsToBase64 下载图片 URL 并转为 data URL
func convertImageURLsToBase64(messages []chat.ChatMessage) []chat.ChatMessage {
	result := make([]chat.ChatMessage, len(messages))
	copy(result, messages)

	for i, msg := range result {
		parts, ok := msg.Content.([]any)
		if !ok {
			continue
		}

		newParts := make([]any, len(parts))
		copy(newParts, parts)
		changed := false

		for j, part := range parts {
			pm, ok := part.(map[string]any)
			if !ok || pm["type"] != "image_url" {
				continue
			}
			imgURL, ok := pm["image_url"].(map[string]any)
			if !ok {
				continue
			}
			url, _ := imgURL["url"].(string)
			if url == "" || strings.HasPrefix(url, "data:") {
				continue
			}

			dataURL, err := downloadToDataURL(url)
			if err != nil {
				logger.Warn("image base64 download failed, keeping URL", zap.String("url", url), zap.Error(err))
				continue
			}

			newImg := make(map[string]any)
			for k, v := range imgURL {
				newImg[k] = v
			}
			newImg["url"] = dataURL
			newPart := make(map[string]any)
			for k, v := range pm {
				newPart[k] = v
			}
			newPart["image_url"] = newImg
			newParts[j] = newPart
			changed = true
		}

		if changed {
			result[i].Content = newParts
		}
	}
	return result
}

func downloadToDataURL(url string) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 20*1024*1024))
	if err != nil {
		return "", err
	}

	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = http.DetectContentType(body)
	}
	if idx := strings.Index(ct, ";"); idx >= 0 {
		ct = ct[:idx]
	}

	encoded := base64.StdEncoding.EncodeToString(body)
	return fmt.Sprintf("data:%s;base64,%s", ct, encoded), nil
}

// toAnthropicRequestBody 将 ChatRequest 转换为 Anthropic Messages API 请求体
func toAnthropicRequestBody(chatReq *chat.ChatRequest) map[string]any {
	body := chat.ConvertRequestToAnthropic(chatReq)
	body["model"] = chatReq.Model

	if chatReq.Stream {
		body["stream"] = true
	}

	for k, v := range chatReq.ExtraBody {
		body[k] = v
	}

	return body
}

// parseAnthropicNonStreamResponse 解析 Anthropic 非流式响应为统一格式
func parseAnthropicNonStreamResponse(body []byte, modelName string) (*CompletionResponse, *chat.ChatResponse, error) {
	chatResp, err := chat.ParseAnthropicResponse(body, modelName)
	if err != nil {
		return nil, nil, err
	}

	return &CompletionResponse{
		ID:      chatResp.ID,
		Object:  chatResp.Object,
		Created: chatResp.Created,
		Model:   modelName,
		Choices: chatResp.Choices,
		Usage:   chatResp.Usage,
	}, chatResp, nil
}

// anthropicStreamAdapter 将 Anthropic SSE 事件流转换为 OpenAI SSE 格式
type anthropicStreamAdapter struct {
	pr     *io.PipeReader
	pw     *io.PipeWriter
	closer io.Closer
}

func newAnthropicStreamAdapter(body io.ReadCloser, modelName string) io.ReadCloser {
	pr, pw := io.Pipe()
	adapter := &anthropicStreamAdapter{pr: pr, pw: pw, closer: body}

	go adapter.translate(body, pw, modelName)

	return adapter
}

func (a *anthropicStreamAdapter) Read(p []byte) (int, error) {
	return a.pr.Read(p)
}

func (a *anthropicStreamAdapter) Close() error {
	a.pr.Close()
	return a.closer.Close()
}

func (a *anthropicStreamAdapter) translate(body io.Reader, pw *io.PipeWriter, modelName string) {
	defer pw.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var msgID string
	var eventType string
	created := time.Now().Unix()
	toolCallIndex := -1
	inputTokens := 0 // message_start 携带,message_delta 组装 usage 时补入 PromptTokens

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
			continue
		}

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")

		switch eventType {
		case "message_start":
			var evt struct {
				Message struct {
					ID    string `json:"id"`
					Usage *struct {
						InputTokens int `json:"input_tokens"`
					} `json:"usage"`
				} `json:"message"`
			}
			if json.Unmarshal([]byte(data), &evt) == nil {
				msgID = evt.Message.ID
				if evt.Message.Usage != nil {
					inputTokens = evt.Message.Usage.InputTokens
				}
				chunk := openAIChunk(msgID, modelName, created, map[string]any{"role": "assistant"}, "", nil)
				writeSSELine(pw, chunk)
			}

		case "content_block_start":
			var evt struct {
				Index        int `json:"index"`
				ContentBlock struct {
					Type string `json:"type"`
					ID   string `json:"id,omitempty"`
					Name string `json:"name,omitempty"`
				} `json:"content_block"`
			}
			if json.Unmarshal([]byte(data), &evt) == nil && evt.ContentBlock.Type == "tool_use" {
				toolCallIndex++
				delta := map[string]any{
					"tool_calls": []map[string]any{{
						"index": toolCallIndex,
						"id":    evt.ContentBlock.ID,
						"type":  "function",
						"function": map[string]any{
							"name":      evt.ContentBlock.Name,
							"arguments": "",
						},
					}},
				}
				chunk := openAIChunk(msgID, modelName, created, delta, "", nil)
				writeSSELine(pw, chunk)
			}

		case "content_block_delta":
			var evt struct {
				Index int `json:"index"`
				Delta struct {
					Type     string `json:"type"`
					Text     string `json:"text,omitempty"`
					Thinking string `json:"thinking,omitempty"`
					PartialJSON string `json:"partial_json,omitempty"`
				} `json:"delta"`
			}
			if json.Unmarshal([]byte(data), &evt) != nil {
				continue
			}

			switch evt.Delta.Type {
			case "text_delta":
				delta := map[string]any{"content": evt.Delta.Text}
				chunk := openAIChunk(msgID, modelName, created, delta, "", nil)
				writeSSELine(pw, chunk)

			case "thinking_delta":
				delta := map[string]any{"reasoning_content": evt.Delta.Thinking}
				chunk := openAIChunk(msgID, modelName, created, delta, "", nil)
				writeSSELine(pw, chunk)

			case "input_json_delta":
				delta := map[string]any{
					"tool_calls": []map[string]any{{
						"index": toolCallIndex,
						"function": map[string]any{
							"arguments": evt.Delta.PartialJSON,
						},
					}},
				}
				chunk := openAIChunk(msgID, modelName, created, delta, "", nil)
				writeSSELine(pw, chunk)
			}

		case "message_delta":
			var evt struct {
				Delta struct {
					StopReason string `json:"stop_reason"`
				} `json:"delta"`
				Usage *struct {
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			}
			if json.Unmarshal([]byte(data), &evt) != nil {
				continue
			}

			finishReason := evt.Delta.StopReason
			if finishReason == "end_turn" {
				finishReason = "stop"
			} else if finishReason == "tool_use" {
				finishReason = "tool_calls"
			}

			var usage *chat.ChatUsage
			if evt.Usage != nil {
				usage = &chat.ChatUsage{
					PromptTokens:     inputTokens,
					CompletionTokens: evt.Usage.OutputTokens,
					TotalTokens:      inputTokens + evt.Usage.OutputTokens,
				}
			}
			chunk := openAIChunk(msgID, modelName, created, map[string]any{}, finishReason, usage)
			writeSSELine(pw, chunk)

		case "message_stop":
			fmt.Fprintf(pw, "data: [DONE]\n\n")
		}

		eventType = ""
	}
}

func openAIChunk(id, model string, created int64, delta map[string]any, finishReason string, usage *chat.ChatUsage) map[string]any {
	choice := map[string]any{
		"index": 0,
		"delta": delta,
	}
	if finishReason != "" {
		choice["finish_reason"] = finishReason
	} else {
		choice["finish_reason"] = nil
	}

	chunk := map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
		"choices": []map[string]any{choice},
	}
	if usage != nil {
		chunk["usage"] = usage
	}
	return chunk
}

func writeSSELine(w io.Writer, chunk map[string]any) {
	data, err := json.Marshal(chunk)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", data)
}

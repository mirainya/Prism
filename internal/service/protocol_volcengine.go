package service

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/provider/chat"
)

// 火山方舟 Responses API 协议适配器 (/api/v3/responses)
//
// 采用无状态用法：每轮请求把完整历史消息转为 input 数组发出，
// 不使用 previous_response_id，历史记录仍由项目自身的 Conversation/Message 管理。
//
// 请求格式差异 (vs OpenAI Chat Completions):
//   messages        -> input
//   system 消息      -> instructions
//   content text    -> {type:"input_text", text}
//   content image   -> {type:"input_image", image_url}
//   max_tokens      -> max_output_tokens
//
// 响应格式差异:
//   choices[].message            -> output[] (type=reasoning / type=message)
//   message.content.text         -> output[].content[].output_text
//   reasoning                    -> output[].summary[].summary_text
//   usage.prompt_tokens          -> usage.input_tokens
//   usage.completion_tokens      -> usage.output_tokens

// toVolcengineRequestBody 将统一 ChatRequest 转换为 Responses API 请求体
//
// previousResponseID 非空时启用「有状态对话(用法B)」：只发送 newMessages 中的新消息，
// 历史由火山服务端通过 previous_response_id 维护，可大幅节省 input token。
// 为空时启用「无状态(用法A)」：发送 req.Messages 中的完整历史。
func toVolcengineRequestBody(req *chat.ChatRequest, previousResponseID string, newMessages []chat.ChatMessage) map[string]any {
	body := map[string]any{
		"model": req.Model,
	}

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

	// 决定本次发送哪些消息：B 模式只发新消息，A 模式发全量
	msgs := req.Messages
	if previousResponseID != "" {
		body["previous_response_id"] = previousResponseID
		if len(newMessages) > 0 {
			msgs = newMessages
		}
	}

	var input []map[string]any
	for _, msg := range msgs {
		// system 消息单独提取为 instructions
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

	// 合并 extra_body (reasoning 等厂商特有字段)
	for k, v := range req.ExtraBody {
		body[k] = v
	}

	return body
}

// toResponsesContent 将 OpenAI 格式的 content 转为 Responses 的 content block 数组
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

// IsPreviousResponseNotFound 判断是否为 previous_response_id 失效/过期错误。
// 火山在此情况返回 code="InvalidParameter.PreviousResponseNotFound"，
// 据此触发「A 兜底自愈」：用本地全量历史重建对话。
func IsPreviousResponseNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "PreviousResponseNotFound")
}

// parseVolcengineNonStreamResponse 解析 Responses 非流式响应为统一格式。
// 第三个返回值为火山返回的 response_id，供 B 模式下一轮使用。
func parseVolcengineNonStreamResponse(body []byte, modelName string) (*CompletionResponse, *chat.ChatResponse, string, error) {
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
		return nil, nil, "", fmt.Errorf("unmarshal responses body failed: %w", err)
	}
	if resp.Error != nil {
		return nil, nil, "", fmt.Errorf("volcengine error: %s", resp.Error.Message)
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

	usage := &chat.ChatUsage{
		PromptTokens:     resp.Usage.InputTokens,
		CompletionTokens: resp.Usage.OutputTokens,
		TotalTokens:      resp.Usage.TotalTokens,
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
		Usage: usage,
	}

	compResp := &CompletionResponse{
		ID:      chatResp.ID,
		Object:  chatResp.Object,
		Created: chatResp.Created,
		Model:   modelName,
		Choices: chatResp.Choices,
		Usage:   usage,
	}

	return compResp, chatResp, resp.ID, nil
}

// volcengineStreamAdapter 将 Responses SSE 事件流转换为 OpenAI SSE 格式
type volcengineStreamAdapter struct {
	pr     *io.PipeReader
	pw     *io.PipeWriter
	closer io.Closer
}

func newVolcengineStreamAdapter(body io.ReadCloser, modelName string) io.ReadCloser {
	pr, pw := io.Pipe()
	adapter := &volcengineStreamAdapter{pr: pr, pw: pw, closer: body}
	go adapter.translate(body, pw, modelName)
	return adapter
}

func (a *volcengineStreamAdapter) Read(p []byte) (int, error) {
	return a.pr.Read(p)
}

func (a *volcengineStreamAdapter) Close() error {
	a.pr.Close()
	return a.closer.Close()
}

func (a *volcengineStreamAdapter) translate(body io.Reader, pw *io.PipeWriter, modelName string) {
	defer pw.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	msgID := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	created := time.Now().Unix()
	var eventType string
	roleSent := false

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
		case "response.output_text.delta":
			var evt struct {
				Delta string `json:"delta"`
			}
			if json.Unmarshal([]byte(data), &evt) != nil {
				continue
			}
			if !roleSent {
				writeSSELine(pw, openAIChunk(msgID, modelName, created, map[string]any{"role": "assistant"}, "", nil))
				roleSent = true
			}
			writeSSELine(pw, openAIChunk(msgID, modelName, created, map[string]any{"content": evt.Delta}, "", nil))

		case "response.reasoning_summary_text.delta":
			var evt struct {
				Delta string `json:"delta"`
			}
			if json.Unmarshal([]byte(data), &evt) != nil {
				continue
			}
			if !roleSent {
				writeSSELine(pw, openAIChunk(msgID, modelName, created, map[string]any{"role": "assistant"}, "", nil))
				roleSent = true
			}
			writeSSELine(pw, openAIChunk(msgID, modelName, created, map[string]any{"reasoning_content": evt.Delta}, "", nil))

		case "response.completed", "response.incomplete":
			var evt struct {
				Response struct {
					ID     string `json:"id"`
					Status string `json:"status"`
					Usage  struct {
						InputTokens  int `json:"input_tokens"`
						OutputTokens int `json:"output_tokens"`
						TotalTokens  int `json:"total_tokens"`
					} `json:"usage"`
				} `json:"response"`
			}
			finishReason := "stop"
			var usage *chat.ChatUsage
			respID := ""
			if json.Unmarshal([]byte(data), &evt) == nil {
				if evt.Response.Status == "incomplete" {
					finishReason = "length"
				}
				respID = evt.Response.ID
				usage = &chat.ChatUsage{
					PromptTokens:     evt.Response.Usage.InputTokens,
					CompletionTokens: evt.Response.Usage.OutputTokens,
					TotalTokens:      evt.Response.Usage.TotalTokens,
				}
			}
			chunk := openAIChunk(msgID, modelName, created, map[string]any{}, finishReason, usage)
			// 透传 response_id 给聚合层(B模式回写)，前端忽略此自定义字段
			if respID != "" {
				chunk["provider_response_id"] = respID
			}
			writeSSELine(pw, chunk)
			fmt.Fprintf(pw, "data: [DONE]\n\n")

		case "error":
			fmt.Fprintf(pw, "data: %s\n\n", data)
			fmt.Fprintf(pw, "data: [DONE]\n\n")
		}

		eventType = ""
	}
}

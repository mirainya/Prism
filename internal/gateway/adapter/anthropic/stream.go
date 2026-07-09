package anthropic

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/gateway/stream"
	"github.com/mirainya/Prism/internal/provider/chat"
)

// streamAdapter 把 Anthropic SSE 事件流翻译成 OpenAI SSE(io.Pipe + goroutine)。
type streamAdapter struct {
	pr     *io.PipeReader
	pw     *io.PipeWriter
	closer io.Closer
}

func newStreamAdapter(body io.ReadCloser, modelName string) io.ReadCloser {
	pr, pw := io.Pipe()
	a := &streamAdapter{pr: pr, pw: pw, closer: body}
	go a.translate(body, pw, modelName)
	return a
}

func (a *streamAdapter) Read(p []byte) (int, error) { return a.pr.Read(p) }

func (a *streamAdapter) Close() error {
	a.pr.Close()
	return a.closer.Close()
}

func (a *streamAdapter) translate(body io.Reader, pw *io.PipeWriter, modelName string) {
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
				chunk := stream.OpenAIChunk(msgID, modelName, created, map[string]any{"role": "assistant"}, "", nil)
				stream.WriteSSELine(pw, chunk)
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
				stream.WriteSSELine(pw, stream.OpenAIChunk(msgID, modelName, created, delta, "", nil))
			}

		case "content_block_delta":
			var evt struct {
				Index int `json:"index"`
				Delta struct {
					Type        string `json:"type"`
					Text        string `json:"text,omitempty"`
					Thinking    string `json:"thinking,omitempty"`
					PartialJSON string `json:"partial_json,omitempty"`
				} `json:"delta"`
			}
			if json.Unmarshal([]byte(data), &evt) != nil {
				continue
			}
			switch evt.Delta.Type {
			case "text_delta":
				delta := map[string]any{"content": evt.Delta.Text}
				stream.WriteSSELine(pw, stream.OpenAIChunk(msgID, modelName, created, delta, "", nil))
			case "thinking_delta":
				delta := map[string]any{"reasoning_content": evt.Delta.Thinking}
				stream.WriteSSELine(pw, stream.OpenAIChunk(msgID, modelName, created, delta, "", nil))
			case "input_json_delta":
				delta := map[string]any{
					"tool_calls": []map[string]any{{
						"index":    toolCallIndex,
						"function": map[string]any{"arguments": evt.Delta.PartialJSON},
					}},
				}
				stream.WriteSSELine(pw, stream.OpenAIChunk(msgID, modelName, created, delta, "", nil))
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
			stream.WriteSSELine(pw, stream.OpenAIChunk(msgID, modelName, created, map[string]any{}, finishReason, usage))

		case "message_stop":
			stream.WriteDone(pw)
		}

		eventType = ""
	}
}

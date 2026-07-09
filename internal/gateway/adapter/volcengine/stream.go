package volcengine

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/gateway/stream"
	"github.com/mirainya/Prism/internal/provider/chat"
)

// streamAdapter 把火山 Responses SSE 翻译成 OpenAI SSE(io.Pipe + goroutine)。
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

	msgID := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	created := time.Now().Unix()
	var eventType string
	roleSent := false

	sendRole := func() {
		if !roleSent {
			stream.WriteSSELine(pw, stream.OpenAIChunk(msgID, modelName, created, map[string]any{"role": "assistant"}, "", nil))
			roleSent = true
		}
	}

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
			sendRole()
			stream.WriteSSELine(pw, stream.OpenAIChunk(msgID, modelName, created, map[string]any{"content": evt.Delta}, "", nil))

		case "response.reasoning_summary_text.delta":
			var evt struct {
				Delta string `json:"delta"`
			}
			if json.Unmarshal([]byte(data), &evt) != nil {
				continue
			}
			sendRole()
			stream.WriteSSELine(pw, stream.OpenAIChunk(msgID, modelName, created, map[string]any{"reasoning_content": evt.Delta}, "", nil))

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
			chunk := stream.OpenAIChunk(msgID, modelName, created, map[string]any{}, finishReason, usage)
			// 透传 response_id 给聚合层(B 模式回写),前端忽略此自定义字段
			if respID != "" {
				chunk["provider_response_id"] = respID
			}
			stream.WriteSSELine(pw, chunk)
			stream.WriteDone(pw)

		case "error":
			fmt.Fprintf(pw, "data: %s\n\n", data)
			stream.WriteDone(pw)
		}

		eventType = ""
	}
}

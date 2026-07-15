package stream

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"

	"github.com/mirainya/Prism/internal/provider/chat"
)

// AggregationResult 流式转发过程中累积的结果(供结算/日志/存会话)。
type AggregationResult struct {
	AssistantContent   string
	ReasoningContent   string
	FinishReason       string
	ResponsePreview    string
	ResponseBody       string
	ErrorMessage       string
	UpstreamError      *StreamError
	Usage              *chat.ChatUsage
	ProviderResponseID string // 火山 B 模式回写
}

type StreamError struct {
	Message string `json:"message"`
	Type    string `json:"type,omitempty"`
	Code    any    `json:"code,omitempty"`
}

func (e *StreamError) Error() string { return e.Message }

// Writer 抽象 gin.ResponseWriter 需要的两个方法,便于测试。
type Writer interface {
	Write([]byte) (int, error)
	Flush()
}

// ProxyStream 把上游(已归一化成 OpenAI SSE)的 body 边转发给客户端边聚合。
// 上游 body 已由 adapter 保证是 OpenAI SSE 格式。
func ProxyStream(w Writer, upstreamBody io.Reader) (*AggregationResult, error) {
	agg := &AggregationResult{}
	reader := bufio.NewReader(upstreamBody)
	done := false
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			written, writeErr := w.Write([]byte(line))
			if writeErr != nil {
				agg.ErrorMessage = writeErr.Error()
				return agg, writeErr
			}
			if written != len(line) {
				agg.ErrorMessage = io.ErrShortWrite.Error()
				return agg, io.ErrShortWrite
			}
			w.Flush()
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "data: ") {
				payload := strings.TrimPrefix(trimmed, "data: ")
				if payload == "[DONE]" {
					done = true
				} else {
					if streamErr := mergeChunk(agg, payload); streamErr != nil {
						agg.UpstreamError = streamErr
						agg.ErrorMessage = streamErr.Error()
						return agg, streamErr
					}
				}
			}
		}
		if err != nil {
			if err != io.EOF {
				agg.ErrorMessage = err.Error()
				return agg, err
			}
			if !done {
				streamErr := &StreamError{Message: "upstream stream ended before [DONE]", Type: "server_error", Code: "incomplete_stream"}
				agg.UpstreamError = streamErr
				agg.ErrorMessage = streamErr.Error()
				return agg, streamErr
			}
			return agg, nil
		}
	}
}

func mergeChunk(agg *AggregationResult, payload string) *StreamError {
	var parsed struct {
		Error   *StreamError `json:"error"`
		Choices []struct {
			Delta struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				ToolCalls        []struct {
					Function struct {
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage              *chat.ChatUsage `json:"usage"`
		ProviderResponseID string          `json:"provider_response_id"`
	}
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		return &StreamError{Message: "invalid upstream stream event", Type: "server_error", Code: "invalid_stream_event"}
	}
	if parsed.Error != nil {
		return parsed.Error
	}
	if parsed.ProviderResponseID != "" {
		agg.ProviderResponseID = parsed.ProviderResponseID
	}
	if len(parsed.Choices) > 0 {
		choice := parsed.Choices[0]
		agg.AssistantContent += choice.Delta.Content
		agg.ReasoningContent += choice.Delta.ReasoningContent
		for _, tc := range choice.Delta.ToolCalls {
			agg.AssistantContent += tc.Function.Arguments
		}
		if choice.FinishReason != "" {
			agg.FinishReason = choice.FinishReason
		}
	}
	if parsed.Usage != nil {
		agg.Usage = parsed.Usage
	}
	runes := []rune(agg.AssistantContent)
	if len(runes) <= 500 {
		agg.ResponsePreview = agg.AssistantContent
	} else {
		agg.ResponsePreview = string(runes[:500]) + "..."
	}
	if body, err := json.Marshal(map[string]string{"content": agg.AssistantContent}); err == nil {
		agg.ResponseBody = string(body)
	}
	return nil
}

// Package stream 提供 SSE 流式辅助:OpenAI chunk 构造、SSE 行写入、以及流式代理聚合。
// 协议适配器(anthropic/volcengine)用这些 helper 把上游 SSE 翻译成 OpenAI SSE。
package stream

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/mirainya/Prism/internal/provider/chat"
)

// OpenAIChunk 构造一个 OpenAI chat.completion.chunk 结构。
// delta 为增量字段(content/reasoning_content/tool_calls/role 等);
// finishReason 空则输出 null;usage 非 nil 时附带。
func OpenAIChunk(id, model string, created int64, delta map[string]any, finishReason string, usage *chat.ChatUsage) map[string]any {
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

// WriteSSELine 把 chunk 序列化为 `data: {...}\n\n` 写入 w。
func WriteSSELine(w io.Writer, chunk map[string]any) {
	data, err := json.Marshal(chunk)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", data)
}

// WriteDone 写入 SSE 终止标记。
func WriteDone(w io.Writer) {
	fmt.Fprint(w, "data: [DONE]\n\n")
}

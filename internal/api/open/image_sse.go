package open

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/service"
	"github.com/tidwall/gjson"
)

// writeImageSSEData 写一帧 SSE data 行（两个换行结尾）。
func writeImageSSEData(w io.Writer, payload []byte) {
	_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
}

// writeImageSSEDone 写 [DONE] 终止帧。
func writeImageSSEDone(w io.Writer) {
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}

// writeImageSSEError 以 OpenAI 错误 JSON 格式写一帧 SSE error 事件。
func writeImageSSEError(w io.Writer, message, errType string) {
	payload, _ := json.Marshal(map[string]any{
		"type":  "image_generation.failed",
		"error": map[string]any{"message": message, "type": errType},
	})
	writeImageSSEData(w, payload)
}

// forwardImageSSEEvents 从 events 通道读取上游原始事件 payload，转换成 OpenAI 标准
// image SSE 格式写入 w。返回值表示是否已向客户端发出了错误帧。
//
// 上游 partial_image / chunk 事件 → image_generation.partial_image
// 上游 completed / result 事件      → 仅标记 seenCompleted，不直接下发
//
//	（让调用方用 InvokeAndWait 结果统一下发，
//	 保证 URL 已落存储、格式已规范）
//
// 上游 error / failed 事件          → 直接下发错误帧
func forwardImageSSEEvents(w io.Writer, events <-chan []byte) (errorForwarded bool) {
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	return forwardImageSSEEventsWithHeartbeat(w, events, heartbeat.C)
}

func forwardImageSSEEventsWithHeartbeat(
	w io.Writer,
	events <-chan []byte,
	heartbeat <-chan time.Time,
) (errorForwarded bool) {
	partialIndex := 0
	for {
		var raw []byte
		select {
		case event, ok := <-events:
			if !ok {
				return errorForwarded
			}
			raw = event
		case <-heartbeat:
			_, _ = io.WriteString(w, ": keep-alive\n\n")
			flushImageSSE(w)
			continue
		}

		payload := string(raw)
		if !gjson.Valid(payload) {
			continue
		}
		typ := gjson.Get(payload, "type").String()
		obj := gjson.Get(payload, "object").String()

		switch {
		case strings.HasSuffix(typ, "partial_image") || strings.HasSuffix(obj, ".chunk"):
			b64 := extractSSEB64(payload)
			if b64 == "" {
				continue
			}
			event := map[string]any{
				"type":                "image_generation.partial_image",
				"partial_image_index": partialIndex,
				"partial_image_b64":   b64,
			}
			if data, err := json.Marshal(event); err == nil {
				writeImageSSEData(w, data)
				flushImageSSE(w)
			}
			partialIndex++

		case strings.HasSuffix(typ, "completed") || strings.HasSuffix(obj, ".result"):
			// 最终结果由 InvokeAndWait 规范化后统一发送。

		case typ == "error" || typ == "api_error" || strings.HasSuffix(typ, "_error") ||
			strings.HasSuffix(typ, "failed") || obj == "error" || gjson.Get(payload, "error").Exists() ||
			(gjson.Get(payload, "message").Exists() && !gjson.Get(payload, "data").Exists()):
			msg := gjson.Get(payload, "error.message").String()
			if msg == "" {
				msg = gjson.Get(payload, "message").String()
			}
			if msg == "" {
				msg = "upstream stream error"
			}
			writeImageSSEError(w, msg, "api_error")
			flushImageSSE(w)
			errorForwarded = true
		}
	}
}

func flushImageSSE(w io.Writer) {
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

// writeImageCompletedSSE 把 InvokeAndWait 结果格式化成 image_generation.completed 帧写入 w。
// 若 responseFormat="b64_json"，通过 encode 把 URL 转成 base64 再下发。
func writeImageCompletedSSE(
	ctx context.Context,
	w io.Writer,
	result *service.ImageResult,
	responseFormat string,
	encode imageBase64Encoder,
) error {
	if result == nil || !result.Success {
		msg := "image generation failed"
		if result != nil && result.Error != "" {
			msg = result.Error
		}
		writeImageSSEError(w, msg, "api_error")
		return nil
	}
	data, err := buildOpenAIImageData(ctx, result.URLs, result.RevisedPrompt, responseFormat, encode)
	if err != nil {
		writeImageSSEError(w, err.Error(), "api_error")
		return err
	}
	event := map[string]any{
		"type":    "image_generation.completed",
		"created": time.Now().Unix(),
		"data":    data,
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	writeImageSSEData(w, payload)
	return nil
}

// extractSSEB64 从单条上游 SSE JSON payload 中提取 base64 图片数据。
// 支持 partial_image_b64、b64_json、数组内对应字段，以及 data URI 格式的 url 字段。
func extractSSEB64(payload string) string {
	for _, path := range []string{
		"partial_image_b64",
		"b64_json",
		"data.0.partial_image_b64",
		"data.0.b64_json",
	} {
		if b64 := gjson.Get(payload, path).String(); b64 != "" {
			return b64
		}
	}
	// url 字段（可能是 data URI）
	for _, path := range []string{"url", "data.0.url"} {
		raw := gjson.Get(payload, path).String()
		if raw == "" {
			continue
		}
		if b64, ok := imageDecodeBase64DataURI(raw); ok {
			return b64
		}
		// 真实 HTTP URL：partial 阶段无法转存，忽略，等 completed 时统一处理
	}
	return ""
}

// imageDecodeBase64DataURI 解析 data:<type>;base64,<data> 格式，返回 base64 载荷。
func imageDecodeBase64DataURI(s string) (string, bool) {
	if !strings.HasPrefix(s, "data:") {
		return "", false
	}
	idx := strings.Index(s, ";base64,")
	if idx < 0 {
		return "", false
	}
	data := s[idx+len(";base64,"):]
	if data == "" {
		return "", false
	}
	return data, true
}

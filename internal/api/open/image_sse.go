package open

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/gateway/payloadview"
	"github.com/tidwall/gjson"
)

const maxImageSSEEventBytes = 128 << 20

var errImageSSEEventTooLarge = errors.New("image SSE event exceeds the size limit")

type imageSSEParser struct {
	pending   []byte
	dataLines [][]byte
	dataBytes int
	emit      func([]byte)
}

func newImageSSEParser(emit func([]byte)) *imageSSEParser {
	return &imageSSEParser{emit: emit}
}

func (p *imageSSEParser) Write(chunk []byte) error {
	if p == nil || len(chunk) == 0 {
		return nil
	}
	if len(p.pending)+p.dataBytes+len(chunk) > maxImageSSEEventBytes {
		return errImageSSEEventTooLarge
	}
	p.pending = append(p.pending, chunk...)
	for {
		index := bytes.IndexAny(p.pending, "\r\n")
		if index < 0 {
			return nil
		}
		if p.pending[index] == '\r' && index+1 == len(p.pending) {
			return nil
		}
		line := bytes.Clone(p.pending[:index])
		separatorLength := 1
		if p.pending[index] == '\r' && p.pending[index+1] == '\n' {
			separatorLength = 2
		}
		p.pending = p.pending[index+separatorLength:]
		p.consumeLine(line)
	}
}

func (p *imageSSEParser) Finish() error {
	if p == nil {
		return nil
	}
	if len(p.pending)+p.dataBytes > maxImageSSEEventBytes {
		return errImageSSEEventTooLarge
	}
	if len(p.pending) != 0 {
		line := bytes.TrimSuffix(p.pending, []byte{'\r'})
		p.consumeLine(line)
		p.pending = nil
	}
	p.emitEvent()
	return nil
}

func (p *imageSSEParser) consumeLine(line []byte) {
	if len(line) == 0 {
		p.emitEvent()
		return
	}
	if line[0] == ':' || !bytes.HasPrefix(line, []byte("data:")) {
		return
	}
	value := line[len("data:"):]
	if len(value) != 0 && value[0] == ' ' {
		value = value[1:]
	}
	value = bytes.Clone(value)
	p.dataLines = append(p.dataLines, value)
	p.dataBytes += len(value)
}

func (p *imageSSEParser) emitEvent() {
	if len(p.dataLines) == 0 {
		return
	}
	payload := bytes.Join(p.dataLines, []byte{'\n'})
	p.dataLines = nil
	p.dataBytes = 0
	if p.emit != nil {
		p.emit(payload)
	}
}

type imageSSESession struct {
	events   chan []byte
	done     chan bool
	parser   *imageSSEParser
	finished bool
}

func startImageSSESession(w io.Writer) *imageSSESession {
	events := make(chan []byte, 16)
	session := &imageSSESession{events: events, done: make(chan bool, 1)}
	session.parser = newImageSSEParser(func(payload []byte) {
		events <- payload
	})
	go func() {
		session.done <- forwardImageSSEEvents(w, events)
	}()
	return session
}

func (s *imageSSESession) Observe(chunk []byte) error {
	if s == nil || s.finished {
		return errors.New("image SSE session is closed")
	}
	return s.parser.Write(chunk)
}

func (s *imageSSESession) Complete(w io.Writer, response OpenAIImageResponse) {
	errorForwarded, parserErr := s.finish()
	if parserErr != nil && !errorForwarded {
		writeImageSSEError(w, "upstream image stream was invalid", "api_error")
	} else if !errorForwarded {
		if err := writeImageCompletedSSE(w, response); err != nil {
			writeImageSSEError(w, "image response serialization failed", "api_error")
		}
	}
	writeImageSSEDone(w)
	flushImageSSE(w)
}

func (s *imageSSESession) Fail(w io.Writer, message string) {
	errorForwarded, _ := s.finish()
	if !errorForwarded {
		message = payloadview.ExtractFailureMessage([]byte(message))
		if message == "" {
			message = "upstream image generation failed"
		}
		writeImageSSEError(w, message, "api_error")
	}
	writeImageSSEDone(w)
	flushImageSSE(w)
}

func (s *imageSSESession) finish() (bool, error) {
	if s == nil || s.finished {
		return false, nil
	}
	s.finished = true
	parserErr := s.parser.Finish()
	close(s.events)
	return <-s.done, parserErr
}

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
			msg := payloadview.ExtractFailureMessage(raw)
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

// writeImageCompletedSSE writes the already-normalized, durably persisted
// image response as the terminal OpenAI-compatible event.
func writeImageCompletedSSE(w io.Writer, response OpenAIImageResponse) error {
	event := map[string]any{
		"type":    "image_generation.completed",
		"created": response.Created,
		"data":    response.Data,
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

package provider

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/tidwall/gjson"
)

// parseImageSSEStream 边读边解析图像生成的 SSE 流(如 sub2api 带 stream:true 的响应)。
//
// 靠 data JSON 的 type 字段区分事件(中转路径可能无 event: 行,不能依赖它):
//   - image_generation.completed / image_edit.completed → 取 b64_json 或 url,立即返回成功
//   - error / *.failed                                  → 取错误消息返回 error
//   - image_generation.partial_image / *.partial_image  → 记为兜底
//
// 多个 data: 行按 SSE 规范用 \n 拼成完整 JSON 再解析;遇 [DONE] 结束。
// parseImageSSEStream 边读边解析图像生成的 SSE 流。
// sink 非 nil 时，每个有效 JSON 事件的原始 payload 都会发送到 sink（非阻塞，满则丢弃）。
func parseImageSSEStream(r io.Reader, sink chan<- []byte) (SubmitResult, error) {
	reader := bufio.NewReader(r)
	var dataLines []string
	var lastPartial imageOutput
	eventName := ""

	// flush 处理一个完整事件块(空行触发)。done=true 表示遇到 [DONE] 应停止。
	flush := func() (res SubmitResult, matched bool, done bool, err error) {
		if len(dataLines) == 0 {
			return
		}
		payload := strings.Join(dataLines, "\n")
		dataLines = dataLines[:0]
		if payload == "[DONE]" {
			eventName = ""
			return SubmitResult{}, false, true, nil
		}
		if !gjson.Valid(payload) {
			eventName = ""
			return // 无效 JSON,跳过继续
		}
		// 有效事件：旁路写入 sink（非阻塞，消费方来不及处理时丢弃而非阻塞上游解析）。
		payload = normalizeImageSSEEvent(payload, eventName)
		eventName = ""
		if sink != nil {
			select {
			case sink <- []byte(payload):
			default:
			}
		}

		// 两种上游 SSE 格式:
		//   A) OpenAI 风格: type=image_generation.completed, b64_json 在顶层
		//   B) sub2api 风格: object=image.generation.result, b64_json 在 data[0]
		typ := gjson.Get(payload, "type").String()
		obj := gjson.Get(payload, "object").String()
		switch {
		// A) completed 事件(顶层 b64_json / url)
		case strings.HasSuffix(typ, "completed"):
			img := extractImageOutput(payload, "")
			if img.empty() {
				img = extractImageOutput(payload, "data.0.")
			}
			if img.empty() {
				return // completed 但无图,继续兜底
			}
			return img.result(), true, true, nil

		// B) result 事件(data[0].b64_json / data[0].url), 兼容 image.generation.result / image.edit.result
		case strings.HasSuffix(obj, ".result"):
			img := extractImageOutput(payload, "data.0.")
			if img.empty() {
				img = extractImageOutput(payload, "")
			}
			if img.empty() {
				return // result 但无图,继续兜底
			}
			return img.result(), true, true, nil

		case typ == "error" || strings.HasSuffix(typ, "failed") || obj == "error":
			msg := gjson.Get(payload, "error.message").String()
			if msg == "" {
				msg = gjson.Get(payload, "message").String()
			}
			code := gjson.Get(payload, "error.code").String()
			if msg == "" {
				msg = "upstream stream error"
			}
			if code != "" {
				msg = fmt.Sprintf("%s (%s)", msg, code)
			}
			return SubmitResult{}, false, true, fmt.Errorf("%s", msg)

		// A) partial_image 事件(顶层 b64_json / url)
		case strings.HasSuffix(typ, "partial_image"):
			img := extractImageOutput(payload, "")
			if img.empty() {
				img = extractImageOutput(payload, "data.0.")
			}
			lastPartial.merge(img)

		// B) chunk 事件(可能带 data[0].b64_json / data[0].url 的 partial 图)
		case strings.HasSuffix(obj, ".chunk"):
			img := extractImageOutput(payload, "data.0.")
			if img.empty() {
				img = extractImageOutput(payload, "")
			}
			lastPartial.merge(img)
		}
		return
	}

	for {
		line, readErr := reader.ReadString('\n')
		trimmed := strings.TrimRight(line, "\r\n")

		if trimmed == "" {
			// 空行:一个事件块结束
			res, matched, done, err := flush()
			if err != nil {
				return SubmitResult{}, err
			}
			if matched {
				return res, nil
			}
			if done {
				break
			}
		} else if event, ok := strings.CutPrefix(trimmed, "event:"); ok {
			eventName = strings.TrimSpace(event)
		} else if data, ok := strings.CutPrefix(trimmed, "data:"); ok {
			dataLines = append(dataLines, strings.TrimPrefix(data, " "))
		}
		// 非 data: 行(如 event:) 忽略

		if readErr != nil {
			if readErr == io.EOF {
				// 处理最后一块(可能无末尾空行)
				res, matched, _, err := flush()
				if err != nil {
					return SubmitResult{}, err
				}
				if matched {
					return res, nil
				}
				break
			}
			return SubmitResult{}, fmt.Errorf("read sse stream: %w", readErr)
		}
	}

	// 无 completed:降级用最后一张 partial
	if !lastPartial.empty() {
		return lastPartial.result(), nil
	}
	return SubmitResult{}, fmt.Errorf("upstream did not return image output")
}

// imageOutput 是单个 SSE 事件里解出的图像产物。
// 上游按 response_format 二选一给出 b64_json 或 url,两者都要能接。
// normalizeImageSSEEvent fills the type omitted by a few image gateways.
// Prefer the SSE event name, then infer a terminal/partial image from its payload.
func normalizeImageSSEEvent(payload, eventName string) string {
	if gjson.Get(payload, "type").Exists() || gjson.Get(payload, "object").Exists() {
		return payload
	}
	typ := ""
	if strings.Contains(strings.ToLower(eventName), "image") {
		typ = eventName
	}
	if typ == "" {
		if gjson.Get(payload, "partial_image_index").Exists() {
			typ = "image_generation.partial_image"
		} else if !extractImageOutput(payload, "").empty() || !extractImageOutput(payload, "data.0.").empty() {
			typ = "image_generation.completed"
		}
	}
	if typ == "" {
		return payload
	}
	var object map[string]any
	if err := json.Unmarshal([]byte(payload), &object); err != nil {
		return payload
	}
	object["type"] = typ
	normalized, err := json.Marshal(object)
	if err != nil {
		return payload
	}
	return string(normalized)
}

type imageOutput struct {
	b64     string
	url     string
	revised string
}

func (o imageOutput) empty() bool { return o.b64 == "" && o.url == "" }

// merge 用新事件的非空字段覆盖旧值(partial 兜底取最后一张)。
func (o *imageOutput) merge(next imageOutput) {
	if next.b64 != "" {
		o.b64 = next.b64
		o.url = ""
	}
	if next.url != "" {
		o.url = next.url
		o.b64 = ""
	}
	if next.revised != "" {
		o.revised = next.revised
	}
}

func (o imageOutput) result() SubmitResult {
	out := SubmitResult{Status: StatusSuccess, RevisedPrompt: o.revised}
	switch {
	case o.b64 != "":
		out.B64Data = []string{o.b64}
	case o.url != "":
		out.URLs = []string{o.url}
	}
	return out
}

// extractImageOutput 从事件 JSON 取图,prefix 区分顶层("")与 sub2api 的 data 数组("data.0.")。
// url 若是 data URI(如 sub2api 在 response_format=url 时返回的 data:image/png;base64,...),
// 剥壳还原成 base64 走转存路径,真实 http 链接才留在 URLs。
func extractImageOutput(payload, prefix string) imageOutput {
	out := imageOutput{
		b64:     gjson.Get(payload, prefix+"b64_json").String(),
		revised: gjson.Get(payload, prefix+"revised_prompt").String(),
	}
	if out.b64 != "" {
		return out
	}
	raw := gjson.Get(payload, prefix+"url").String()
	if raw == "" {
		return out
	}
	if b64, ok := decodeBase64DataURI(raw); ok {
		out.b64 = b64
		return out
	}
	out.url = raw
	return out
}

// decodeBase64DataURI 解出 data:<mediatype>;base64,<data> 里的 base64 载荷。
func decodeBase64DataURI(s string) (string, bool) {
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

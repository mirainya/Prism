package provider

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/tidwall/gjson"
)

// parseImageSSEStream 边读边解析图像生成的 SSE 流(如 sub2api 带 stream:true 的响应)。
//
// 靠 data JSON 的 type 字段区分事件(中转路径可能无 event: 行,不能依赖它):
//   - image_generation.completed / image_edit.completed → 取 b64_json,立即返回成功
//   - error / *.failed                                  → 取错误消息返回 error
//   - image_generation.partial_image / *.partial_image  → 记为兜底
//
// 多个 data: 行按 SSE 规范用 \n 拼成完整 JSON 再解析;遇 [DONE] 结束。
func parseImageSSEStream(r io.Reader) (SubmitResult, error) {
	reader := bufio.NewReader(r)
	var dataLines []string
	var lastPartialB64 string
	var partialRevised string

	// flush 处理一个完整事件块(空行触发)。done=true 表示遇到 [DONE] 应停止。
	flush := func() (res SubmitResult, matched bool, done bool, err error) {
		if len(dataLines) == 0 {
			return
		}
		payload := strings.Join(dataLines, "\n")
		dataLines = dataLines[:0]
		if payload == "[DONE]" {
			return SubmitResult{}, false, true, nil
		}
		if !gjson.Valid(payload) {
			return // 无效 JSON,跳过继续
		}

		// 两种上游 SSE 格式:
		//   A) OpenAI 风格: type=image_generation.completed, b64_json 在顶层
		//   B) sub2api 风格: object=image.generation.result, b64_json 在 data[0]
		typ := gjson.Get(payload, "type").String()
		obj := gjson.Get(payload, "object").String()
		switch {
		// A) completed 事件(顶层 b64_json)
		case strings.HasSuffix(typ, "completed"):
			b64 := gjson.Get(payload, "b64_json").String()
			revised := gjson.Get(payload, "revised_prompt").String()
			if b64 == "" {
				return // completed 但无图,继续兜底
			}
			out := SubmitResult{Status: StatusSuccess, B64Data: []string{b64}}
			if revised != "" {
				out.RevisedPrompt = revised
			}
			return out, true, true, nil

		// B) result 事件(data[0].b64_json), 兼容 image.generation.result / image.edit.result
		case strings.HasSuffix(obj, ".result"):
			b64 := gjson.Get(payload, "data.0.b64_json").String()
			revised := gjson.Get(payload, "data.0.revised_prompt").String()
			if b64 == "" {
				return // result 但无图,继续兜底
			}
			out := SubmitResult{Status: StatusSuccess, B64Data: []string{b64}}
			if revised != "" {
				out.RevisedPrompt = revised
			}
			return out, true, true, nil

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

		// A) partial_image 事件(顶层 b64_json)
		case strings.HasSuffix(typ, "partial_image"):
			if b64 := gjson.Get(payload, "b64_json").String(); b64 != "" {
				lastPartialB64 = b64
			}
			if rp := gjson.Get(payload, "revised_prompt").String(); rp != "" {
				partialRevised = rp
			}

		// B) chunk 事件(可能带 data[0].b64_json 的 partial 图)
		case strings.HasSuffix(obj, ".chunk"):
			if b64 := gjson.Get(payload, "data.0.b64_json").String(); b64 != "" {
				lastPartialB64 = b64
			}
			if rp := gjson.Get(payload, "data.0.revised_prompt").String(); rp != "" {
				partialRevised = rp
			}
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
	if lastPartialB64 != "" {
		out := SubmitResult{Status: StatusSuccess, B64Data: []string{lastPartialB64}}
		if partialRevised != "" {
			out.RevisedPrompt = partialRevised
		}
		return out, nil
	}
	return SubmitResult{}, fmt.Errorf("upstream did not return image output")
}

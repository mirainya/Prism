package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/model"
)

// resolveFileParams 图生图文件预处理:仅对配了 extra_config.image_edit 的端点,
// 把其 file_field 字段里的图片 URL 下载并转为 @base64:filename:data 格式,
// 供 BaseProvider.buildMultipartBody 上传文件流(OpenAI /v1/images/edits 约定)。
// 支持单个 URL 或 URL 数组(多参考图)。
//
// 未配 image_edit 的端点(豆包/duomi 等 JSON 直传 URL,或文生图/chat/video)一律原样返回,
// 图 URL 由 param_mapping 的 field_mapping 透传给上游,不受本函数影响。
func resolveFileParams(ctx context.Context, params map[string]any, endpoint *model.Endpoint) (map[string]any, error) {
	ie := endpoint.ImageEdit()
	if ie == nil {
		return params, nil // 端点未启用图生图文件上传,原样透传
	}

	val, ok := params[ie.FileField]
	if !ok {
		return params, nil // 本次请求未带参考图(纯文生图)
	}

	// 把单个 URL 下载并转为 @base64:filename:data
	convert := func(urlStr string) (string, error) {
		if !strings.HasPrefix(urlStr, "http") {
			return urlStr, nil // 已是 @base64:... 或非 URL,原样保留
		}
		b64Data, filename, err := downloadToBase64(ctx, urlStr)
		if err != nil {
			return "", fmt.Errorf("download reference image for field %s: %w", ie.FileField, err)
		}
		return "@base64:" + filename + ":" + b64Data, nil
	}

	result := make(map[string]any, len(params))
	for k, v := range params {
		result[k] = v
	}

	switch v := val.(type) {
	case string:
		converted, err := convert(v)
		if err != nil {
			return nil, err
		}
		result[ie.FileField] = converted
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				out = append(out, item)
				continue
			}
			converted, err := convert(s)
			if err != nil {
				return nil, err
			}
			out = append(out, converted)
		}
		result[ie.FileField] = out
	case []string:
		out := make([]string, 0, len(v))
		for _, item := range v {
			converted, err := convert(item)
			if err != nil {
				return nil, err
			}
			out = append(out, converted)
		}
		result[ie.FileField] = out
	}

	return result, nil
}

// downloadToBase64 下载 URL 内容并返回 base64 编码字符串和文件名
func downloadToBase64(ctx context.Context, urlStr string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return "", "", fmt.Errorf("create request: %w", err)
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return "", "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// 限制最大 32MiB 避免内存溢出
	maxSize := int64(32 * 1024 * 1024)
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSize+1))
	if err != nil {
		return "", "", fmt.Errorf("read body: %w", err)
	}
	if int64(len(data)) > maxSize {
		return "", "", fmt.Errorf("file exceeds 32MiB limit")
	}

	// 根据 Content-Type 生成文件名
	contentType := resp.Header.Get("Content-Type")
	ext := inferExtension(contentType, urlStr)
	filename := "upload" + ext

	return base64.StdEncoding.EncodeToString(data), filename, nil
}

// inferExtension 根据 Content-Type 和 URL 推断文件扩展名
func inferExtension(contentType, urlStr string) string {
	// 优先从 Content-Type 推断
	if strings.Contains(contentType, "jpeg") || strings.Contains(contentType, "jpg") {
		return ".jpg"
	}
	if strings.Contains(contentType, "png") {
		return ".png"
	}
	if strings.Contains(contentType, "webp") {
		return ".webp"
	}
	if strings.Contains(contentType, "gif") {
		return ".gif"
	}

	// 回退：从 URL 提取扩展名
	ext := filepath.Ext(urlStr)
	if idx := strings.Index(ext, "?"); idx > 0 {
		ext = ext[:idx]
	}
	if ext != "" && len(ext) <= 5 {
		return ext
	}

	// 默认 PNG
	return ".png"
}

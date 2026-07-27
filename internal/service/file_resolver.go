package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/filestorage"
	"github.com/mirainya/Prism/pkg/safeurl"
)

var uploadImageEditBytes = filestorage.TransferBytes
var downloadImageEditURL = downloadToBase64

// resolveFileParams prepares reference images according to endpoint image_edit config.
// URL mode uploads internal images and emits public URLs. Multipart mode converts
// public URLs to the internal file representation used by BaseProvider.
func resolveFileParams(ctx context.Context, params map[string]any, endpoint *model.Endpoint, capabilityCode string) (map[string]any, error) {
	ie := endpoint.ImageEdit()
	if ie == nil {
		return params, nil
	}

	sourceField, val, ok := findImageEditParam(params, ie.FileField)
	if !ok {
		return params, nil
	}

	convert := func(field, value string) (string, error) {
		if ie.InputMode == model.ImageInputModeURL {
			if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
				return value, nil
			}
			data, contentType, ok, err := decodeInternalImage(value)
			if err != nil {
				return "", fmt.Errorf("decode reference image for field %s: %w", field, err)
			}
			if !ok {
				return "", fmt.Errorf("field %s must contain an image URL or uploaded image", field)
			}
			imageURL, err := uploadImageEditBytes(ctx, data, contentType, capabilityCode)
			if err != nil {
				return "", fmt.Errorf("upload reference image for field %s: %w", field, err)
			}
			return imageURL, nil
		}

		if !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
			return value, nil // 已是 @base64:... 或非 URL,原样保留
		}
		b64Data, filename, err := downloadImageEditURL(ctx, value)
		if err != nil {
			return "", fmt.Errorf("download reference image for field %s: %w", field, err)
		}
		return "@base64:" + filename + ":" + b64Data, nil
	}

	result := make(map[string]any, len(params))
	for k, v := range params {
		result[k] = v
	}
	if sourceField != ie.FileField {
		delete(result, sourceField)
	}

	converted, err := convertImageEditValue(ie.FileField, val, convert)
	if err != nil {
		return nil, err
	}
	result[ie.FileField] = converted

	// Stored masks also need to become file parts for multipart edit endpoints.
	if ie.InputMode == model.ImageInputModeMultipart {
		if mask, exists := result["mask"]; exists {
			convertedMask, err := convertImageEditValue("mask", mask, convert)
			if err != nil {
				return nil, err
			}
			result["mask"] = convertedMask
		}
	}

	return result, nil
}

func convertImageEditValue(
	field string,
	value any,
	convert func(string, string) (string, error),
) (any, error) {
	switch typed := value.(type) {
	case string:
		return convert(field, typed)
	case []any:
		converted := make([]any, 0, len(typed))
		for _, item := range typed {
			stringValue, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("field %s must contain only strings", field)
			}
			convertedValue, err := convert(field, stringValue)
			if err != nil {
				return nil, err
			}
			converted = append(converted, convertedValue)
		}
		return converted, nil
	case []string:
		converted := make([]string, 0, len(typed))
		for _, item := range typed {
			convertedValue, err := convert(field, item)
			if err != nil {
				return nil, err
			}
			converted = append(converted, convertedValue)
		}
		return converted, nil
	default:
		return nil, fmt.Errorf("field %s must be a string or string array", field)
	}
}

func findImageEditParam(params map[string]any, configuredField string) (string, any, bool) {
	configuredValue, configuredExists := params[configuredField]
	if configuredExists && hasImageEditValue(configuredValue) {
		return configuredField, configuredValue, true
	}
	for _, field := range []string{"image_urls", "image", "images"} {
		if field == configuredField {
			continue
		}
		if value, ok := params[field]; ok && hasImageEditValue(value) {
			return field, value, true
		}
	}
	if configuredExists {
		return configuredField, configuredValue, true
	}
	return "", nil, false
}

func hasImageEditValue(value any) bool {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed) != ""
	case []any:
		return len(typed) > 0
	case []string:
		return len(typed) > 0
	default:
		return value != nil
	}
}

func decodeInternalImage(value string) ([]byte, string, bool, error) {
	encoded := ""
	if strings.HasPrefix(value, "@base64:") {
		parts := strings.SplitN(strings.TrimPrefix(value, "@base64:"), ":", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
			return nil, "", true, fmt.Errorf("invalid internal base64 image")
		}
		encoded = parts[1]
	} else if strings.HasPrefix(value, "data:") {
		parts := strings.SplitN(value, ",", 2)
		if len(parts) != 2 || !strings.Contains(parts[0], ";base64") {
			return nil, "", true, fmt.Errorf("invalid image data URL")
		}
		encoded = parts[1]
	} else {
		return nil, "", false, nil
	}

	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, "", true, err
	}
	contentType := http.DetectContentType(data)
	switch contentType {
	case "image/png", "image/jpeg", "image/webp":
		return data, contentType, true, nil
	default:
		return nil, "", true, fmt.Errorf("unsupported image type %s", contentType)
	}
}

// downloadToBase64 下载 URL 内容并返回 base64 编码字符串和文件名
func downloadToBase64(ctx context.Context, urlStr string) (string, string, error) {
	maxSize := int64(32 * 1024 * 1024)
	result, err := safeurl.Download(ctx, urlStr, maxSize)
	if err != nil {
		return "", "", err
	}
	contentType := result.ContentType
	ext := inferExtension(contentType, urlStr)
	filename := "upload" + ext
	return base64.StdEncoding.EncodeToString(result.Data), filename, nil
}

// DownloadImageAsBase64 downloads a public image URL and returns its base64 payload.
func DownloadImageAsBase64(ctx context.Context, urlStr string) (string, error) {
	b64Data, _, err := downloadToBase64(ctx, urlStr)
	return b64Data, err
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

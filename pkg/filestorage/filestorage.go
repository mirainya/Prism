package filestorage

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mirainya/Prism/pkg/config"
	"github.com/mirainya/Prism/pkg/safeurl"
)

type xfsResponse struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type UploadResult struct {
	URL string `json:"url"`
}

// IsBase64Data 判断字符串是否为 base64 图片数据
func IsBase64Data(s string) bool {
	if strings.HasPrefix(s, "data:") {
		return true
	}
	for _, prefix := range []string{"/9j/", "iVBOR", "R0lGOD", "Qk0", "UklGR"} {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

// TransferURL 从 URL 下载文件并上传到 xfilestorage，返回最终 URL
func TransferURL(ctx context.Context, originURL string, capabilityCode string) (string, error) {
	cfg := config.C.FileStorage
	if cfg.BaseURL == "" {
		return originURL, nil
	}

	maxBytes := int64(cfg.MaxFileSizeMB) * 1024 * 1024
	if maxBytes <= 0 {
		maxBytes = 64 * 1024 * 1024 // 默认 64MiB 兜底
	}
	result, err := safeurl.Download(ctx, originURL, maxBytes)
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	return upload(ctx, result.Data, result.ContentType, capabilityCode)
}

// TransferBase64 解码 base64 数据并上传到 xfilestorage，返回最终 URL
func TransferBase64(ctx context.Context, b64 string, capabilityCode string) (string, error) {
	cfg := config.C.FileStorage
	if cfg.BaseURL == "" {
		return "", fmt.Errorf("file storage not configured")
	}

	raw, contentType := parseBase64(b64)
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return "", fmt.Errorf("decode base64: %w", err)
	}

	return upload(ctx, decoded, contentType, capabilityCode)
}

// TransferBytes uploads in-memory file data to xfilestorage and returns its public URL.
func TransferBytes(ctx context.Context, data []byte, contentType string, capabilityCode string) (string, error) {
	cfg := config.C.FileStorage
	if cfg.BaseURL == "" || cfg.APIKey == "" {
		return "", fmt.Errorf("file storage not configured")
	}
	if len(data) == 0 {
		return "", fmt.Errorf("file data is empty")
	}
	return upload(ctx, data, contentType, capabilityCode)
}

func upload(ctx context.Context, data []byte, contentType string, capabilityCode string) (string, error) {
	cfg := config.C.FileStorage

	storagePath := fmt.Sprintf("%s%s/%s/", cfg.UploadPath, capabilityCode, time.Now().Format("2006/01/02"))
	ext := ".png"
	if strings.Contains(contentType, "jpeg") || strings.Contains(contentType, "jpg") {
		ext = ".jpg"
	} else if strings.Contains(contentType, "webp") {
		ext = ".webp"
	} else if strings.Contains(contentType, "mp4") || strings.Contains(contentType, "video") {
		ext = ".mp4"
	}
	filename := uuid.New().String() + ext

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return "", fmt.Errorf("create form file: %w", err)
	}
	part.Write(data)
	writer.WriteField("path", storagePath)
	writer.Close()

	endpoint := cfg.BaseURL + "/api/v1/upload"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &buf)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-Api-Key", cfg.APIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("upload request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var xfsResp xfsResponse
	if err := json.Unmarshal(body, &xfsResp); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	if xfsResp.Code != 200 {
		return "", fmt.Errorf("xfilestorage error: %s", xfsResp.Message)
	}

	var result UploadResult
	if err := json.Unmarshal(xfsResp.Data, &result); err != nil {
		return "", fmt.Errorf("parse upload result: %w", err)
	}

	result.URL = normalizePublicURL(result.URL)
	return result.URL, nil
}

func normalizePublicURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if strings.HasPrefix(rawURL, "http://") {
		return "https://" + strings.TrimPrefix(rawURL, "http://")
	}
	if rawURL != "" && !strings.HasPrefix(rawURL, "https://") {
		return "https://" + rawURL
	}
	return rawURL
}

func parseBase64(s string) (data string, contentType string) {
	if strings.HasPrefix(s, "data:") {
		parts := strings.SplitN(s, ",", 2)
		if len(parts) == 2 {
			meta := strings.TrimPrefix(parts[0], "data:")
			meta = strings.TrimSuffix(meta, ";base64")
			return parts[1], meta
		}
	}
	return s, "image/png"
}

// GenerateStoragePath 生成存储路径（供外部使用）
func GenerateStoragePath(capabilityCode string, originURL string) string {
	ext := filepath.Ext(originURL)
	if ext == "" || len(ext) > 10 {
		if strings.Contains(capabilityCode, "video") {
			ext = ".mp4"
		} else {
			ext = ".png"
		}
	}
	if idx := strings.Index(ext, "?"); idx > 0 {
		ext = ext[:idx]
	}
	return fmt.Sprintf("%s/%s/%s%s", capabilityCode, time.Now().Format("2006/01/02"), uuid.New().String(), ext)
}

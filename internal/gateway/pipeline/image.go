package pipeline

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/provider/chat"
	"github.com/mirainya/Prism/pkg/logger"
	"go.uber.org/zap"
)

// convertImageURLsToBase64 下载图片 URL 转 data URL。移植自 service.protocol_anthropic.go。
func convertImageURLsToBase64(messages []chat.ChatMessage) []chat.ChatMessage {
	result := make([]chat.ChatMessage, len(messages))
	copy(result, messages)

	for i, msg := range result {
		parts, ok := msg.Content.([]any)
		if !ok {
			continue
		}
		newParts := make([]any, len(parts))
		copy(newParts, parts)
		changed := false

		for j, part := range parts {
			pm, ok := part.(map[string]any)
			if !ok || pm["type"] != "image_url" {
				continue
			}
			imgURL, ok := pm["image_url"].(map[string]any)
			if !ok {
				continue
			}
			url, _ := imgURL["url"].(string)
			if url == "" || strings.HasPrefix(url, "data:") {
				continue
			}
			dataURL, err := downloadToDataURL(url)
			if err != nil {
				logger.Warn("gw image base64 download failed, keeping URL", zap.String("url", url), zap.Error(err))
				continue
			}
			newImg := make(map[string]any)
			for k, v := range imgURL {
				newImg[k] = v
			}
			newImg["url"] = dataURL
			newPart := make(map[string]any)
			for k, v := range pm {
				newPart[k] = v
			}
			newPart["image_url"] = newImg
			newParts[j] = newPart
			changed = true
		}
		if changed {
			result[i].Content = newParts
		}
	}
	return result
}

func downloadToDataURL(url string) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 20*1024*1024))
	if err != nil {
		return "", err
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = http.DetectContentType(body)
	}
	if idx := strings.Index(ct, ";"); idx >= 0 {
		ct = ct[:idx]
	}
	return fmt.Sprintf("data:%s;base64,%s", ct, base64.StdEncoding.EncodeToString(body)), nil
}

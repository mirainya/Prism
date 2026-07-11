package pipeline

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/mirainya/Prism/internal/provider/chat"
	"github.com/mirainya/Prism/pkg/logger"
	"github.com/mirainya/Prism/pkg/safeurl"
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
	result, err := safeurl.Download(context.Background(), url, 20*1024*1024)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("data:%s;base64,%s", result.ContentType, base64.StdEncoding.EncodeToString(result.Data)), nil
}

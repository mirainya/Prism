package service

import (
	"encoding/json"
	"strings"

	"github.com/mirainya/Prism/internal/model"
)

func truncateString(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

func parseExtraHeaders(raw []byte) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	var headers map[string]string
	if err := json.Unmarshal(raw, &headers); err == nil && len(headers) > 0 {
		return headers
	}
	return nil
}

func parseExtraBody(raw []byte) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var extraConfig struct {
		ExtraBody map[string]any `json:"extra_body"`
	}
	if err := json.Unmarshal(raw, &extraConfig); err == nil && len(extraConfig.ExtraBody) > 0 {
		return extraConfig.ExtraBody
	}
	return nil
}

func maskSensitiveHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	masked := make(map[string]string, len(headers))
	for key, value := range headers {
		lowerKey := strings.ToLower(key)
		switch {
		case strings.Contains(lowerKey, "authorization"), strings.Contains(lowerKey, "api-key"), strings.Contains(lowerKey, "apikey"), strings.Contains(lowerKey, "cookie"), strings.Contains(lowerKey, "token"):
			masked[key] = maskSecret(value)
		default:
			masked[key] = value
		}
	}
	return masked
}

func maskSecret(value string) string {
	if value == "" {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= 8 {
		return "***"
	}
	return string(runes[:4]) + "***" + string(runes[len(runes)-4:])
}

func requestLogID(log *model.ChannelRequestLog) uint {
	if log == nil {
		return 0
	}
	return log.ID
}

package service

import (
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider/chat"
)

func truncateString(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

// lastUserMessage 从消息列表中提取最后一条 user message
func lastUserMessage(messages []chat.ChatMessage) []chat.ChatMessage {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == model.RoleUser {
			return []chat.ChatMessage{messages[i]}
		}
	}
	return nil
}

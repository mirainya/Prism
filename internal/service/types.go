package service

import (
	"github.com/mirainya/Prism/internal/provider/chat"
)

// CompletionRequest 对话补全请求
type CompletionRequest struct {
	UserID           uint
	TokenID          uint
	Model            string
	Messages         []chat.ChatMessage
	Temperature      float64
	MaxTokens        int
	TopP             float64
	FrequencyPenalty float64
	PresencePenalty  float64
	Stop             []string
	Stream           bool
	StreamSpecified  bool
	Tools            []chat.ToolDefinition
	ToolChoice       any
	ResponseFormat   *chat.ResponseFormat
	Seed             *int
	User             string
	ConversationID   string
}

// CompletionResponse 对话补全响应
type CompletionResponse struct {
	ID             string                `json:"id"`
	Object         string                `json:"object"`
	Created        int64                 `json:"created"`
	Model          string                `json:"model"`
	ConversationID string                `json:"conversation_id,omitempty"`
	Choices        []chat.ChatChoice     `json:"choices"`
	Usage          *chat.ChatUsage       `json:"usage,omitempty"`
	Debug          *PlaygroundDebugDetail `json:"debug,omitempty"`
}

// StreamAggregationResult 流式聚合结果
type StreamAggregationResult struct {
	AssistantContent string
	ReasoningContent string
	FinishReason     string
	ResponsePreview  string
	ResponseBody     string
	ErrorMessage     string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	Usage            *chat.ChatUsage
}

// PlaygroundDebugDetail 调试详情
type PlaygroundDebugDetail struct {
	RequestLogID    uint              `json:"request_log_id"`
	ConversationID  uint              `json:"conversation_id,omitempty"`
	Status          string            `json:"status"`
	ModelCode       string            `json:"model_code"`
	VendorModel     string            `json:"vendor_model"`
	ChannelID       uint              `json:"channel_id"`
	ChannelName     string            `json:"channel_name,omitempty"`
	ChannelType     string            `json:"channel_type,omitempty"`
	AccountID       uint              `json:"account_id"`
	RequestPath     string            `json:"request_path"`
	IsStream        bool              `json:"is_stream"`
	LatencyMs       int64             `json:"latency_ms"`
	StatusCode      int               `json:"status_code"`
	ErrorMessage    string            `json:"error_message,omitempty"`
	FinishReason    string            `json:"finish_reason,omitempty"`
	ResponsePreview string            `json:"response_preview,omitempty"`
	RequestHeaders  map[string]string `json:"request_headers,omitempty"`
	RequestBody     map[string]any    `json:"request_body,omitempty"`
	ResponseBody    any               `json:"response_body,omitempty"`
	Usage           *chat.ChatUsage   `json:"usage,omitempty"`
}

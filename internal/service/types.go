package service

import (
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider/chat"
)

// CompletionRequest 对话补全请求
type CompletionRequest struct {
	UserID              uint
	TokenID             uint
	Model               string
	Messages            []chat.ChatMessage
	Temperature         *float64
	MaxTokens           int
	MaxCompletionTokens *int
	TopP                *float64
	FrequencyPenalty    *float64
	PresencePenalty     *float64
	Stop                []string
	Stream              bool
	StreamSpecified     bool
	StreamOptions       *chat.StreamOptions
	N                   *int
	Logprobs            *bool
	TopLogprobs         *int
	Tools               []chat.ToolDefinition
	ToolChoice          any
	ParallelToolCalls   *bool
	ResponseFormat      *chat.ResponseFormat
	Seed                *int
	User                string
	Modalities          []string
	Audio               *chat.AudioConfig
	Prediction          *chat.Prediction
	Store               *bool
	Metadata            map[string]string
	ServiceTier         *string
	ConversationID      string

	// ReasoningEffort 请求级思考档位覆盖(nil=未指定,用模型默认)
	// 值为模型 thinking_config.options 里的某个 value
	ReasoningEffort *string

	// --- 内部字段：火山 Responses 有状态对话(B模式) ---
	PreviousResponseID string             // 非空时启用 B 模式(只发新消息)
	NewMessages        []chat.ChatMessage // B 模式下本轮要发送的新消息
	ProviderKeyID      uint
	UpstreamTransport  model.UpstreamTransport
}

// CompletionResponse 对话补全响应
type CompletionResponse struct {
	ID                string                 `json:"id"`
	Object            string                 `json:"object"`
	Created           int64                  `json:"created"`
	Model             string                 `json:"model"`
	ConversationID    string                 `json:"conversation_id,omitempty"`
	Choices           []chat.ChatChoice      `json:"choices"`
	Usage             *chat.ChatUsage        `json:"usage,omitempty"`
	SystemFingerprint string                 `json:"system_fingerprint,omitempty"`
	ServiceTier       string                 `json:"service_tier,omitempty"`
	Debug             *PlaygroundDebugDetail `json:"debug,omitempty"`

	// ProviderResponseID 火山 Responses 返回的 response_id。
	// 对外输出,供客户端托管的 B 模式续话:下轮请求带回 previous_response_id 即可只发新消息省 token。
	ProviderResponseID string `json:"provider_response_id,omitempty"`

	// RequestLogID 本次请求的 channel_request_logs 主键(内部用,不对外)。
	// playground 据此下发 prism-debug 事件让前端拉完整调试详情。
	RequestLogID      uint                    `json:"-"`
	ProviderKeyID     uint                    `json:"-"`
	UpstreamTransport model.UpstreamTransport `json:"-"`
}

// StreamAggregationResult 流式聚合结果
type StreamAggregationResult struct {
	AssistantContent   string
	ReasoningContent   string
	FinishReason       string
	ResponsePreview    string
	ResponseBody       string
	ErrorMessage       string
	PromptTokens       int
	CompletionTokens   int
	TotalTokens        int
	Usage              *chat.ChatUsage
	ProviderResponseID string // 火山 Responses 的 response_id(B模式回写用)
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

package domain

import (
	"context"
	"io"
)

// ModelInfo 上游模型信息（动态发现用）
type ModelInfo struct {
	ID        string         // 上游模型 ID
	Name      string         // 显示名
	Provider  string         // 供应商标识
	Type      string         // chat/image/video/embedding
	MaxTokens int            // 最大 token 数
	Features  []string       // streaming, tools, vision, json_mode...
	RawMeta   map[string]any // 上游返回的原始元数据
}

// ChatProvider LLM 对话供应商接口
type ChatProvider interface {
	// Complete 非流式对话补全
	Complete(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error)
	// StreamComplete 流式对话补全，返回原始 HTTP 响应体
	StreamComplete(ctx context.Context, req *ChatCompletionRequest) (io.ReadCloser, error)
	// ListModels 查询上游可用模型列表
	ListModels(ctx context.Context) ([]ModelInfo, error)
	// Name 供应商名称
	Name() string
}

// ChatCompletionRequest 统一对话请求（由 service 层构建）
type ChatCompletionRequest struct {
	Model            string
	Messages         []ChatMessage
	Temperature      *float64
	MaxTokens        *int
	TopP             *float64
	FrequencyPenalty *float64
	PresencePenalty  *float64
	Stop             []string
	Stream           bool
	Tools            []map[string]any
	ToolChoice       any
	ResponseFormat   map[string]any
	ExtraBody        map[string]any // 供应商特定字段
}

// ChatMessage 对话消息
type ChatMessage struct {
	Role       string `json:"role"`
	Content    any    `json:"content"` // string 或 []ContentPart
	Name       string `json:"name,omitempty"`
	ToolCalls  any    `json:"tool_calls,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// ChatCompletionResponse 统一对话响应
type ChatCompletionResponse struct {
	ID      string
	Model   string
	Choices []ChatChoice
	Usage   *ChatUsage
}

// ChatChoice 选项
type ChatChoice struct {
	Index        int
	Message      ChatMessage
	FinishReason string
}

// ChatUsage token 用量
type ChatUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// ProviderConfig 供应商配置
type ProviderConfig struct {
	BaseURL      string
	APIKey       string
	VendorModel  string
	RequestPath  string
	Timeout      int // seconds
	ExtraHeaders map[string]string
}

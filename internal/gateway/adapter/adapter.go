// Package adapter 定义上游协议适配器接口与注册表。
//
// 每个上游协议(openai/anthropic/volcengine/google...)实现一个 UpstreamAdapter,
// 通过 Register 注册到全局注册表。加新协议 = 写一个实现 + Register,零核心改动。
//
// adapter 包之外的任何包都不应 import 具体 adapter 子包,只通过 Get 拿接口。
package adapter

import (
	"context"
	"net/http"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider/chat"
)

// UpstreamRequest 适配器收到的请求:pipeline 完成选路后构造。
// 内含 canonical hub 类型 chat.ChatRequest + 上游连接上下文。
type UpstreamRequest struct {
	Chat         *chat.ChatRequest // 归一化的请求体(hub 类型)
	VendorModel  string            // 上游真实模型名(发给上游的 model 字段)
	APIKey       string            // 上游鉴权 key
	BaseURL      string            // 渠道 base_url
	RequestPath  string            // 请求路径(空则适配器用协议默认)
	ExtraHeaders map[string]string // 渠道级额外请求头
	Timeout      time.Duration     // 请求超时

	// 火山 B 模式(有状态对话):零值=A 模式/不适用
	PreviousResponseID string            // 上一轮 provider_response_id
	NewMessages        []chat.ChatMessage // 本轮新消息(B 模式只发这些)
}

// UpstreamResponse 非流式响应:适配器已归一化成 canonical ChatResponse。
type UpstreamResponse struct {
	Chat               *chat.ChatResponse
	ProviderResponseID string // 仅火山 B 模式回填,其他协议为空
}

// UpstreamAdapter 单个协议适配器实现的唯一接口。
type UpstreamAdapter interface {
	// Protocol 返回本适配器处理的协议名。
	Protocol() model.Protocol

	// Complete 发送非流式请求,返回归一化的 ChatResponse。
	Complete(ctx context.Context, req *UpstreamRequest) (*UpstreamResponse, error)

	// StreamComplete 发送流式请求,返回 *http.Response,
	// 其 Body 已被适配器归一化成 OpenAI SSE 格式(anthropic/volcengine 用 io.Pipe 翻译)。
	// 调用方(pipeline)负责关闭 Body。
	StreamComplete(ctx context.Context, req *UpstreamRequest) (*http.Response, error)
}

// AdapterFactory 构造无状态适配器(适配器不持有单请求状态)。
type AdapterFactory func() UpstreamAdapter

var registry = map[model.Protocol]AdapterFactory{}

// Register 注册协议适配器工厂。在 gateway.NewGateway 中调用。
func Register(proto model.Protocol, f AdapterFactory) {
	registry[proto] = f
}

// Get 按协议取适配器实例;未注册返回 ok=false。
func Get(proto model.Protocol) (UpstreamAdapter, bool) {
	f, ok := registry[proto]
	if !ok {
		return nil, false
	}
	return f(), true
}

// Package gateway 装配网关 v2:注册协议适配器 + 构造 pipeline + 暴露路由注册。
package gateway

import (
	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/gateway/adapter"
	"github.com/mirainya/Prism/internal/gateway/adapter/anthropic"
	"github.com/mirainya/Prism/internal/gateway/adapter/google"
	"github.com/mirainya/Prism/internal/gateway/adapter/openai"
	"github.com/mirainya/Prism/internal/gateway/adapter/volcengine"
	"github.com/mirainya/Prism/internal/gateway/handler"
	"github.com/mirainya/Prism/internal/gateway/pipeline"
	"github.com/mirainya/Prism/internal/model"
)

// Gateway 网关实例。
type Gateway struct {
	chat *handler.ChatHandler
	pipe *pipeline.Pipeline
}

// New 构造网关:注册适配器 + 建 pipeline。
func New() *Gateway {
	registerAdapters()
	pipe := pipeline.New()
	return &Gateway{chat: handler.NewChatHandler(pipe), pipe: pipe}
}

// Pipeline 返回共享 pipeline 实例(playground 等 console 路径复用,同源同熔断/计费状态)。
func (g *Gateway) Pipeline() *pipeline.Pipeline {
	return g.pipe
}

// registerAdapters 注册所有协议适配器。加新协议在这里加一行。
func registerAdapters() {
	// OpenAI 兼容协议(openai/deepseek/qwen/moonshot 等)共用透传适配器
	openaiFactory := func() adapter.UpstreamAdapter { return openai.New() }
	adapter.Register(model.ProtocolOpenAI, openaiFactory)

	adapter.Register(model.ProtocolAnthropic, func() adapter.UpstreamAdapter { return anthropic.New() })
	adapter.Register(model.ProtocolVolcengine, func() adapter.UpstreamAdapter { return volcengine.New() })
	adapter.Register(model.ProtocolGoogle, func() adapter.UpstreamAdapter { return google.New() })
}

// RegisterRoutes 在给定分组下注册网关 v2 路由。分组应已挂 Auth()+限流中间件。
func (g *Gateway) RegisterRoutes(group *gin.RouterGroup) {
	g.RegisterChat(group)
}

// RegisterChat 在给定分组下注册 chat/completions。/v1 与 /v2 共享同一 pipeline 实例
// (熔断/计费状态统一),故两处均调用本方法。分组应已挂 Auth()+限流中间件。
func (g *Gateway) RegisterChat(group *gin.RouterGroup) {
	group.POST("/chat/completions", g.chat.Completions)
}

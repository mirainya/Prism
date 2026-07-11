// Package gateway 装配聊天网关:注册协议适配器 + 构造 pipeline + 暴露路由注册。
package gateway

import (
	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/gateway/engine"
	"github.com/mirainya/Prism/internal/gateway/handler"
	"github.com/mirainya/Prism/internal/gateway/pipeline"
	responsepipeline "github.com/mirainya/Prism/internal/gateway/responses"
)

// Gateway 网关实例。
type Gateway struct {
	chat      *handler.ChatHandler
	anthropic *handler.AnthropicHandler
	responses *handler.ResponsesHandler
	pipe      *pipeline.Pipeline
}

// New 构造网关:注册适配器 + 建 pipeline。
func New(executionEngine *engine.Engine) *Gateway {
	if executionEngine == nil {
		panic("Gateway V2 engine is required")
	}
	pipe := pipeline.New(executionEngine)
	return &Gateway{
		chat: handler.NewChatHandler(pipe), anthropic: handler.NewAnthropicHandler(executionEngine),
		responses: handler.NewResponsesHandler(responsepipeline.New(executionEngine)), pipe: pipe,
	}
}

// Pipeline 返回共享 pipeline 实例(playground 等 console 路径复用,同源同熔断/计费状态)。
func (g *Gateway) Pipeline() *pipeline.Pipeline {
	return g.pipe
}

// RegisterChat 在给定分组下注册 chat/completions。
// 分组应已挂 Auth()+限流中间件。
func (g *Gateway) RegisterChat(group *gin.RouterGroup) {
	group.POST("/chat/completions", g.chat.Completions)
}

func (g *Gateway) RegisterAnthropic(group *gin.RouterGroup) {
	if g.anthropic != nil {
		group.POST("/messages", g.anthropic.Messages)
	}
}

func (g *Gateway) RegisterResponses(group *gin.RouterGroup) {
	group.POST("/responses", g.responses.Create)
	group.GET("/responses/:id", g.responses.Get)
	group.DELETE("/responses/:id", g.responses.Delete)
	group.POST("/responses/:id/cancel", g.responses.Cancel)
	group.GET("/responses/:id/input_items", g.responses.InputItems)
}

func (g *Gateway) RegisterFiles(group *gin.RouterGroup) {
	group.POST("/files", handler.UploadFile)
	group.GET("/files", handler.ListFiles)
	group.GET("/files/:id", handler.GetFile)
	group.GET("/files/:id/content", handler.GetFileContent)
	group.DELETE("/files/:id", handler.DeleteFile)
}

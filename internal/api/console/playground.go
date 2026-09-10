package console

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/gateway/engine"
	"github.com/mirainya/Prism/internal/gateway/handler"
	"github.com/mirainya/Prism/internal/gateway/pipeline"
	responsepipeline "github.com/mirainya/Prism/internal/gateway/responses"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/pkg/errors"
)

var queryService = service.NewQueryService()

// chatPipeline 由 router 注入的共享网关 pipeline(playground chat 走它,与 /v1 同源)。
var chatPipeline *pipeline.Pipeline
var playgroundResponsesHandler *handler.ResponsesHandler
var playgroundAnthropicHandler *handler.AnthropicHandler

// SetChatPipeline 注入共享 pipeline(在 router SetupRouter 装配时调用一次)。
func SetChatPipeline(p *pipeline.Pipeline) {
	chatPipeline = p
}

// SetGatewayEngine injects the shared V2 engine used by the public protocol handlers.
func SetGatewayEngine(executionEngine *engine.Engine) {
	if executionEngine == nil {
		playgroundResponsesHandler = nil
		playgroundAnthropicHandler = nil
		return
	}
	playgroundResponsesHandler = handler.NewResponsesHandler(responsepipeline.New(executionEngine))
	playgroundAnthropicHandler = handler.NewAnthropicHandler(executionEngine)
}

func usePlaygroundToken(c *gin.Context) (*model.Token, bool) {
	token, ok := getPlaygroundToken(c)
	if !ok {
		return nil, false
	}
	c.Set(middleware.ContextKeyTokenID, token.ID)
	c.Set(middleware.ContextKeyToken, token)
	return token, true
}

// PlaygroundResponses POST /api/playground/:token_id/responses.
func PlaygroundResponses(c *gin.Context) {
	if _, ok := usePlaygroundToken(c); !ok {
		return
	}
	if playgroundResponsesHandler == nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, "responses handler not initialized")
		return
	}
	playgroundResponsesHandler.Create(c)
}

// PlaygroundAnthropicMessages POST /api/playground/:token_id/messages.
func PlaygroundAnthropicMessages(c *gin.Context) {
	if _, ok := usePlaygroundToken(c); !ok {
		return
	}
	if playgroundAnthropicHandler == nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, "anthropic handler not initialized")
		return
	}
	playgroundAnthropicHandler.Messages(c)
}

func getPlaygroundToken(c *gin.Context) (*model.Token, bool) {
	userID := middleware.GetUserID(c)
	tokenID, err := resp.ParseUintParam(c, "token_id")
	if err != nil {
		return nil, false
	}

	var token model.Token
	if err := model.DB().Where("id = ? AND user_id = ? AND status = 1", tokenID, userID).First(&token).Error; err != nil {
		resp.NotFound(c, errors.WithMessage(errors.ErrInvalidParams, "token not found"))
		return nil, false
	}
	return &token, true
}

// PlaygroundListModels GET /api/playground/:token_id/models
func PlaygroundListModels(c *gin.Context) {
	token, ok := getPlaygroundToken(c)
	if !ok {
		return
	}
	_ = token

	models, err := queryService.ListAvailableCapabilities(c.Request.Context(), "", "chat")
	if err != nil {
		resp.InternalError(c, errors.ErrInternalError)
		return
	}

	resp.Success(c, playgroundChatModelList(models))
}

func playgroundChatModelList(models []service.AvailableModelCapability) gin.H {
	data := make([]gin.H, 0, len(models))
	for _, m := range models {
		operations := make([]string, 0, len(m.Operations))
		endpoints := make([]string, 0, len(m.Operations))
		for _, operation := range m.Operations {
			operations = appendUniquePlaygroundModelValue(operations, operation.ID)
			endpoints = appendUniquePlaygroundModelValue(endpoints, operation.Path)
		}
		data = append(data, gin.H{
			"id":                       m.ID,
			"object":                   "model",
			"created":                  0,
			"owned_by":                 "prism",
			"supports_stream":          m.SupportsStream,
			"default_stream":           m.DefaultStream,
			"supports_tools":           m.SupportsTools,
			"supports_response_format": m.SupportsResponseFormat,
			"supports_multimodal":      m.SupportsMultimodal,
			"max_tokens":               m.MaxTokens,
			"group":                    m.Group,
			"thinking":                 m.Thinking,
			"supported_operations":     operations,
			"supported_endpoints":      endpoints,
		})
	}

	return gin.H{
		"object": "list",
		"data":   data,
	}
}

func appendUniquePlaygroundModelValue(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, candidate := range values {
		if candidate == value {
			return values
		}
	}
	return append(values, value)
}

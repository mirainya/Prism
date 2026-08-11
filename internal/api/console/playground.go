package console

import (
	"fmt"
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

var capabilityService = service.NewUnifiedService()
var queryService = service.NewQueryService()
var playgroundDashboardService = service.NewDashboardService()

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
	var tokenID uint
	if _, err := fmt.Sscanf(c.Param("token_id"), "%d", &tokenID); err != nil {
		resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, "invalid token_id"))
		return nil, false
	}

	var token model.Token
	if err := model.DB().Where("id = ? AND user_id = ? AND status = 1", tokenID, userID).First(&token).Error; err != nil {
		resp.NotFound(c, errors.WithMessage(errors.ErrInvalidParams, "token not found"))
		return nil, false
	}
	return &token, true
}

// PlaygroundListCapabilities GET /api/playground/:token_id/capabilities
func PlaygroundListCapabilities(c *gin.Context) {
	token, ok := getPlaygroundToken(c)
	if !ok {
		return
	}
	_ = token

	channelType := c.Query("channel")
	capabilityType := c.Query("type")
	result, err := queryService.ListAvailableCapabilities(channelType, capabilityType)
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, "failed to get capabilities")
		return
	}
	resp.Success(c, result)
}

// PlaygroundListModels GET /api/playground/:token_id/models
func PlaygroundListModels(c *gin.Context) {
	token, ok := getPlaygroundToken(c)
	if !ok {
		return
	}
	_ = token

	models, err := service.NewGatewayAdminService().ListPlaygroundModels()
	if err != nil {
		resp.InternalError(c, errors.ErrInternalError)
		return
	}

	data := make([]gin.H, 0, len(models))
	for _, m := range models {
		data = append(data, gin.H{
			"id":                       m.ID,
			"object":                   m.Object,
			"created":                  m.Created,
			"owned_by":                 m.OwnedBy,
			"supports_stream":          m.SupportsStream,
			"default_stream":           m.DefaultStream,
			"supports_tools":           m.SupportsTools,
			"supports_response_format": m.SupportsResponseFormat,
			"supports_multimodal":      m.SupportsMultimodal,
			"max_tokens":               m.MaxTokens,
			"group":                    m.Group,
			"thinking":                 m.Thinking,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"object": "list",
		"data":   data,
	})
}

// PlaygroundInvokeCapability POST /api/playground/:token_id/capabilities/:capability
func PlaygroundInvokeCapability(c *gin.Context) {
	token, ok := getPlaygroundToken(c)
	if !ok {
		return
	}

	capability := c.Param("capability")
	var body struct {
		Channel         string         `json:"channel"`
		Model           string         `json:"model"`
		Operation       string         `json:"operation"`
		InteractionMode string         `json:"interaction_mode"`
		CallbackURL     string         `json:"callback_url"`
		Params          map[string]any `json:"params"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		resp.ErrorMsg(c, http.StatusBadRequest, 400, "invalid request body")
		return
	}

	userID := middleware.GetUserID(c)
	result, err := capabilityService.Invoke(c.Request.Context(), &service.InvokeRequest{
		UserID:          userID,
		TokenID:         token.ID,
		Capability:      capability,
		RouteOperation:  body.Operation,
		Channel:         body.Channel,
		Model:           body.Model,
		InteractionMode: body.InteractionMode,
		CallbackURL:     body.CallbackURL,
		Params:          body.Params,
		// playground: sync/stream 慢上游不阻塞按钮,立即返回 task_no 由前端轮询
		Async: true,
	})
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}
	resp.Success(c, result)
}

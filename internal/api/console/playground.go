package console

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/pkg/errors"
)

var chatService = service.NewUnifiedService()
var capabilityService = service.NewUnifiedService()
var queryService = service.NewQueryService()
var playgroundDashboardService = service.NewDashboardService()

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

	models, err := chatService.ListPlaygroundModels(c.Request.Context(), token.ID)
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
		Channel:         body.Channel,
		Model:           body.Model,
		InteractionMode: body.InteractionMode,
		CallbackURL:     body.CallbackURL,
		Params:      body.Params,
	})
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}
	resp.Success(c, result)
}

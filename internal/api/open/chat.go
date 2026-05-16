package open

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/provider/chat"
	"github.com/mirainya/Prism/internal/service"
)

// ChatCompletions POST /v1/chat/completions
func ChatCompletions(c *gin.Context) {
	var req struct {
		Model            string                `json:"model" binding:"required"`
		Messages         []chat.ChatMessage    `json:"messages" binding:"required,min=1"`
		Temperature      float64               `json:"temperature"`
		MaxTokens        int                   `json:"max_tokens"`
		TopP             float64               `json:"top_p"`
		FrequencyPenalty float64               `json:"frequency_penalty"`
		PresencePenalty  float64               `json:"presence_penalty"`
		Stop             []string              `json:"stop"`
		Stream           bool                  `json:"stream"`
		Tools            []chat.ToolDefinition `json:"tools"`
		ToolChoice       any                   `json:"tool_choice"`
		ResponseFormat   *chat.ResponseFormat  `json:"response_format"`
		Seed             *int                  `json:"seed"`
		User             string                `json:"user"`
		ConversationID   string                `json:"conversation_id"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		resp.ErrorMsg(c, http.StatusBadRequest, 400, err.Error())
		return
	}

	token := middleware.GetToken(c)
	completionReq := &service.CompletionRequest{
		UserID:           token.UserID,
		TokenID:          token.ID,
		Model:            req.Model,
		Messages:         req.Messages,
		Temperature:      req.Temperature,
		MaxTokens:        req.MaxTokens,
		TopP:             req.TopP,
		FrequencyPenalty: req.FrequencyPenalty,
		PresencePenalty:  req.PresencePenalty,
		Stop:             req.Stop,
		Stream:           req.Stream,
		StreamSpecified:  true,
		Tools:            req.Tools,
		ToolChoice:       req.ToolChoice,
		ResponseFormat:   req.ResponseFormat,
		Seed:             req.Seed,
		User:             req.User,
		ConversationID:   req.ConversationID,
	}

	svc := service.NewUnifiedService()

	if req.Stream {
		session, err := svc.StreamComplete(c.Request.Context(), completionReq)
		if err != nil {
			resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
			return
		}
		defer session.CleanupFunc()
		defer session.UpstreamResp.Body.Close()

		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")
		c.Status(http.StatusOK)

		io.Copy(c.Writer, session.UpstreamResp.Body)
		c.Writer.Flush()
		svc.FinalizeStream(session, nil, nil)
		return
	}

	chatResp, err := svc.Complete(c.Request.Context(), completionReq)
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}

	c.JSON(http.StatusOK, chatResp)
}

// GetChatModelDetail GET /v1/models/:code
func GetChatModelDetail(c *gin.Context) {
	svc := service.NewUnifiedService()
	m, err := svc.GetModelDetail(c.Request.Context(), c.Param("code"))
	if err != nil {
		resp.ErrorMsg(c, http.StatusNotFound, 404, "model not found")
		return
	}

	item := gin.H{
		"id":       m.Code,
		"object":   "model",
		"created":  m.CreatedAt.Unix(),
		"owned_by": m.Provider,
	}
	if m.Name != "" {
		item["name"] = m.Name
	}
	if m.Description != "" {
		item["description"] = m.Description
	}
	if m.MaxTokens > 0 {
		item["max_tokens"] = m.MaxTokens
	}
	if len(m.Features) > 0 {
		var features []string
		if json.Unmarshal(m.Features, &features) == nil && len(features) > 0 {
			item["features"] = features
		}
	}
	if len(m.ParamSchema) > 0 {
		var schema any
		if json.Unmarshal(m.ParamSchema, &schema) == nil && schema != nil {
			item["param_schema"] = schema
		}
	}

	c.JSON(http.StatusOK, item)
}

// ListChatModelsPublic GET /v1/models
func ListChatModelsPublic(c *gin.Context) {
	svc := service.NewUnifiedService()
	models, err := svc.ListModels(c.Request.Context())
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}

	data := make([]gin.H, 0, len(models))
	for _, m := range models {
		item := gin.H{
			"id":       m.Code,
			"object":   "model",
			"created":  m.CreatedAt.Unix(),
			"owned_by": m.Provider,
			"type":     m.Type,
		}
		if m.MaxTokens > 0 {
			item["max_tokens"] = m.MaxTokens
		}
		if len(m.Features) > 0 {
			var features []string
			if json.Unmarshal(m.Features, &features) == nil && len(features) > 0 {
				item["features"] = features
			}
		}
		data = append(data, item)
	}

	c.JSON(http.StatusOK, gin.H{
		"object": "list",
		"data":   data,
	})
}

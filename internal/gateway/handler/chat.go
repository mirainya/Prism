// Package handler 提供网关 v2 的 gin handler。
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/gateway/pipeline"
	"github.com/mirainya/Prism/internal/gateway/stream"
	"github.com/mirainya/Prism/internal/provider/chat"
	"github.com/mirainya/Prism/internal/service"
)

// ChatHandler 持有 pipeline。
type ChatHandler struct {
	pipe *pipeline.Pipeline
}

// NewChatHandler 构造。
func NewChatHandler(pipe *pipeline.Pipeline) *ChatHandler {
	return &ChatHandler{pipe: pipe}
}

// Completions POST /v2/chat/completions
func (h *ChatHandler) Completions(c *gin.Context) {
	var req struct {
		Model            string                `json:"model" binding:"required"`
		Messages         []chat.ChatMessage    `json:"messages" binding:"required,min=1"`
		Temperature      *float64              `json:"temperature"`
		MaxTokens        int                   `json:"max_tokens"`
		TopP             *float64              `json:"top_p"`
		FrequencyPenalty *float64              `json:"frequency_penalty"`
		PresencePenalty  *float64              `json:"presence_penalty"`
		Stop             []string              `json:"stop"`
		Stream           bool                  `json:"stream"`
		Tools            []chat.ToolDefinition `json:"tools"`
		ToolChoice       any                   `json:"tool_choice"`
		ResponseFormat   *chat.ResponseFormat  `json:"response_format"`
		Seed             *int                  `json:"seed"`
		User             string                `json:"user"`
		ReasoningEffort  *string               `json:"reasoning_effort"`
		// PreviousResponseID 客户端托管的火山 B 模式续话:带上次响应的 provider_response_id,
		// 上游只需处理本轮新消息即可省 token(自愈失效见 pipeline.Complete)。
		PreviousResponseID string `json:"previous_response_id"`
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
		ReasoningEffort:  req.ReasoningEffort,
		PreviousResponseID: req.PreviousResponseID,
	}

	if req.Stream {
		session, err := h.pipe.StreamComplete(c.Request.Context(), completionReq)
		if err != nil {
			resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
			return
		}
		defer session.Cleanup()

		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")
		c.Status(http.StatusOK)

		agg, streamErr := stream.ProxyStream(c.Writer, session.UpstreamResp.Body)
		session.FinalizeStream(agg, streamErr)
		return
	}

	chatResp, err := h.pipe.Complete(c.Request.Context(), completionReq)
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}
	c.JSON(http.StatusOK, chatResp)
}

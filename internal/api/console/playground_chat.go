package console

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	gwstream "github.com/mirainya/Prism/internal/gateway/stream"
	"github.com/mirainya/Prism/internal/provider/chat"
	"github.com/mirainya/Prism/internal/service"
	perrors "github.com/mirainya/Prism/pkg/errors"
)

// PlaygroundChatCompletions POST /api/playground/:token_id/chat/completions
// 走共享 gateway pipeline(与 /v1 同源)。会话续聊/历史/火山 B 模式由本 handler 编排,
// pipeline 保持无状态。
func PlaygroundChatCompletions(c *gin.Context) {
	token, ok := getPlaygroundToken(c)
	if !ok {
		return
	}

	var req struct {
		Model            string                `json:"model" binding:"required"`
		Messages         []chat.ChatMessage    `json:"messages" binding:"required,min=1"`
		Temperature      *float64              `json:"temperature"`
		MaxTokens        int                   `json:"max_tokens"`
		TopP             *float64              `json:"top_p"`
		FrequencyPenalty *float64              `json:"frequency_penalty"`
		PresencePenalty  *float64              `json:"presence_penalty"`
		Stop             []string              `json:"stop"`
		Stream           *bool                 `json:"stream"`
		Tools            []chat.ToolDefinition `json:"tools"`
		ToolChoice       any                   `json:"tool_choice"`
		ResponseFormat   *chat.ResponseFormat  `json:"response_format"`
		Seed             *int                  `json:"seed"`
		User             string                `json:"user"`
		ConversationID   string                `json:"conversation_id"`
		ReasoningEffort  *string               `json:"reasoning_effort"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.ErrorMsg(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	if chatPipeline == nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, "chat pipeline not initialized")
		return
	}

	stream := req.Stream != nil && *req.Stream

	// 会话编排:加载历史 + 火山 B 模式(previous_response_id)
	newMessages := req.Messages
	cc := service.LoadConversationContext(req.ConversationID, token.ID, req.Model)
	fullMessages := append(append([]chat.ChatMessage{}, cc.History...), newMessages...)

	completionReq := &service.CompletionRequest{
		UserID:           token.UserID,
		TokenID:          token.ID,
		Model:            req.Model,
		Messages:         fullMessages,
		Temperature:      req.Temperature,
		MaxTokens:        req.MaxTokens,
		TopP:             req.TopP,
		FrequencyPenalty: req.FrequencyPenalty,
		PresencePenalty:  req.PresencePenalty,
		Stop:             req.Stop,
		Stream:           stream,
		StreamSpecified:  req.Stream != nil,
		Tools:            req.Tools,
		ToolChoice:       req.ToolChoice,
		ResponseFormat:   req.ResponseFormat,
		Seed:             req.Seed,
		User:             req.User,
		ReasoningEffort:  req.ReasoningEffort,
	}
	// 有状态对话:只发新消息,历史由上游维护
	if cc.PreviousResponseID != "" {
		completionReq.PreviousResponseID = cc.PreviousResponseID
		completionReq.NewMessages = newMessages
		completionReq.ProviderKeyID = cc.ProviderKeyID
		completionReq.UpstreamTransport = cc.UpstreamTransport
	}

	if stream {
		playgroundStream(c, completionReq, cc, newMessages)
		return
	}
	playgroundNonStream(c, completionReq, cc, newMessages)
}

// playgroundStream 流式:转发 SSE + 聚合 + 存会话 + 下发 prism-debug。
func playgroundStream(c *gin.Context, req *service.CompletionRequest, cc *service.ConversationContext, newMessages []chat.ChatMessage) {
	session, err := chatPipeline.StreamComplete(c.Request.Context(), req)
	if err != nil {
		respondPlaygroundChatError(c, err)
		return
	}
	defer session.Cleanup()

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)

	agg, streamErr := gwstream.ProxyStream(c.Writer, session.UpstreamResp.Body)
	provRespID := session.FinalizeStream(agg, streamErr)
	if streamErr != nil {
		return
	}

	reqLogID := session.RequestLogID()
	if agg != nil {
		assistant := chat.ChatMessage{
			Role:             "assistant",
			Content:          agg.AssistantContent,
			ReasoningContent: agg.ReasoningContent,
		}
		service.SaveConversationTurn(cc, req.UserID, req.TokenID, req.Model,
			newMessages, assistant, agg.Usage, agg.FinishReason, provRespID, reqLogID,
			service.ConversationProvenance{KeyID: session.ProviderKeyID(), Transport: session.UpstreamTransport()})
	}

	// 下发 prism-debug 事件,前端据 request_log_id 拉完整调试详情
	if reqLogID > 0 {
		debugPayload, _ := json.Marshal(gin.H{"request_log_id": reqLogID})
		fmt.Fprintf(c.Writer, "event: prism-debug\ndata: %s\n\n", debugPayload)
		c.Writer.Flush()
	}
}

// playgroundNonStream 非流式:补全 + 存会话 + 返回(带 debug 供前端展示)。
func playgroundNonStream(c *gin.Context, req *service.CompletionRequest, cc *service.ConversationContext, newMessages []chat.ChatMessage) {
	chatResp, err := chatPipeline.Complete(c.Request.Context(), req)
	if err != nil {
		respondPlaygroundChatError(c, err)
		return
	}

	if len(chatResp.Choices) > 0 {
		convID := service.SaveConversationTurn(cc, req.UserID, req.TokenID, req.Model,
			newMessages, chatResp.Choices[0].Message, chatResp.Usage,
			chatResp.Choices[0].FinishReason, chatResp.ProviderResponseID, chatResp.RequestLogID,
			service.ConversationProvenance{KeyID: chatResp.ProviderKeyID, Transport: chatResp.UpstreamTransport})
		if convID > 0 {
			chatResp.ConversationID = fmt.Sprint(convID)
		}
	}

	c.JSON(http.StatusOK, chatResp)
}

func respondPlaygroundChatError(c *gin.Context, err error) {
	if errors.Is(err, service.ErrInsufficientTokenBalance) ||
		errors.Is(err, service.ErrInsufficientUserBalance) {
		resp.BadRequest(c, perrors.WithMessage(perrors.ErrInsufficientQuota, err.Error()))
		return
	}
	resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
}

package console

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/gateway/pipeline"
	gwstream "github.com/mirainya/Prism/internal/gateway/stream"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider/chat"
	"github.com/mirainya/Prism/internal/service"
	perrors "github.com/mirainya/Prism/pkg/errors"
	"github.com/mirainya/Prism/pkg/logger"
	"go.uber.org/zap"
)

type playgroundPayloadStreamWriter struct {
	writer   gwstream.Writer
	capture  io.Writer
	writeErr error
}

func (w *playgroundPayloadStreamWriter) Write(data []byte) (int, error) {
	written, err := w.writer.Write(data)
	if err != nil {
		w.writeErr = err
	} else if written != len(data) {
		w.writeErr = io.ErrShortWrite
	}
	if written > 0 && w.capture != nil {
		captured := written
		if captured > len(data) {
			captured = len(data)
		}
		_, _ = w.capture.Write(data[:captured])
	}
	return written, err
}

func (w *playgroundPayloadStreamWriter) Flush() { w.writer.Flush() }

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
	cc, loadErr := service.LoadConversationContextStrict(req.ConversationID, token.ID, req.Model)
	if loadErr != nil {
		if errors.Is(loadErr, service.ErrConversationNotFound) {
			resp.ErrorMsg(c, http.StatusNotFound, 404, loadErr.Error())
			return
		}
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, "failed to load conversation history")
		return
	}
	fullMessages := append(append([]chat.ChatMessage{}, cc.History...), newMessages...)
	callID := "call_" + uuid.NewString()
	c.Header("X-Prism-Call-ID", callID)
	downstreamRequest, _ := json.Marshal(req)

	completionReq := &service.CompletionRequest{
		UserID:             token.UserID,
		TokenID:            token.ID,
		CallID:             callID,
		RequestID:          middleware.GetRequestID(c.Request.Context()),
		DownstreamEndpoint: c.FullPath(),
		DownstreamRequest:  downstreamRequest,
		Model:              req.Model,
		Messages:           fullMessages,
		Temperature:        req.Temperature,
		MaxTokens:          req.MaxTokens,
		TopP:               req.TopP,
		FrequencyPenalty:   req.FrequencyPenalty,
		PresencePenalty:    req.PresencePenalty,
		Stop:               req.Stop,
		Stream:             stream,
		StreamSpecified:    req.Stream != nil,
		Tools:              req.Tools,
		ToolChoice:         req.ToolChoice,
		ResponseFormat:     req.ResponseFormat,
		Seed:               req.Seed,
		User:               req.User,
		ReasoningEffort:    req.ReasoningEffort,
	}
	if level := strings.TrimSpace(c.GetHeader(pipeline.ThinkingLevelHeader)); level != "" {
		completionReq.ThinkingLevel = &level
	}
	if cc.Conv != nil {
		completionReq.ConversationRecordID = cc.Conv.ID
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
		recordPlaygroundFailedTurn(cc, req, newMessages, nil, 0, model.ConversationTurnFailed, err)
		respondPlaygroundChatError(c, err)
		return
	}
	defer session.Cleanup()

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)

	capture := service.NewAPICallService().NewPayloadCaptureBestEffort(
		session.CallID(), session.AttemptID(), model.APICallPayloadResponse, "text/event-stream",
	)
	defer capture.SaveBestEffort()
	writer := &playgroundPayloadStreamWriter{writer: gwstream.Writer(c.Writer), capture: capture}
	agg, streamErr := gwstream.ProxyStream(writer, session.UpstreamResp.Body)
	clientDisconnected := writer.writeErr != nil || c.Request.Context().Err() != nil
	provRespID, finalizeErr := session.FinalizeStreamDelivery(streamErr, clientDisconnected)
	if finalizeErr != nil {
		logger.Error("finalize playground stream call",
			zap.String("call_id", session.CallID()), zap.Error(finalizeErr))
	}
	reqLogID := session.RequestLogID()
	if streamErr != nil {
		var assistant *chat.ChatMessage
		if agg != nil {
			assistant = &chat.ChatMessage{
				Role: model.RoleAssistant, Content: agg.AssistantContent,
				ReasoningContent: agg.ReasoningContent,
			}
		}
		status := model.ConversationTurnFailed
		if clientDisconnected {
			status = model.ConversationTurnAborted
		}
		recordPlaygroundFailedTurn(cc, req, newMessages, assistant, reqLogID, status, streamErr)
		return
	}

	if agg != nil {
		assistant := chat.ChatMessage{
			Role:             "assistant",
			Content:          agg.AssistantContent,
			ReasoningContent: agg.ReasoningContent,
		}
		_, saveErr := service.SaveConversationTurn(cc, req.UserID, req.TokenID, req.Model,
			newMessages, assistant, agg.Usage, agg.FinishReason, provRespID, session.CallID(), reqLogID,
			service.ConversationProvenance{KeyID: session.ProviderKeyID(), Transport: session.UpstreamTransport()})
		if saveErr != nil {
			logger.Error("save playground stream conversation",
				zap.String("call_id", session.CallID()), zap.Error(saveErr))
		}
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
		recordPlaygroundFailedTurn(cc, req, newMessages, nil, 0, model.ConversationTurnFailed, err)
		respondPlaygroundChatError(c, err)
		return
	}

	if len(chatResp.Choices) > 0 {
		convID, saveErr := service.SaveConversationTurn(cc, req.UserID, req.TokenID, req.Model,
			newMessages, chatResp.Choices[0].Message, chatResp.Usage,
			chatResp.Choices[0].FinishReason, chatResp.ProviderResponseID, chatResp.CallID, chatResp.RequestLogID,
			service.ConversationProvenance{KeyID: chatResp.ProviderKeyID, Transport: chatResp.UpstreamTransport})
		if saveErr != nil {
			logger.Error("save playground conversation",
				zap.String("call_id", chatResp.CallID), zap.Error(saveErr))
		}
		if convID > 0 {
			chatResp.ConversationID = fmt.Sprint(convID)
		}
	}

	c.JSON(http.StatusOK, chatResp)
}

func recordPlaygroundFailedTurn(
	cc *service.ConversationContext,
	req *service.CompletionRequest,
	newMessages []chat.ChatMessage,
	assistant *chat.ChatMessage,
	requestLogID uint,
	status model.ConversationTurnStatus,
	cause error,
) {
	if req == nil || req.CallID == "" {
		return
	}
	errorMessage := ""
	if cause != nil {
		errorMessage = service.SanitizeAPICallErrorMessage(cause.Error())
	}
	_, err := service.RecordConversationTurnFailure(cc, service.ConversationTurnRecord{
		UserID: req.UserID, TokenID: req.TokenID, Model: req.Model,
		NewMessages: newMessages, Assistant: assistant, Status: status,
		CallID: req.CallID, RequestLogID: requestLogID,
		ErrorType: "playground_error", ErrorCode: "playground_call_failed", ErrorMessage: errorMessage,
	})
	if err != nil {
		logger.Error("save failed playground conversation turn",
			zap.String("call_id", req.CallID), zap.Error(err))
	}
}

func respondPlaygroundChatError(c *gin.Context, err error) {
	if errors.Is(err, service.ErrInsufficientTokenBalance) ||
		errors.Is(err, service.ErrInsufficientUserBalance) {
		resp.BadRequest(c, perrors.WithMessage(perrors.ErrInsufficientQuota, err.Error()))
		return
	}
	resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
}

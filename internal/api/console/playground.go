package console

import (
	"bufio"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider/chat"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/pkg/errors"
)

var chatService = service.NewChatService()
var capabilityService = service.NewCapabilityService()
var queryService = service.NewQueryService()
var playgroundDashboardService = service.NewDashboardService()

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
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"object": "list",
		"data":   data,
	})
}

// PlaygroundChatCompletions POST /api/playground/:token_id/chat/completions
func PlaygroundChatCompletions(c *gin.Context) {
	token, ok := getPlaygroundToken(c)
	if !ok {
		return
	}

	var req struct {
		Model            string                `json:"model" binding:"required"`
		Messages         []chat.ChatMessage    `json:"messages" binding:"required,min=1"`
		Temperature      float64               `json:"temperature"`
		MaxTokens        int                   `json:"max_tokens"`
		TopP             float64               `json:"top_p"`
		FrequencyPenalty float64               `json:"frequency_penalty"`
		PresencePenalty  float64               `json:"presence_penalty"`
		Stop             []string              `json:"stop"`
		Stream           *bool                 `json:"stream"`
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

	streamValue := false
	streamSpecified := false
	if req.Stream != nil {
		streamValue = *req.Stream
		streamSpecified = true
	}

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
		Stream:           streamValue,
		StreamSpecified:  streamSpecified,
		Tools:            req.Tools,
		ToolChoice:       req.ToolChoice,
		ResponseFormat:   req.ResponseFormat,
		Seed:             req.Seed,
		User:             req.User,
		ConversationID:   req.ConversationID,
	}

	if completionReq.StreamSpecified && completionReq.Stream {
		session, err := chatService.StreamComplete(c.Request.Context(), completionReq)
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

		aggregation, streamErr := proxyPlaygroundStream(c, session.UpstreamResp)
		debugDetail, finalizeErr := chatService.FinalizeStream(session, aggregation, streamErr)
		if finalizeErr != nil {
			resp.ErrorMsg(c, http.StatusInternalServerError, 500, finalizeErr.Error())
			return
		}
		if debugDetail != nil {
			c.Writer.Write([]byte(fmt.Sprintf("event: prism-debug\ndata: %s\n\n", mustJSON(debugDetail))))
			c.Writer.Flush()
		}
		if streamErr != nil {
			return
		}
		return
	}

	chatResp, err := chatService.Complete(c.Request.Context(), completionReq)
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}

	c.JSON(http.StatusOK, chatResp)
}

// PlaygroundInvokeCapability POST /api/playground/:token_id/capabilities/:capability
func PlaygroundInvokeCapability(c *gin.Context) {
	token, ok := getPlaygroundToken(c)
	if !ok {
		return
	}

	capability := c.Param("capability")
	if capability == "" {
		resp.ErrorMsg(c, http.StatusBadRequest, 400, "capability is required")
		return
	}

	var params map[string]any
	if err := c.ShouldBindJSON(&params); err != nil {
		resp.ErrorMsg(c, http.StatusBadRequest, 400, "invalid request body")
		return
	}

	channel, _ := params["channel"].(string)
	modelName, _ := params["model"].(string)
	callbackURL, _ := params["callback_url"].(string)
	delete(params, "channel")
	delete(params, "model")
	delete(params, "callback_url")

	req := &service.InvokeRequest{
		UserID:      token.UserID,
		TokenID:     token.ID,
		Capability:  capability,
		Channel:     channel,
		Model:       modelName,
		CallbackURL: callbackURL,
		Params:      params,
	}

	invokeResp, err := capabilityService.Invoke(c.Request.Context(), req)
	if err != nil {
		if stderrors.Is(err, service.ErrInsufficientTokenBalance) || stderrors.Is(err, service.ErrInsufficientUserBalance) {
			resp.BadRequest(c, errors.WithMessage(errors.ErrInsufficientQuota, err.Error()))
			return
		}
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}

	resp.Success(c, invokeResp)
}

// PlaygroundListTasks GET /api/playground/:token_id/tasks
func PlaygroundListTasks(c *gin.Context) {
	token, ok := getPlaygroundToken(c)
	if !ok {
		return
	}

	var req service.ListTasksRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		resp.ErrorMsg(c, http.StatusBadRequest, 400, "invalid query params")
		return
	}
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.PageSize <= 0 {
		req.PageSize = 20
	}
	if req.PageSize > 100 {
		req.PageSize = 100
	}
	if req.Keyword == "" {
		req.Keyword = strings.TrimSpace(c.Query("keyword"))
	}
	if req.Status == "" {
		req.Status = strings.TrimSpace(c.Query("status"))
	}
	if req.Capability == "" {
		req.Capability = strings.TrimSpace(c.Query("capability"))
	}
	req.TokenID = token.ID

	result, err := playgroundDashboardService.ListTasks(&req, token.UserID, false)
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, "failed to list tasks")
		return
	}

	resp.Success(c, gin.H{
		"items":     result.Items,
		"total":     result.Total,
		"page":      result.Page,
		"page_size": result.PageSize,
	})
}

// PlaygroundGetTask GET /api/playground/:token_id/tasks/:task_no
func PlaygroundGetTask(c *gin.Context) {
	token, ok := getPlaygroundToken(c)
	if !ok {
		return
	}

	taskNo := c.Param("task_no")
	task, err := capabilityService.GetTask(c.Request.Context(), taskNo, token.UserID, token.ID)
	if err != nil {
		resp.ErrorMsg(c, http.StatusNotFound, 404, "task not found")
		return
	}

	var rawParams any
	if len(task.RequestParams) > 0 {
		_ = json.Unmarshal(task.RequestParams, &rawParams)
	}

	var mappedParams any
	if len(task.MappedParams) > 0 {
		_ = json.Unmarshal(task.MappedParams, &mappedParams)
	}

	var vendorResponse any
	if len(task.VendorResponse) > 0 {
		_ = json.Unmarshal(task.VendorResponse, &vendorResponse)
	}

	var result any
	if len(task.Result) > 0 {
		_ = json.Unmarshal(task.Result, &result)
	}

	detail := gin.H{
		"task_id":         task.TaskNo,
		"task_no":         task.TaskNo,
		"status":          task.Status,
		"progress":        task.Progress,
		"result":          result,
		"error":           task.ErrorMessage,
		"cost":            task.Cost,
		"raw_params":      rawParams,
		"mapped_params":   mappedParams,
		"vendor_response": vendorResponse,
		"vendor_task_id":  task.VendorTaskID,
		"created_at":      task.CreatedAt,
	}

	if task.StartedAt != nil {
		detail["started_at"] = task.StartedAt
	}
	if task.CompletedAt != nil {
		detail["completed_at"] = task.CompletedAt
	}

	resp.Success(c, detail)
}

// getPlaygroundToken 校验 token 归属当前用户
func PlaygroundListConversations(c *gin.Context) {
	token, ok := getPlaygroundToken(c)
	if !ok {
		return
	}

	var req service.ListConversationsRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, err.Error()))
		return
	}
	req.UserID = token.UserID
	req.TokenID = token.ID

	listResp, err := conversationService.ListConversations(&req)
	if err != nil {
		resp.InternalError(c, errors.ErrInternalError)
		return
	}

	items := make([]gin.H, len(listResp.Items))
	for i, conv := range listResp.Items {
		items[i] = gin.H{
			"id":                  conv.ID,
			"user_id":             conv.UserID,
			"token_id":            conv.TokenID,
			"title":               conv.Title,
			"model":               conv.Model,
			"system_prompt":       conv.SystemPrompt,
			"last_request_log_id": conv.LastRequestLogID,
			"last_status":         conv.LastStatus,
			"total_tokens":        conv.TotalTokens,
			"message_count":       conv.MessageCount,
			"total_cost":          conv.TotalCost,
			"status":              conv.Status,
			"created_at":          conv.CreatedAt,
			"updated_at":          conv.UpdatedAt,
		}
	}

	resp.Success(c, gin.H{
		"items":     items,
		"total":     listResp.Total,
		"page":      listResp.Page,
		"page_size": listResp.PageSize,
	})
}

func PlaygroundGetConversationMessages(c *gin.Context) {
	token, ok := getPlaygroundToken(c)
	if !ok {
		return
	}

	conversationID, err := resp.ParseUintParam(c, "conversation_id")
	if err != nil {
		return
	}
	conversation, err := conversationService.GetConversation(conversationID)
	if err != nil {
		resp.NotFound(c, errors.ErrTaskNotFound)
		return
	}
	if conversation.UserID != token.UserID || conversation.TokenID != token.ID {
		resp.Forbidden(c, errors.ErrNoPermission)
		return
	}

	page := 1
	pageSize := 100
	_, _ = fmt.Sscanf(c.DefaultQuery("page", "1"), "%d", &page)
	_, _ = fmt.Sscanf(c.DefaultQuery("page_size", "100"), "%d", &pageSize)

	msgResp, err := conversationService.ListMessages(conversationID, page, pageSize)
	if err != nil {
		resp.InternalError(c, errors.ErrInternalError)
		return
	}

	items := make([]gin.H, len(msgResp.Items))
	for i, msg := range msgResp.Items {
		items[i] = gin.H{
			"id":                msg.ID,
			"conversation_id":   msg.ConversationID,
			"request_log_id":    msg.RequestLogID,
			"role":              msg.Role,
			"content":           msg.Content,
			"reasoning_content": msg.ReasoningContent,
			"finish_reason":     msg.FinishReason,
			"input_tokens":      msg.InputTokens,
			"output_tokens":     msg.OutputTokens,
			"model":             msg.Model,
			"channel_id":        msg.ChannelID,
			"account_id":        msg.AccountID,
			"latency_ms":        msg.LatencyMs,
			"cost":              msg.Cost,
			"created_at":        msg.CreatedAt,
		}
	}

	resp.Success(c, gin.H{
		"items":     items,
		"total":     msgResp.Total,
		"page":      msgResp.Page,
		"page_size": msgResp.PageSize,
		"conversation": gin.H{
			"id":                  msgResp.Conversation.ID,
			"user_id":             msgResp.Conversation.UserID,
			"token_id":            msgResp.Conversation.TokenID,
			"title":               msgResp.Conversation.Title,
			"model":               msgResp.Conversation.Model,
			"system_prompt":       msgResp.Conversation.SystemPrompt,
			"last_request_log_id": msgResp.Conversation.LastRequestLogID,
			"last_status":         msgResp.Conversation.LastStatus,
			"total_tokens":        msgResp.Conversation.TotalTokens,
			"message_count":       msgResp.Conversation.MessageCount,
			"status":              msgResp.Conversation.Status,
			"created_at":          msgResp.Conversation.CreatedAt,
			"updated_at":          msgResp.Conversation.UpdatedAt,
		},
	})
}

func PlaygroundGetDebug(c *gin.Context) {
	token, ok := getPlaygroundToken(c)
	if !ok {
		return
	}

	requestLogID, err := resp.ParseUintParam(c, "request_log_id")
	if err != nil {
		return
	}
	logDetail, err := service.NewRequestLogService().GetRequestLog(requestLogID)
	if err != nil {
		resp.NotFound(c, errors.ErrTaskNotFound)
		return
	}
	if logDetail.ConversationID > 0 {
		conversation, convErr := conversationService.GetConversation(logDetail.ConversationID)
		if convErr != nil || conversation.UserID != token.UserID || conversation.TokenID != token.ID {
			resp.Forbidden(c, errors.ErrNoPermission)
			return
		}
	}

	var requestBody any
	if logDetail.RequestBody != "" {
		_ = jsonUnmarshalString(logDetail.RequestBody, &requestBody)
	}
	var requestHeaders any
	if logDetail.RequestHeaders != "" {
		_ = jsonUnmarshalString(logDetail.RequestHeaders, &requestHeaders)
	}
	var responseBody any
	if logDetail.ResponseBody != "" {
		if err := jsonUnmarshalString(logDetail.ResponseBody, &responseBody); err != nil {
			responseBody = logDetail.ResponseBody
		}
	}

	resp.Success(c, gin.H{
		"request_log_id":  logDetail.ID,
		"conversation_id": logDetail.ConversationID,
		"channel_id":      logDetail.ChannelID,
		"account_id":      logDetail.AccountID,
		"channel_name": func() string {
			if logDetail.Channel != nil {
				return logDetail.Channel.Name
			}
			return ""
		}(),
		"channel_type": func() string {
			if logDetail.Channel != nil {
				return logDetail.Channel.Type
			}
			return ""
		}(),
		"model_code":              logDetail.ModelCode,
		"vendor_model":            logDetail.VendorModel,
		"request_path":            logDetail.RequestPath,
		"is_stream":               logDetail.IsStream,
		"status_code":             logDetail.StatusCode,
		"duration_ms":             logDetail.DurationMs,
		"error_message":           logDetail.ErrorMessage,
		"finish_reason":           logDetail.FinishReason,
		"response_preview":        logDetail.ResponsePreview,
		"usage_prompt_tokens":     logDetail.UsagePromptTokens,
		"usage_completion_tokens": logDetail.UsageCompletionTokens,
		"usage_total_tokens":      logDetail.UsageTotalTokens,
		"request_headers":         requestHeaders,
		"request_body":            requestBody,
		"response_body":           responseBody,
		"request_at":              logDetail.RequestAt,
	})
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

func proxyPlaygroundStream(c *gin.Context, upstreamResp *http.Response) (*service.StreamAggregationResult, error) {
	aggregation := &service.StreamAggregationResult{}
	reader := bufio.NewReader(upstreamResp.Body)
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			_, _ = c.Writer.Write([]byte(line))
			c.Writer.Flush()
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "data: ") {
				payload := strings.TrimPrefix(trimmed, "data: ")
				if payload != "[DONE]" {
					mergeSSEChunk(aggregation, payload)
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				return aggregation, nil
			}
			aggregation.ErrorMessage = err.Error()
			return aggregation, err
		}
	}
}

func mergeSSEChunk(aggregation *service.StreamAggregationResult, payload string) {
	var parsed struct {
		Choices []struct {
			Delta struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"delta"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage *chat.ChatUsage `json:"usage"`
		Error any             `json:"error"`
	}
	if err := jsonUnmarshalString(payload, &parsed); err != nil {
		return
	}
	if len(parsed.Choices) > 0 {
		choice := parsed.Choices[0]
		aggregation.AssistantContent += choice.Delta.Content
		aggregation.ReasoningContent += choice.Delta.ReasoningContent
		if choice.FinishReason != "" {
			aggregation.FinishReason = choice.FinishReason
		}
	}
	if parsed.Usage != nil {
		aggregation.Usage = parsed.Usage
	}
	if parsed.Error != nil {
		aggregation.ErrorMessage = fmt.Sprint(parsed.Error)
	}
	aggregation.ResponsePreview = truncateForStreamPreview(aggregation.AssistantContent)
	aggregation.ResponseBody = payload
}

func jsonUnmarshalString(value string, target any) error {
	return json.Unmarshal([]byte(value), target)
}

func mustJSON(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func truncateForStreamPreview(value string) string {
	runes := []rune(value)
	if len(runes) <= 500 {
		return value
	}
	return string(runes[:500]) + "..."
}

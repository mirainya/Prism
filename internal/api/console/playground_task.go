package console

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/pkg/errors"
)

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
	task, err := capabilityService.GetTask(c.Request.Context(), taskNo, token.UserID)
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

// PlaygroundCancelTask POST /api/playground/:token_id/tasks/:task_no/cancel
func PlaygroundCancelTask(c *gin.Context) {
	token, ok := getPlaygroundToken(c)
	if !ok {
		return
	}

	taskNo := c.Param("task_no")
	if err := capabilityService.CancelTask(c.Request.Context(), taskNo, token.UserID); err != nil {
		resp.ErrorMsg(c, http.StatusBadRequest, 400, err.Error())
		return
	}

	resp.Success(c, gin.H{"message": "task cancelled"})
}

// PlaygroundListConversations GET /api/playground/:token_id/conversations
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

// PlaygroundGetConversationMessages GET /api/playground/:token_id/conversations/:conversation_id/messages
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

// PlaygroundGetDebug GET /api/playground/:token_id/debug/:request_log_id
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
		_ = json.Unmarshal([]byte(logDetail.RequestBody), &requestBody)
	}
	var requestHeaders any
	if logDetail.RequestHeaders != "" {
		_ = json.Unmarshal([]byte(logDetail.RequestHeaders), &requestHeaders)
	}
	var responseBody any
	if logDetail.ResponseBody != "" {
		if err := json.Unmarshal([]byte(logDetail.ResponseBody), &responseBody); err != nil {
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

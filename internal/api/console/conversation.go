package console

import (
	"fmt"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/pkg/errors"
)

var conversationService = service.NewConversationService()

// ListConversations 获取对话列表
func ListConversations(c *gin.Context) {
	var req service.ListConversationsRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, err.Error()))
		return
	}

	// 普通用户强制过滤自己的数据
	userID := middleware.GetUserID(c)
	userRole := middleware.GetUserRole(c)
	if userRole != string(model.UserRoleAdmin) {
		req.UserID = userID
	}

	listResp, err := conversationService.ListConversations(&req)
	if err != nil {
		resp.InternalError(c, errors.ErrInternalError)
		return
	}

	// 转换为前端友好的格式
	items := make([]gin.H, len(listResp.Items))
	for i, conv := range listResp.Items {
		item := gin.H{
			"id":            conv.ID,
			"title":         conv.Title,
			"model":         conv.Model,
			"system_prompt": conv.SystemPrompt,
			"last_call_id":  conv.CallID,
			"total_tokens":  conv.TotalTokens,
			"message_count": conv.MessageCount,
			"total_cost":    conv.TotalCost,
			"last_status":   conv.LastStatus,
			"status":        conv.Status,
			"created_at":    conv.CreatedAt,
			"updated_at":    conv.UpdatedAt,
		}
		if userRole == string(model.UserRoleAdmin) {
			item["user_id"] = conv.UserID
			item["token_id"] = conv.TokenID
			item["last_request_log_id"] = conv.LastRequestLogID
		}
		items[i] = item
	}

	resp.Success(c, gin.H{
		"items":     items,
		"total":     listResp.Total,
		"page":      listResp.Page,
		"page_size": listResp.PageSize,
	})
}

// GetConversationMessages 获取对话的消息列表
func GetConversationMessages(c *gin.Context) {
	idStr := c.Param("id")
	var id uint
	if _, err := fmt.Sscanf(idStr, "%d", &id); err != nil {
		resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, "invalid conversation id"))
		return
	}

	// 验证权限：检查对话是否属于当前用户
	userID := middleware.GetUserID(c)
	userRole := middleware.GetUserRole(c)

	conversation, err := conversationService.GetConversation(id)
	if err != nil {
		resp.NotFound(c, errors.WithMessage(errors.ErrTaskNotFound, "conversation not found"))
		return
	}

	// 非管理员只能查看自己的对话
	if userRole != string(model.UserRoleAdmin) && conversation.UserID != userID {
		resp.Forbidden(c, errors.WithMessage(errors.ErrNoPermission, "no permission to access this conversation"))
		return
	}

	// 获取分页参数
	var page, pageSize int
	fmt.Sscanf(c.DefaultQuery("page", "1"), "%d", &page)
	fmt.Sscanf(c.DefaultQuery("page_size", "50"), "%d", &pageSize)

	msgResp, err := conversationService.ListMessages(id, page, pageSize)
	if err != nil {
		resp.InternalError(c, errors.ErrInternalError)
		return
	}
	callStatuses, err := conversationCallStatuses(msgResp.Items)
	if err != nil {
		resp.InternalError(c, errors.ErrInternalError)
		return
	}

	// 转换消息为前端友好格式
	items := make([]gin.H, len(msgResp.Items))
	for i, msg := range msgResp.Items {
		items[i] = gin.H{
			"id":                msg.ID,
			"conversation_id":   msg.ConversationID,
			"call_id":           msg.CallID,
			"call_status":       callStatuses[msg.CallID],
			"role":              msg.Role,
			"content":           msg.Content,
			"attachments":       msg.Attachments,
			"reasoning_content": msg.ReasoningContent,
			"finish_reason":     msg.FinishReason,
			"input_tokens":      msg.InputTokens,
			"output_tokens":     msg.OutputTokens,
			"model":             msg.Model,
			"latency_ms":        msg.LatencyMs,
			"cost":              msg.Cost,
			"created_at":        msg.CreatedAt,
		}
		if userRole == string(model.UserRoleAdmin) {
			items[i]["request_log_id"] = msg.RequestLogID
			items[i]["channel_id"] = msg.ChannelID
			items[i]["account_id"] = msg.AccountID
		}
	}

	// 对话信息
	convInfo := gin.H{
		"id":            msgResp.Conversation.ID,
		"title":         msgResp.Conversation.Title,
		"model":         msgResp.Conversation.Model,
		"system_prompt": msgResp.Conversation.SystemPrompt,
		"last_call_id":  msgResp.Conversation.CallID,
		"last_status":   msgResp.Conversation.LastStatus,
		"total_tokens":  msgResp.Conversation.TotalTokens,
		"message_count": msgResp.Conversation.MessageCount,
		"status":        msgResp.Conversation.Status,
		"created_at":    msgResp.Conversation.CreatedAt,
		"updated_at":    msgResp.Conversation.UpdatedAt,
	}
	if userRole == string(model.UserRoleAdmin) {
		convInfo["user_id"] = msgResp.Conversation.UserID
		convInfo["token_id"] = msgResp.Conversation.TokenID
	}

	resp.Success(c, gin.H{
		"items":        items,
		"total":        msgResp.Total,
		"page":         msgResp.Page,
		"page_size":    msgResp.PageSize,
		"conversation": convInfo,
	})
}

// GetConversationTurns returns canonical conversation turns independently
// from the legacy message projection so both datasets have stable pagination.
func GetConversationTurns(c *gin.Context) {
	var id uint
	if _, err := fmt.Sscanf(c.Param("id"), "%d", &id); err != nil {
		resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, "invalid conversation id"))
		return
	}
	conversation, err := conversationService.GetConversation(id)
	if err != nil {
		resp.NotFound(c, errors.WithMessage(errors.ErrTaskNotFound, "conversation not found"))
		return
	}
	userRole := middleware.GetUserRole(c)
	if userRole != string(model.UserRoleAdmin) && conversation.UserID != middleware.GetUserID(c) {
		resp.Forbidden(c, errors.WithMessage(errors.ErrNoPermission, "no permission to access this conversation"))
		return
	}
	var page, pageSize int
	fmt.Sscanf(c.DefaultQuery("page", "1"), "%d", &page)
	fmt.Sscanf(c.DefaultQuery("page_size", "50"), "%d", &pageSize)
	result, err := conversationService.ListTurns(id, page, pageSize)
	if err != nil {
		resp.InternalError(c, errors.ErrInternalError)
		return
	}
	resp.Success(c, gin.H{
		"items":     conversationTurnResponses(result.Items, userRole == string(model.UserRoleAdmin)),
		"total":     result.Total,
		"page":      result.Page,
		"page_size": result.PageSize,
	})
}

func conversationTurnResponses(turns []service.ConversationTurnItem, includeInternal bool) []gin.H {
	result := make([]gin.H, len(turns))
	for i, turn := range turns {
		items := make([]gin.H, len(turn.Items))
		for j, item := range turn.Items {
			items[j] = gin.H{
				"id":        strconv.FormatUint(item.ID, 10),
				"direction": item.Direction,
				"ordinal":   item.Ordinal,
				"canonical": item.CanonicalJSON,
			}
		}
		response := gin.H{
			"id":              strconv.FormatUint(turn.ID, 10),
			"conversation_id": turn.ConversationID,
			"sequence":        strconv.FormatUint(turn.Sequence, 10),
			"call_id":         turn.CallID,
			"model":           turn.Model,
			"status":          turn.Status,
			"context_mode":    turn.ContextMode,
			"input_tokens":    turn.InputTokens,
			"output_tokens":   turn.OutputTokens,
			"total_tokens":    turn.TotalTokens,
			"cost":            turn.Cost,
			"latency_ms":      turn.LatencyMs,
			"finish_reason":   turn.FinishReason,
			"error_type":      turn.ErrorType,
			"error_code":      turn.ErrorCode,
			"error_message":   turn.ErrorMessage,
			"created_at":      turn.CreatedAt,
			"items":           items,
		}
		if includeInternal {
			response["request_log_id"] = turn.RequestLogID
			response["provider_response_id"] = turn.ProviderResponseID
		}
		result[i] = response
	}
	return result
}

func conversationCallStatuses(messages []model.Message) (map[string]model.APICallStatus, error) {
	type callStatusRow struct {
		ID     string              `gorm:"column:id"`
		Status model.APICallStatus `gorm:"column:status"`
	}
	callIDs := make([]string, 0, len(messages))
	seen := make(map[string]struct{}, len(messages))
	for _, message := range messages {
		if message.CallID == "" {
			continue
		}
		if _, exists := seen[message.CallID]; exists {
			continue
		}
		seen[message.CallID] = struct{}{}
		callIDs = append(callIDs, message.CallID)
	}
	statuses := make(map[string]model.APICallStatus, len(callIDs))
	if len(callIDs) == 0 {
		return statuses, nil
	}
	var rows []callStatusRow
	if err := model.DB().Model(&model.APICall{}).
		Select("id", "status").Where("id IN ?", callIDs).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		statuses[row.ID] = row.Status
	}
	return statuses, nil
}

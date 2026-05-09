package console

import (
	"fmt"

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
		items[i] = gin.H{
			"id":            conv.ID,
			"user_id":       conv.UserID,
			"token_id":      conv.TokenID,
			"title":         conv.Title,
			"model":         conv.Model,
			"system_prompt": conv.SystemPrompt,
			"total_tokens":  conv.TotalTokens,
			"message_count": conv.MessageCount,
			"total_cost":    conv.TotalCost,
			"status":        conv.Status,
			"created_at":    conv.CreatedAt,
			"updated_at":    conv.UpdatedAt,
		}
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
		resp.NotFound(c, errors.ErrTaskNotFound)
		return
	}

	// 非管理员只能查看自己的对话
	if userRole != string(model.UserRoleAdmin) && conversation.UserID != userID {
		resp.Forbidden(c, errors.ErrNoPermission)
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

	// 转换消息为前端友好格式
	items := make([]gin.H, len(msgResp.Items))
	for i, msg := range msgResp.Items {
		items[i] = gin.H{
			"id":              msg.ID,
			"conversation_id": msg.ConversationID,
			"role":            msg.Role,
			"content":         msg.Content,
			"input_tokens":    msg.InputTokens,
			"output_tokens":   msg.OutputTokens,
			"model":           msg.Model,
			"latency_ms":      msg.LatencyMs,
			"cost":            msg.Cost,
			"created_at":      msg.CreatedAt,
		}
	}

	// 对话信息
	convInfo := gin.H{
		"id":            msgResp.Conversation.ID,
		"user_id":       msgResp.Conversation.UserID,
		"token_id":      msgResp.Conversation.TokenID,
		"title":         msgResp.Conversation.Title,
		"model":         msgResp.Conversation.Model,
		"system_prompt": msgResp.Conversation.SystemPrompt,
		"total_tokens":  msgResp.Conversation.TotalTokens,
		"message_count": msgResp.Conversation.MessageCount,
		"status":        msgResp.Conversation.Status,
		"created_at":    msgResp.Conversation.CreatedAt,
		"updated_at":    msgResp.Conversation.UpdatedAt,
	}

	resp.Success(c, gin.H{
		"items":        items,
		"total":        msgResp.Total,
		"page":         msgResp.Page,
		"page_size":    msgResp.PageSize,
		"conversation": convInfo,
	})
}

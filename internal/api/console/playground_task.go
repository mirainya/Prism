package console

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/gateway/payloadview"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/pkg/errors"
)

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
	if err != nil || conversation.UserID != token.UserID || conversation.TokenID != token.ID {
		resp.ErrorMsg(c, http.StatusNotFound, 404, "conversation not found")
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
	callStatuses, err := conversationCallStatuses(msgResp.Items)
	if err != nil {
		resp.InternalError(c, errors.ErrInternalError)
		return
	}

	items := make([]gin.H, len(msgResp.Items))
	for i, msg := range msgResp.Items {
		items[i] = gin.H{
			"id":                msg.ID,
			"conversation_id":   msg.ConversationID,
			"call_id":           msg.CallID,
			"call_status":       callStatuses[msg.CallID],
			"request_log_id":    msg.RequestLogID,
			"role":              msg.Role,
			"content":           msg.Content,
			"attachments":       msg.Attachments,
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
			"last_call_id":        msgResp.Conversation.CallID,
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

// PlaygroundGetConversationTurns returns the canonical turn history owned by
// the selected Playground token.
func PlaygroundGetConversationTurns(c *gin.Context) {
	token, ok := getPlaygroundToken(c)
	if !ok {
		return
	}
	var conversationID uint
	if _, err := fmt.Sscanf(c.Param("conversation_id"), "%d", &conversationID); err != nil {
		resp.ErrorMsg(c, http.StatusBadRequest, 400, "invalid conversation id")
		return
	}
	conversation, err := conversationService.GetConversation(conversationID)
	if err != nil || conversation.UserID != token.UserID || conversation.TokenID != token.ID {
		resp.ErrorMsg(c, http.StatusNotFound, 404, "conversation not found")
		return
	}
	var page, pageSize int
	fmt.Sscanf(c.DefaultQuery("page", "1"), "%d", &page)
	fmt.Sscanf(c.DefaultQuery("page_size", "50"), "%d", &pageSize)
	result, err := conversationService.ListTurns(conversationID, page, pageSize)
	if err != nil {
		resp.InternalError(c, errors.ErrInternalError)
		return
	}
	resp.Success(c, gin.H{
		"items":     conversationTurnResponses(result.Items, true),
		"total":     result.Total,
		"page":      result.Page,
		"page_size": result.PageSize,
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
	database, err := model.DB().DB()
	if err != nil {
		resp.InternalError(c, errors.ErrInternalError)
		return
	}
	type debugRow struct {
		RequestLogID, CallID, ChannelID               uint64
		CallPublicID, CallStatus, ModelCode           string
		ChannelName, ChannelType, VendorModel         string
		RequestPath, ErrorCode                        string
		HTTPStatus, DurationMS                        sql.NullInt64
		RequestBlobID, ResponseBlobID                 sql.NullInt64
		RequestComplete, ResponseComplete             bool
		RequestAt                                     time.Time
		ConversationID                                uint
		FinishReason, ProviderResponseID, ContextMode string
		InputTokens, OutputTokens, TotalTokens        int
		ErrorMessage                                  string
	}
	var detail debugRow
	err = model.DB().Raw(`SELECT
		request_log.id AS request_log_id,
		gateway_call.id AS call_id,
		gateway_call.public_id AS call_public_id,
		gateway_call.status AS call_status,
		gateway_model.model_code,
		product.channel_id,
		channel.display_name AS channel_name,
		channel_transport.protocol AS channel_type,
		product.vendor_model,
		channel_transport.request_path,
		request_log.http_status,
		request_log.duration_ms,
		request_log.error_code,
		request_log.request_payload_blob_id AS request_blob_id,
		request_log.response_payload_blob_id AS response_blob_id,
		request_log.request_bytes_complete AS request_complete,
		request_log.response_bytes_complete AS response_complete,
		request_log.created_at AS request_at,
		COALESCE(turn.conversation_id, 0) AS conversation_id,
		COALESCE(turn.finish_reason, '') AS finish_reason,
		COALESCE(turn.provider_response_id, '') AS provider_response_id,
		COALESCE(turn.context_mode, '') AS context_mode,
		COALESCE(turn.input_tokens, 0) AS input_tokens,
		COALESCE(turn.output_tokens, 0) AS output_tokens,
		COALESCE(turn.total_tokens, 0) AS total_tokens,
		COALESCE(turn.error_message, '') AS error_message
	FROM gw_channel_request_logs AS request_log
	JOIN gw_api_call_attempts AS attempt ON attempt.id = request_log.attempt_id
	JOIN gw_api_calls AS gateway_call ON gateway_call.id = attempt.call_id
	JOIN gw_model_operations AS model_operation
		ON model_operation.id = gateway_call.model_operation_id
		AND model_operation.release_id = gateway_call.catalog_release_id
	JOIN gw_catalog_models AS catalog_model
		ON catalog_model.id = model_operation.catalog_model_id
		AND catalog_model.release_id = model_operation.release_id
	JOIN gw_models AS gateway_model ON gateway_model.id = catalog_model.model_id
	JOIN gw_product_transports AS product_transport
		ON product_transport.id = attempt.product_transport_id
		AND product_transport.release_id = attempt.catalog_release_id
	JOIN gw_products AS product
		ON product.id = product_transport.product_id
		AND product.release_id = product_transport.release_id
	JOIN gw_channel_transports AS channel_transport
		ON channel_transport.id = product_transport.channel_transport_id
		AND channel_transport.release_id = product_transport.release_id
	JOIN gateway_channels AS channel ON channel.id = product.channel_id
	LEFT JOIN conversation_turns AS turn ON turn.call_id = gateway_call.public_id
	WHERE request_log.id = ? AND gateway_call.user_id = ? AND gateway_call.token_id = ?`,
		requestLogID, token.UserID, token.ID).Take(&detail).Error
	if err != nil {
		resp.NotFound(c, errors.ErrTaskNotFound)
		return
	}
	store, err := repository.New(database)
	if err != nil {
		resp.InternalError(c, errors.ErrInternalError)
		return
	}
	requestBody, responseBody := any(nil), any(nil)
	for _, payload := range []struct {
		kind string
		id   sql.NullInt64
		dest *any
	}{{"request", detail.RequestBlobID, &requestBody}, {"response", detail.ResponseBlobID, &responseBody}} {
		if !payload.id.Valid || payload.id.Int64 <= 0 {
			continue
		}
		plain, readErr := payloadview.ReadRequestLogPayload(
			c.Request.Context(), store, detail.RequestLogID, uint64(payload.id.Int64), payload.kind,
		)
		if readErr != nil {
			resp.InternalError(c, errors.ErrInternalError)
			return
		}
		*payload.dest = decodePlaygroundDebugPayload(plain)
		clear(plain)
	}
	status := detail.CallStatus
	if status == "retry_pending" {
		status = "in_progress"
	} else if status == "indeterminate" {
		status = "failed"
	}
	responsePreview := ""
	if encoded, encodeErr := json.Marshal(responseBody); encodeErr == nil && responseBody != nil {
		responsePreview = string(encoded)
		if len(responsePreview) > 1000 {
			responsePreview = responsePreview[:1000]
		}
	}
	errorMessage := detail.ErrorMessage
	if errorMessage == "" {
		errorMessage = detail.ErrorCode
	}

	resp.Success(c, gin.H{
		"request_log_id":          detail.RequestLogID,
		"conversation_id":         detail.ConversationID,
		"call_id":                 detail.CallPublicID,
		"channel_id":              detail.ChannelID,
		"account_id":              0,
		"channel_name":            detail.ChannelName,
		"channel_type":            detail.ChannelType,
		"model_code":              detail.ModelCode,
		"vendor_model":            detail.VendorModel,
		"request_path":            detail.RequestPath,
		"is_stream":               false,
		"status":                  status,
		"status_code":             nullablePlaygroundDebugInt(detail.HTTPStatus),
		"duration_ms":             nullablePlaygroundDebugInt(detail.DurationMS),
		"error_message":           errorMessage,
		"finish_reason":           detail.FinishReason,
		"response_preview":        responsePreview,
		"usage_prompt_tokens":     detail.InputTokens,
		"usage_completion_tokens": detail.OutputTokens,
		"usage_total_tokens":      detail.TotalTokens,
		"request_headers":         nil,
		"request_body":            requestBody,
		"response_body":           responseBody,
		"request_complete":        detail.RequestComplete,
		"response_complete":       detail.ResponseComplete,
		"request_at":              detail.RequestAt,
		"context_mode":            detail.ContextMode,
		"provider_response_id":    detail.ProviderResponseID,
	})
}

func decodePlaygroundDebugPayload(raw []byte) any {
	var value any
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	return string(raw)
}

func nullablePlaygroundDebugInt(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}

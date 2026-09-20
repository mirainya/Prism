package open

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/gateway/payloadview"
	gatewayruntime "github.com/mirainya/Prism/internal/gateway/runtime"
)

type imageTaskSummary struct {
	Model          string `json:"model"`
	Operation      string `json:"operation"`
	ResponseFormat string `json:"response_format"`
}

// GetImageTask returns the token-owned task projection and exposes the final
// OpenAI image response only after the task has completed.
func GetImageTask(c *gin.Context) {
	token := middleware.GetToken(c)
	if token == nil {
		resp.ErrorMsg(c, http.StatusUnauthorized, 401, "unauthorized")
		return
	}
	if unifiedVideoStore == nil {
		resp.ErrorMsg(c, http.StatusServiceUnavailable, 503, gatewayruntime.ErrNotReady.Error())
		return
	}

	var callID uint64
	var callStatus, taskStatus string
	var progress uint8
	var summaryRaw []byte
	var createdAt, updatedAt time.Time
	err := unifiedVideoStore.DB().QueryRowContext(c.Request.Context(), `SELECT c.id,c.status,t.status,t.progress,t.parameter_summary,t.created_at,t.updated_at
FROM gw_api_resources r
JOIN gw_api_calls c ON c.id=r.call_id AND c.user_id=r.user_id AND c.token_id=r.token_id
JOIN gw_capability_tasks t ON t.resource_id=r.id
WHERE r.public_id=? AND r.resource_kind='capability_task' AND r.user_id=? AND r.token_id=? AND r.deleted_at IS NULL`, c.Param("id"), token.UserID, token.ID).
		Scan(&callID, &callStatus, &taskStatus, &progress, &summaryRaw, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		resp.ErrorMsg(c, http.StatusNotFound, 404, "image task not found")
		return
	}
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, "image task lookup failed")
		return
	}

	var summary imageTaskSummary
	if json.Unmarshal(summaryRaw, &summary) != nil ||
		(summary.Operation != "images.generate" && summary.Operation != "images.edit") {
		resp.ErrorMsg(c, http.StatusNotFound, 404, "image task not found")
		return
	}
	responseFormat, ok := normalizeOpenAIImageResponseFormat(summary.ResponseFormat)
	if !ok {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, "image task result format is invalid")
		return
	}

	status := imageTaskPublicStatus(callStatus, taskStatus)
	result := gin.H{
		"id": c.Param("id"), "status": status, "progress": progress,
		"model": summary.Model, "operation": summary.Operation,
		"created_at": createdAt.UTC().Format(time.RFC3339),
		"updated_at": updatedAt.UTC().Format(time.RFC3339),
	}
	if status == "completed" {
		imageResult, readErr := readUnifiedOpenAIImageResponse(c.Request.Context(), callID, uint64(token.UserID), uint64(token.ID), responseFormat)
		if readErr != nil {
			resp.ErrorMsg(c, http.StatusInternalServerError, 500, "image task result is unavailable")
			return
		}
		result["result"] = imageResult
	}
	if status == "failed" {
		failure, readErr := payloadview.ReadLatestRequestFailure(c.Request.Context(), unifiedVideoStore, callID)
		if readErr != nil {
			resp.ErrorMsg(c, http.StatusInternalServerError, 500, "image task failure is unavailable")
			return
		}
		code := strings.TrimSpace(failure.Code)
		if code == "" {
			code = "image_task_failed"
		}
		message := strings.TrimSpace(failure.Message)
		if message == "" {
			message = "image task failed"
		}
		result["error"] = gin.H{"code": code, "message": message}
	}
	resp.Success(c, result)
}

func imageTaskPublicStatus(callStatus, taskStatus string) string {
	switch callStatus {
	case "completed":
		return "completed"
	case "failed", "cancelled", "indeterminate":
		return "failed"
	}
	switch taskStatus {
	case "completed", "succeeded":
		return "completed"
	case "failed", "cancelled", "not_created", "terminated_unknown":
		return "failed"
	case "queued":
		return "queued"
	default:
		return "processing"
	}
}

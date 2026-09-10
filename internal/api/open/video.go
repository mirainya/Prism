package open

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/gateway/delivery"
	"github.com/mirainya/Prism/internal/gateway/repository"
	gatewayruntime "github.com/mirainya/Prism/internal/gateway/runtime"
	"github.com/mirainya/Prism/internal/video"
	perrors "github.com/mirainya/Prism/pkg/errors"
)

func GetVideoGeneration(c *gin.Context) {
	token := middleware.GetToken(c)
	if token == nil {
		resp.ErrorMsg(c, http.StatusUnauthorized, 401, "unauthorized")
		return
	}
	_, err := getUnifiedVideoGeneration(c, token.UserID, token.ID)
	if err != nil {
		writeUnifiedVideoAPIError(c, err, "video generation not found")
	}
}

func writeUnifiedVideoAPIError(c *gin.Context, err error, notFoundMessage string) {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		resp.ErrorMsg(c, http.StatusNotFound, 404, notFoundMessage)
	case errors.Is(err, gatewayruntime.ErrNotReady):
		resp.ErrorMsg(c, http.StatusServiceUnavailable, 503, gatewayruntime.ErrNotReady.Error())
	default:
		resp.InternalError(c, perrors.ErrInternalError)
	}
}

// ListVideoGenerations returns the token-owned video resources without loading
// encrypted request/result bodies.
func ListVideoGenerations(c *gin.Context) {
	token := middleware.GetToken(c)
	if token == nil {
		resp.ErrorMsg(c, http.StatusUnauthorized, 401, "unauthorized")
		return
	}
	statusFilter, validStatus := videoListStatus(c.Query("status"))
	if !validStatus {
		resp.BadRequest(c, perrors.WithMessage(perrors.ErrInvalidParams, "invalid video status filter"))
		return
	}
	page, pageSize, ok := videoListPagination(c)
	if !ok {
		return
	}
	modelFilter := strings.TrimSpace(c.Query("model"))
	if len(modelFilter) > 255 {
		resp.BadRequest(c, perrors.WithMessage(perrors.ErrInvalidParams, "invalid model filter"))
		return
	}
	if _, err := listUnifiedVideoGenerations(c, token.UserID, token.ID, page, pageSize, statusFilter, modelFilter); err != nil {
		writeUnifiedVideoAPIError(c, err, "video generation not found")
	}
}

func videoListStatus(raw string) (string, bool) {
	status := strings.TrimSpace(strings.ToLower(raw))
	switch status {
	case "", string(video.VideoTaskStatusQueued), string(video.VideoTaskStatusSubmitted), string(video.VideoTaskStatusTracking), string(video.VideoTaskStatusCompleted), string(video.VideoTaskStatusFailed), string(video.VideoTaskStatusCancelled), string(video.VideoTaskStatusSubmissionUnknown):
		return status, true
	case "running":
		return string(video.VideoTaskStatusTracking), true
	case "succeeded":
		return string(video.VideoTaskStatusCompleted), true
	case "unknown":
		return string(video.VideoTaskStatusSubmissionUnknown), true
	default:
		return "", false
	}
}

func videoListPagination(c *gin.Context) (int, int, bool) {
	page, pageSize := 1, 20
	var err error
	if value := c.Query("page"); value != "" {
		page, err = strconv.Atoi(value)
		if err != nil || page < 1 {
			resp.BadRequest(c, perrors.WithMessage(perrors.ErrInvalidParams, "invalid page"))
			return 0, 0, false
		}
	}
	if value := c.Query("page_size"); value != "" {
		pageSize, err = strconv.Atoi(value)
		if err != nil || pageSize < 1 || pageSize > 100 {
			resp.BadRequest(c, perrors.WithMessage(perrors.ErrInvalidParams, "invalid page_size"))
			return 0, 0, false
		}
	}
	if page-1 > int(^uint(0)>>1)/pageSize {
		resp.BadRequest(c, perrors.WithMessage(perrors.ErrInvalidParams, "page offset is too large"))
		return 0, 0, false
	}
	return page, pageSize, true
}

const unifiedVideoPublicStatusSQL = `(CASE
WHEN c.status='completed' THEN 'completed'
WHEN c.status='failed' THEN 'failed'
WHEN c.status='cancelled' THEN 'cancelled'
WHEN c.status='indeterminate' THEN 'submission_unknown'
WHEN v.status IN ('queued','allocated','submitting') THEN 'queued'
WHEN v.status IN ('submitted','accepted') THEN 'submitted'
WHEN v.status IN ('tracking','running','cancel_requested') THEN 'tracking'
WHEN v.status IN ('completed','succeeded') THEN 'completed'
WHEN v.status IN ('failed','not_created') THEN 'failed'
WHEN v.status='cancelled' THEN 'cancelled'
WHEN v.status IN ('submission_unknown','manual_review','cancel_unknown','terminated_unknown','unknown') THEN 'submission_unknown'
WHEN c.status IN ('received','retry_pending') THEN 'queued'
WHEN c.status='in_progress' THEN 'tracking'
ELSE 'queued' END)`

func listUnifiedVideoGenerations(c *gin.Context, userID, tokenID uint, page, pageSize int, statusFilter, modelFilter string) (bool, error) {
	if unifiedVideoStore == nil {
		return true, gatewayruntime.ErrNotReady
	}
	ctx := c.Request.Context()
	where := ` FROM gw_api_resources r
JOIN gw_api_calls c ON c.id=r.call_id AND c.user_id=r.user_id AND c.token_id=r.token_id
JOIN gw_video_tasks v ON v.resource_id=r.id
WHERE r.resource_kind='video_task' AND r.user_id=? AND r.token_id=?`
	args := []any{userID, tokenID}
	if statusFilter != "" {
		where += ` AND ` + unifiedVideoPublicStatusSQL + `=?`
		args = append(args, statusFilter)
	}
	if modelFilter != "" {
		where += ` AND JSON_UNQUOTE(JSON_EXTRACT(v.specification_summary,'$.model'))=?`
		args = append(args, modelFilter)
	}
	var total int64
	if err := unifiedVideoStore.DB().QueryRowContext(ctx, `SELECT COUNT(*)`+where, args...).Scan(&total); err != nil {
		return true, err
	}
	query := `SELECT r.public_id,c.status,v.status,v.progress,v.specification_summary,c.created_at,c.updated_at` + where + ` ORDER BY r.id DESC LIMIT ? OFFSET ?`
	args = append(args, pageSize, (page-1)*pageSize)
	rows, err := unifiedVideoStore.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return true, err
	}
	defer rows.Close()
	items := make([]gin.H, 0, pageSize)
	for rows.Next() {
		var id, callStatus, taskStatus string
		var progress uint8
		var specification []byte
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&id, &callStatus, &taskStatus, &progress, &specification, &createdAt, &updatedAt); err != nil {
			return true, err
		}
		item := gin.H{"id": id, "status": unifiedVideoPublicStatus(callStatus, taskStatus), "progress": progress, "created_at": createdAt.UTC().Format(time.RFC3339), "updated_at": updatedAt.UTC().Format(time.RFC3339)}
		applyUnifiedVideoSummary(item, specification)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return true, err
	}
	resp.Success(c, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize})
	return true, nil
}

func getUnifiedVideoGeneration(c *gin.Context, userID, tokenID uint) (bool, error) {
	if unifiedVideoStore == nil {
		return true, gatewayruntime.ErrNotReady
	}
	ctx := c.Request.Context()
	var callID, resourceUserID, resourceTokenID, callUserID, callTokenID uint64
	var status, taskStatus string
	var progress uint8
	var specification []byte
	var createdAt, updatedAt time.Time
	var requestPayload, resultPayload sql.NullInt64
	err := unifiedVideoStore.DB().QueryRowContext(ctx, `SELECT c.id,r.user_id,r.token_id,c.user_id,c.token_id,c.status,c.created_at,c.updated_at,c.request_payload_id,c.result_payload_id,v.status,v.progress,v.specification_summary
FROM gw_api_resources r
JOIN gw_api_calls c ON c.id=r.call_id
JOIN gw_video_tasks v ON v.resource_id=r.id
WHERE r.public_id=? AND r.resource_kind='video_task'`, c.Param("id")).
		Scan(&callID, &resourceUserID, &resourceTokenID, &callUserID, &callTokenID, &status, &createdAt, &updatedAt, &requestPayload, &resultPayload, &taskStatus, &progress, &specification)
	if err == sql.ErrNoRows {
		return unifiedVideoMissing(ctx)
	}
	if err != nil {
		return true, err
	}
	if resourceUserID != uint64(userID) || resourceTokenID != uint64(tokenID) || callUserID != uint64(userID) || callTokenID != uint64(tokenID) {
		resp.ErrorMsg(c, http.StatusNotFound, 404, "video generation not found")
		return true, nil
	}
	publicStatus := unifiedVideoPublicStatus(status, taskStatus)
	response := gin.H{"id": c.Param("id"), "status": publicStatus, "progress": progress, "created_at": createdAt.Format(time.RFC3339), "updated_at": updatedAt.Format(time.RFC3339)}
	applyUnifiedVideoSummary(response, specification)
	if requestPayload.Valid {
		plain, err := readUnifiedPayload(ctx, uint64(requestPayload.Int64), callID, "request")
		if err != nil {
			return true, err
		}
		var request gatewayruntime.VideoRequestPayload
		if err := json.Unmarshal(plain, &request); err != nil {
			return true, err
		}
		response["prompt"] = request.Prompt
		if request.TaskMode != "" {
			response["task_mode"] = request.TaskMode
		}
	}
	if resultPayload.Valid {
		publicResult, err := readUnifiedVideoResult(ctx, uint64(resultPayload.Int64), callID, userID, tokenID)
		if err != nil {
			return true, err
		}
		response["result"] = publicResult
	}
	if isUnifiedVideoTerminal(status, taskStatus) {
		response["completed_at"] = updatedAt.Format(time.RFC3339)
	}
	resp.Success(c, response)
	return true, nil
}

func readUnifiedVideoResult(ctx context.Context, payloadID, callID uint64, userID, tokenID uint) (map[string]any, error) {
	plain, err := readUnifiedPayload(ctx, payloadID, callID, "result")
	if err != nil {
		return nil, err
	}
	var result delivery.VideoResult
	if err := json.Unmarshal(plain, &result); err != nil {
		return nil, err
	}
	if result.SchemaVersion != 1 || result.VideoDeliveryID == 0 {
		return nil, delivery.ErrInvalidResult
	}
	publicResult := map[string]any{
		"schema_version":    result.SchemaVersion,
		"video_delivery_id": fmt.Sprintf("%d", result.VideoDeliveryID),
	}
	if result.Duration != "" {
		publicResult["duration"] = result.Duration
	}
	videoURL, sourceErr := readUnifiedDeliveryURL(ctx, result.VideoDeliveryID, callID, userID, tokenID)
	if sourceErr == nil {
		publicResult["video_url"] = videoURL
	} else if !errors.Is(sourceErr, repository.ErrConflict) && !errors.Is(sourceErr, repository.ErrNotFound) {
		return nil, sourceErr
	} else {
		publicResult["delivery_status"] = "unavailable"
	}
	if result.ThumbnailDeliveryID != 0 {
		publicResult["thumbnail_delivery_id"] = fmt.Sprintf("%d", result.ThumbnailDeliveryID)
		thumbnailURL, thumbnailErr := readUnifiedDeliveryURL(ctx, result.ThumbnailDeliveryID, callID, userID, tokenID)
		if thumbnailErr == nil {
			publicResult["thumbnail_url"] = thumbnailURL
		} else if !errors.Is(thumbnailErr, repository.ErrConflict) && !errors.Is(thumbnailErr, repository.ErrNotFound) {
			return nil, thumbnailErr
		}
	}
	return publicResult, nil
}

func unifiedVideoPublicStatus(callStatus, taskStatus string) string {
	// Call terminal facts are authoritative if a damaged or partially imported
	// projection disagrees with its parent.
	switch callStatus {
	case "completed":
		return string(video.VideoTaskStatusCompleted)
	case "failed":
		return string(video.VideoTaskStatusFailed)
	case "cancelled":
		return string(video.VideoTaskStatusCancelled)
	case "indeterminate":
		return string(video.VideoTaskStatusSubmissionUnknown)
	}
	switch taskStatus {
	case "queued", "allocated", "submitting":
		return string(video.VideoTaskStatusQueued)
	case "submitted", "accepted":
		return string(video.VideoTaskStatusSubmitted)
	case "tracking", "running", "cancel_requested":
		return string(video.VideoTaskStatusTracking)
	case "completed", "succeeded":
		return string(video.VideoTaskStatusCompleted)
	case "failed", "not_created":
		return string(video.VideoTaskStatusFailed)
	case "cancelled":
		return string(video.VideoTaskStatusCancelled)
	case "submission_unknown", "manual_review", "cancel_unknown", "terminated_unknown", "unknown":
		return string(video.VideoTaskStatusSubmissionUnknown)
	}
	status := map[string]string{
		"received":      string(video.VideoTaskStatusQueued),
		"in_progress":   string(video.VideoTaskStatusTracking),
		"retry_pending": string(video.VideoTaskStatusQueued),
		"completed":     string(video.VideoTaskStatusCompleted),
		"failed":        string(video.VideoTaskStatusFailed),
		"cancelled":     string(video.VideoTaskStatusCancelled),
		"indeterminate": string(video.VideoTaskStatusSubmissionUnknown),
	}[callStatus]
	if status == "" {
		return string(video.VideoTaskStatusQueued)
	}
	return status
}

func applyUnifiedVideoSummary(target gin.H, raw []byte) {
	if len(raw) == 0 {
		return
	}
	var summary map[string]any
	if json.Unmarshal(raw, &summary) != nil {
		return
	}
	for _, key := range []string{"model", "service_tier", "resolution", "ratio", "duration", "generate_audio", "task_mode"} {
		if value, exists := summary[key]; exists {
			target[key] = value
		}
	}
}

func readUnifiedPayload(ctx context.Context, payloadID, callID uint64, kind string) ([]byte, error) {
	var blobID uint64
	if err := unifiedVideoStore.DB().QueryRowContext(ctx, `SELECT encrypted_blob_id FROM gw_api_call_payloads WHERE id=? AND call_id=? AND kind=?`, payloadID, callID, kind).Scan(&blobID); err != nil {
		return nil, err
	}
	envelope, err := unifiedVideoStore.ReadEncryptedBlob(ctx, unifiedVideoStore.DB(), blobID)
	if err != nil {
		return nil, err
	}
	kek, err := gatewayKey("PRISM_GATEWAY_PAYLOAD_KEK_B64")
	if err != nil {
		return nil, err
	}
	defer clear(kek)
	hmac, err := gatewayKey("PRISM_GATEWAY_PAYLOAD_HMAC_B64")
	if err != nil {
		return nil, err
	}
	defer clear(hmac)
	return repository.OpenBlob(envelope, blobID, []byte(fmt.Sprintf("call:%d:%s", callID, kind)), kek, hmac)
}

func readUnifiedDeliveryURL(ctx context.Context, deliveryID, callID uint64, userID, tokenID uint) (string, error) {
	record, err := unifiedVideoStore.ReadResultDeliveryByID(ctx, deliveryID, callID, uint64(userID), uint64(tokenID))
	if err != nil {
		return "", err
	}
	if record.State != "ready" {
		return "", repository.ErrConflict
	}
	if record.Mode == "managed_copy" {
		if record.MediaLocator == "" {
			return "", repository.ErrConflict
		}
		return record.MediaLocator, nil
	}
	if record.SourceBlobID == 0 {
		return "", repository.ErrConflict
	}
	envelope, err := unifiedVideoStore.ReadEncryptedBlob(ctx, unifiedVideoStore.DB(), record.SourceBlobID)
	if err != nil {
		return "", err
	}
	kek, err := gatewayKey("PRISM_GATEWAY_PAYLOAD_KEK_B64")
	if err != nil {
		return "", err
	}
	defer clear(kek)
	hmac, err := gatewayKey("PRISM_GATEWAY_PAYLOAD_HMAC_B64")
	if err != nil {
		return "", err
	}
	defer clear(hmac)
	plain, err := repository.OpenBlob(envelope, record.SourceBlobID, []byte(fmt.Sprintf("delivery:%d:source:%d", deliveryID, record.SourceSequence)), kek, hmac)
	if err != nil {
		return "", err
	}
	result := string(plain)
	clear(plain)
	return result, nil
}

func GetVideoGenerationQueue(c *gin.Context) {
	token := middleware.GetToken(c)
	if token == nil {
		resp.ErrorMsg(c, http.StatusUnauthorized, 401, "unauthorized")
		return
	}
	if _, err := getUnifiedVideoGenerationQueue(c, token.UserID, token.ID); err != nil {
		writeUnifiedVideoAPIError(c, err, "video task not found")
	}
}

func getUnifiedVideoGenerationQueue(c *gin.Context, userID, tokenID uint) (bool, error) {
	if unifiedVideoStore == nil {
		return true, gatewayruntime.ErrNotReady
	}
	var callID, resourceUserID, resourceTokenID, callUserID, callTokenID uint64
	var callStatus string
	var asyncStatus sql.NullString
	var nextAction sql.NullTime
	var asyncID sql.NullInt64
	err := unifiedVideoStore.DB().QueryRowContext(c.Request.Context(), `SELECT c.id,r.user_id,r.token_id,c.user_id,c.token_id,c.status,x.id,x.state,x.next_action_at
FROM gw_api_resources r
JOIN gw_api_calls c ON c.id=r.call_id
LEFT JOIN gw_api_call_attempts a ON a.id=COALESCE(c.current_attempt_id,c.final_attempt_id)
LEFT JOIN gw_async_executions x ON x.attempt_id=a.id
WHERE r.public_id=? AND r.resource_kind='video_task'`, c.Param("id")).
		Scan(&callID, &resourceUserID, &resourceTokenID, &callUserID, &callTokenID, &callStatus, &asyncID, &asyncStatus, &nextAction)
	if err == sql.ErrNoRows {
		return unifiedVideoMissing(c.Request.Context())
	}
	if err != nil {
		return true, err
	}
	if resourceUserID != uint64(userID) || resourceTokenID != uint64(tokenID) || callUserID != uint64(userID) || callTokenID != uint64(tokenID) {
		resp.ErrorMsg(c, http.StatusNotFound, 404, "video task not found")
		return true, nil
	}
	asyncState := ""
	if asyncStatus.Valid {
		asyncState = asyncStatus.String
	}
	queueStatus := unifiedVideoPublicStatus(callStatus, asyncState)
	if queueStatus == string(video.VideoTaskStatusSubmitted) || queueStatus == string(video.VideoTaskStatusTracking) {
		queueStatus = "running"
	}
	queue := gin.H{"status": queueStatus}
	if asyncID.Valid {
		queue["async_execution_id"] = asyncID.Int64
		if asyncStatus.Valid {
			queue["async_status"] = asyncStatus.String
		}
		if nextAction.Valid {
			queue["next_action_at"] = nextAction.Time.UTC().Format(time.RFC3339Nano)
		}
	}
	resp.Success(c, gin.H{"id": c.Param("id"), "status": queueStatus, "queue": queue})
	return true, nil
}

func ListVideoQueue(c *gin.Context) {
	token := middleware.GetToken(c)
	if token == nil {
		resp.ErrorMsg(c, http.StatusUnauthorized, 401, "unauthorized")
		return
	}
	if _, err := listUnifiedVideoQueue(c, token.UserID, token.ID); err != nil {
		writeUnifiedVideoAPIError(c, err, "video task not found")
	}
}

// unifiedVideoQueueItem is deliberately a small public projection.  Request
// and result payloads remain encrypted and are never loaded by the queue list.
type unifiedVideoQueueItem struct {
	ID            string
	CallStatus    string
	AsyncState    string
	AsyncID       sql.NullInt64
	NextActionAt  sql.NullTime
	Specification []byte
}

// listUnifiedVideoQueue reads only bounded, non-sensitive projections. Once
// unified configuration exists, its resource tables are authoritative even
// while traffic readiness is temporarily unavailable.
func listUnifiedVideoQueue(c *gin.Context, userID, tokenID uint) (bool, error) {
	if unifiedVideoStore == nil {
		return true, gatewayruntime.ErrNotReady
	}
	ctx := c.Request.Context()
	rows, err := unifiedVideoStore.DB().QueryContext(ctx, `
SELECT r.public_id,c.status,
       COALESCE(x.state,''),x.id,x.next_action_at,
       v.specification_summary
FROM gw_api_resources r
JOIN gw_video_tasks v ON v.resource_id=r.id
JOIN gw_api_calls c ON c.id=r.call_id AND c.user_id=r.user_id AND c.token_id=r.token_id
LEFT JOIN gw_api_call_attempts a ON a.id=COALESCE(c.current_attempt_id,c.final_attempt_id)
LEFT JOIN gw_async_executions x ON x.attempt_id=a.id
WHERE r.resource_kind='video_task' AND r.user_id=? AND r.token_id=?
  AND EXISTS (
    SELECT 1 FROM gw_operation_routes op
    WHERE op.operation_contract_id=c.operation_contract_id
      AND op.http_method='POST'
      AND op.route_template='/v1/videos/generations'
  )
  AND c.status IN ('received','in_progress','retry_pending')
ORDER BY r.id ASC
LIMIT 100`, userID, tokenID)
	if err != nil {
		return true, err
	}
	defer rows.Close()
	items := make([]any, 0, 100)
	queued, running := 0, 0
	for rows.Next() {
		var item unifiedVideoQueueItem
		if err := rows.Scan(&item.ID, &item.CallStatus, &item.AsyncState, &item.AsyncID, &item.NextActionAt, &item.Specification); err != nil {
			return true, err
		}
		status, isQueued := unifiedVideoQueueStatus(item.CallStatus, item.AsyncState)
		if isQueued {
			queued++
		} else {
			running++
		}
		queue := gin.H{"status": status}
		if item.AsyncID.Valid {
			queue["async_execution_id"] = item.AsyncID.Int64
		}
		if item.AsyncState != "" {
			queue["async_status"] = item.AsyncState
		}
		if item.NextActionAt.Valid {
			queue["next_action_at"] = item.NextActionAt.Time.UTC().Format(time.RFC3339Nano)
		}
		entry := gin.H{"id": item.ID, "status": status, "queue": queue}
		if tier := unifiedVideoServiceTier(item.Specification); tier != "" {
			entry["service_tier"] = tier
		}
		items = append(items, entry)
	}
	if err := rows.Err(); err != nil {
		return true, err
	}
	resp.Success(c, gin.H{"active_count": len(items), "queued_count": queued, "running_count": running, "items": items})
	return true, nil
}

func unifiedVideoQueueStatus(callStatus, asyncState string) (string, bool) {
	// A submitting/allocated execution has not been accepted upstream yet;
	// accepted/running/manual-review executions are active but no longer queued.
	switch asyncState {
	case "allocated", "submitting":
		return "queued", true
	case "submission_unknown", "accepted", "running", "manual_review", "cancel_requested", "cancel_unknown":
		return "running", false
	}
	if callStatus == "received" {
		return "queued", true
	}
	return "running", false
}

func unifiedVideoServiceTier(specification []byte) string {
	if len(specification) == 0 {
		return ""
	}
	var summary struct {
		ServiceTier string `json:"service_tier"`
	}
	if json.Unmarshal(specification, &summary) != nil {
		return ""
	}
	return summary.ServiceTier
}

func isUnifiedVideoTerminal(callStatus, asyncState string) bool {
	switch callStatus {
	case "completed", "failed", "cancelled", "indeterminate":
		return true
	}
	switch asyncState {
	case "succeeded", "failed", "cancelled", "not_created", "terminated_unknown":
		return true
	default:
		return false
	}
}

func unifiedVideoMissing(ctx context.Context) (bool, error) {
	_ = ctx
	return true, repository.ErrNotFound
}

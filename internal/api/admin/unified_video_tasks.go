package admin

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
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/execution"
	"github.com/mirainya/Prism/internal/gateway/payloadview"
	"github.com/mirainya/Prism/internal/gateway/repository"
	gatewayruntime "github.com/mirainya/Prism/internal/gateway/runtime"
	"github.com/mirainya/Prism/internal/model"
	pkgErrors "github.com/mirainya/Prism/pkg/errors"
)

type unifiedVideoTaskSummary struct {
	Model         string `json:"model"`
	VendorModel   string `json:"vendor_model"`
	TaskMode      string `json:"task_mode"`
	ServiceTier   string `json:"service_tier"`
	Resolution    string `json:"resolution"`
	Ratio         string `json:"ratio"`
	Duration      int    `json:"duration"`
	GenerateAudio bool   `json:"generate_audio"`
}

type unifiedVideoTaskRow struct {
	ResourceID, CallID, UserID, TokenID                   uint64
	PublicID, CallStatus, TaskStatus                      string
	Progress                                              uint8
	Summary                                               unifiedVideoTaskSummary
	ChannelID, CredentialID                               uint64
	AdapterCode, QuotedAmount, FinalAmount, BillingStatus string
	CreatedAt                                             time.Time
	SubmittedAt, CompletedAt                              *time.Time
	RequestPayloadID, ResultPayloadID, AsyncExecutionID   uint64
}

type unifiedVideoTaskResolutionRequest struct {
	Resolution string `json:"resolution"`
}

var finishUnifiedVideoTaskManualReview = func(ctx context.Context, store *repository.Store, asyncID uint64) error {
	service, err := gatewayruntime.New(store)
	if err != nil {
		return err
	}
	return service.FinishAsync(ctx, asyncID, execution.AsyncTerminatedUnknown, "operator_closed_unverifiable_submission", billing.Facts{})
}

func ListUnifiedVideoTasks(c *gin.Context) {
	page, size, ok := unifiedGatewayPagination(c)
	if !ok {
		return
	}
	db, err := model.DB().DB()
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	where, args, snapshot, ok := unifiedVideoTaskFilters(c)
	if !ok {
		return
	}
	var total int64
	if err := db.QueryRowContext(c.Request.Context(), `SELECT COUNT(*) `+unifiedVideoTaskCountFrom+where, args...).Scan(&total); err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	queryArgs := append(append([]any{}, args...), size, (page-1)*size)
	rows, err := db.QueryContext(c.Request.Context(), unifiedVideoTaskSelect+unifiedVideoTaskFrom+where+` ORDER BY r.id DESC LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0, size)
	for rows.Next() {
		row, err := scanUnifiedVideoTask(rows)
		if err != nil {
			resp.InternalError(c, pkgErrors.ErrInternalError)
			return
		}
		items = append(items, unifiedVideoTaskJSON(row, false))
	}
	if err := rows.Err(); err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	resp.Success(c, gin.H{"items": items, "total": total, "page": page, "page_size": size, "snapshot_at": snapshot.Format(time.RFC3339Nano)})
}

func GetUnifiedVideoTask(c *gin.Context) {
	db, err := model.DB().DB()
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	rows, err := db.QueryContext(c.Request.Context(), unifiedVideoTaskSelect+unifiedVideoTaskFrom+` WHERE r.resource_kind='video_task' AND r.public_id=? LIMIT 1`, c.Param("id"))
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	defer rows.Close()
	if !rows.Next() {
		if rows.Err() != nil {
			resp.InternalError(c, pkgErrors.ErrInternalError)
		} else {
			resp.NotFound(c, pkgErrors.ErrTaskNotFound)
		}
		return
	}
	row, err := scanUnifiedVideoTask(rows)
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	item := unifiedVideoTaskJSON(row, true)
	store, err := repository.New(db)
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	if status, _ := item["status"].(string); status == "failed" || status == "submission_unknown" {
		failure, readErr := payloadview.ReadLatestRequestFailure(c.Request.Context(), store, row.CallID)
		if readErr != nil {
			resp.InternalError(c, pkgErrors.ErrInternalError)
			return
		}
		item["error_code"] = failure.Code
		item["error_message"] = failure.Message
	}
	payloads := make([]gin.H, 0, 1)
	if row.RequestPayloadID != 0 {
		plain, err := readAdminCallPayload(c, store, row.CallID, row.RequestPayloadID, "request")
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				item["request_payload_expired"] = true
			} else {
				resp.InternalError(c, pkgErrors.ErrInternalError)
				return
			}
		} else {
			var request any
			if json.Unmarshal(plain, &request) != nil {
				resp.InternalError(c, pkgErrors.ErrInternalError)
				return
			}
			item["prompt"] = jsonStringField(request, "prompt")
			item["content_json"] = jsonField(request, "content")
			item["params_json"] = jsonField(request, "params")
			payloads = append(payloads, gin.H{"id": row.RequestPayloadID, "call_id": row.PublicID, "attempt_id": row.AsyncExecutionID, "kind": "request", "content_type": "application/json", "data": string(plain), "encrypted": true, "truncated": false, "original_bytes": len(plain), "created_at": row.CreatedAt.UTC().Format(time.RFC3339Nano)})
		}
	}
	if row.ResultPayloadID != 0 {
		plain, err := readAdminCallPayload(c, store, row.CallID, row.ResultPayloadID, "result")
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				item["result_payload_expired"] = true
			} else {
				resp.InternalError(c, pkgErrors.ErrInternalError)
				return
			}
		} else {
			var result any
			if json.Unmarshal(plain, &result) != nil {
				resp.InternalError(c, pkgErrors.ErrInternalError)
				return
			}
			item["result_json"] = result
		}
	}
	item["call_payloads"] = payloads
	resp.Success(c, item)
}

// ResolveUnifiedVideoTask ends local processing for an upstream submission
// whose outcome cannot be verified. It deliberately records an unknown terminal
// state instead of claiming that the provider cancelled or never created work.
func ResolveUnifiedVideoTask(c *gin.Context) {
	if currentAdminID(c) == 0 {
		resp.ErrorMsg(c, http.StatusForbidden, http.StatusForbidden, "需要管理员身份")
		return
	}
	var in unifiedVideoTaskResolutionRequest
	if !unifiedChannelBody(c, &in) {
		return
	}
	in.Resolution = strings.ToLower(strings.TrimSpace(in.Resolution))
	if in.Resolution != string(execution.AsyncTerminatedUnknown) {
		resp.ErrorMsg(c, http.StatusBadRequest, http.StatusBadRequest, "resolution 仅支持 terminated_unknown")
		return
	}
	if !model.HasDB() {
		resp.ErrorMsg(c, http.StatusServiceUnavailable, http.StatusServiceUnavailable, "数据库尚未就绪")
		return
	}
	db, err := model.DB().DB()
	if err != nil {
		writeUnifiedVideoResolutionError(c, err)
		return
	}
	store, err := repository.New(db)
	if err != nil {
		writeUnifiedVideoResolutionError(c, err)
		return
	}
	var asyncID uint64
	var state execution.AsyncState
	err = db.QueryRowContext(c.Request.Context(), `SELECT x.id,x.state
FROM gw_api_resources r
JOIN gw_api_calls call_record ON call_record.id=r.call_id
JOIN gw_api_call_attempts attempt ON attempt.call_id=call_record.id
JOIN gw_async_executions x ON x.attempt_id=attempt.id
WHERE r.public_id=? AND r.resource_kind='video_task'
ORDER BY attempt.attempt_no DESC LIMIT 1`, c.Param("id")).Scan(&asyncID, &state)
	if err != nil {
		writeUnifiedVideoResolutionError(c, err)
		return
	}
	if state != execution.AsyncManualReview && state != execution.AsyncSubmissionUnknown {
		writeUnifiedVideoResolutionError(c, repository.ErrConflict)
		return
	}
	if err := finishUnifiedVideoTaskManualReview(c.Request.Context(), store, asyncID); err != nil {
		writeUnifiedVideoResolutionError(c, err)
		return
	}
	resp.Success(c, gin.H{"id": c.Param("id"), "status": execution.AsyncTerminatedUnknown, "resolution": execution.AsyncTerminatedUnknown})
}

func writeUnifiedVideoResolutionError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows), errors.Is(err, repository.ErrNotFound):
		resp.ErrorMsg(c, http.StatusNotFound, http.StatusNotFound, "视频任务不存在")
	case errors.Is(err, repository.ErrInvalidInput):
		resp.ErrorMsg(c, http.StatusBadRequest, http.StatusBadRequest, "处理参数无效")
	case errors.Is(err, repository.ErrConflict):
		resp.ErrorMsg(c, http.StatusConflict, http.StatusConflict, "任务状态已变化，无法执行该处理")
	default:
		resp.ErrorMsg(c, http.StatusInternalServerError, http.StatusInternalServerError, "处理视频任务失败")
	}
}

func GetUnifiedVideoStats(c *gin.Context) {
	db, err := model.DB().DB()
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	var channels, credentials, tasks, active int64
	queries := []struct {
		query string
		value *int64
	}{
		{`SELECT COUNT(*) FROM gateway_channels WHERE status='active'`, &channels},
		{`SELECT COUNT(*) FROM gw_credentials WHERE status='active'`, &credentials},
		{`SELECT COUNT(*) FROM gw_api_resources WHERE resource_kind='video_task'`, &tasks},
		{`SELECT COUNT(*) FROM gw_api_resources r JOIN gw_api_calls c ON c.id=r.call_id WHERE r.resource_kind='video_task' AND c.status IN ('received','in_progress','retry_pending')`, &active},
	}
	for _, item := range queries {
		if err := db.QueryRowContext(c.Request.Context(), item.query).Scan(item.value); err != nil {
			resp.InternalError(c, pkgErrors.ErrInternalError)
			return
		}
	}
	resp.Success(c, gin.H{"channels": channels, "keys": credentials, "total_tasks": tasks, "active_tasks": active})
}

const unifiedVideoTaskFrom = `FROM gw_api_resources r
JOIN gw_api_calls c ON c.id=r.call_id AND c.user_id=r.user_id AND c.token_id=r.token_id
JOIN gw_video_tasks v ON v.resource_id=r.id
LEFT JOIN gw_api_call_attempts a ON a.id=COALESCE(c.current_attempt_id,c.final_attempt_id)
LEFT JOIN gw_async_executions x ON x.attempt_id=a.id
LEFT JOIN gw_product_transports pt ON pt.id=a.product_transport_id AND pt.release_id=a.catalog_release_id
LEFT JOIN gw_products p ON p.id=pt.product_id AND p.release_id=pt.release_id
LEFT JOIN gw_channel_transports ct ON ct.id=pt.channel_transport_id AND ct.release_id=pt.release_id
LEFT JOIN gw_adapter_implementations ai ON ai.id=ct.adapter_implementation_id
LEFT JOIN billing_reservations br ON br.call_id=c.id
LEFT JOIN billing_settlements bs ON bs.reservation_id=br.id `

// The count query intentionally omits response, adapter, and billing joins.
// Those projections are needed for page rows but cannot affect the filters.
const unifiedVideoTaskCountFrom = `FROM gw_api_resources r
JOIN gw_api_calls c ON c.id=r.call_id AND c.user_id=r.user_id AND c.token_id=r.token_id
JOIN gw_video_tasks v ON v.resource_id=r.id
LEFT JOIN gw_api_call_attempts a ON a.id=COALESCE(c.current_attempt_id,c.final_attempt_id)
LEFT JOIN gw_product_transports pt ON pt.id=a.product_transport_id AND pt.release_id=a.catalog_release_id
LEFT JOIN gw_products p ON p.id=pt.product_id AND p.release_id=pt.release_id `

const unifiedVideoTaskSelect = `SELECT r.id,c.id,r.public_id,c.user_id,c.token_id,c.status,v.status,v.progress,v.specification_summary,
COALESCE(p.channel_id,0),COALESCE(a.credential_id,0),COALESCE(ai.adapter_code,''),c.quoted_amount,
COALESCE(bs.actual_amount,0),COALESCE(br.state,''),c.created_at,x.created_at,
CASE WHEN c.status IN ('completed','failed','cancelled','indeterminate') THEN c.updated_at ELSE NULL END,
COALESCE(c.request_payload_id,0),COALESCE(c.result_payload_id,0),COALESCE(x.id,0) `

func unifiedVideoTaskFilters(c *gin.Context) (string, []any, time.Time, bool) {
	snapshot := time.Now().UTC()
	if raw := strings.TrimSpace(c.Query("snapshot_at")); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, "invalid snapshot_at"))
			return "", nil, time.Time{}, false
		}
		snapshot = parsed.UTC()
	}
	where := `WHERE r.resource_kind='video_task' AND c.created_at<=?`
	args := []any{snapshot}
	if keyword := strings.TrimSpace(c.Query("keyword")); keyword != "" {
		where += ` AND r.public_id LIKE ?`
		args = append(args, "%"+keyword+"%")
	}
	if status := strings.TrimSpace(c.Query("status")); status != "" {
		callStatuses, ok := unifiedVideoCallStatuses(status)
		if !ok {
			resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, "invalid status"))
			return "", nil, time.Time{}, false
		}
		where += ` AND c.status IN (` + placeholders(len(callStatuses)) + `)`
		for _, status := range callStatuses {
			args = append(args, status)
		}
	}
	for query, path := range map[string]string{"model": "$.model", "task_mode": "$.task_mode", "service_tier": "$.service_tier"} {
		if value := strings.TrimSpace(c.Query(query)); value != "" {
			where += ` AND JSON_UNQUOTE(JSON_EXTRACT(v.specification_summary,'` + path + `'))=?`
			args = append(args, value)
		}
	}
	for _, filter := range []struct{ query, column string }{{"channel_id", "p.channel_id"}, {"user_id", "c.user_id"}, {"token_id", "c.token_id"}} {
		if raw := strings.TrimSpace(c.Query(filter.query)); raw != "" {
			value, err := strconv.ParseUint(raw, 10, 64)
			if err != nil || value == 0 {
				resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, "invalid "+filter.query))
				return "", nil, time.Time{}, false
			}
			where += ` AND ` + filter.column + `=?`
			args = append(args, value)
		}
	}
	for _, filter := range []struct{ query, operator string }{{"start_date", ">="}, {"end_date", "<"}} {
		if raw := strings.TrimSpace(c.Query(filter.query)); raw != "" {
			value, err := time.ParseInLocation("2006-01-02", raw, time.Local)
			if err != nil {
				resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, "invalid "+filter.query))
				return "", nil, time.Time{}, false
			}
			if filter.query == "end_date" {
				value = value.AddDate(0, 0, 1)
			}
			where += ` AND c.created_at` + filter.operator + `?`
			args = append(args, value)
		}
	}
	return where, args, snapshot, true
}

func scanUnifiedVideoTask(rows *sql.Rows) (unifiedVideoTaskRow, error) {
	var row unifiedVideoTaskRow
	var summary []byte
	var submitted, completed sql.NullTime
	if err := rows.Scan(&row.ResourceID, &row.CallID, &row.PublicID, &row.UserID, &row.TokenID, &row.CallStatus, &row.TaskStatus, &row.Progress, &summary,
		&row.ChannelID, &row.CredentialID, &row.AdapterCode, &row.QuotedAmount, &row.FinalAmount, &row.BillingStatus, &row.CreatedAt, &submitted, &completed,
		&row.RequestPayloadID, &row.ResultPayloadID, &row.AsyncExecutionID); err != nil {
		return row, err
	}
	if len(summary) > 0 && json.Unmarshal(summary, &row.Summary) != nil {
		return row, repository.ErrConflict
	}
	if submitted.Valid {
		value := submitted.Time
		row.SubmittedAt = &value
	}
	if completed.Valid {
		value := completed.Time
		row.CompletedAt = &value
	}
	return row, nil
}

func unifiedVideoTaskJSON(row unifiedVideoTaskRow, detail bool) gin.H {
	item := gin.H{
		"id": row.PublicID, "call_id": row.PublicID, "user_id": row.UserID, "token_id": row.TokenID,
		"channel_id": row.ChannelID, "key_id": row.CredentialID, "model": row.Summary.Model, "vendor_model": row.Summary.VendorModel,
		"status": unifiedVideoPublicStatus(row.CallStatus, row.TaskStatus), "progress": row.Progress, "task_mode": row.Summary.TaskMode,
		"service_tier": row.Summary.ServiceTier, "resolution": row.Summary.Resolution, "ratio": row.Summary.Ratio,
		"duration": row.Summary.Duration, "generate_audio": row.Summary.GenerateAudio, "adapter_type": row.AdapterCode,
		"provider_task_id": "", "estimated_cost": row.QuotedAmount, "final_cost": row.FinalAmount, "billing_status": row.BillingStatus,
		"error_message": "", "created_at": row.CreatedAt.UTC().Format(time.RFC3339Nano),
		"execution_state": row.TaskStatus,
		"can_resolve":     row.TaskStatus == string(execution.AsyncManualReview) || row.TaskStatus == string(execution.AsyncSubmissionUnknown) || row.TaskStatus == string(execution.AsyncTerminatedUnknown),
	}
	if row.SubmittedAt != nil {
		item["submitted_at"] = row.SubmittedAt.UTC().Format(time.RFC3339Nano)
	}
	if row.CompletedAt != nil {
		item["completed_at"] = row.CompletedAt.UTC().Format(time.RFC3339Nano)
	}
	if detail {
		item["gateway_call_id"] = row.CallID
		item["poll_count"] = 0
		item["result_json"] = nil
		item["provider_response"] = nil
	}
	return item
}

func unifiedVideoPublicStatus(callStatus, taskStatus string) string {
	switch taskStatus {
	case "allocated", "submitting", "queued":
		return "queued"
	case "accepted":
		return "submitted"
	case "running":
		return "tracking"
	case "succeeded":
		return "completed"
	case "failed", "not_created":
		return "failed"
	case "cancelled":
		return "cancelled"
	case "submission_unknown", "manual_review", "cancel_unknown", "terminated_unknown":
		return "submission_unknown"
	}
	switch callStatus {
	case "completed":
		return "completed"
	case "failed":
		return "failed"
	case "cancelled":
		return "cancelled"
	case "indeterminate":
		return "submission_unknown"
	case "in_progress":
		return "tracking"
	default:
		return "queued"
	}
}

func unifiedVideoCallStatuses(public string) ([]string, bool) {
	switch strings.ToLower(public) {
	case "queued", "submitted", "tracking", "running":
		return []string{"received", "in_progress", "retry_pending"}, true
	case "completed", "succeeded":
		return []string{"completed"}, true
	case "failed":
		return []string{"failed"}, true
	case "cancelled":
		return []string{"cancelled"}, true
	case "submission_unknown", "unknown":
		return []string{"indeterminate"}, true
	default:
		return nil, false
	}
}

func placeholders(count int) string {
	if count <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
}

func readAdminCallPayload(c *gin.Context, store *repository.Store, callID, payloadID uint64, kind string) ([]byte, error) {
	var blobID sql.NullInt64
	if err := store.DB().QueryRowContext(c.Request.Context(), `SELECT encrypted_blob_id FROM gw_api_call_payloads WHERE id=? AND call_id=? AND kind=? AND purged_at IS NULL`, payloadID, callID, kind).Scan(&blobID); err != nil {
		return nil, err
	}
	if !blobID.Valid {
		return nil, repository.ErrNotFound
	}
	return readAdminGatewayPayload(c.Request.Context(), store, uint64(blobID.Int64), "gateway-payload", fmt.Sprintf("call:%d:%s", callID, kind))
}

func jsonField(value any, key string) any {
	object, _ := value.(map[string]any)
	return object[key]
}

func jsonStringField(value any, key string) string {
	text, _ := jsonField(value, key).(string)
	return text
}

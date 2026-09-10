package admin

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/gateway/payloadview"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/model"
	pkgErrors "github.com/mirainya/Prism/pkg/errors"
)

// Request metadata is independently paginated. Executable payloads, provider
// identities, credentials and raw URLs never enter this list response.
func UnifiedCallRequestLogs(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	page, size, ok := unifiedGatewayPagination(c)
	if !ok {
		return
	}
	db, err := model.DB().DB()
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	ctx := c.Request.Context()
	var callID uint64
	if err := db.QueryRowContext(ctx, `SELECT id FROM gw_api_calls WHERE id=?`, id).Scan(&callID); err == sql.ErrNoRows {
		resp.ErrorMsg(c, http.StatusNotFound, 404, "call not found")
		return
	} else if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	var total int64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_channel_request_logs l JOIN gw_api_call_attempts a ON a.id=l.attempt_id WHERE a.call_id=?`, id).Scan(&total); err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	rows, err := db.QueryContext(ctx, `SELECT l.id,a.attempt_no,l.request_seq,l.action,l.status,l.http_status,l.duration_ms,l.error_code,l.request_bytes_complete,l.response_bytes_complete,l.created_at,l.completed_at,l.async_execution_id,x.state,l.request_payload_blob_id,l.response_payload_blob_id
FROM gw_channel_request_logs l JOIN gw_api_call_attempts a ON a.id=l.attempt_id
LEFT JOIN gw_async_executions x ON x.id=l.async_execution_id
WHERE a.call_id=? ORDER BY l.id DESC LIMIT ? OFFSET ?`, id, size, (page-1)*size)
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0, size)
	for rows.Next() {
		var requestID, attemptNo, sequence uint64
		var action, status, errorCode string
		var httpStatus, duration, asyncID, requestBlobID, responseBlobID sql.NullInt64
		var asyncState sql.NullString
		var requestComplete, responseComplete bool
		var created, completed sql.NullTime
		if err := rows.Scan(&requestID, &attemptNo, &sequence, &action, &status, &httpStatus, &duration, &errorCode, &requestComplete, &responseComplete, &created, &completed, &asyncID, &asyncState, &requestBlobID, &responseBlobID); err != nil {
			resp.InternalError(c, pkgErrors.ErrInternalError)
			return
		}
		items = append(items, gin.H{
			"id": requestID, "attempt_no": attemptNo, "request_seq": sequence, "action": action, "status": status,
			"http_status": nullableInt(httpStatus), "duration_ms": nullableInt(duration), "error_code": errorCode,
			"request_complete": requestComplete, "response_complete": responseComplete,
			"created_at": nullableTime(created), "completed_at": nullableTime(completed),
			"async_execution_id": nullableInt(asyncID), "async_state": asyncState.String,
			"request_payload_available": requestBlobID.Valid, "response_payload_available": responseBlobID.Valid,
		})
	}
	if err := rows.Err(); err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	resp.Success(c, unifiedGatewayPage{Items: items, Page: page, PageSize: size, Total: total})
}

// UnifiedRequestLogPayloads decrypts one exchange only after the administrator
// explicitly opens it. The call join prevents cross-call request enumeration.
func UnifiedRequestLogPayloads(c *gin.Context) {
	callID, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	requestID, err := resp.ParseUintParam(c, "request_id")
	if err != nil {
		return
	}
	db, err := model.DB().DB()
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	var requestBlobID, responseBlobID sql.NullInt64
	var requestComplete, responseComplete bool
	err = db.QueryRowContext(c.Request.Context(), `SELECT l.request_payload_blob_id,l.response_payload_blob_id,l.request_bytes_complete,l.response_bytes_complete
FROM gw_channel_request_logs l
JOIN gw_api_call_attempts a ON a.id=l.attempt_id
WHERE a.call_id=? AND l.id=?`, callID, requestID).Scan(&requestBlobID, &responseBlobID, &requestComplete, &responseComplete)
	if err == sql.ErrNoRows {
		resp.ErrorMsg(c, http.StatusNotFound, 404, "request log not found")
		return
	}
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	store, err := repository.New(db)
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	result := gin.H{
		"request_body": nil, "response_body": nil,
		"request_complete": requestComplete, "response_complete": responseComplete,
	}
	for _, payload := range []struct {
		name, kind string
		id         sql.NullInt64
	}{{"request_body", "request", requestBlobID}, {"response_body", "response", responseBlobID}} {
		if !payload.id.Valid || payload.id.Int64 <= 0 {
			continue
		}
		plain, readErr := payloadview.ReadRequestLogPayload(c.Request.Context(), store, uint64(requestID), uint64(payload.id.Int64), payload.kind)
		if readErr != nil {
			resp.InternalError(c, pkgErrors.ErrInternalError)
			return
		}
		result[payload.name] = string(plain)
		clear(plain)
	}
	resp.Success(c, result)
}

package admin

import (
	"database/sql"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/model"
	pkgErrors "github.com/mirainya/Prism/pkg/errors"
)

// The circuit breaker was the one piece of routing state with no control-plane
// surface at all: Engine writes gw_route_states, the candidate SQL filters on
// it, and nothing ever showed it to an operator. A correctly configured model
// could return 503 for six hours with no way to see why, and replacing the
// rejected key did not help, because the breaker is keyed on credential id
// rather than on the secret. These endpoints make "configured but not serving"
// both visible and recoverable without waiting out the backoff.

// routeStateCredentialExpr strips the namespace marker Engine ORs into key_id.
// The mask is a compile-time constant, so it is inlined rather than bound: that
// keeps every ? in the queries below matched to a real caller-supplied value.
var routeStateCredentialExpr = "(rs.key_id & " + strconv.FormatUint(routing.CircuitKeyMask-1, 10) + ")"

// ListUnifiedRouteStates reports the breakers currently holding a route out of
// rotation. Expired rows are excluded by default because they no longer affect
// routing; ?include_expired=1 keeps them as failure history.
func ListUnifiedRouteStates(c *gin.Context) {
	page, size, ok := unifiedGatewayPagination(c)
	if !ok {
		return
	}
	where := " WHERE rs.disabled_until>CURRENT_TIMESTAMP(3)"
	if c.Query("include_expired") == "1" {
		where = " WHERE 1=1"
	}
	args := make([]any, 0, 2)
	if raw := c.Query("credential_id"); raw != "" {
		credentialID, err := strconv.ParseUint(raw, 10, 63)
		if err != nil || credentialID == 0 {
			unifiedChannelError(c, repository.ErrInvalidInput)
			return
		}
		where += " AND " + routeStateCredentialExpr + "=?"
		args = append(args, int64(credentialID))
	}
	if raw := c.Query("model_name"); raw != "" {
		where += " AND rs.model_name=?"
		args = append(args, raw)
	}
	db, err := model.DB().DB()
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	ctx := c.Request.Context()
	var total int64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_route_states rs`+where, args...).Scan(&total); err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	rows, err := db.QueryContext(ctx, `SELECT rs.id,rs.model_name,rs.transport,rs.disabled_until,rs.reason,rs.status_code,rs.fail_count,rs.updated_at,
       `+routeStateCredentialExpr+`,c.credential_code,c.status,p.id,p.pool_code,p.display_name,ch.id,ch.display_name,
		TIMESTAMPDIFF(SECOND,CURRENT_TIMESTAMP(3),rs.disabled_until)
FROM gw_route_states rs
LEFT JOIN gw_credentials c ON c.id=`+routeStateCredentialExpr+`
LEFT JOIN gw_credential_pools p ON p.id=c.credential_pool_id
LEFT JOIN gateway_channels ch ON ch.id=c.channel_id`+where+`
ORDER BY rs.disabled_until DESC,rs.id DESC LIMIT ? OFFSET ?`, append(append([]any{}, args...), size, (page-1)*size)...)
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0, size)
	for rows.Next() {
		var id, statusCode, failCount, credentialID int64
		var modelName, transport, reason string
		var disabledUntil, updatedAt sql.NullTime
		var credentialCode, credentialStatus, poolCode, poolName, channelName sql.NullString
		var poolID, channelID, remainingSeconds sql.NullInt64
		if err := rows.Scan(&id, &modelName, &transport, &disabledUntil, &reason, &statusCode, &failCount, &updatedAt,
			&credentialID, &credentialCode, &credentialStatus, &poolID, &poolCode, &poolName, &channelID, &channelName,
			&remainingSeconds); err != nil {
			resp.InternalError(c, pkgErrors.ErrInternalError)
			return
		}
		remaining := remainingSeconds.Int64
		if !remainingSeconds.Valid || remaining < 0 {
			remaining = 0
		}
		items = append(items, gin.H{
			"id": id, "model_name": modelName, "transport": transport,
			"disabled_until": nullableTime(disabledUntil), "updated_at": nullableTime(updatedAt),
			"remaining_seconds": remaining, "active": remaining > 0,
			"reason": reason, "status_code": statusCode, "fail_count": failCount,
			"credential_id": credentialID, "credential_code": credentialCode.String,
			"credential_status":  credentialStatus.String,
			"credential_pool_id": nullableInt(poolID), "pool_code": poolCode.String, "pool_name": poolName.String,
			"channel_id": nullableInt(channelID), "channel_name": channelName.String,
		})
	}
	if err := rows.Err(); err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	resp.Success(c, unifiedGatewayPage{Items: items, Page: page, PageSize: size, Total: total})
}

// ClearUnifiedRouteState deletes one breaker so the route is eligible again on
// the next request. Deleting the row is the whole recovery: the candidate SQL
// only skips breakers whose window has not elapsed, so the route returns
// immediately instead of after the remaining backoff.
func ClearUnifiedRouteState(c *gin.Context) {
	stateID, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	db, dbErr := model.DB().DB()
	if dbErr != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	result, execErr := db.ExecContext(c.Request.Context(), `DELETE FROM gw_route_states WHERE id=?`, stateID)
	if execErr != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		unifiedChannelError(c, repository.ErrNotFound)
		return
	}
	resp.Success(c, gin.H{"cleared": affected})
}

// ClearUnifiedCredentialRouteStates clears every breaker for one credential.
// This is what an operator actually needs after rotating a rejected key: the
// breaker is keyed on credential id, so replacing the secret otherwise leaves
// every broken route broken until its window expires.
func ClearUnifiedCredentialRouteStates(c *gin.Context) {
	// The param is :id, not :credential_id — gin requires one wildcard name per
	// path position, and /unified-gateway/credentials/:id is already registered.
	credentialID, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	db, dbErr := model.DB().DB()
	if dbErr != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	result, execErr := db.ExecContext(c.Request.Context(),
		`DELETE FROM gw_route_states WHERE (key_id & `+strconv.FormatUint(routing.CircuitKeyMask, 10)+`)<>0 AND (key_id & `+strconv.FormatUint(routing.CircuitKeyMask-1, 10)+`)=?`, int64(credentialID))
	if execErr != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	affected, _ := result.RowsAffected()
	resp.Success(c, gin.H{"cleared": affected})
}

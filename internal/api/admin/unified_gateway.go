package admin

import (
	"database/sql"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/model"
	pkgErrors "github.com/mirainya/Prism/pkg/errors"
)

// UnifiedGatewayOverview exposes the migration/runtime boundary to operators.
// It is intentionally read-only: activating a catalog release remains a
// separate audited control-plane operation.
func UnifiedGatewayOverview(c *gin.Context) {
	db, err := model.DB().DB()
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	ctx := c.Request.Context()
	count := func(table string) int64 {
		var n int64
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM `"+table+"`").Scan(&n); err != nil {
			return 0
		}
		return n
	}

	var activeRelease sql.NullInt64
	var releaseVersion int64
	_ = db.QueryRowContext(ctx, "SELECT active_release_id,state_version FROM gw_catalog_runtime_state WHERE id=1").Scan(&activeRelease, &releaseVersion)
	var activeReleaseValue any
	if activeRelease.Valid {
		activeReleaseValue = activeRelease.Int64
	}

	var deploymentStatus string
	var deploymentID int64
	_ = db.QueryRowContext(ctx, "SELECT id,status FROM gw_deployment_generations ORDER BY id DESC LIMIT 1").Scan(&deploymentID, &deploymentStatus)

	legacyChannels := count("gw_channels")
	legacyAbilities := count("gw_abilities")
	targetChannels := count("gateway_channels")
	targetModels := count("gw_models")
	targetCredentials := count("gw_credentials")
	targetReleases := count("gw_catalog_releases")
	targetCalls := count("gw_api_calls")

	state := "target_empty"
	if targetChannels > 0 && targetModels > 0 && targetReleases > 0 && activeRelease.Valid {
		state = "target_configured"
	} else if legacyChannels > 0 || legacyAbilities > 0 {
		state = "legacy_runtime"
	}
	readyForCutover := legacyChannels == 0 && legacyAbilities == 0 && targetChannels > 0 && targetModels > 0 && targetCredentials > 0 && targetReleases > 0 && activeRelease.Valid && deploymentStatus == "active"

	resp.Success(c, gin.H{
		"state":             state,
		"ready_for_cutover": readyForCutover,
		"runtime": gin.H{
			"active_release_id":     activeReleaseValue,
			"release_state_version": releaseVersion,
			"deployment_id":         deploymentID,
			"deployment_status":     deploymentStatus,
		},
		"target": gin.H{
			"channels":         targetChannels,
			"models":           targetModels,
			"credentials":      targetCredentials,
			"catalog_releases": targetReleases,
			"calls":            targetCalls,
		},
		"legacy": gin.H{
			"channels":       legacyChannels,
			"abilities":      legacyAbilities,
			"runtime_active": legacyChannels > 0 || legacyAbilities > 0,
		},
	})
}

type unifiedGatewayPage struct {
	Items    []gin.H `json:"items"`
	Page     int     `json:"page"`
	PageSize int     `json:"page_size"`
	Total    int64   `json:"total"`
}

func nullableTime(value sql.NullTime) any {
	if !value.Valid {
		return nil
	}
	return value.Time.UTC().Format(time.RFC3339Nano)
}

func nullableInt(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}

func unifiedGatewayPagination(c *gin.Context) (int, int, bool) {
	page, size := 1, 20
	var err error
	if value := c.Query("page"); value != "" {
		page, err = strconv.Atoi(value)
		if err != nil || page < 1 {
			resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, "invalid page"))
			return 0, 0, false
		}
	}
	if value := c.Query("page_size"); value != "" {
		size, err = strconv.Atoi(value)
		if err != nil || size < 1 || size > 100 {
			resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, "invalid page_size"))
			return 0, 0, false
		}
	}
	return page, size, true
}

// UnifiedGatewayCatalog lists immutable catalog releases without loading JSON
// snapshots or encrypted payloads.
func UnifiedGatewayCatalog(c *gin.Context) {
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
	var total int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM gw_catalog_releases").Scan(&total); err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	rows, err := db.QueryContext(ctx, `SELECT id,release_no,status,semantic_version,content_hash,semantic_digest,published_at,created_at FROM gw_catalog_releases ORDER BY id DESC LIMIT ? OFFSET ?`, size, (page-1)*size)
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0, size)
	for rows.Next() {
		var id, releaseNo int64
		var status, semanticVersion, contentHash, semanticDigest string
		var publishedAt, createdAt sql.NullTime
		if err := rows.Scan(&id, &releaseNo, &status, &semanticVersion, &contentHash, &semanticDigest, &publishedAt, &createdAt); err != nil {
			resp.InternalError(c, pkgErrors.ErrInternalError)
			return
		}
		items = append(items, gin.H{"id": id, "release_no": releaseNo, "status": status, "semantic_version": semanticVersion, "content_hash": contentHash, "semantic_digest": semanticDigest, "published_at": nullableTime(publishedAt), "created_at": nullableTime(createdAt)})
	}
	resp.Success(c, unifiedGatewayPage{Items: items, Page: page, PageSize: size, Total: total})
}

// UnifiedGatewayCredentials lists credential metadata and never returns key
// material, ciphertext, or blob identifiers.
func UnifiedGatewayCredentials(c *gin.Context) {
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
	var total int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM gw_credentials").Scan(&total); err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	rows, err := db.QueryContext(ctx, `SELECT c.id,c.channel_id,c.credential_pool_id,c.credential_code,c.status,c.config_version,c.request_limit,c.task_limit,c.weight,c.current_version_id,p.pool_code,p.display_name FROM gw_credentials c LEFT JOIN gw_credential_pools p ON p.id=c.credential_pool_id ORDER BY c.id DESC LIMIT ? OFFSET ?`, size, (page-1)*size)
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0, size)
	for rows.Next() {
		var id, channelID int64
		var poolID, currentVersion sql.NullInt64
		var code, status string
		var configVersion, weight int64
		var requestLimit, taskLimit sql.NullInt64
		var poolCode, poolName sql.NullString
		if err := rows.Scan(&id, &channelID, &poolID, &code, &status, &configVersion, &requestLimit, &taskLimit, &weight, &currentVersion, &poolCode, &poolName); err != nil {
			resp.InternalError(c, pkgErrors.ErrInternalError)
			return
		}
		items = append(items, gin.H{"id": id, "channel_id": channelID, "credential_pool_id": nullableInt(poolID), "credential_code": code, "status": status, "config_version": configVersion, "request_limit": nullableInt(requestLimit), "task_limit": nullableInt(taskLimit), "weight": weight, "current_version_id": nullableInt(currentVersion), "pool_code": poolCode.String, "pool_name": poolName.String})
	}
	resp.Success(c, unifiedGatewayPage{Items: items, Page: page, PageSize: size, Total: total})
}

// UnifiedGatewayCalls lists the new call ledger. Payloads are deliberately
// excluded; the detail endpoint will expose only bounded, authorized previews.
func UnifiedGatewayCalls(c *gin.Context) {
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
	var total int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM gw_api_calls").Scan(&total); err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	rows, err := db.QueryContext(ctx, `SELECT id,public_id,user_id,token_id,status,quoted_amount,price_currency,delivery_mode,created_at,updated_at FROM gw_api_calls ORDER BY id DESC LIMIT ? OFFSET ?`, size, (page-1)*size)
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0, size)
	for rows.Next() {
		var id, userID, tokenID int64
		var publicID, status, amount, currency, delivery string
		var createdAt, updatedAt sql.NullTime
		if err := rows.Scan(&id, &publicID, &userID, &tokenID, &status, &amount, &currency, &delivery, &createdAt, &updatedAt); err != nil {
			resp.InternalError(c, pkgErrors.ErrInternalError)
			return
		}
		items = append(items, gin.H{"id": id, "public_id": publicID, "user_id": userID, "token_id": tokenID, "status": status, "quoted_amount": amount, "price_currency": currency, "delivery_mode": delivery, "created_at": nullableTime(createdAt), "updated_at": nullableTime(updatedAt)})
	}
	resp.Success(c, unifiedGatewayPage{Items: items, Page: page, PageSize: size, Total: total})
}

func UnifiedGatewayPublishCatalog(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	store, err := repository.New(mustSQLDB())
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	if err := store.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		return store.PublishRelease(c.Request.Context(), tx, uint64(id), uint64(currentAdminID(c)))
	}); err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	resp.Success(c, gin.H{"published": true})
}

func UnifiedGatewayRetireCatalog(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	store, err := repository.New(mustSQLDB())
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	if err := store.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		return store.RetireRelease(c.Request.Context(), tx, uint64(id))
	}); err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	resp.Success(c, gin.H{"retired": true})
}

func mustSQLDB() *sql.DB {
	db, _ := model.DB().DB()
	return db
}

func currentAdminID(c *gin.Context) uint {
	// JWT middleware stores the authenticated administrator on the context. A
	// zero reviewer is accepted for system-triggered control-plane changes.
	if value, ok := c.Get("user_id"); ok {
		if id, ok := value.(uint); ok {
			return id
		}
	}
	return 0
}

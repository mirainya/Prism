package admin

import (
	"database/sql"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/gateway/adapter"
	"github.com/mirainya/Prism/internal/gateway/repository"
	gatewayruntime "github.com/mirainya/Prism/internal/gateway/runtime"
	"github.com/mirainya/Prism/internal/model"
	pkgErrors "github.com/mirainya/Prism/pkg/errors"
)

// UnifiedGatewayOverview exposes the migration/runtime boundary to operators.
// It is intentionally read-only; configuration writes use the direct edit
// endpoints and take effect on the next request.
func UnifiedGatewayOverview(c *gin.Context) {
	orm := model.DB()
	db, err := orm.DB()
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	ctx := c.Request.Context()
	var queryErr error
	count := func(table string) int64 {
		var n int64
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM `"+table+"`").Scan(&n); err != nil {
			queryErr = err
			return 0
		}
		return n
	}
	legacyCount := func(table string) int64 {
		if !orm.Migrator().HasTable(table) {
			return 0
		}
		return count(table)
	}

	var activeRelease sql.NullInt64
	var releaseVersion int64
	if err := db.QueryRowContext(ctx, "SELECT active_release_id,state_version FROM gw_catalog_runtime_state WHERE id=1").Scan(&activeRelease, &releaseVersion); err != nil {
		queryErr = err
	}
	var activeReleaseValue any
	if activeRelease.Valid {
		activeReleaseValue = activeRelease.Int64
	}
	// Deployment generations remain visible for older API consumers, but no
	// longer participate in runtime readiness.
	var activeDeployment sql.NullInt64
	if err := db.QueryRowContext(ctx, "SELECT active_deployment_generation_id FROM gw_catalog_runtime_state WHERE id=1").Scan(&activeDeployment); err != nil {
		queryErr = err
	}
	var deploymentStatus string
	var deploymentID int64
	if activeDeployment.Valid {
		deploymentID = activeDeployment.Int64
		if err := db.QueryRowContext(ctx, "SELECT status FROM gw_deployment_generations WHERE id=?", deploymentID).Scan(&deploymentStatus); err != nil && err != sql.ErrNoRows {
			queryErr = err
		}
	}
	var latestGenerationNo int64
	if err := db.QueryRowContext(ctx, "SELECT COALESCE(MAX(generation_no),0) FROM gw_deployment_generations").Scan(&latestGenerationNo); err != nil {
		queryErr = err
	}

	legacyChannels := legacyCount("gw_channels")
	legacyAbilities := legacyCount("gw_abilities")
	targetChannels := count("gateway_channels")
	targetModels := count("gw_models")
	targetCredentials := count("gw_credentials")
	targetReleases := count("gw_catalog_releases")
	targetCalls := count("gw_api_calls")
	targetOfferings := count("gw_offerings")
	targetRoutes := count("gw_routes")
	sellRates := count("gw_sell_rates")
	costRates := count("gw_cost_rates")
	currencies := count("billing_currency_definitions")
	if queryErr != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	blockers := make([]string, 0)
	for _, check := range []struct {
		blocked bool
		code    string
	}{
		{targetChannels == 0, "channels_missing"},
		{targetModels == 0, "models_missing"},
		{targetCredentials == 0, "credentials_missing"},
		{targetReleases == 0, "catalog_missing"},
		{targetOfferings == 0 || targetRoutes == 0, "routes_missing"},
		{sellRates == 0, "sell_rates_missing"},
		{costRates == 0, "cost_rates_missing"},
		{currencies == 0, "currency_missing"},
		{!activeRelease.Valid, "catalog_inactive"},
		{legacyChannels > 0 || legacyAbilities > 0, "legacy_data_present"},
	} {
		if check.blocked {
			blockers = append(blockers, check.code)
		}
	}
	runtimeReady := false
	if activeRelease.Valid && activeRelease.Int64 > 0 {
		runtimeReady = gatewayruntime.CheckReadiness(ctx, db, uint64(activeRelease.Int64)) == nil
		if !runtimeReady {
			blockers = append(blockers, "runtime_not_ready")
		}
	}

	state := "target_empty"
	if runtimeReady && len(blockers) == 0 {
		state = "target_configured"
	} else if targetChannels > 0 || targetReleases > 0 || legacyChannels > 0 || legacyAbilities > 0 {
		state = "migration_pending"
	}

	resp.Success(c, gin.H{
		"state":               state,
		"ready_for_cutover":   runtimeReady && len(blockers) == 0,
		"runtime_ready":       runtimeReady,
		"management_revision": 4,
		"blockers":            blockers,
		"runtime": gin.H{
			"active_release_id":     activeReleaseValue,
			"release_state_version": releaseVersion,
			"deployment_id":         deploymentID,
			"deployment_status":     deploymentStatus,
			"latest_generation_no":  latestGenerationNo,
		},
		"process": gin.H{
			"semantic_digest": adapter.SemanticDigest(),
		},
		"target": gin.H{
			"channels":         targetChannels,
			"models":           targetModels,
			"credentials":      targetCredentials,
			"catalog_releases": targetReleases,
			"calls":            targetCalls,
			"offerings":        targetOfferings,
			"routes":           targetRoutes,
			"sell_rates":       sellRates,
			"cost_rates":       costRates,
			"currencies":       currencies,
		},
		"legacy": gin.H{
			"channels":     legacyChannels,
			"abilities":    legacyAbilities,
			"data_present": legacyChannels > 0 || legacyAbilities > 0,
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
	if page-1 > int(^uint(0)>>1)/size {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, "page offset is too large"))
		return 0, 0, false
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
	rows, err := db.QueryContext(ctx, `SELECT id,release_no,status,config_version,semantic_version,content_hash,semantic_digest,published_at,created_at,updated_at FROM gw_catalog_releases ORDER BY id DESC LIMIT ? OFFSET ?`, size, (page-1)*size)
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0, size)
	for rows.Next() {
		var id, releaseNo, configVersion int64
		var status, semanticVersion, contentHash, semanticDigest string
		var publishedAt, createdAt, updatedAt sql.NullTime
		if err := rows.Scan(&id, &releaseNo, &status, &configVersion, &semanticVersion, &contentHash, &semanticDigest, &publishedAt, &createdAt, &updatedAt); err != nil {
			resp.InternalError(c, pkgErrors.ErrInternalError)
			return
		}
		items = append(items, gin.H{"id": id, "release_no": releaseNo, "status": status, "config_version": configVersion, "semantic_version": semanticVersion, "content_hash": contentHash, "semantic_digest": semanticDigest, "published_at": nullableTime(publishedAt), "created_at": nullableTime(createdAt), "updated_at": nullableTime(updatedAt)})
	}
	if err := rows.Err(); err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
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
	where := ""
	args := make([]any, 0, 3)
	if raw := c.Query("pool_id"); raw != "" {
		poolID, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || poolID == 0 {
			unifiedChannelError(c, repository.ErrInvalidInput)
			return
		}
		where = " WHERE c.credential_pool_id=?"
		args = append(args, poolID)
	}
	db, err := model.DB().DB()
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	ctx := c.Request.Context()
	var total int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM gw_credentials c"+where, args...).Scan(&total); err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	rows, err := db.QueryContext(ctx, `SELECT c.id,c.channel_id,c.credential_pool_id,c.credential_code,c.status,c.config_version,c.request_limit,c.task_limit,c.weight,c.current_version_id,p.pool_code,p.display_name FROM gw_credentials c LEFT JOIN gw_credential_pools p ON p.id=c.credential_pool_id`+where+` ORDER BY c.id DESC LIMIT ? OFFSET ?`, append(args, size, (page-1)*size)...)
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
	if err := rows.Err(); err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	if err := rows.Close(); err != nil {
		unifiedChannelError(c, err)
		return
	}
	if err := appendUnifiedCredentialPurposes(ctx, db, items); err != nil {
		unifiedChannelError(c, err)
		return
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
	if err := rows.Err(); err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	resp.Success(c, unifiedGatewayPage{Items: items, Page: page, PageSize: size, Total: total})
}

func UnifiedGatewayCallDetail(c *gin.Context) {
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
	var call gin.H
	var publicID, status, amount, currency, delivery string
	var userID, tokenID, releaseID, operationID, skuID int64
	var createdAt, updatedAt sql.NullTime
	err = db.QueryRowContext(ctx, `SELECT public_id,user_id,token_id,status,catalog_release_id,model_operation_id,sku_id,quoted_amount,price_currency,delivery_mode,created_at,updated_at FROM gw_api_calls WHERE id=?`, id).Scan(&publicID, &userID, &tokenID, &status, &releaseID, &operationID, &skuID, &amount, &currency, &delivery, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		resp.ErrorMsg(c, 404, 404, "call not found")
		return
	}
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	call = gin.H{"id": id, "public_id": publicID, "user_id": userID, "token_id": tokenID, "status": status, "catalog_release_id": releaseID, "model_operation_id": operationID, "sku_id": skuID, "quoted_amount": amount, "price_currency": currency, "delivery_mode": delivery, "created_at": nullableTime(createdAt), "updated_at": nullableTime(updatedAt)}
	var total int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM gw_api_call_attempts WHERE call_id=?", id).Scan(&total); err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	// 抽屉里原来只有 #route_id #offering_id #credential_id 三个裸 id，看不出这次尝试
	// 走的是哪条 transport、哪个渠道、哪把 Key，更看不出它为什么失败。
	// 注意这里全部用 LEFT JOIN：COUNT(*) 不带 join，任何一个 INNER JOIN 掉行都会让
	// total 与本页条数对不上。错误信息只有 error_code——v2 没有存上游错误正文的列，
	// 正文在开启 retain_payload 时才落到加密 blob 里，走「上游请求」页签取。
	rows, err := db.QueryContext(ctx, `SELECT a.id,a.attempt_no,a.state,a.catalog_release_id,a.sku_id,a.route_id,a.offering_id,a.credential_id,a.credential_version_id,a.purpose_grant_id,a.created_at,a.updated_at,
       COALESCE(ct.transport_code,''),COALESCE(ch.display_name,''),COALESCE(c.credential_code,''),
       COALESCE((SELECT l.error_code FROM gw_channel_request_logs l WHERE l.attempt_id=a.id AND l.error_code<>'' ORDER BY l.request_seq DESC LIMIT 1),''),
       (SELECT l.http_status FROM gw_channel_request_logs l WHERE l.attempt_id=a.id ORDER BY l.request_seq DESC LIMIT 1)
FROM gw_api_call_attempts a
LEFT JOIN gw_product_transports pt ON pt.release_id=a.catalog_release_id AND pt.id=a.product_transport_id
LEFT JOIN gw_channel_transports ct ON ct.release_id=pt.release_id AND ct.id=pt.channel_transport_id
LEFT JOIN gw_products p ON p.release_id=pt.release_id AND p.id=pt.product_id
LEFT JOIN gateway_channels ch ON ch.id=p.channel_id
LEFT JOIN gw_credentials c ON c.id=a.credential_id
WHERE a.call_id=? ORDER BY a.attempt_no DESC LIMIT ? OFFSET ?`, id, size, (page-1)*size)
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	defer rows.Close()
	attempts := make([]gin.H, 0, size)
	for rows.Next() {
		var aid, no, ar, as, routeID, offering, credential, version, grant int64
		var ast string
		var ca, ua sql.NullTime
		var transport, channelName, credentialCode, errorCode string
		var httpStatus sql.NullInt64
		if err := rows.Scan(&aid, &no, &ast, &ar, &as, &routeID, &offering, &credential, &version, &grant, &ca, &ua,
			&transport, &channelName, &credentialCode, &errorCode, &httpStatus); err != nil {
			resp.InternalError(c, pkgErrors.ErrInternalError)
			return
		}
		attempt := gin.H{"id": aid, "attempt_no": no, "state": ast, "catalog_release_id": ar, "sku_id": as, "route_id": routeID, "offering_id": offering, "credential_id": credential, "credential_version_id": version, "purpose_grant_id": grant, "created_at": nullableTime(ca), "updated_at": nullableTime(ua),
			"transport_code": transport, "channel_name": channelName, "credential_code": credentialCode, "error_code": errorCode, "http_status": nil}
		if httpStatus.Valid {
			attempt["http_status"] = httpStatus.Int64
		}
		attempts = append(attempts, attempt)
	}
	if err := rows.Err(); err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	resp.Success(c, gin.H{"call": call, "attempts": unifiedGatewayPage{Items: attempts, Page: page, PageSize: size, Total: total}, "request_logs_available": true})
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

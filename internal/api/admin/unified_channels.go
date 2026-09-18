package admin

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/go-sql-driver/mysql"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/gateway/credentials"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/logger"
	"go.uber.org/zap"
)

func unifiedChannelError(c *gin.Context, err error) {
	var mysqlErr *mysql.MySQLError
	switch {
	case errors.Is(err, repository.ErrDuplicateCredentialSecret):
		resp.ErrorMsg(c, http.StatusConflict, 409, "密钥已存在于该渠道")
	case errors.Is(err, repository.ErrCredentialEncryptionUnavailable):
		resp.ErrorMsg(c, http.StatusServiceUnavailable, 503, "凭据加密配置不可用，无法保存密钥")
	case errors.Is(err, repository.ErrInvalidInput), errors.Is(err, repository.ErrUnknownVariant):
		resp.ErrorMsg(c, http.StatusBadRequest, 400, "配置字段无效")
	case errors.Is(err, repository.ErrNotFound), errors.Is(err, sql.ErrNoRows):
		resp.ErrorMsg(c, http.StatusNotFound, 404, "配置不存在")
	case errors.Is(err, repository.ErrConflict):
		resp.ErrorMsg(c, http.StatusConflict, 409, "配置状态已变化或仍有任务占用，请刷新后重试")
	case errors.As(err, &mysqlErr) && mysqlErr.Number == 1062:
		resp.ErrorMsg(c, http.StatusConflict, 409, "标识已存在")
	default:
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, "读取或保存网关配置失败")
	}
}

func unifiedChannelBody(c *gin.Context, value any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16384)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		unifiedChannelError(c, repository.ErrInvalidInput)
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		unifiedChannelError(c, repository.ErrInvalidInput)
		return false
	}
	return true
}

type unifiedChannelListRow struct {
	id              uint64
	code            string
	name            string
	status          string
	created         sql.NullTime
	poolCount       uint64
	credentialCount uint64
}

type unifiedChannelCapabilitySummary struct {
	productIDs   map[uint64]struct{}
	modelTypes   map[string]struct{}
	vendorModels map[string]struct{}
	protocols    map[string]struct{}
	adapters     map[string]struct{}
}

func newUnifiedChannelCapabilitySummary() *unifiedChannelCapabilitySummary {
	return &unifiedChannelCapabilitySummary{
		productIDs:   make(map[uint64]struct{}),
		modelTypes:   make(map[string]struct{}),
		vendorModels: make(map[string]struct{}),
		protocols:    make(map[string]struct{}),
		adapters:     make(map[string]struct{}),
	}
}

func unifiedChannelSortedValues(values map[string]struct{}) []string {
	items := make([]string, 0, len(values))
	for value := range values {
		items = append(items, value)
	}
	sort.Strings(items)
	return items
}

func unifiedChannelModelTypes(values map[string]struct{}) []string {
	items := make([]string, 0, len(values))
	for _, value := range []string{"llm", "image", "video", "other"} {
		if _, ok := values[value]; ok {
			items = append(items, value)
		}
	}
	return items
}

const unifiedChannelCapabilitySummarySQL = `
SELECT p.channel_id,p.id,p.product_code,p.vendor_model,
       COALESCE(cm.capability_tags,JSON_ARRAY()),COALESCE(oc.operation_code,''),
       COALESCE(adapter.adapter_code,''),COALESCE(ct.protocol,'')
FROM gw_products p
JOIN gateway_channels c ON c.id=p.channel_id
LEFT JOIN gw_product_transports pt
  ON pt.release_id=p.release_id AND pt.product_id=p.id
LEFT JOIN gw_channel_transports ct
  ON ct.release_id=pt.release_id AND ct.id=pt.channel_transport_id AND ct.channel_id=p.channel_id
LEFT JOIN gw_adapter_implementations adapter ON adapter.id=ct.adapter_implementation_id
LEFT JOIN gw_offerings offering
  ON offering.release_id=pt.release_id AND offering.product_transport_id=pt.id
LEFT JOIN gw_routes route
  ON route.release_id=offering.release_id AND route.offering_id=offering.id
LEFT JOIN gw_skus sku
  ON sku.release_id=route.release_id AND sku.id=route.sku_id
LEFT JOIN gw_model_operations model_operation
  ON model_operation.release_id=sku.release_id AND model_operation.id=sku.model_operation_id
LEFT JOIN gw_catalog_models cm
  ON cm.release_id=model_operation.release_id AND cm.id=model_operation.catalog_model_id
LEFT JOIN gw_operation_contracts oc ON oc.id=model_operation.operation_contract_id`

func UnifiedChannels(c *gin.Context) {
	page, size, ok := unifiedGatewayPagination(c)
	if !ok {
		return
	}
	search := strings.TrimSpace(c.Query("q"))
	status := strings.ToLower(strings.TrimSpace(c.Query("status")))
	modelType := strings.ToLower(strings.TrimSpace(c.Query("type")))
	if !utf8.ValidString(search) || utf8.RuneCountInString(search) > 128 ||
		status != "" && status != "active" && status != "disabled" ||
		modelType != "" && modelType != "llm" && modelType != "image" && modelType != "video" && modelType != "other" {
		unifiedChannelError(c, repository.ErrInvalidInput)
		return
	}
	db, err := model.DB().DB()
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	where := ` WHERE (?='' OR c.status=?) AND (?='' OR LOCATE(?,c.channel_code)>0 OR LOCATE(?,c.display_name)>0)`
	args := []any{status, status, search, search, search}
	ctx := c.Request.Context()
	rows, err := db.QueryContext(ctx, `SELECT c.id,c.channel_code,c.display_name,c.status,c.created_at,(SELECT COUNT(*) FROM gw_credential_pools p WHERE p.channel_id=c.id),(SELECT COUNT(*) FROM gw_credentials k WHERE k.channel_id=c.id) FROM gateway_channels c`+where+` ORDER BY c.id DESC`, args...)
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	channels := make([]unifiedChannelListRow, 0, size)
	for rows.Next() {
		var row unifiedChannelListRow
		if err := rows.Scan(&row.id, &row.code, &row.name, &row.status, &row.created, &row.poolCount, &row.credentialCount); err != nil {
			_ = rows.Close()
			unifiedChannelError(c, err)
			return
		}
		channels = append(channels, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		unifiedChannelError(c, err)
		return
	}
	_ = rows.Close()

	summaries := make(map[uint64]*unifiedChannelCapabilitySummary, len(channels))
	if len(channels) > 0 {
		summaryRows, err := db.QueryContext(ctx, unifiedChannelCapabilitySummarySQL+where+`
  AND p.release_id=(SELECT active_release_id FROM gw_catalog_runtime_state WHERE id=1)
ORDER BY p.channel_id,p.id,cm.id,oc.id`, args...)
		if err != nil {
			unifiedChannelError(c, err)
			return
		}
		for summaryRows.Next() {
			var channelID, productID uint64
			var productCode, vendorModel, operationCode, adapterCode, protocol string
			var rawTags []byte
			if err := summaryRows.Scan(&channelID, &productID, &productCode, &vendorModel, &rawTags, &operationCode, &adapterCode, &protocol); err != nil {
				_ = summaryRows.Close()
				unifiedChannelError(c, err)
				return
			}
			tags, err := decodeCatalogTags(rawTags)
			if err != nil {
				_ = summaryRows.Close()
				unifiedChannelError(c, repository.ErrConflict)
				return
			}
			summary := summaries[channelID]
			if summary == nil {
				summary = newUnifiedChannelCapabilitySummary()
				summaries[channelID] = summary
			}
			summary.productIDs[productID] = struct{}{}
			if vendorModel = strings.TrimSpace(vendorModel); vendorModel != "" {
				summary.vendorModels[vendorModel] = struct{}{}
			} else if productCode = strings.TrimSpace(productCode); productCode != "" {
				summary.vendorModels[productCode] = struct{}{}
			}
			if adapterCode = strings.TrimSpace(adapterCode); adapterCode != "" {
				summary.adapters[adapterCode] = struct{}{}
			}
			if protocol = strings.TrimSpace(protocol); protocol != "" {
				summary.protocols[protocol] = struct{}{}
			}
			entry := newUnifiedCatalogModelEntry()
			entry.capabilityTags = tags
			addCatalogModelSignal(entry.operationCodes, operationCode)
			addCatalogModelSignal(entry.adapterCodes, adapterCode)
			addCatalogModelSignal(entry.protocols, protocol)
			summary.modelTypes[classifyUnifiedCatalogModel(&entry)] = struct{}{}
		}
		if err := summaryRows.Err(); err != nil {
			_ = summaryRows.Close()
			unifiedChannelError(c, err)
			return
		}
		_ = summaryRows.Close()
	}

	filtered := make([]unifiedChannelListRow, 0, len(channels))
	for _, channel := range channels {
		summary := summaries[channel.id]
		if modelType != "" {
			if summary == nil {
				continue
			}
			if _, ok := summary.modelTypes[modelType]; !ok {
				continue
			}
		}
		filtered = append(filtered, channel)
	}
	total := int64(len(filtered))
	start := (page - 1) * size
	if start >= len(filtered) {
		filtered = filtered[:0]
	} else {
		end := start + size
		if end > len(filtered) {
			end = len(filtered)
		}
		filtered = filtered[start:end]
	}
	items := make([]gin.H, 0, len(filtered))
	for _, channel := range filtered {
		summary := summaries[channel.id]
		if summary == nil {
			summary = newUnifiedChannelCapabilitySummary()
		}
		items = append(items, gin.H{
			"id": channel.id, "channel_code": channel.code, "display_name": channel.name,
			"status": channel.status, "created_at": nullableTime(channel.created),
			"pool_count": channel.poolCount, "credential_count": channel.credentialCount,
			"model_types":   unifiedChannelModelTypes(summary.modelTypes),
			"product_count": len(summary.productIDs), "vendor_models": unifiedChannelSortedValues(summary.vendorModels),
			"protocols": unifiedChannelSortedValues(summary.protocols), "adapters": unifiedChannelSortedValues(summary.adapters),
		})
	}
	resp.Success(c, unifiedGatewayPage{Items: items, Page: page, PageSize: size, Total: total})
}

func CreateUnifiedChannel(c *gin.Context) {
	var in repository.ChannelInput
	if !unifiedChannelBody(c, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Code = strings.TrimSpace(in.Code)
	unifiedChannelWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) (uint64, error) {
		return store.CreateChannel(c.Request.Context(), tx, in, actor)
	})
}

func UpdateUnifiedChannel(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	var in repository.ChannelUpdate
	if !unifiedChannelBody(c, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	unifiedChannelWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) (uint64, error) {
		return uint64(id), store.UpdateChannel(c.Request.Context(), tx, uint64(id), in, actor)
	})
}

func unifiedChannelWrite(c *gin.Context, action func(*repository.Store, *sql.Tx, uint64) (uint64, error)) {
	var id uint64
	if !unifiedGatewayWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) error {
		var err error
		id, err = action(store, tx, actor)
		return err
	}) {
		return
	}
	resp.Success(c, gin.H{"id": id})
}

// unifiedGatewayWrite carries the plumbing every admin write shares — an
// authenticated actor, one store, one transaction, one error mapping — for rows
// whose identity is not a numeric id and so cannot be reported as one. It reports
// whether the write committed; the caller owns the success body.
func unifiedGatewayWrite(c *gin.Context, action func(*repository.Store, *sql.Tx, uint64) error) bool {
	actor := uint64(currentAdminID(c))
	if actor == 0 {
		resp.ErrorMsg(c, 403, 403, "需要管理员身份")
		return false
	}
	// A management request can arrive before the application has finished
	// opening its database.  model.DB() intentionally returns a session, so
	// guard the package-level handle before creating that session instead of
	// allowing a nil-pointer panic to escape the HTTP handler.
	if !model.HasDB() {
		resp.ErrorMsg(c, http.StatusServiceUnavailable, 503, "数据库尚未就绪")
		return false
	}
	db, err := model.DB().DB()
	if err != nil {
		unifiedChannelError(c, err)
		return false
	}
	store, err := repository.New(db)
	if err != nil {
		unifiedChannelError(c, err)
		return false
	}
	if err := store.WithTx(c.Request.Context(), func(tx *sql.Tx) error { return action(store, tx, actor) }); err != nil {
		if logger.L != nil {
			logger.Error("unified gateway write failed", zap.String("route", c.FullPath()), zap.Error(err))
		}
		unifiedChannelError(c, err)
		return false
	}
	return true
}

func UnifiedChannelPools(c *gin.Context) {
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
		unifiedChannelError(c, err)
		return
	}
	ctx := c.Request.Context()
	var total int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM gateway_channels WHERE id=?`, id).Scan(new(uint64)); err != nil {
		unifiedChannelError(c, err)
		return
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_credential_pools WHERE channel_id=?`, id).Scan(&total); err != nil {
		unifiedChannelError(c, err)
		return
	}
	rows, err := db.QueryContext(ctx, `SELECT p.id,p.pool_code,p.display_name,p.status,p.config_version,p.request_limit,p.task_limit,p.cost_group_ratio,(SELECT COUNT(*) FROM gw_credentials k WHERE k.credential_pool_id=p.id) FROM gw_credential_pools p WHERE p.channel_id=? ORDER BY p.id DESC LIMIT ? OFFSET ?`, id, size, (page-1)*size)
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0, size)
	for rows.Next() {
		var poolID, version, count uint64
		var code, name, status string
		var requests, tasks sql.NullInt64
		// The ratio is carried as the decimal string MySQL stored, not a float: it
		// is money-adjacent, and the console has to show the operator the same
		// digits it will send back on the next edit.
		var ratio string
		if err := rows.Scan(&poolID, &code, &name, &status, &version, &requests, &tasks, &ratio, &count); err != nil {
			unifiedChannelError(c, err)
			return
		}
		items = append(items, gin.H{"id": poolID, "channel_id": id, "pool_code": code, "display_name": name, "status": status, "config_version": version, "request_limit": nullableInt(requests), "task_limit": nullableInt(tasks), "cost_group_ratio": ratio, "credential_count": count})
	}
	if err := rows.Err(); err != nil {
		unifiedChannelError(c, err)
		return
	}
	resp.Success(c, unifiedGatewayPage{Items: items, Page: page, PageSize: size, Total: total})
}

func CreateUnifiedChannelPool(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	var in struct {
		Code         string  `json:"pool_code"`
		Name         string  `json:"display_name"`
		RequestLimit *uint64 `json:"request_limit"`
		TaskLimit    *uint64 `json:"task_limit"`
	}
	if !unifiedChannelBody(c, &in) {
		return
	}
	unifiedChannelWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) (uint64, error) {
		return store.CreateManagedCredentialPool(c.Request.Context(), tx, repository.PoolInput{ChannelID: uint64(id), PoolCode: strings.TrimSpace(in.Code), DisplayName: strings.TrimSpace(in.Name), RequestLimit: in.RequestLimit, TaskLimit: in.TaskLimit}, actor)
	})
}

func UpdateUnifiedPool(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	var in repository.PoolUpdate
	if !unifiedChannelBody(c, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	unifiedChannelWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) (uint64, error) {
		return uint64(id), store.UpdateCredentialPool(c.Request.Context(), tx, uint64(id), in, actor)
	})
}

func TransitionUnifiedPool(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	var in struct {
		Status          credentials.PoolState `json:"status"`
		ExpectedVersion uint64                `json:"expected_version"`
	}
	if !unifiedChannelBody(c, &in) {
		return
	}
	if in.ExpectedVersion == 0 || in.Status != credentials.PoolDraining && in.Status != credentials.PoolDisabled {
		unifiedChannelError(c, repository.ErrInvalidInput)
		return
	}
	unifiedChannelWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) (uint64, error) {
		var status credentials.PoolState
		var version uint64
		if err := tx.QueryRowContext(c.Request.Context(), `SELECT status,config_version FROM gw_credential_pools WHERE id=? FOR UPDATE`, id).Scan(&status, &version); err != nil {
			return 0, err
		}
		if version != in.ExpectedVersion || credentials.TransitionPool(status, in.Status) != nil {
			return 0, repository.ErrConflict
		}
		return uint64(id), store.TransitionCredentialPool(c.Request.Context(), tx, uint64(id), status, in.Status, "admin:"+strconv.FormatUint(actor, 10))
	})
}

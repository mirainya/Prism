package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/gateway/adapter"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/model"
)

func CreateUnifiedCatalogDraft(c *gin.Context) {
	var in repository.CatalogDraftInput
	if !unifiedChannelBody(c, &in) {
		return
	}
	in.SemanticVersion = strings.TrimSpace(in.SemanticVersion)
	in.SemanticDigest = adapter.SemanticDigest()
	unifiedChannelWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) (uint64, error) {
		return store.CreateCatalogDraft(c.Request.Context(), tx, in, actor)
	})
}

func GetUnifiedCatalogRelease(c *gin.Context) {
	releaseID, ok := catalogReleaseID(c)
	if !ok {
		return
	}
	db, err := model.DB().DB()
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	var releaseNo, version uint64
	var status, semanticVersion, contentHash, semanticDigest string
	var publishedAt, createdAt, updatedAt sql.NullTime
	err = db.QueryRowContext(c.Request.Context(), `SELECT release_no,status,config_version,semantic_version,content_hash,semantic_digest,published_at,created_at,updated_at FROM gw_catalog_releases WHERE id=?`, releaseID).Scan(&releaseNo, &status, &version, &semanticVersion, &contentHash, &semanticDigest, &publishedAt, &createdAt, &updatedAt)
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	resp.Success(c, gin.H{"id": releaseID, "release_no": releaseNo, "status": status, "config_version": version, "semantic_version": semanticVersion, "content_hash": contentHash, "semantic_digest": semanticDigest, "published_at": nullableTime(publishedAt), "created_at": nullableTime(createdAt), "updated_at": nullableTime(updatedAt)})
}

func CreateUnifiedCatalogSKU(c *gin.Context) {
	releaseID, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	var in repository.CatalogSKUInput
	if !unifiedChannelBody(c, &in) {
		return
	}
	unifiedChannelWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) (uint64, error) {
		return store.CreateCatalogSKU(c.Request.Context(), tx, uint64(releaseID), in, actor)
	})
}

func DeleteUnifiedCatalogSKU(c *gin.Context) {
	releaseID, skuID, ok := catalogResourceIDs(c, "sku_id")
	if !ok {
		return
	}
	var in struct {
		ExpectedVersion uint64 `json:"expected_version"`
	}
	if !unifiedChannelBody(c, &in) {
		return
	}
	unifiedChannelWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) (uint64, error) {
		return skuID, store.DeleteCatalogSKU(c.Request.Context(), tx, releaseID, skuID, in.ExpectedVersion, actor)
	})
}

func ListUnifiedCatalogSKUs(c *gin.Context) {
	releaseID, ok := catalogReleaseID(c)
	if !ok {
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
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_skus WHERE release_id=?`, releaseID).Scan(&total); err != nil {
		unifiedChannelError(c, err)
		return
	}
	rows, err := db.QueryContext(ctx, `SELECT s.id,s.sku_code,s.delivery_mode,s.max_results,s.idempotency_mode,s.service_tiers,m.model_code,n.api_name,cm.display_name,cm.visibility,oc.operation_code,oc.contract_version,op.http_method,op.route_template,(SELECT COUNT(*) FROM gw_sell_rates r WHERE r.release_id=s.release_id AND r.sku_id=s.id),(SELECT COUNT(*) FROM gw_routes r WHERE r.release_id=s.release_id AND r.sku_id=s.id)
FROM gw_skus s JOIN gw_model_operations mo ON mo.id=s.model_operation_id AND mo.release_id=s.release_id
JOIN gw_catalog_models cm ON cm.id=mo.catalog_model_id AND cm.release_id=mo.release_id JOIN gw_models m ON m.id=cm.model_id
JOIN gw_catalog_model_names cn ON cn.catalog_model_id=cm.id AND cn.release_id=cm.release_id AND cn.is_primary=TRUE JOIN gw_model_names n ON n.id=cn.model_name_id
JOIN gw_operation_contracts oc ON oc.id=mo.operation_contract_id JOIN gw_operation_routes op ON op.operation_contract_id=oc.id
WHERE s.release_id=? ORDER BY s.id DESC LIMIT ? OFFSET ?`, releaseID, size, (page-1)*size)
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0, size)
	for rows.Next() {
		var id, contractVersion, maxResults, sellRateCount, routeCount uint64
		var skuCode, deliveryMode, idempotencyMode, modelCode, apiName, displayName, visibility, operationCode, method, route string
		var tiers []byte
		if err := rows.Scan(&id, &skuCode, &deliveryMode, &maxResults, &idempotencyMode, &tiers, &modelCode, &apiName, &displayName, &visibility, &operationCode, &contractVersion, &method, &route, &sellRateCount, &routeCount); err != nil {
			unifiedChannelError(c, err)
			return
		}
		var serviceTiers []string
		if json.Unmarshal(tiers, &serviceTiers) != nil {
			unifiedChannelError(c, repository.ErrConflict)
			return
		}
		items = append(items, gin.H{"id": id, "sku_code": skuCode, "delivery_mode": deliveryMode, "max_results": maxResults, "idempotency_mode": idempotencyMode, "service_tiers": serviceTiers, "model_code": modelCode, "api_name": apiName, "display_name": displayName, "visibility": visibility, "operation_code": operationCode, "contract_version": contractVersion, "http_method": method, "route_template": route, "sell_rate_count": sellRateCount, "route_count": routeCount})
	}
	if err := rows.Err(); err != nil {
		unifiedChannelError(c, err)
		return
	}
	resp.Success(c, unifiedGatewayPage{Items: items, Page: page, PageSize: size, Total: total})
}

func CreateUnifiedCatalogProduct(c *gin.Context) {
	releaseID, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	var in repository.CatalogProductInput
	if !unifiedChannelBody(c, &in) {
		return
	}
	descriptor, found := adapter.DescriptorFor(strings.ToLower(strings.TrimSpace(in.AdapterCode)), in.AdapterVersion)
	if !found {
		unifiedChannelError(c, repository.ErrInvalidInput)
		return
	}
	if descriptor.Code == "generic" {
		if err := adapter.ValidateGenericVideoCatalog(in.BaseURL, in.RequestMethod, in.RequestPath, in.VendorModel, in.CapabilityConstraints); err != nil {
			unifiedChannelError(c, repository.ErrInvalidInput)
			return
		}
	} else if descriptor.Code == "seedance" {
		if err := adapter.ValidateSeedanceVideoCatalog(in.RequestMethod, in.RequestPath); err != nil {
			unifiedChannelError(c, repository.ErrInvalidInput)
			return
		}
	}
	in.Protocol = descriptor.Protocol
	in.Adapter = repository.CatalogAdapterInput{Code: descriptor.Code, Version: descriptor.Version, Protocol: descriptor.Protocol, ImplementationDigest: descriptor.ImplementationDigest, MinimumSemanticVersion: descriptor.MinimumSemanticVersion}
	unifiedChannelWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) (uint64, error) {
		return store.CreateCatalogProduct(c.Request.Context(), tx, uint64(releaseID), in, actor)
	})
}

func DeleteUnifiedCatalogProduct(c *gin.Context) {
	releaseID, productID, ok := catalogResourceIDs(c, "product_id")
	if !ok {
		return
	}
	var in struct {
		ExpectedVersion uint64 `json:"expected_version"`
	}
	if !unifiedChannelBody(c, &in) {
		return
	}
	unifiedChannelWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) (uint64, error) {
		return productID, store.DeleteCatalogProduct(c.Request.Context(), tx, releaseID, productID, in.ExpectedVersion, actor)
	})
}

func ListUnifiedCatalogProducts(c *gin.Context) {
	releaseID, ok := catalogReleaseID(c)
	if !ok {
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
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_products WHERE release_id=?`, releaseID).Scan(&total); err != nil {
		unifiedChannelError(c, err)
		return
	}
	rows, err := db.QueryContext(ctx, `SELECT p.id,p.product_code,p.vendor_model,ch.id,ch.display_name,pt.id,ct.id,ct.transport_code,ct.base_url,ct.protocol,ct.request_method,ct.request_path,pt.task_scope,pt.cancel_mode,pt.source_url_policy,a.adapter_code,a.contract_version,o.id,pool.id,pool.display_name,cp.id,cp.plan_code,(SELECT COUNT(*) FROM gw_routes r WHERE r.release_id=p.release_id AND r.offering_id=o.id),(SELECT COUNT(*) FROM gw_cost_rates r WHERE r.release_id=p.release_id AND r.cost_plan_id=cp.id),
COALESCE((SELECT cs.state FROM gw_commercial_state cs JOIN gw_commercial_validation_events ce ON ce.id=cs.latest_event_id AND ce.commercial_fingerprint=cs.commercial_fingerprint WHERE cs.commercial_fingerprint=o.commercial_fingerprint AND (ce.valid_until IS NULL OR ce.valid_until>UTC_TIMESTAMP(3))),'unknown'),
(SELECT COUNT(*) FROM gw_credentials c JOIN gw_credential_versions cv ON cv.id=c.current_version_id AND cv.credential_id=c.id AND cv.status='active' JOIN gw_credential_entitlement_state es ON es.credential_id=c.id AND es.credential_version_id=cv.id AND es.entitlement_fingerprint=o.entitlement_fingerprint AND es.state='valid' JOIN gw_credential_validation_events ee ON ee.id=es.latest_event_id AND ee.credential_id=es.credential_id AND ee.credential_version_id=es.credential_version_id AND ee.entitlement_fingerprint=es.entitlement_fingerprint AND ee.state='valid' AND ee.valid_until>UTC_TIMESTAMP(3) WHERE c.credential_pool_id=o.credential_pool_id AND c.status='active')
FROM gw_products p JOIN gateway_channels ch ON ch.id=p.channel_id JOIN gw_product_transports pt ON pt.product_id=p.id AND pt.release_id=p.release_id
JOIN gw_channel_transports ct ON ct.id=pt.channel_transport_id AND ct.release_id=pt.release_id JOIN gw_adapter_implementations a ON a.id=ct.adapter_implementation_id
JOIN gw_offerings o ON o.product_transport_id=pt.id AND o.release_id=pt.release_id JOIN gw_credential_pools pool ON pool.id=o.credential_pool_id
JOIN gw_cost_plans cp ON cp.offering_id=o.id AND cp.release_id=o.release_id WHERE p.release_id=? ORDER BY p.id DESC LIMIT ? OFFSET ?`, releaseID, size, (page-1)*size)
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0, size)
	for rows.Next() {
		var productID, channelID, productTransportID, channelTransportID, adapterVersion, offeringID, poolID, costPlanID, routeCount, rateCount, entitledCredentials uint64
		var productCode, vendorModel, channelName, transportCode, baseURL, protocol, method, path, taskScope, cancelMode, sourcePolicy, adapterCode, poolName, planCode, commercialState string
		if err := rows.Scan(&productID, &productCode, &vendorModel, &channelID, &channelName, &productTransportID, &channelTransportID, &transportCode, &baseURL, &protocol, &method, &path, &taskScope, &cancelMode, &sourcePolicy, &adapterCode, &adapterVersion, &offeringID, &poolID, &poolName, &costPlanID, &planCode, &routeCount, &rateCount, &commercialState, &entitledCredentials); err != nil {
			unifiedChannelError(c, err)
			return
		}
		items = append(items, gin.H{"id": productID, "product_code": productCode, "vendor_model": vendorModel, "channel_id": channelID, "channel_name": channelName, "product_transport_id": productTransportID, "channel_transport_id": channelTransportID, "transport_code": transportCode, "base_url": baseURL, "protocol": protocol, "request_method": method, "request_path": path, "task_scope": taskScope, "cancel_mode": cancelMode, "source_url_policy": sourcePolicy, "adapter_code": adapterCode, "adapter_version": adapterVersion, "offering_id": offeringID, "credential_pool_id": poolID, "pool_name": poolName, "cost_plan_id": costPlanID, "cost_plan_code": planCode, "route_count": routeCount, "cost_rate_count": rateCount, "commercial_state": commercialState, "entitled_credential_count": entitledCredentials})
	}
	if err := rows.Err(); err != nil {
		unifiedChannelError(c, err)
		return
	}
	resp.Success(c, unifiedGatewayPage{Items: items, Page: page, PageSize: size, Total: total})
}

func UnifiedCatalogOptions(c *gin.Context) {
	releaseID, ok := catalogReleaseID(c)
	if !ok {
		return
	}
	db, err := model.DB().DB()
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	ctx := c.Request.Context()
	channels, err := catalogOptionRows(ctx, db, `SELECT c.id,c.channel_code,c.display_name,p.id,p.pool_code,p.display_name FROM gateway_channels c JOIN gw_credential_pools p ON p.channel_id=c.id AND p.status='active' WHERE c.status='active' ORDER BY c.display_name,p.display_name`, "channel")
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	skus, err := catalogOptionRows(ctx, db, `SELECT s.id,s.sku_code,cm.display_name,0,'','' FROM gw_skus s JOIN gw_model_operations mo ON mo.id=s.model_operation_id AND mo.release_id=s.release_id JOIN gw_catalog_models cm ON cm.id=mo.catalog_model_id AND cm.release_id=mo.release_id WHERE s.release_id=? ORDER BY cm.display_name,s.sku_code`, "sku", releaseID)
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	descriptors := adapter.Descriptors()
	executionAdapters := descriptors[:0]
	for _, descriptor := range descriptors {
		if descriptor.Protocol != "catalog_discovery" {
			executionAdapters = append(executionAdapters, descriptor)
		}
	}
	resp.Success(c, gin.H{"channels": channels, "skus": skus, "adapters": executionAdapters})
}

func catalogOptionRows(ctx context.Context, db *sql.DB, query, kind string, args ...any) ([]gin.H, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var id, childID uint64
		var code, name, childCode, childName string
		if err := rows.Scan(&id, &code, &name, &childID, &childCode, &childName); err != nil {
			return nil, err
		}
		if kind == "channel" {
			items = append(items, gin.H{"id": id, "code": code, "name": name, "pool_id": childID, "pool_code": childCode, "pool_name": childName})
		} else {
			items = append(items, gin.H{"id": id, "code": code, "name": name})
		}
	}
	return items, rows.Err()
}

func catalogReleaseID(c *gin.Context) (uint64, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		unifiedChannelError(c, repository.ErrInvalidInput)
		return 0, false
	}
	return id, true
}

func catalogResourceIDs(c *gin.Context, name string) (uint64, uint64, bool) {
	releaseID, ok := catalogReleaseID(c)
	if !ok {
		return 0, 0, false
	}
	resourceID, err := strconv.ParseUint(c.Param(name), 10, 64)
	if err != nil || resourceID == 0 {
		unifiedChannelError(c, repository.ErrInvalidInput)
		return 0, 0, false
	}
	return releaseID, resourceID, true
}

func rateEvidenceHMAC(in repository.RateEvidenceInput) (string, error) {
	key, err := adminGatewayKey("PRISM_GATEWAY_HMAC_B64")
	if err != nil {
		return "", repository.ErrCredentialEncryptionUnavailable
	}
	defer clear(key)
	return repository.RateEvidenceFactHMAC(in, key)
}

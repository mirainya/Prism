package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/gateway/adapter"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/model"
)

// decodeCatalogTags accepts both the v5 string-array shape and the object
// shape written by legacy imports (for example {"source":"legacy"}).
// Keeping the object keys as stable key=value labels lets the admin views
// continue rendering old releases without discarding their metadata.
func decodeCatalogTags(raw []byte) ([]string, error) {
	if len(raw) == 0 {
		return []string{}, nil
	}
	var tags []string
	if err := json.Unmarshal(raw, &tags); err == nil {
		return tags, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	tags = make([]string, 0, len(fields))
	for key, value := range fields {
		var text string
		if err := json.Unmarshal(value, &text); err == nil && text != "" {
			tags = append(tags, key+"="+text)
		} else {
			tags = append(tags, key)
		}
	}
	sort.Strings(tags)
	return tags, nil
}

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

// ForkUnifiedCatalogRelease is retained for migration and in-process
// compatibility only. It is intentionally not registered in the admin router:
// operators edit the active configuration directly.
func ForkUnifiedCatalogRelease(c *gin.Context) {
	releaseID, ok := catalogReleaseID(c)
	if !ok {
		return
	}
	var in repository.CatalogDraftInput
	if !unifiedChannelBody(c, &in) {
		return
	}
	in.SemanticVersion = strings.TrimSpace(in.SemanticVersion)
	in.SemanticDigest = adapter.SemanticDigest()
	var draftID uint64
	if !unifiedGatewayWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) error {
		var err error
		draftID, err = store.ForkCatalogRelease(c.Request.Context(), tx, releaseID, in, actor)
		return err
	}) {
		return
	}
	resp.Success(c, gin.H{"id": draftID})
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

func UpdateUnifiedCatalogSKU(c *gin.Context) {
	releaseID, skuID, ok := catalogResourceIDs(c, "sku_id")
	if !ok {
		return
	}
	var in repository.CatalogSKUInput
	if !unifiedChannelBody(c, &in) {
		return
	}
	unifiedChannelWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) (uint64, error) {
		return skuID, store.UpdateCatalogSKU(c.Request.Context(), tx, releaseID, skuID, in, actor)
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
	rows, err := db.QueryContext(ctx, `SELECT s.id,s.sku_code,s.variant_code,s.delivery_mode,s.max_results,s.idempotency_mode,s.service_tiers,m.model_code,n.api_name,cm.display_name,cm.description,cm.visibility,COALESCE(cm.capability_tags,JSON_ARRAY()),oc.operation_code,oc.contract_version,op.http_method,op.route_template,COALESCE((SELECT JSON_ARRAYAGG(dp.path) FROM gw_sku_downstream_paths dp WHERE dp.release_id=s.release_id AND dp.sku_id=s.id),JSON_ARRAY()),(SELECT COUNT(*) FROM gw_sell_rates r WHERE r.release_id=s.release_id AND r.sku_id=s.id),(SELECT COUNT(*) FROM gw_routes r WHERE r.release_id=s.release_id AND r.sku_id=s.id)
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
		var skuCode, variantCode, deliveryMode, idempotencyMode, modelCode, apiName, displayName, description, visibility, operationCode, method, route string
		var tiers, capabilityTags, downstreamPaths []byte
		if err := rows.Scan(&id, &skuCode, &variantCode, &deliveryMode, &maxResults, &idempotencyMode, &tiers, &modelCode, &apiName, &displayName, &description, &visibility, &capabilityTags, &operationCode, &contractVersion, &method, &route, &downstreamPaths, &sellRateCount, &routeCount); err != nil {
			unifiedChannelError(c, err)
			return
		}
		var serviceTiers []string
		if json.Unmarshal(tiers, &serviceTiers) != nil {
			unifiedChannelError(c, repository.ErrConflict)
			return
		}
		tags, tagErr := decodeCatalogTags(capabilityTags)
		var paths []string
		if tagErr != nil || json.Unmarshal(downstreamPaths, &paths) != nil {
			unifiedChannelError(c, repository.ErrConflict)
			return
		}
		items = append(items, gin.H{"id": id, "sku_code": skuCode, "variant_code": variantCode, "delivery_mode": deliveryMode, "max_results": maxResults, "idempotency_mode": idempotencyMode, "service_tiers": serviceTiers, "model_code": modelCode, "api_name": apiName, "display_name": displayName, "description": description, "visibility": visibility, "capability_tags": tags, "operation_code": operationCode, "contract_version": contractVersion, "http_method": method, "route_template": route, "downstream_paths": paths, "sell_rate_count": sellRateCount, "route_count": routeCount})
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
	if err := adapter.ValidateCatalogProduct(descriptor.Code, descriptor.Version, in.BaseURL, in.RequestMethod, in.RequestPath, in.VendorModel, in.CapabilityConstraints); err != nil {
		unifiedChannelError(c, repository.ErrInvalidInput)
		return
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
	rows, err := db.QueryContext(ctx, `SELECT p.id,p.product_code,p.vendor_model,COALESCE(p.capability_constraints,JSON_OBJECT()),p.constraints_schema_version,ch.id,ch.display_name,pt.id,ct.id,ct.transport_code,ct.base_url,ct.protocol,ct.request_method,ct.request_path,pt.task_scope,pt.cancel_mode,pt.source_url_policy,a.adapter_code,a.contract_version,o.id,pool.id,pool.pool_code,pool.display_name,cp.id,cp.plan_code,(SELECT COUNT(*) FROM gw_routes r WHERE r.release_id=p.release_id AND r.offering_id=o.id),(SELECT COUNT(*) FROM gw_cost_rates r WHERE r.release_id=p.release_id AND r.cost_plan_id=cp.id),
'not_required',
(SELECT COUNT(*) FROM gw_credentials c
 JOIN gw_credential_secret_identities si ON si.id=c.secret_identity_id AND si.channel_id=c.channel_id AND si.status='active'
 JOIN gw_credential_purpose_grants g ON g.credential_id=c.id AND g.purpose='execution' AND g.status='active'
JOIN gw_credential_versions cv ON cv.id=c.current_version_id AND cv.credential_id=c.id AND cv.status='active' AND (cv.valid_until IS NULL OR cv.valid_until>CURRENT_TIMESTAMP(3))
 WHERE c.credential_pool_id=o.credential_pool_id AND c.channel_id=ch.id AND c.status='active'
 AND (c.secret IS NOT NULL AND c.secret<>'' OR EXISTS (
   SELECT 1 FROM encrypted_blobs eb
   JOIN crypto_keyring_state ks ON ks.id=eb.keyring_id
   JOIN crypto_key_versions kv ON kv.keyring_id=ks.id AND kv.key_version=ks.current_version AND kv.status='current'
   JOIN encrypted_blob_key_wraps w ON w.encrypted_blob_id=eb.id AND w.keyring_id=eb.keyring_id AND w.kek_version=ks.current_version
   WHERE eb.id=cv.encrypted_blob_id AND eb.purged_at IS NULL
 )))
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
		var productID, constraintsVersion, channelID, productTransportID, channelTransportID, adapterVersion, offeringID, poolID, costPlanID, routeCount, rateCount, entitledCredentials uint64
		var productCode, vendorModel, channelName, transportCode, baseURL, protocol, method, path, taskScope, cancelMode, sourcePolicy, adapterCode, poolCode, poolName, planCode, commercialState string
		var constraints []byte
		if err := rows.Scan(&productID, &productCode, &vendorModel, &constraints, &constraintsVersion, &channelID, &channelName, &productTransportID, &channelTransportID, &transportCode, &baseURL, &protocol, &method, &path, &taskScope, &cancelMode, &sourcePolicy, &adapterCode, &adapterVersion, &offeringID, &poolID, &poolCode, &poolName, &costPlanID, &planCode, &routeCount, &rateCount, &commercialState, &entitledCredentials); err != nil {
			unifiedChannelError(c, err)
			return
		}
		items = append(items, gin.H{"id": productID, "product_code": productCode, "vendor_model": vendorModel, "capability_constraints": json.RawMessage(constraints), "constraints_schema_version": constraintsVersion, "channel_id": channelID, "channel_name": channelName, "product_transport_id": productTransportID, "channel_transport_id": channelTransportID, "transport_code": transportCode, "base_url": baseURL, "protocol": protocol, "request_method": method, "request_path": path, "task_scope": taskScope, "cancel_mode": cancelMode, "source_url_policy": sourcePolicy, "adapter_code": adapterCode, "adapter_version": adapterVersion, "offering_id": offeringID, "credential_pool_id": poolID, "pool_code": poolCode, "pool_name": poolName, "cost_plan_id": costPlanID, "cost_plan_code": planCode, "route_count": routeCount, "cost_rate_count": rateCount, "commercial_state": commercialState, "entitled_credential_count": entitledCredentials})
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
	key, err := adminGatewayKey("PRISM_GATEWAY_PAYLOAD_HMAC_B64")
	if err != nil {
		return "", repository.ErrCredentialEncryptionUnavailable
	}
	defer clear(key)
	return repository.RateEvidenceFactHMAC(in, key)
}

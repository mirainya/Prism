package admin

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/model"
)

// The summary rows cover every catalog model before pagination. The final
// boolean mirrors the catalog, pricing and upstream eligibility required for a
// request to reach an executable offering.
const unifiedCatalogModelSignalsSQL = `
SELECT cm.id,
       COALESCE(s.id,0),COALESCE(s.sku_code,''),
       COALESCE(mn.api_name,''),COALESCE(oc.operation_code,''),COALESCE(op.route_template,''),
       COALESCE(dp.path,''),COALESCE(r.id,0),COALESCE(p.id,0),COALESCE(o.id,0),
       COALESCE(p.product_code,''),COALESCE(p.vendor_model,''),COALESCE(ch.display_name,''),
       COALESCE(a.adapter_code,''),COALESCE(ct.protocol,''),COALESCE(ct.request_path,''),
       COALESCE(pt.task_scope,''),
       CASE WHEN cm.visibility<>'hidden' AND mn.id IS NOT NULL
                  AND oc.status='active' AND op.id IS NOT NULL
                  AND s.id IS NOT NULL
                  AND EXISTS (SELECT 1 FROM gw_sell_rates sr WHERE sr.release_id=s.release_id AND sr.sku_id=s.id)
                  AND dp.id IS NOT NULL AND dp.path=op.route_template
                  AND r.id IS NOT NULL AND o.id IS NOT NULL
                  AND pt.id IS NOT NULL AND p.id IS NOT NULL AND ct.id IS NOT NULL AND a.id IS NOT NULL
                  AND ch.status='active' AND pool.status='active' AND runtime_state.state='active'
                  AND cost_plan.id IS NOT NULL
                  AND EXISTS (
                      SELECT 1
                      FROM gw_credentials credential
                      JOIN gw_credential_secret_identities secret_identity
                        ON secret_identity.id=credential.secret_identity_id
                       AND secret_identity.channel_id=credential.channel_id
                       AND secret_identity.status='active'
                      JOIN gw_credential_purpose_grants purpose_grant
                        ON purpose_grant.credential_id=credential.id
                       AND purpose_grant.purpose='execution'
                       AND purpose_grant.status='active'
                      JOIN gw_credential_versions credential_version
                        ON credential_version.id=credential.current_version_id
                       AND credential_version.credential_id=credential.id
                       AND credential_version.secret_identity_id=credential.secret_identity_id
                       AND credential_version.status='active'
	AND (credential_version.valid_until IS NULL OR credential_version.valid_until>CURRENT_TIMESTAMP(3))
                      WHERE credential.channel_id=ch.id
                        AND credential.credential_pool_id=o.credential_pool_id
                        AND credential.status='active'
                        AND (
                            credential.secret IS NOT NULL AND credential.secret<>''
                            OR EXISTS (
                                SELECT 1
                                FROM encrypted_blobs encrypted_blob
                                JOIN crypto_keyring_state keyring ON keyring.id=encrypted_blob.keyring_id
                                JOIN crypto_key_versions key_version
                                  ON key_version.keyring_id=keyring.id
                                 AND key_version.key_version=keyring.current_version
                                 AND key_version.status='current'
                                JOIN encrypted_blob_key_wraps key_wrap
                                  ON key_wrap.encrypted_blob_id=encrypted_blob.id
                                 AND key_wrap.keyring_id=encrypted_blob.keyring_id
                                 AND key_wrap.kek_version=keyring.current_version
                                WHERE encrypted_blob.id=credential_version.encrypted_blob_id
                                  AND encrypted_blob.purged_at IS NULL
                            )
                        )
                  )
            THEN TRUE ELSE FALSE END
FROM gw_catalog_models cm
LEFT JOIN gw_catalog_model_names cmn
  ON cmn.release_id=cm.release_id AND cmn.catalog_model_id=cm.id
LEFT JOIN gw_model_names mn
  ON mn.id=cmn.model_name_id AND mn.model_id=cm.model_id
LEFT JOIN gw_model_operations mo
  ON mo.release_id=cm.release_id AND mo.catalog_model_id=cm.id
LEFT JOIN gw_operation_contracts oc ON oc.id=mo.operation_contract_id
LEFT JOIN gw_operation_routes op ON op.operation_contract_id=oc.id
LEFT JOIN gw_skus s
  ON s.release_id=mo.release_id AND s.model_operation_id=mo.id
LEFT JOIN gw_sku_downstream_paths dp
  ON dp.release_id=s.release_id AND dp.sku_id=s.id
LEFT JOIN gw_routes r
  ON r.release_id=s.release_id AND r.sku_id=s.id
LEFT JOIN gw_offerings o
  ON o.release_id=r.release_id AND o.id=r.offering_id
LEFT JOIN gw_product_transports pt
  ON pt.release_id=o.release_id AND pt.id=o.product_transport_id
LEFT JOIN gw_products p
  ON p.release_id=pt.release_id AND p.id=pt.product_id
LEFT JOIN gw_channel_transports ct
  ON ct.release_id=pt.release_id AND ct.id=pt.channel_transport_id AND ct.channel_id=p.channel_id
LEFT JOIN gw_adapter_implementations a ON a.id=ct.adapter_implementation_id
LEFT JOIN gateway_channels ch ON ch.id=p.channel_id
LEFT JOIN gw_credential_pools pool
  ON pool.id=o.credential_pool_id AND pool.channel_id=ch.id
LEFT JOIN gw_offering_runtime_state runtime_state
  ON runtime_state.release_id=o.release_id AND runtime_state.offering_id=o.id
LEFT JOIN gw_cost_plans cost_plan
  ON cost_plan.release_id=o.release_id AND cost_plan.offering_id=o.id AND cost_plan.plan_code=o.cost_plan_code
WHERE cm.release_id=?
ORDER BY cm.sort_order,cm.id,s.id,r.id,o.id,mn.id`

type unifiedCatalogModelEntry struct {
	id             uint64
	modelID        uint64
	modelCode      string
	displayName    string
	description    string
	capabilityTags []string
	visibility     string
	modelType      string
	status         string
	skus           []gin.H
	products       []gin.H
	skuByID        map[uint64]struct{}
	skuIDs         map[uint64]struct{}
	productByKey   map[string]map[string]any
	searchValues   map[string]struct{}
	operationCodes map[string]struct{}
	adapterCodes   map[string]struct{}
	protocols      map[string]struct{}
	taskScopes     map[string]struct{}
	productKeys    map[string]struct{}
	serviceRoutes  map[uint64]struct{}
}

func newUnifiedCatalogModelEntry() unifiedCatalogModelEntry {
	return unifiedCatalogModelEntry{
		skus: make([]gin.H, 0), products: make([]gin.H, 0),
		skuByID: make(map[uint64]struct{}), skuIDs: make(map[uint64]struct{}),
		productByKey: make(map[string]map[string]any),
		searchValues: make(map[string]struct{}), operationCodes: make(map[string]struct{}),
		adapterCodes: make(map[string]struct{}), protocols: make(map[string]struct{}),
		taskScopes: make(map[string]struct{}), productKeys: make(map[string]struct{}),
		serviceRoutes: make(map[uint64]struct{}),
	}
}

func addCatalogModelSignal(values map[string]struct{}, value string) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value != "" {
		values[value] = struct{}{}
	}
}

func catalogModelTagType(tags []string) string {
	for _, tag := range tags {
		value := strings.ToLower(strings.TrimSpace(tag))
		if split := strings.LastIndexByte(value, '='); split >= 0 {
			value = strings.TrimSpace(value[split+1:])
		}
		switch value {
		case "video", "video_generation":
			return "video"
		case "image", "image_generation":
			return "image"
		case "llm", "chat", "text_generation", "language":
			return "llm"
		}
	}
	return ""
}

func catalogModelExplicitTagType(tags []string) string {
	for _, tag := range tags {
		value := strings.ToLower(strings.TrimSpace(tag))
		split := strings.LastIndexByte(value, '=')
		if split < 0 {
			continue
		}
		key := strings.TrimSpace(value[:split])
		if key != "type" && key != "model_type" {
			continue
		}
		if modelType := catalogModelTagType([]string{value[split+1:]}); modelType != "" {
			return modelType
		}
	}
	return ""
}

func catalogModelHasPrefix(values map[string]struct{}, prefixes ...string) bool {
	for value := range values {
		for _, prefix := range prefixes {
			if value == prefix || strings.HasPrefix(value, prefix+".") {
				return true
			}
		}
	}
	return false
}

func classifyUnifiedCatalogModel(entry *unifiedCatalogModelEntry) string {
	if tagged := catalogModelExplicitTagType(entry.capabilityTags); tagged != "" {
		return tagged
	}
	if catalogModelHasPrefix(entry.operationCodes, "video", "videos") ||
		catalogModelHasPrefix(entry.adapterCodes, "seedance") ||
		catalogModelHasPrefix(entry.protocols, "video_generation", "seedance") {
		return "video"
	}
	if catalogModelHasPrefix(entry.operationCodes, "image", "images") ||
		catalogModelHasPrefix(entry.adapterCodes, "openai_images") ||
		catalogModelHasPrefix(entry.protocols, "openai_images") {
		return "image"
	}
	if catalogModelHasPrefix(entry.operationCodes, "chat", "completion", "completions", "responses", "messages", "generate_content") ||
		catalogModelHasPrefix(entry.adapterCodes, "openai_chat", "openai_responses", "anthropic_messages", "google_generate_content", "volcengine_responses_v3") ||
		catalogModelHasPrefix(entry.protocols, "openai", "openai_responses", "anthropic_messages", "google_generate_content", "volcengine_responses_v3") {
		return "llm"
	}
	if tagged := catalogModelTagType(entry.capabilityTags); tagged != "" {
		return tagged
	}
	return "other"
}

func unifiedCatalogModelStatus(entry *unifiedCatalogModelEntry, activeRelease bool) string {
	if entry.visibility == "hidden" {
		return "disabled"
	}
	if activeRelease && len(entry.skuIDs) > 0 && len(entry.serviceRoutes) > 0 {
		return "active"
	}
	return "inactive"
}

func unifiedCatalogModelMatches(entry *unifiedCatalogModelEntry, search, modelType, status string) bool {
	if modelType != "" && entry.modelType != modelType || status != "" && entry.status != status {
		return false
	}
	if search == "" {
		return true
	}
	for value := range entry.searchValues {
		if strings.Contains(value, search) {
			return true
		}
	}
	return false
}

// ListUnifiedCatalogModelEntries returns one row per catalog model.  Releases
// remain the immutable version boundary, but are not used as model rows by the
// operations UI.  SKU and routed upstream-product data are kept as independent
// children so callers can render or filter each level without reloading every
// release on the page.
func ListUnifiedCatalogModelEntries(c *gin.Context) {
	releaseID, ok := catalogReleaseID(c)
	if !ok {
		return
	}
	page, size, ok := unifiedGatewayPagination(c)
	if !ok {
		return
	}
	search := strings.TrimSpace(c.Query("q"))
	modelType := strings.ToLower(strings.TrimSpace(c.Query("type")))
	status := strings.ToLower(strings.TrimSpace(c.Query("status")))
	if !utf8.ValidString(search) || utf8.RuneCountInString(search) > 128 ||
		modelType != "" && modelType != "llm" && modelType != "image" && modelType != "video" && modelType != "other" ||
		status != "" && status != "active" && status != "inactive" && status != "disabled" && status != "pending" && status != "failed" {
		unifiedChannelError(c, repository.ErrInvalidInput)
		return
	}
	search = strings.ToLower(search)
	db, err := model.DB().DB()
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	ctx := c.Request.Context()

	var activeRelease bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(
SELECT 1 FROM gw_catalog_runtime_state state
JOIN gw_catalog_releases release_row ON release_row.id=state.active_release_id AND release_row.status='published'
WHERE state.id=1 AND state.active_release_id=?)`, releaseID).Scan(&activeRelease); err != nil {
		unifiedChannelError(c, err)
		return
	}

	modelRows, err := db.QueryContext(ctx, `
SELECT cm.id,cm.model_id,m.model_code,cm.display_name,cm.description,
       COALESCE(cm.capability_tags,JSON_ARRAY()),cm.visibility
FROM gw_catalog_models cm
JOIN gw_models m ON m.id=cm.model_id
WHERE cm.release_id=?
ORDER BY cm.sort_order,cm.id`, releaseID)
	if err != nil {
		unifiedChannelError(c, err)
		return
	}

	entries := make([]*unifiedCatalogModelEntry, 0, size)
	byID := make(map[uint64]*unifiedCatalogModelEntry, size)
	for modelRows.Next() {
		entry := newUnifiedCatalogModelEntry()
		var tags []byte
		if err := modelRows.Scan(&entry.id, &entry.modelID, &entry.modelCode, &entry.displayName, &entry.description, &tags, &entry.visibility); err != nil {
			_ = modelRows.Close()
			unifiedChannelError(c, err)
			return
		}
		decodedTags, err := decodeCatalogTags(tags)
		if err != nil {
			_ = modelRows.Close()
			unifiedChannelError(c, repository.ErrConflict)
			return
		}
		entry.capabilityTags = decodedTags
		addCatalogModelSignal(entry.searchValues, entry.modelCode)
		addCatalogModelSignal(entry.searchValues, entry.displayName)
		addCatalogModelSignal(entry.searchValues, entry.description)
		for _, tag := range entry.capabilityTags {
			addCatalogModelSignal(entry.searchValues, tag)
		}
		item := &entry
		entries = append(entries, item)
		byID[entry.id] = item
	}
	if err := modelRows.Err(); err != nil {
		_ = modelRows.Close()
		unifiedChannelError(c, err)
		return
	}
	_ = modelRows.Close()

	if len(entries) > 0 {
		signalRows, err := db.QueryContext(ctx, unifiedCatalogModelSignalsSQL, releaseID)
		if err != nil {
			unifiedChannelError(c, err)
			return
		}
		for signalRows.Next() {
			var modelID, skuID, routeID, productID, offeringID uint64
			var skuCode, apiName, operationCode, operationRoute, downstreamPath string
			var productCode, vendorModel, channelName, adapterCode, protocol, requestPath, taskScope string
			var serviceable bool
			if err := signalRows.Scan(&modelID, &skuID, &skuCode, &apiName, &operationCode, &operationRoute,
				&downstreamPath, &routeID, &productID, &offeringID, &productCode, &vendorModel, &channelName,
				&adapterCode, &protocol, &requestPath, &taskScope, &serviceable); err != nil {
				_ = signalRows.Close()
				unifiedChannelError(c, err)
				return
			}
			entry := byID[modelID]
			if entry == nil {
				continue
			}
			for _, value := range []string{skuCode, apiName, operationCode, operationRoute, downstreamPath,
				productCode, vendorModel, channelName, adapterCode, protocol, requestPath, taskScope} {
				addCatalogModelSignal(entry.searchValues, value)
			}
			addCatalogModelSignal(entry.operationCodes, operationCode)
			addCatalogModelSignal(entry.adapterCodes, adapterCode)
			addCatalogModelSignal(entry.protocols, protocol)
			addCatalogModelSignal(entry.taskScopes, taskScope)
			if skuID > 0 {
				entry.skuIDs[skuID] = struct{}{}
			}
			if productID > 0 && offeringID > 0 {
				entry.productKeys[fmt.Sprintf("%d:%d", productID, offeringID)] = struct{}{}
			}
			if serviceable && routeID > 0 {
				entry.serviceRoutes[routeID] = struct{}{}
			}
		}
		if err := signalRows.Err(); err != nil {
			_ = signalRows.Close()
			unifiedChannelError(c, err)
			return
		}
		_ = signalRows.Close()
	}

	filtered := make([]*unifiedCatalogModelEntry, 0, len(entries))
	for _, entry := range entries {
		entry.modelType = classifyUnifiedCatalogModel(entry)
		entry.status = unifiedCatalogModelStatus(entry, activeRelease)
		if unifiedCatalogModelMatches(entry, search, modelType, status) {
			filtered = append(filtered, entry)
		}
	}
	total := int64(len(filtered))
	start := (page - 1) * size
	if start >= len(filtered) {
		entries = entries[:0]
	} else {
		end := start + size
		if end > len(filtered) {
			end = len(filtered)
		}
		entries = filtered[start:end]
	}
	byID = make(map[uint64]*unifiedCatalogModelEntry, len(entries))
	for _, entry := range entries {
		byID[entry.id] = entry
	}

	if len(entries) > 0 {
		placeholders := make([]string, len(entries))
		args := make([]any, 0, len(entries)+1)
		args = append(args, releaseID)
		for i, entry := range entries {
			placeholders[i] = "?"
			args = append(args, entry.id)
		}
		modelIDs := strings.Join(placeholders, ",")

		skuRows, err := db.QueryContext(ctx, fmt.Sprintf(`
SELECT cm.id,m.model_code,cm.display_name,cm.visibility,
       COALESCE(cm.capability_tags,JSON_ARRAY()),s.id,s.sku_code,s.variant_code,
       s.delivery_mode,s.max_results,s.idempotency_mode,s.service_tiers,COALESCE(n.api_name,m.model_code),
       oc.operation_code,oc.contract_version,COALESCE(op.http_method,''),COALESCE(op.route_template,''),
       (SELECT COUNT(*) FROM gw_sell_rates sr WHERE sr.release_id=s.release_id AND sr.sku_id=s.id),
       (SELECT COUNT(*) FROM gw_routes rr WHERE rr.release_id=s.release_id AND rr.sku_id=s.id),
       COALESCE((SELECT JSON_ARRAYAGG(dp.path)
                 FROM gw_sku_downstream_paths dp
                 WHERE dp.release_id=s.release_id AND dp.sku_id=s.id),JSON_ARRAY())
FROM gw_skus s
JOIN gw_model_operations mo ON mo.id=s.model_operation_id AND mo.release_id=s.release_id
JOIN gw_catalog_models cm ON cm.id=mo.catalog_model_id AND cm.release_id=mo.release_id
JOIN gw_models m ON m.id=cm.model_id
LEFT JOIN gw_catalog_model_names cn ON cn.catalog_model_id=cm.id AND cn.release_id=cm.release_id AND cn.is_primary=TRUE
LEFT JOIN gw_model_names n ON n.id=cn.model_name_id
JOIN gw_operation_contracts oc ON oc.id=mo.operation_contract_id
LEFT JOIN gw_operation_routes op ON op.operation_contract_id=oc.id
WHERE s.release_id=? AND cm.id IN (%s)
ORDER BY cm.id,s.id,op.id`, modelIDs), args...)
		if err != nil {
			unifiedChannelError(c, err)
			return
		}
		for skuRows.Next() {
			var modelID, skuID, maxResults, contractVersion, sellRateCount, routeCount uint64
			var modelCode, displayName, visibility, skuCode, variantCode, deliveryMode, idempotencyMode, apiName, operationCode, method, route string
			var capabilityTags, tiers, downstreamPaths []byte
			if err := skuRows.Scan(&modelID, &modelCode, &displayName, &visibility, &capabilityTags, &skuID, &skuCode, &variantCode, &deliveryMode, &maxResults, &idempotencyMode, &tiers, &apiName, &operationCode, &contractVersion, &method, &route, &sellRateCount, &routeCount, &downstreamPaths); err != nil {
				_ = skuRows.Close()
				unifiedChannelError(c, err)
				return
			}
			entry := byID[modelID]
			if entry == nil {
				continue
			}
			if _, seen := entry.skuByID[skuID]; seen {
				continue
			}
			var serviceTiers, paths []string
			tags, tagErr := decodeCatalogTags(capabilityTags)
			if json.Unmarshal(tiers, &serviceTiers) != nil || tagErr != nil || json.Unmarshal(downstreamPaths, &paths) != nil {
				_ = skuRows.Close()
				unifiedChannelError(c, repository.ErrConflict)
				return
			}
			entry.skuByID[skuID] = struct{}{}
			entry.skus = append(entry.skus, gin.H{
				"id": skuID, "sku_code": skuCode, "variant_code": variantCode,
				"delivery_mode": deliveryMode, "max_results": maxResults,
				"idempotency_mode": idempotencyMode, "service_tiers": serviceTiers,
				"model_code": modelCode, "api_name": apiName, "display_name": displayName,
				"description": entry.description,
				"visibility":  visibility, "capability_tags": tags,
				"operation_code":   operationCode,
				"contract_version": contractVersion, "http_method": method,
				"route_template": route, "downstream_paths": paths,
				"sell_rate_count": sellRateCount, "route_count": routeCount,
			})
		}
		if err := skuRows.Err(); err != nil {
			_ = skuRows.Close()
			unifiedChannelError(c, err)
			return
		}
		_ = skuRows.Close()

		productRows, err := db.QueryContext(ctx, fmt.Sprintf(`
SELECT cm.id,p.id,p.product_code,p.vendor_model,
       COALESCE(p.capability_constraints,JSON_OBJECT()),p.constraints_schema_version,
       ch.id,ch.display_name,pt.id,pt.task_scope,pt.cancel_mode,pt.source_url_policy,
       ct.id,ct.transport_code,ct.base_url,ct.protocol,ct.request_method,ct.request_path,
       a.adapter_code,a.contract_version,o.id,runtime_state.state,runtime_state.state_version,
       pool.id,pool.pool_code,pool.display_name,
       COALESCE(cp.id,0),COALESCE(cp.plan_code,''),
       (SELECT COUNT(*) FROM gw_routes rr WHERE rr.release_id=o.release_id AND rr.offering_id=o.id),
       (SELECT COUNT(*) FROM gw_cost_rates cr WHERE cr.release_id=o.release_id AND cr.cost_plan_id=cp.id),
       r.sku_id,r.priority,r.weight
FROM gw_routes r
JOIN gw_skus s ON s.release_id=r.release_id AND s.id=r.sku_id
JOIN gw_model_operations mo ON mo.release_id=s.release_id AND mo.id=s.model_operation_id
JOIN gw_catalog_models cm ON cm.release_id=mo.release_id AND cm.id=mo.catalog_model_id
JOIN gw_offerings o ON o.release_id=r.release_id AND o.id=r.offering_id
JOIN gw_offering_runtime_state runtime_state ON runtime_state.release_id=o.release_id AND runtime_state.offering_id=o.id
JOIN gw_product_transports pt ON pt.release_id=o.release_id AND pt.id=o.product_transport_id
JOIN gw_products p ON p.release_id=pt.release_id AND p.id=pt.product_id
JOIN gateway_channels ch ON ch.id=p.channel_id
JOIN gw_channel_transports ct ON ct.release_id=pt.release_id AND ct.id=pt.channel_transport_id
JOIN gw_adapter_implementations a ON a.id=ct.adapter_implementation_id
JOIN gw_credential_pools pool ON pool.id=o.credential_pool_id
LEFT JOIN gw_cost_plans cp ON cp.release_id=o.release_id AND cp.offering_id=o.id AND cp.plan_code=o.cost_plan_code
WHERE r.release_id=? AND cm.id IN (%s)
ORDER BY cm.id,p.id,o.id,r.sku_id`, modelIDs), args...)
		if err != nil {
			unifiedChannelError(c, err)
			return
		}
		for productRows.Next() {
			var modelID, productID, constraintsVersion, channelID, productTransportID, channelTransportID, adapterVersion, offeringID, offeringStateVersion, poolID, costPlanID, routeCount, costRateCount, skuID, routePriority, routeWeight uint64
			var productCode, vendorModel, channelName, taskScope, cancelMode, sourcePolicy, transportCode, baseURL, protocol, method, path, adapterCode, offeringState, poolCode, poolName string
			var costPlanCode string
			var constraints []byte
			if err := productRows.Scan(&modelID, &productID, &productCode, &vendorModel, &constraints, &constraintsVersion, &channelID, &channelName, &productTransportID, &taskScope, &cancelMode, &sourcePolicy, &channelTransportID, &transportCode, &baseURL, &protocol, &method, &path, &adapterCode, &adapterVersion, &offeringID, &offeringState, &offeringStateVersion, &poolID, &poolCode, &poolName, &costPlanID, &costPlanCode, &routeCount, &costRateCount, &skuID, &routePriority, &routeWeight); err != nil {
				_ = productRows.Close()
				unifiedChannelError(c, err)
				return
			}
			entry := byID[modelID]
			if entry == nil {
				continue
			}
			key := fmt.Sprintf("%d:%d", productID, offeringID)
			product := entry.productByKey[key]
			if product == nil {
				product = map[string]any{
					"id": productID, "product_code": productCode, "vendor_model": vendorModel,
					"capability_constraints":     json.RawMessage(constraints),
					"constraints_schema_version": constraintsVersion, "channel_id": channelID,
					"channel_name": channelName, "product_transport_id": productTransportID,
					"channel_transport_id": channelTransportID, "transport_code": transportCode,
					"base_url": baseURL, "protocol": protocol, "request_method": method,
					"request_path": path, "task_scope": taskScope, "cancel_mode": cancelMode,
					"source_url_policy": sourcePolicy, "adapter_code": adapterCode,
					"adapter_version": adapterVersion, "offering_id": offeringID,
					"offering_state": offeringState, "offering_state_version": offeringStateVersion,
					"credential_pool_id": poolID, "pool_code": poolCode, "pool_name": poolName,
					"cost_plan_id": costPlanID, "cost_plan_code": costPlanCode,
					"route_count": routeCount, "cost_rate_count": costRateCount,
					"commercial_state": "unknown", "entitled_credential_count": 0,
					"sku_ids": make([]uint64, 0, 1),
					// One row per route, not per product: priority and weight are
					// properties of the (offering, sku) pair. Without them the console
					// can address a route-weight change but cannot show what it is
					// changing from.
					"routes": make([]gin.H, 0, 1),
				}
				entry.productByKey[key] = product
				entry.products = append(entry.products, product)
			}
			ids := product["sku_ids"].([]uint64)
			found := false
			for _, id := range ids {
				if id == skuID {
					found = true
					break
				}
			}
			if !found {
				product["sku_ids"] = append(ids, skuID)
				product["routes"] = append(product["routes"].([]gin.H), gin.H{"sku_id": skuID, "priority": routePriority, "weight": routeWeight})
			}
		}
		if err := productRows.Err(); err != nil {
			_ = productRows.Close()
			unifiedChannelError(c, err)
			return
		}
		_ = productRows.Close()
	}

	items := make([]gin.H, 0, len(entries))
	for _, entry := range entries {
		items = append(items, gin.H{
			"id": entry.id, "release_id": releaseID, "model_id": entry.modelID,
			"model_code": entry.modelCode, "display_name": entry.displayName,
			"description": entry.description, "capability_tags": entry.capabilityTags,
			"visibility": entry.visibility, "status": entry.status, "model_type": entry.modelType,
			"sku_count": len(entry.skuIDs), "product_count": len(entry.productKeys), "skus": entry.skus,
			"products": entry.products,
		})
	}
	resp.Success(c, unifiedGatewayPage{Items: items, Page: page, PageSize: size, Total: total})
}

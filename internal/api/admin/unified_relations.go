package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/model"
	pkgErrors "github.com/mirainya/Prism/pkg/errors"
	"github.com/mirainya/Prism/pkg/logger"
	"go.uber.org/zap"
)

// A key does not point at a model. The real chain is
//
//	credential → pool ← offering → route → sku → model_operation → catalog_model → model_name
//
// which no single endpoint could walk, so the console answered "which keys serve
// this model?" with an N+1 fan-out from the browser and could not answer "which
// models does this key serve?" at all.
//
// One query walks it in both directions. The joins that decide whether a link is
// actually live are LEFT JOINs on purpose: the selector uses INNER JOINs and
// makes a broken link vanish, and a link that vanishes is exactly the one the
// operator needs to see.

const unifiedRelationSQL = `
SELECT mn.api_name, cm.display_name, cm.visibility,
       COALESCE(cm.capability_tags,JSON_ARRAY()),COALESCE(oc.operation_code,''),
       sku.id, sku.sku_code, sku.delivery_mode,
       r.id, r.priority, r.weight,
       o.id, COALESCE(ors.state,''),
	   c.id, c.credential_code, c.status, c.config_version, c.weight, c.request_limit, c.task_limit,
       pool.id, pool.pool_code, pool.display_name, pool.status,
       ch.id, ch.display_name, ch.status, ct.transport_code, p.product_code, p.vendor_model,
	   p.id, pt.id, ct.id, adapter.adapter_code, adapter.contract_version,
	   ct.base_url, ct.protocol, ct.request_method, ct.request_path, pt.task_scope,
	   COALESCE(p.capability_constraints,JSON_OBJECT()),
       TRUE, TRUE,
       si.id IS NOT NULL, g.id IS NOT NULL, cv.id IS NOT NULL,
		COALESCE(TIMESTAMPDIFF(SECOND,CURRENT_TIMESTAMP(3),rs.disabled_until),0)
FROM gw_catalog_releases rel
JOIN gw_catalog_models cm ON cm.release_id=rel.id
JOIN gw_catalog_model_names cmn ON cmn.release_id=cm.release_id AND cmn.catalog_model_id=cm.id
JOIN gw_model_names mn ON mn.id=cmn.model_name_id AND mn.model_id=cm.model_id
JOIN gw_model_operations mo ON mo.release_id=cm.release_id AND mo.catalog_model_id=cm.id
JOIN gw_operation_contracts oc ON oc.id=mo.operation_contract_id
JOIN gw_skus sku ON sku.release_id=mo.release_id AND sku.model_operation_id=mo.id
JOIN gw_routes r ON r.release_id=sku.release_id AND r.sku_id=sku.id
JOIN gw_offerings o ON o.release_id=r.release_id AND o.id=r.offering_id
JOIN gw_product_transports pt ON pt.release_id=o.release_id AND pt.id=o.product_transport_id
JOIN gw_products p ON p.release_id=pt.release_id AND p.id=pt.product_id
JOIN gw_channel_transports ct ON ct.release_id=pt.release_id AND ct.id=pt.channel_transport_id
JOIN gw_adapter_implementations adapter ON adapter.id=ct.adapter_implementation_id
JOIN gateway_channels ch ON ch.id=p.channel_id
JOIN gw_credential_pools pool ON pool.id=o.credential_pool_id AND pool.channel_id=ch.id
JOIN gw_credentials c ON c.channel_id=ch.id AND c.credential_pool_id=o.credential_pool_id
LEFT JOIN gw_offering_runtime_state ors ON ors.release_id=o.release_id AND ors.offering_id=o.id
LEFT JOIN gw_credential_secret_identities si ON si.id=c.secret_identity_id AND si.channel_id=c.channel_id AND si.status='active'
LEFT JOIN gw_credential_purpose_grants g ON g.credential_id=c.id AND g.purpose='execution' AND g.status='active'
LEFT JOIN gw_credential_versions cv ON cv.credential_id=c.id AND cv.id=c.current_version_id AND cv.status='active' AND (cv.valid_until IS NULL OR cv.valid_until>CURRENT_TIMESTAMP(3))
LEFT JOIN gw_route_states rs ON rs.key_id=(c.id | ?)
  AND rs.model_name COLLATE utf8mb4_unicode_ci=mn.api_name COLLATE utf8mb4_unicode_ci
  AND rs.transport COLLATE utf8mb4_unicode_ci=ct.transport_code COLLATE utf8mb4_unicode_ci
	AND rs.disabled_until>CURRENT_TIMESTAMP(3)
WHERE rel.id=? AND `

type unifiedRelationLink struct {
	APIName       string `json:"api_name"`
	DisplayName   string `json:"display_name"`
	Visibility    string `json:"visibility"`
	ModelType     string `json:"model_type"`
	SKUID         uint64 `json:"sku_id"`
	SKUCode       string `json:"sku_code"`
	DeliveryMode  string `json:"delivery_mode"`
	RouteID       uint64 `json:"route_id"`
	Priority      int64  `json:"priority"`
	RouteWeight   uint64 `json:"route_weight"`
	OfferingID    uint64 `json:"offering_id"`
	OfferingState string `json:"offering_state"`

	CredentialID      uint64 `json:"credential_id"`
	CredentialCode    string `json:"credential_code"`
	CredentialStatus  string `json:"credential_status"`
	CredentialVersion uint64 `json:"credential_config_version"`
	CredentialWeight  uint64 `json:"credential_weight"`
	// Two-level concurrency caps. NULL means unlimited, which the console has to
	// be able to tell apart from a cap of zero, so they stay nullable on the wire.
	RequestLimit *int64 `json:"request_limit"`
	TaskLimit    *int64 `json:"task_limit"`

	PoolID     uint64 `json:"credential_pool_id"`
	PoolCode   string `json:"pool_code"`
	PoolName   string `json:"pool_name"`
	PoolStatus string `json:"pool_status"`

	ChannelID     uint64 `json:"channel_id"`
	ChannelName   string `json:"channel_name"`
	ChannelStatus string `json:"channel_status"`
	TransportCode string `json:"transport_code"`
	ProductCode   string `json:"product_code"`
	VendorModel   string `json:"vendor_model"`

	ProductID          uint64 `json:"product_id"`
	ProductTransportID uint64 `json:"product_transport_id"`
	ChannelTransportID uint64 `json:"channel_transport_id"`
	AdapterCode        string `json:"adapter_code"`
	AdapterVersion     uint32 `json:"adapter_version"`
	BaseURL            string `json:"base_url"`
	Protocol           string `json:"protocol"`
	RequestMethod      string `json:"request_method"`
	RequestPath        string `json:"request_path"`
	TaskScope          string `json:"task_scope"`
	// CapabilityConstraints is the product's adapter configuration and mapping
	// document. Catalog writes reject secrets and commercial fields before this
	// JSON reaches gw_products, so it is safe to expose to an administrator.
	CapabilityConstraints json.RawMessage         `json:"capability_constraints"`
	Actions               []unifiedRelationAction `json:"actions"`

	CommercialValid  bool  `json:"commercial_valid"`
	EntitlementValid bool  `json:"entitlement_valid"`
	SecretActive     bool  `json:"secret_identity_active"`
	ExecutionGranted bool  `json:"execution_granted"`
	VersionUsable    bool  `json:"credential_version_usable"`
	BreakerSeconds   int64 `json:"breaker_seconds"`
	// Serving is the single answer the page exists to give: would the selector
	// pick this link right now. It mirrors the INNER JOINs in routing/unified.go.
	Serving bool     `json:"serving"`
	Blocked []string `json:"blocked_by,omitempty"`
}

type unifiedRelationAction struct {
	ActionCode            string `json:"action_code"`
	AllowedSourceState    string `json:"allowed_source_state"`
	IdempotencyMode       string `json:"idempotency_mode"`
	RequestSchemaVersion  uint32 `json:"request_schema_version"`
	ResponseSchemaVersion uint32 `json:"response_schema_version"`
}

func queryUnifiedRelations(c *gin.Context, condition string, args ...any) {
	db, err := model.DB().DB()
	if err != nil {
		logUnifiedRelationError("database", err)
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	ctx := c.Request.Context()
	var releaseID sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT active_release_id FROM gw_catalog_runtime_state WHERE id=1`).Scan(&releaseID); err != nil && err != sql.ErrNoRows {
		logUnifiedRelationError("active_release", err)
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	if !releaseID.Valid || releaseID.Int64 <= 0 {
		// Nothing is active, so nothing serves anything. Say so rather than
		// returning an empty list that reads like a configuration mistake.
		resp.Success(c, gin.H{"items": []unifiedRelationLink{}, "active_release_id": nil})
		return
	}
	full := append([]any{int64(routing.CircuitKeyMask), releaseID.Int64}, args...)
	rows, err := db.QueryContext(ctx, unifiedRelationSQL+condition+` ORDER BY mn.api_name, r.priority DESC, c.id`, full...)
	if err != nil {
		logUnifiedRelationError("query", err)
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	defer rows.Close()
	items := make([]unifiedRelationLink, 0, 32)
	for rows.Next() {
		link := unifiedRelationLink{Actions: make([]unifiedRelationAction, 0)}
		var requestLimit, taskLimit sql.NullInt64
		var capabilityConstraints []byte
		var capabilityTags []byte
		var operationCode string
		if err := rows.Scan(&link.APIName, &link.DisplayName, &link.Visibility, &capabilityTags, &operationCode,
			&link.SKUID, &link.SKUCode, &link.DeliveryMode,
			&link.RouteID, &link.Priority, &link.RouteWeight,
			&link.OfferingID, &link.OfferingState,
			&link.CredentialID, &link.CredentialCode, &link.CredentialStatus, &link.CredentialVersion, &link.CredentialWeight, &requestLimit, &taskLimit,
			&link.PoolID, &link.PoolCode, &link.PoolName, &link.PoolStatus,
			&link.ChannelID, &link.ChannelName, &link.ChannelStatus, &link.TransportCode, &link.ProductCode, &link.VendorModel,
			&link.ProductID, &link.ProductTransportID, &link.ChannelTransportID, &link.AdapterCode, &link.AdapterVersion,
			&link.BaseURL, &link.Protocol, &link.RequestMethod, &link.RequestPath, &link.TaskScope, &capabilityConstraints,
			&link.CommercialValid, &link.EntitlementValid,
			&link.SecretActive, &link.ExecutionGranted, &link.VersionUsable,
			&link.BreakerSeconds); err != nil {
			logUnifiedRelationError("scan", err)
			resp.InternalError(c, pkgErrors.ErrInternalError)
			return
		}
		if requestLimit.Valid {
			link.RequestLimit = &requestLimit.Int64
		}
		if taskLimit.Valid {
			link.TaskLimit = &taskLimit.Int64
		}
		link.CapabilityConstraints = append(json.RawMessage(nil), capabilityConstraints...)
		tags, err := decodeCatalogTags(capabilityTags)
		if err != nil {
			logUnifiedRelationError("capability_tags", err)
			resp.InternalError(c, pkgErrors.ErrInternalError)
			return
		}
		entry := newUnifiedCatalogModelEntry()
		entry.capabilityTags = tags
		addCatalogModelSignal(entry.operationCodes, operationCode)
		addCatalogModelSignal(entry.adapterCodes, link.AdapterCode)
		addCatalogModelSignal(entry.protocols, link.Protocol)
		link.ModelType = classifyUnifiedCatalogModel(&entry)
		if link.OfferingState != "active" {
			link.Blocked = append(link.Blocked, "offering_"+orUnknown(link.OfferingState))
		}
		if link.CredentialStatus != "active" {
			link.Blocked = append(link.Blocked, "credential_"+orUnknown(link.CredentialStatus))
		}
		if link.PoolStatus != "active" {
			link.Blocked = append(link.Blocked, "pool_"+orUnknown(link.PoolStatus))
		}
		if link.ChannelStatus != "active" {
			link.Blocked = append(link.Blocked, "channel_"+orUnknown(link.ChannelStatus))
		}
		if !link.SecretActive {
			link.Blocked = append(link.Blocked, "secret_identity_inactive")
		}
		if !link.ExecutionGranted {
			link.Blocked = append(link.Blocked, "execution_grant_missing")
		}
		if !link.VersionUsable {
			link.Blocked = append(link.Blocked, "credential_version_unusable")
		}
		if link.BreakerSeconds > 0 {
			link.Blocked = append(link.Blocked, "circuit_broken")
		}
		link.Serving = len(link.Blocked) == 0
		items = append(items, link)
	}
	if err := rows.Err(); err != nil {
		logUnifiedRelationError("rows", err)
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	if err := rows.Close(); err != nil {
		logUnifiedRelationError("close", err)
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	if err := appendUnifiedRelationActions(ctx, db, releaseID.Int64, items); err != nil {
		logUnifiedRelationError("actions", err)
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	serving := 0
	for _, item := range items {
		if item.Serving {
			serving++
		}
	}
	resp.Success(c, gin.H{"items": items, "active_release_id": releaseID.Int64, "total": len(items), "serving": serving})
}

// appendUnifiedRelationActions loads the one-to-many action contract in one
// query. Joining it into unifiedRelationSQL would duplicate every credential
// and route row once per action and make the relationship counts misleading.
func appendUnifiedRelationActions(ctx context.Context, db *sql.DB, releaseID int64, items []unifiedRelationLink) error {
	if len(items) == 0 {
		return nil
	}
	byTransport := make(map[uint64][]int, len(items))
	args := make([]any, 0, len(items)+1)
	args = append(args, releaseID)
	for index := range items {
		id := items[index].ProductTransportID
		if _, exists := byTransport[id]; !exists {
			args = append(args, id)
		}
		byTransport[id] = append(byTransport[id], index)
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(byTransport)), ",")
	rows, err := db.QueryContext(ctx, `SELECT product_transport_id,action_code,allowed_source_state,idempotency_mode,request_schema_version,response_schema_version
FROM gw_product_transport_actions
WHERE release_id=? AND product_transport_id IN (`+placeholders+`)
ORDER BY product_transport_id,action_code`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var productTransportID uint64
		var action unifiedRelationAction
		if err := rows.Scan(&productTransportID, &action.ActionCode, &action.AllowedSourceState, &action.IdempotencyMode, &action.RequestSchemaVersion, &action.ResponseSchemaVersion); err != nil {
			return err
		}
		for _, index := range byTransport[productTransportID] {
			items[index].Actions = append(items[index].Actions, action)
		}
	}
	return rows.Err()
}

func logUnifiedRelationError(stage string, err error) {
	logger.Error("unified relation query failed", zap.String("stage", stage), zap.Error(err))
}

func orUnknown(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unset"
	}
	return value
}

// ListUnifiedCredentialModels answers "what does this key actually serve?".
func ListUnifiedCredentialModels(c *gin.Context) {
	credentialID, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	queryUnifiedRelations(c, `c.id=?`, int64(credentialID))
}

// ListUnifiedChannelRelations returns every current model, credential and
// route chain backed by one upstream channel.
func ListUnifiedChannelRelations(c *gin.Context) {
	channelID, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	queryUnifiedRelations(c, `ch.id=?`, int64(channelID))
}

// ListUnifiedModelCredentials answers the reverse, replacing the browser-side
// N+1 the ops console used to run to build the same table.
//
// The model name arrives as a query parameter rather than a path segment: API
// names are allowed to contain "/" (catalogIdentityPattern permits it, and
// vendor-prefixed names use it), which no path segment can carry.
func ListUnifiedModelCredentials(c *gin.Context) {
	name := strings.TrimSpace(c.Query("model_name"))
	if name == "" {
		unifiedChannelError(c, repository.ErrInvalidInput)
		return
	}
	queryUnifiedRelations(c, `mn.api_name=?`, name)
}

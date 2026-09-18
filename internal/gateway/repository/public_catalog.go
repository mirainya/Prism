package repository

import (
	"context"
	"database/sql"
)

// PublicCatalogRoute is one executable route in the active immutable catalog.
// It deliberately excludes credentials and secret-bearing transport fields.
type PublicCatalogRoute struct {
	ReleaseID             uint64
	CatalogModelID        uint64
	ModelID               uint64
	SortOrder             int
	ModelCode             string
	APIName               string
	IsPrimary             bool
	DisplayName           string
	Description           string
	Visibility            string
	CapabilityTags        []byte
	OperationCode         string
	HTTPMethod            string
	RouteTemplate         string
	DownstreamOperation   string
	DownstreamHTTPMethod  string
	DownstreamPath        string
	SKUID                 uint64
	SKUCode               string
	DeliveryMode          string
	MaxResults            uint32
	IdempotencyMode       string
	ServiceTiers          []byte
	ChannelID             uint64
	ChannelCode           string
	ChannelName           string
	VendorModel           string
	Protocol              string
	TaskScope             string
	CapabilityConstraints []byte
	CancelMode            string
}

// activePublicCatalogRoutesSQL mirrors the eligibility joins used by the data
// plane. The credential EXISTS clause prevents eligible credentials from
// multiplying public routes; sell-rate components are loaded separately.
const activePublicCatalogRoutesSQL = `
SELECT rel.id,cm.id,cm.model_id,cm.sort_order,m.model_code,mn.api_name,cmn.is_primary,
	       cm.display_name,cm.description,cm.visibility,COALESCE(cm.capability_tags,'[]'),
       oc.operation_code,op.http_method,op.route_template,
	       downstream_oc.operation_code,downstream_route.http_method,downstream.path,
       sku.id,sku.sku_code,sku.delivery_mode,sku.max_results,sku.idempotency_mode,sku.service_tiers,
	       ch.id,ch.channel_code,ch.display_name,p.vendor_model,ct.protocol,pt.task_scope,
	       COALESCE(p.capability_constraints,'{}'),COALESCE(pt.cancel_mode,'none')
FROM gw_catalog_runtime_state state
JOIN gw_catalog_releases rel ON rel.id=state.active_release_id AND rel.status='published'
JOIN gw_catalog_models cm ON cm.release_id=rel.id AND cm.visibility<>'hidden'
JOIN gw_models m ON m.id=cm.model_id
JOIN gw_catalog_model_names cmn ON cmn.release_id=cm.release_id AND cmn.catalog_model_id=cm.id AND cmn.model_id=cm.model_id AND cmn.is_primary=TRUE
JOIN gw_model_names mn ON mn.id=cmn.model_name_id AND mn.model_id=cm.model_id
JOIN gw_model_operations mo ON mo.release_id=cm.release_id AND mo.catalog_model_id=cm.id
JOIN gw_operation_contracts oc ON oc.id=mo.operation_contract_id AND oc.status='active'
JOIN gw_operation_routes op ON op.operation_contract_id=oc.id
JOIN gw_skus sku ON sku.release_id=mo.release_id AND sku.model_operation_id=mo.id
JOIN gw_sku_downstream_paths downstream ON downstream.release_id=sku.release_id AND downstream.sku_id=sku.id
JOIN gw_operation_routes downstream_route ON downstream_route.http_method=op.http_method AND downstream_route.route_template=downstream.path
JOIN gw_operation_contracts downstream_oc ON downstream_oc.id=downstream_route.operation_contract_id AND downstream_oc.status='active'
JOIN gw_routes route ON route.release_id=sku.release_id AND route.sku_id=sku.id
JOIN gw_offerings offering ON offering.release_id=route.release_id AND offering.id=route.offering_id
JOIN gw_offering_runtime_state runtime_state ON runtime_state.release_id=offering.release_id AND runtime_state.offering_id=offering.id AND runtime_state.state='active'
JOIN gw_product_transports pt ON pt.release_id=offering.release_id AND pt.id=offering.product_transport_id
JOIN gw_products p ON p.release_id=pt.release_id AND p.id=pt.product_id
JOIN gw_channel_transports ct ON ct.release_id=pt.release_id AND ct.id=pt.channel_transport_id AND ct.channel_id=p.channel_id
JOIN gateway_channels ch ON ch.id=p.channel_id AND ch.status='active'
WHERE state.id=1
AND (
	downstream_oc.operation_code=oc.operation_code
	OR NOT EXISTS (
		SELECT 1
		FROM gw_model_operations native_mo
		JOIN gw_operation_contracts native_contract
		  ON native_contract.id=native_mo.operation_contract_id
		 AND native_contract.status='active'
		 AND native_contract.operation_code=downstream_oc.operation_code
		JOIN gw_skus native_sku
		  ON native_sku.release_id=native_mo.release_id
		 AND native_sku.model_operation_id=native_mo.id
		JOIN gw_sku_downstream_paths native_downstream
		  ON native_downstream.release_id=native_sku.release_id
		 AND native_downstream.sku_id=native_sku.id
		 AND native_downstream.path=downstream.path
		WHERE native_mo.release_id=mo.release_id
		  AND native_mo.catalog_model_id=mo.catalog_model_id
	)
)
AND EXISTS (
	SELECT 1
	FROM gw_credential_pools pool
	JOIN gw_credentials credential ON credential.channel_id=ch.id AND credential.credential_pool_id=pool.id AND credential.status='active'
	JOIN gw_credential_secret_identities secret_identity ON secret_identity.id=credential.secret_identity_id AND secret_identity.channel_id=credential.channel_id AND secret_identity.status='active'
	JOIN gw_credential_purpose_grants purpose_grant ON purpose_grant.credential_id=credential.id AND purpose_grant.purpose='execution' AND purpose_grant.status='active'
	JOIN gw_credential_versions credential_version ON credential_version.id=credential.current_version_id AND credential_version.credential_id=credential.id AND credential_version.secret_identity_id=credential.secret_identity_id AND credential_version.status='active' AND (credential_version.valid_until IS NULL OR credential_version.valid_until>CURRENT_TIMESTAMP)
	WHERE pool.id=offering.credential_pool_id AND pool.channel_id=ch.id AND pool.status='active'
	  AND (
		credential.secret IS NOT NULL AND credential.secret<>''
		OR EXISTS (
			SELECT 1
			FROM encrypted_blobs encrypted_blob
			JOIN crypto_keyring_state keyring ON keyring.id=encrypted_blob.keyring_id
			JOIN crypto_key_versions key_version ON key_version.keyring_id=keyring.id AND key_version.key_version=keyring.current_version AND key_version.status='current'
			JOIN encrypted_blob_key_wraps key_wrap ON key_wrap.encrypted_blob_id=encrypted_blob.id AND key_wrap.keyring_id=encrypted_blob.keyring_id AND key_wrap.kek_version=keyring.current_version
			WHERE encrypted_blob.id=credential_version.encrypted_blob_id AND encrypted_blob.purged_at IS NULL
		)
	  )
)
ORDER BY cm.sort_order,mn.api_name,downstream_oc.operation_code,downstream_route.http_method,downstream.path,sku.id,ch.channel_code,p.vendor_model`

func (s *Store) ListActivePublicCatalog(ctx context.Context) ([]PublicCatalogRoute, error) {
	if s == nil || s.db == nil || ctx == nil {
		return nil, ErrInvalidInput
	}
	rows, err := s.db.QueryContext(ctx, activePublicCatalogRoutesSQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]PublicCatalogRoute, 0)
	for rows.Next() {
		var row PublicCatalogRoute
		if err := rows.Scan(
			&row.ReleaseID, &row.CatalogModelID, &row.ModelID, &row.SortOrder,
			&row.ModelCode, &row.APIName, &row.IsPrimary, &row.DisplayName,
			&row.Description, &row.Visibility, &row.CapabilityTags, &row.OperationCode,
			&row.HTTPMethod, &row.RouteTemplate, &row.DownstreamOperation,
			&row.DownstreamHTTPMethod, &row.DownstreamPath, &row.SKUID, &row.SKUCode,
			&row.DeliveryMode, &row.MaxResults, &row.IdempotencyMode,
			&row.ServiceTiers, &row.ChannelID, &row.ChannelCode, &row.ChannelName,
			&row.VendorModel, &row.Protocol, &row.TaskScope, &row.CapabilityConstraints,
			&row.CancelMode,
		); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return result, nil
}

// PublicCatalogReleaseID returns the active published release, or zero when no
// catalog is active. It never consults legacy configuration tables.
func (s *Store) PublicCatalogReleaseID(ctx context.Context) (uint64, error) {
	if s == nil || s.db == nil || ctx == nil {
		return 0, ErrInvalidInput
	}
	var releaseID sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT rel.id FROM gw_catalog_runtime_state state JOIN gw_catalog_releases rel ON rel.id=state.active_release_id AND rel.status='published' WHERE state.id=1`).Scan(&releaseID)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !releaseID.Valid || releaseID.Int64 <= 0 {
		return 0, nil
	}
	return uint64(releaseID.Int64), nil
}

package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"hash"
)

var catalogDigestQueries = []string{
	`SELECT cm.id,m.model_code,cm.display_name,cm.description,COALESCE(cm.capability_tags,'null'),cm.sort_order,cm.visibility FROM gw_catalog_models cm JOIN gw_models m ON m.id=cm.model_id WHERE cm.release_id=? ORDER BY cm.id`,
	`SELECT n.id,n.catalog_model_id,mn.api_name,n.is_primary FROM gw_catalog_model_names n JOIN gw_model_names mn ON mn.id=n.model_name_id WHERE n.release_id=? ORDER BY n.id`,
	`SELECT mo.id,mo.catalog_model_id,oc.operation_code,oc.contract_version,oc.status,mo.normalization_version,mo.semantic_digest,opr.http_method,opr.route_template FROM gw_model_operations mo JOIN gw_operation_contracts oc ON oc.id=mo.operation_contract_id JOIN gw_operation_routes opr ON opr.operation_contract_id=oc.id WHERE mo.release_id=? ORDER BY mo.id,opr.id`,
	`SELECT id,model_operation_id,sku_code,delivery_mode,max_results,idempotency_mode,service_tiers FROM gw_skus WHERE release_id=? ORDER BY id`,
	`SELECT ct.id,ch.channel_code,a.adapter_code,a.contract_version,a.implementation_digest,a.minimum_semantic_version,ct.transport_code,ct.base_url,ct.protocol,ct.request_method,ct.request_path,ct.auth_scheme,ct.execution_fingerprint,COALESCE(ct.state_compatibility_fingerprint,''),ct.timeout_ms FROM gw_channel_transports ct JOIN gateway_channels ch ON ch.id=ct.channel_id JOIN gw_adapter_implementations a ON a.id=ct.adapter_implementation_id WHERE ct.release_id=? ORDER BY ct.id`,
	`SELECT id,channel_transport_id,protocol,host_pattern,port FROM gw_transport_allowed_hosts WHERE release_id=? ORDER BY id`,
	`SELECT p.id,ch.channel_code,p.product_code,p.vendor_model,COALESCE(p.capability_constraints,'null'),p.constraints_schema_version FROM gw_products p JOIN gateway_channels ch ON ch.id=p.channel_id WHERE p.release_id=? ORDER BY p.id`,
	`SELECT id,product_id,channel_transport_id,task_scope,cancel_mode,source_url_policy,upstream_scope_kind,upstream_scope_key,execution_fingerprint,COALESCE(state_compatibility_fingerprint,''),timeout_ms FROM gw_product_transports WHERE release_id=? ORDER BY id`,
	`SELECT id,product_transport_id,action_code,allowed_source_state,idempotency_mode,request_schema_version,response_schema_version FROM gw_product_transport_actions WHERE release_id=? ORDER BY id`,
	`SELECT o.id,o.product_transport_id,p.pool_code,o.commercial_fingerprint,o.cost_plan_code FROM gw_offerings o JOIN gw_credential_pools p ON p.id=o.credential_pool_id WHERE o.release_id=? ORDER BY o.id`,
	`SELECT id,sku_id,offering_id,priority,weight FROM gw_routes WHERE release_id=? ORDER BY id`,
	`SELECT r.id,r.sku_id,r.component_code,r.unit_code,r.quantity_source,r.charge_event,r.unit_price,r.unit_scale,r.quantity_step,r.max_quantity,r.currency_code,r.currency_version,e.rate_evidence_id,e.decision FROM gw_sell_rates r JOIN gw_rate_evidence_review_events e ON e.id=r.rate_evidence_review_event_id WHERE r.release_id=? ORDER BY r.id`,
	`SELECT id,offering_id,plan_code FROM gw_cost_plans WHERE release_id=? ORDER BY id`,
	`SELECT r.id,r.cost_plan_id,r.component_code,r.unit_code,r.quantity_source,r.charge_event,r.unit_price,r.unit_scale,r.quantity_step,r.max_quantity,r.currency_code,r.currency_version,e.rate_evidence_id,e.decision FROM gw_cost_rates r JOIN gw_rate_evidence_review_events e ON e.id=r.rate_evidence_review_event_id WHERE r.release_id=? ORDER BY r.id`,
}

func catalogContentDigest(ctx context.Context, tx *sql.Tx, releaseID uint64) (string, error) {
	hasher := sha256.New()
	writeDigestValue(hasher, []byte("prism-catalog-content-v1"))
	for _, query := range catalogDigestQueries {
		writeDigestValue(hasher, []byte(query))
		rows, err := tx.QueryContext(ctx, query, releaseID)
		if err != nil {
			return "", err
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			return "", err
		}
		for rows.Next() {
			values := make([]sql.RawBytes, len(columns))
			targets := make([]any, len(columns))
			for index := range values {
				targets[index] = &values[index]
			}
			if err := rows.Scan(targets...); err != nil {
				rows.Close()
				return "", err
			}
			for _, value := range values {
				if value == nil {
					writeDigestNull(hasher)
				} else {
					writeDigestValue(hasher, value)
				}
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return "", err
		}
		if err := rows.Close(); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func writeDigestValue(hasher hash.Hash, value []byte) {
	var length [9]byte
	length[0] = 1
	binary.BigEndian.PutUint64(length[1:], uint64(len(value)))
	hasher.Write(length[:])
	hasher.Write(value)
}

func writeDigestNull(hasher hash.Hash) {
	hasher.Write([]byte{0})
}

func finalizeCatalogDigest(ctx context.Context, tx *sql.Tx, releaseID uint64) (string, error) {
	digest, err := catalogContentDigest(ctx, tx, releaseID)
	if err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE gw_catalog_releases SET content_hash=?,updated_at=? WHERE id=? AND status='draft'`, digest, nowUTC(), releaseID); err != nil {
		return "", err
	}
	return digest, nil
}

func CheckCatalogStructure(ctx context.Context, db CatalogPricingQuery, releaseID uint64) error {
	if db == nil || releaseID == 0 {
		return ErrInvalidInput
	}
	var total, invalid uint64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(CASE WHEN NOT EXISTS (SELECT 1 FROM gw_routes r WHERE r.release_id=s.release_id AND r.sku_id=s.id) OR JSON_LENGTH(s.service_tiers)=0 THEN 1 ELSE 0 END),0) FROM gw_skus s WHERE s.release_id=?`, releaseID).Scan(&total, &invalid); err != nil {
		return err
	}
	if total == 0 || invalid != 0 {
		return ErrConflict
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(CASE WHEN p.channel_id<>ct.channel_id OR NOT EXISTS (SELECT 1 FROM gw_transport_allowed_hosts h WHERE h.release_id=pt.release_id AND h.channel_transport_id=ct.id) OR (pt.task_scope='task' AND (NOT EXISTS (SELECT 1 FROM gw_product_transport_actions a WHERE a.release_id=pt.release_id AND a.product_transport_id=pt.id AND a.action_code='submit') OR NOT EXISTS (SELECT 1 FROM gw_product_transport_actions a WHERE a.release_id=pt.release_id AND a.product_transport_id=pt.id AND a.action_code='query'))) OR (pt.cancel_mode='upstream' AND NOT EXISTS (SELECT 1 FROM gw_product_transport_actions a WHERE a.release_id=pt.release_id AND a.product_transport_id=pt.id AND a.action_code='cancel')) THEN 1 ELSE 0 END),0) FROM gw_product_transports pt JOIN gw_products p ON p.release_id=pt.release_id AND p.id=pt.product_id JOIN gw_channel_transports ct ON ct.release_id=pt.release_id AND ct.id=pt.channel_transport_id WHERE pt.release_id=?`, releaseID).Scan(&total, &invalid); err != nil {
		return err
	}
	if total == 0 || invalid != 0 {
		return ErrConflict
	}
	return nil
}

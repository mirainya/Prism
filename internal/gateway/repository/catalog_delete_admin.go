package repository

import (
	"context"
	"database/sql"
)

func (s *Store) DeleteCatalogSKU(ctx context.Context, tx *sql.Tx, releaseID, skuID, expectedVersion, actorID uint64) error {
	if tx == nil || releaseID == 0 || skuID == 0 || expectedVersion == 0 || actorID == 0 {
		return ErrInvalidInput
	}
	lock, err := lockCatalogDraft(ctx, tx, releaseID, expectedVersion)
	if err != nil {
		return err
	}
	var operationID, catalogModelID uint64
	if err := tx.QueryRowContext(ctx, `SELECT s.model_operation_id,m.catalog_model_id FROM gw_skus s JOIN gw_model_operations m ON m.id=s.model_operation_id AND m.release_id=s.release_id WHERE s.id=? AND s.release_id=? FOR UPDATE`, skuID, releaseID).Scan(&operationID, &catalogModelID); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	for _, statement := range []string{
		`DELETE FROM gw_sell_rates WHERE release_id=? AND sku_id=?`,
		`DELETE FROM gw_routes WHERE release_id=? AND sku_id=?`,
		`DELETE FROM gw_skus WHERE release_id=? AND id=?`,
	} {
		if _, err := tx.ExecContext(ctx, statement, releaseID, skuID); err != nil {
			return err
		}
	}
	var remaining uint64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_skus WHERE release_id=? AND model_operation_id=?`, releaseID, operationID).Scan(&remaining); err != nil {
		return err
	}
	if remaining == 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM gw_model_operations WHERE release_id=? AND id=?`, releaseID, operationID); err != nil {
			return err
		}
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_model_operations WHERE release_id=? AND catalog_model_id=?`, releaseID, catalogModelID).Scan(&remaining); err != nil {
		return err
	}
	if remaining == 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM gw_catalog_model_names WHERE release_id=? AND catalog_model_id=?`, releaseID, catalogModelID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM gw_catalog_models WHERE release_id=? AND id=?`, releaseID, catalogModelID); err != nil {
			return err
		}
	}
	return advanceCatalogDraft(ctx, tx, releaseID, lock, "unified.catalog.sku.delete", skuID, actorID, ginSafeMetadata{"sku_id": skuID})
}

func (s *Store) DeleteCatalogProduct(ctx context.Context, tx *sql.Tx, releaseID, productID, expectedVersion, actorID uint64) error {
	if tx == nil || releaseID == 0 || productID == 0 || expectedVersion == 0 || actorID == 0 {
		return ErrInvalidInput
	}
	lock, err := lockCatalogDraft(ctx, tx, releaseID, expectedVersion)
	if err != nil {
		return err
	}
	var transportID uint64
	if err := tx.QueryRowContext(ctx, `SELECT channel_transport_id FROM gw_product_transports WHERE release_id=? AND product_id=? FOR UPDATE`, releaseID, productID).Scan(&transportID); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	statements := []string{
		`DELETE r FROM gw_routes r JOIN gw_offerings o ON o.id=r.offering_id AND o.release_id=r.release_id JOIN gw_product_transports p ON p.id=o.product_transport_id AND p.release_id=o.release_id WHERE p.release_id=? AND p.product_id=?`,
		`DELETE e FROM gw_offering_state_events e JOIN gw_offerings o ON o.id=e.offering_id AND o.release_id=e.release_id JOIN gw_product_transports p ON p.id=o.product_transport_id AND p.release_id=o.release_id WHERE p.release_id=? AND p.product_id=?`,
		`DELETE s FROM gw_offering_runtime_state s JOIN gw_offerings o ON o.id=s.offering_id AND o.release_id=s.release_id JOIN gw_product_transports p ON p.id=o.product_transport_id AND p.release_id=o.release_id WHERE p.release_id=? AND p.product_id=?`,
		`DELETE r FROM gw_cost_rates r JOIN gw_cost_plans c ON c.id=r.cost_plan_id AND c.release_id=r.release_id JOIN gw_offerings o ON o.id=c.offering_id AND o.release_id=c.release_id JOIN gw_product_transports p ON p.id=o.product_transport_id AND p.release_id=o.release_id WHERE p.release_id=? AND p.product_id=?`,
		`DELETE c FROM gw_cost_plans c JOIN gw_offerings o ON o.id=c.offering_id AND o.release_id=c.release_id JOIN gw_product_transports p ON p.id=o.product_transport_id AND p.release_id=o.release_id WHERE p.release_id=? AND p.product_id=?`,
		`DELETE o FROM gw_offerings o JOIN gw_product_transports p ON p.id=o.product_transport_id AND p.release_id=o.release_id WHERE p.release_id=? AND p.product_id=?`,
		`DELETE FROM gw_product_transport_actions WHERE release_id=? AND product_transport_id IN (SELECT id FROM gw_product_transports WHERE release_id=? AND product_id=?)`,
		`DELETE FROM gw_product_transports WHERE release_id=? AND product_id=?`,
		`DELETE FROM gw_transport_allowed_hosts WHERE release_id=? AND channel_transport_id=?`,
		`DELETE FROM gw_channel_transports WHERE release_id=? AND id=?`,
		`DELETE FROM gw_products WHERE release_id=? AND id=?`,
	}
	for index, statement := range statements {
		var args []any
		switch index {
		case 6:
			args = []any{releaseID, releaseID, productID}
		case 8, 9:
			args = []any{releaseID, transportID}
		default:
			args = []any{releaseID, productID}
		}
		if _, err := tx.ExecContext(ctx, statement, args...); err != nil {
			return err
		}
	}
	return advanceCatalogDraft(ctx, tx, releaseID, lock, "unified.catalog.product.delete", productID, actorID, ginSafeMetadata{"product_id": productID})
}

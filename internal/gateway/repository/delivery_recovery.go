package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mirainya/Prism/internal/gateway/delivery"
)

type DeliveryRecoveryTarget struct {
	DeliveryID, AttemptID, AsyncExecutionID uint64
	SourceBlobID, SourceSequence            uint64
	Ordinal                                 uint32
	State, Mode, SourceKind, ResourceKind   string
	StateVersion, ActionSeq                 uint64
}

// ScheduleDeliveryReconciliation creates one immutable action for a delivery
// revision. Repeated scanners reuse the existing action.
func (s *Store) ScheduleDeliveryReconciliation(ctx context.Context, tx *sql.Tx, deliveryID uint64, at time.Time) (uint64, error) {
	if tx == nil || deliveryID == 0 || at.IsZero() {
		return 0, ErrInvalidInput
	}
	var attemptID, version, sequence uint64
	var state, mode, sourceKind, sourcePolicy, reason string
	var sourceAvailable bool
	err := tx.QueryRowContext(ctx, `SELECT d.attempt_id,d.state,d.state_version,d.action_seq,d.delivery_mode,d.source_kind,pt.source_url_policy,d.reason_code,(s.id IS NOT NULL) FROM gw_result_deliveries d JOIN gw_api_call_attempts a ON a.id=d.attempt_id JOIN gw_product_transports pt ON pt.id=a.product_transport_id AND pt.release_id=a.catalog_release_id LEFT JOIN gw_result_delivery_sources s ON s.id=d.current_source_id AND s.result_delivery_id=d.id AND s.state='active' WHERE d.id=? FOR UPDATE`, deliveryID).Scan(&attemptID, &state, &version, &sequence, &mode, &sourceKind, &sourcePolicy, &reason, &sourceAvailable)
	if err == sql.ErrNoRows {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	referenceRecovery := mode == "reference" && sourcePolicy == "refreshable" && (state == "expired" || state == "delivery_failed")
	managedCopyRecovery := mode == "managed_copy" && sourceKind == "remote_url" && sourceAvailable && state == "delivery_failed" && delivery.RetryableManagedCopyFailure(reason)
	if attemptID == 0 || !referenceRecovery && !managedCopyRecovery {
		return 0, ErrConflict
	}
	var existingID uint64
	err = tx.QueryRowContext(ctx, `SELECT id FROM gw_async_outbox WHERE result_delivery_id=? AND state_version=? AND action='reconcile_delivery' LIMIT 1 FOR UPDATE`, deliveryID, version).Scan(&existingID)
	if err == nil {
		return existingID, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	if sequence == ^uint64(0) {
		return 0, ErrConflict
	}
	now := nowUTC()
	if err := requireOneRow(tx.ExecContext(ctx, `UPDATE gw_result_deliveries SET action_seq=action_seq+1,retry_at=?,updated_at=? WHERE id=? AND state=? AND state_version=? AND action_seq=?`, at.UTC(), now, deliveryID, state, version, sequence)); err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_async_outbox(result_delivery_id,action_seq,action,status,state_version,available_at,attempt_count,created_at,updated_at) VALUES (?,?,'reconcile_delivery','pending',?,?,0,?,?)`, deliveryID, sequence+1, version, at.UTC(), now, now)
	if err != nil {
		return 0, fmt.Errorf("schedule delivery reconciliation: %w", err)
	}
	return lastID(result)
}

// ScheduleDueDeliveryReconciliations repairs pre-worker or interrupted
// delivery transitions which have no durable action for their current state.
func (s *Store) ScheduleDueDeliveryReconciliations(ctx context.Context, limit int) (int, error) {
	if s == nil || s.db == nil || limit <= 0 || limit > 1000 {
		return 0, ErrInvalidInput
	}
	rows, err := s.db.QueryContext(ctx, `SELECT d.id FROM gw_result_deliveries d JOIN gw_api_call_attempts a ON a.id=d.attempt_id JOIN gw_product_transports pt ON pt.id=a.product_transport_id AND pt.release_id=a.catalog_release_id WHERE ((d.delivery_mode='reference' AND d.state IN ('delivery_failed','expired') AND pt.source_url_policy='refreshable') OR (d.delivery_mode='managed_copy' AND d.source_kind='remote_url' AND d.state='delivery_failed' AND d.reason_code IN ('managed_copy_download_failed','managed_copy_upload_failed','managed_copy_verification_failed') AND EXISTS (SELECT 1 FROM gw_result_delivery_sources s WHERE s.id=d.current_source_id AND s.result_delivery_id=d.id AND s.state='active'))) AND (d.retry_at IS NULL OR d.retry_at<=CURRENT_TIMESTAMP(3)) AND NOT EXISTS (SELECT 1 FROM gw_async_outbox o WHERE o.result_delivery_id=d.id AND o.state_version=d.state_version AND o.action='reconcile_delivery') ORDER BY d.id LIMIT ?`, limit)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var ids []uint64
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	processed := 0
	for _, id := range ids {
		err := s.WithTx(ctx, func(tx *sql.Tx) error {
			_, err := s.ScheduleDeliveryReconciliation(ctx, tx, id, nowUTC())
			return err
		})
		if errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return processed, err
		}
		processed++
	}
	return processed, nil
}

func (s *Store) ReadDeliveryRecoveryTarget(ctx context.Context, deliveryID uint64) (DeliveryRecoveryTarget, error) {
	if s == nil || s.db == nil || deliveryID == 0 {
		return DeliveryRecoveryTarget{}, ErrInvalidInput
	}
	var out DeliveryRecoveryTarget
	err := s.db.QueryRowContext(ctx, `SELECT d.id,d.attempt_id,COALESCE(x.id,0),d.result_ordinal,d.state,d.state_version,d.action_seq,d.delivery_mode,d.source_kind,r.resource_kind,COALESCE(s.encrypted_url_blob_id,0),COALESCE(s.source_seq,0) FROM gw_result_deliveries d JOIN gw_api_call_attempts a ON a.id=d.attempt_id JOIN gw_product_transports pt ON pt.id=a.product_transport_id AND pt.release_id=a.catalog_release_id JOIN gw_api_resources r ON r.call_id=d.call_id LEFT JOIN gw_async_executions x ON x.attempt_id=a.id LEFT JOIN gw_upstream_task_identities i ON i.async_execution_id=x.id AND i.status='bound' AND i.expires_at>CURRENT_TIMESTAMP(3) LEFT JOIN gw_result_delivery_sources s ON s.id=d.current_source_id AND s.result_delivery_id=d.id AND s.state='active' WHERE d.id=? AND ((d.delivery_mode='reference' AND d.source_kind='remote_url' AND d.state IN ('delivery_failed','expired') AND pt.source_url_policy='refreshable' AND x.state='succeeded' AND i.id IS NOT NULL) OR (d.delivery_mode='managed_copy' AND d.source_kind='remote_url' AND d.state='delivery_failed' AND d.reason_code IN ('managed_copy_download_failed','managed_copy_upload_failed','managed_copy_verification_failed') AND s.id IS NOT NULL))`, deliveryID).Scan(&out.DeliveryID, &out.AttemptID, &out.AsyncExecutionID, &out.Ordinal, &out.State, &out.StateVersion, &out.ActionSeq, &out.Mode, &out.SourceKind, &out.ResourceKind, &out.SourceBlobID, &out.SourceSequence)
	if err == sql.ErrNoRows {
		return DeliveryRecoveryTarget{}, ErrNotFound
	}
	if err != nil {
		return DeliveryRecoveryTarget{}, err
	}
	if out.DeliveryID == 0 || out.AttemptID == 0 || out.ResourceKind == "" || out.Mode != "reference" && out.Mode != "managed_copy" ||
		out.Mode == "reference" && out.AsyncExecutionID == 0 || out.Mode == "managed_copy" && (out.SourceBlobID == 0 || out.SourceSequence == 0) {
		return DeliveryRecoveryTarget{}, ErrConflict
	}
	return out, nil
}

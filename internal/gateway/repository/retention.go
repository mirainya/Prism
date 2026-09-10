package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const maxRetentionBatchSize = 5000

// PurgeExpiredCallbackReceipts detaches callback bodies whose authenticated
// receipt window has elapsed.  Receipt metadata and state-transition evidence
// remain queryable; only encrypted payload bytes are removed.
func (s *Store) PurgeExpiredCallbackReceipts(ctx context.Context, now time.Time, limit int) (int, error) {
	if s == nil || now.IsZero() || limit <= 0 || limit > maxRetentionBatchSize {
		return 0, ErrInvalidInput
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM gw_upstream_callback_receipts WHERE encrypted_payload_blob_id IS NOT NULL AND expires_at<=? ORDER BY expires_at,id LIMIT ?`, now.UTC(), limit)
	if err != nil {
		return 0, err
	}
	ids, err := collectUint64Rows(rows)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, receiptID := range ids {
		cleared := false
		err = s.WithTx(ctx, func(tx *sql.Tx) error {
			var status string
			var blobID sql.NullInt64
			var expiresAt time.Time
			if err := tx.QueryRowContext(ctx, s.forUpdate(`SELECT status,encrypted_payload_blob_id,expires_at FROM gw_upstream_callback_receipts WHERE id=?`), receiptID).Scan(&status, &blobID, &expiresAt); err == sql.ErrNoRows {
				return nil
			} else if err != nil {
				return err
			}
			if !expiresAt.IsZero() && expiresAt.After(now.UTC()) {
				return nil
			}
			if status == "received" {
				if err := requireOneRow(tx.ExecContext(ctx, `UPDATE gw_upstream_callback_receipts SET status='rejected',state_version=state_version+1 WHERE id=? AND status='received'`, receiptID)); err != nil {
					return err
				}
				var version uint64
				if err := tx.QueryRowContext(ctx, `SELECT state_version FROM gw_upstream_callback_receipts WHERE id=?`, receiptID).Scan(&version); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO gw_state_transition_events(callback_receipt_id,old_state,new_state,state_version,reason_code,created_at) VALUES (?,?,?,?,?,?)`, receiptID, "received", "rejected", version, "callback_expired", now.UTC()); err != nil {
					return err
				}
			}
			if blobID.Valid && blobID.Int64 > 0 {
				if err := s.DetachCallbackReceiptPayload(ctx, tx, receiptID, uint64(blobID.Int64)); err != nil {
					return err
				}
				cleared = true
			}
			return nil
		})
		if err != nil {
			return removed, fmt.Errorf("purge callback receipt %d: %w", receiptID, err)
		}
		if cleared {
			removed++
		}
	}
	return removed, nil
}

// PurgeExpiredCallbackBindings revokes expired token aliases and deletes the
// shared encrypted token Blob once the last alias reference is detached.
func (s *Store) PurgeExpiredCallbackBindings(ctx context.Context, now time.Time, limit int) (int, error) {
	if s == nil || now.IsZero() || limit <= 0 || limit > maxRetentionBatchSize {
		return 0, ErrInvalidInput
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM gw_callback_binding_token_aliases WHERE encrypted_blob_id IS NOT NULL AND ((status='active' AND expires_at<=?) OR status IN ('expired','revoked')) ORDER BY expires_at,id LIMIT ?`, now.UTC(), limit)
	if err != nil {
		return 0, err
	}
	ids, err := collectUint64Rows(rows)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, aliasID := range ids {
		changed := false
		err = s.WithTx(ctx, func(tx *sql.Tx) error {
			var status string
			var blobID uint64
			if err := tx.QueryRowContext(ctx, s.forUpdate(`SELECT status,encrypted_blob_id FROM gw_callback_binding_token_aliases WHERE id=? AND encrypted_blob_id IS NOT NULL AND ((status='active' AND expires_at<=?) OR status IN ('expired','revoked'))`), aliasID, now.UTC()).Scan(&status, &blobID); err == sql.ErrNoRows {
				return nil
			} else if err != nil {
				return err
			}
			if status == "active" {
				if err := requireOneRow(tx.ExecContext(ctx, `UPDATE gw_callback_binding_token_aliases SET status='expired',invalidated_at=?,encrypted_blob_id=NULL WHERE id=? AND status='active' AND expires_at<=? AND encrypted_blob_id=?`, now.UTC(), aliasID, now.UTC(), blobID)); err != nil {
					return err
				}
			} else if status == "expired" || status == "revoked" {
				if err := requireOneRow(tx.ExecContext(ctx, `UPDATE gw_callback_binding_token_aliases SET encrypted_blob_id=NULL WHERE id=? AND status=? AND encrypted_blob_id=?`, aliasID, status, blobID)); err != nil {
					return err
				}
			} else {
				return ErrConflict
			}
			// The nullable FK is cleared before the encrypted Blob is considered
			// for deletion. Count every remaining alias, including already
			// expired aliases from an interrupted older cleanup.
			var references uint64
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_callback_binding_token_aliases WHERE encrypted_blob_id=?`, blobID).Scan(&references); err != nil {
				return err
			}
			if references == 0 {
				if err := s.deleteDetachedEncryptedBlob(ctx, tx, blobID, "gateway-callback-binding-token"); err != nil {
					return err
				}
			}
			changed = true
			return nil
		})
		if err != nil {
			return removed, fmt.Errorf("purge callback binding alias %d: %w", aliasID, err)
		}
		if changed {
			removed++
		}
	}
	return removed, nil
}

// PurgeExpiredCallPayloads removes ciphertext only after the immutable
// payload metadata has stopped referencing it. Calls keep their payload
// metadata identity and return payload_expired when the body is requested.
func (s *Store) PurgeExpiredCallPayloads(ctx context.Context, now time.Time, limit int) (int, error) {
	if s == nil || now.IsZero() || limit <= 0 || limit > maxRetentionBatchSize {
		return 0, ErrInvalidInput
	}
	rows, err := s.db.QueryContext(ctx, `SELECT payload.id FROM gw_api_call_payloads payload JOIN gw_api_calls gateway_call ON gateway_call.id=payload.call_id WHERE payload.encrypted_blob_id IS NOT NULL AND payload.retention_until IS NOT NULL AND payload.retention_until<=? AND gateway_call.status IN ('completed','failed','cancelled') ORDER BY payload.retention_until,payload.id LIMIT ?`, now.UTC(), limit)
	if err != nil {
		return 0, err
	}
	ids, err := collectUint64Rows(rows)
	if err != nil {
		return 0, err
	}

	purged := 0
	for _, payloadID := range ids {
		removed := false
		err = s.WithTx(ctx, func(tx *sql.Tx) error {
			var blobID uint64
			err := tx.QueryRowContext(ctx, s.forUpdate(`SELECT payload.encrypted_blob_id FROM gw_api_call_payloads payload JOIN gw_api_calls gateway_call ON gateway_call.id=payload.call_id WHERE payload.id=? AND payload.encrypted_blob_id IS NOT NULL AND payload.retention_until IS NOT NULL AND payload.retention_until<=? AND gateway_call.status IN ('completed','failed','cancelled')`), payloadID, now.UTC()).Scan(&blobID)
			if err == sql.ErrNoRows {
				return nil
			}
			if err != nil {
				return err
			}
			if err := requireOneRow(tx.ExecContext(ctx, `UPDATE gw_api_call_payloads SET encrypted_blob_id=NULL,purged_at=? WHERE id=? AND encrypted_blob_id=?`, now.UTC(), payloadID, blobID)); err != nil {
				return err
			}
			if err := s.deleteDetachedEncryptedBlob(ctx, tx, blobID, "gateway-payload"); err != nil {
				return err
			}
			removed = true
			return nil
		})
		if err != nil {
			return purged, fmt.Errorf("purge call payload %d: %w", payloadID, err)
		}
		if removed {
			purged++
		}
	}
	return purged, nil
}

// PurgeExpiredRequestLogPayloads applies the observability retention window
// to exact upstream request and response bodies. Request-log metadata and
// content HMACs remain available after the ciphertext is removed.
func (s *Store) PurgeExpiredRequestLogPayloads(ctx context.Context, cutoff, now time.Time, limit int) (int, error) {
	if s == nil || cutoff.IsZero() || now.IsZero() || cutoff.After(now) || limit <= 0 || limit > maxRetentionBatchSize {
		return 0, ErrInvalidInput
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM gw_channel_request_logs WHERE created_at<=? AND (request_payload_blob_id IS NOT NULL OR response_payload_blob_id IS NOT NULL) ORDER BY created_at,id LIMIT ?`, cutoff.UTC(), limit)
	if err != nil {
		return 0, err
	}
	ids, err := collectUint64Rows(rows)
	if err != nil {
		return 0, err
	}

	purged := 0
	for _, requestLogID := range ids {
		removed := false
		err = s.WithTx(ctx, func(tx *sql.Tx) error {
			var requestBlob, responseBlob sql.NullInt64
			err := tx.QueryRowContext(ctx, s.forUpdate(`SELECT request_payload_blob_id,response_payload_blob_id FROM gw_channel_request_logs WHERE id=? AND created_at<=? AND (request_payload_blob_id IS NOT NULL OR response_payload_blob_id IS NOT NULL)`), requestLogID, cutoff.UTC()).Scan(&requestBlob, &responseBlob)
			if err == sql.ErrNoRows {
				return nil
			}
			if err != nil {
				return err
			}
			if err := requireOneRow(tx.ExecContext(ctx, `UPDATE gw_channel_request_logs SET request_payload_blob_id=NULL,response_payload_blob_id=NULL WHERE id=?`, requestLogID)); err != nil {
				return err
			}
			seen := make(map[uint64]struct{}, 2)
			for _, candidate := range []struct {
				id      sql.NullInt64
				purpose string
			}{{requestBlob, "gateway-upstream-request"}, {responseBlob, "gateway-upstream-response"}} {
				if !candidate.id.Valid || candidate.id.Int64 <= 0 {
					continue
				}
				blobID := uint64(candidate.id.Int64)
				if _, exists := seen[blobID]; exists {
					continue
				}
				seen[blobID] = struct{}{}
				if err := s.deleteDetachedEncryptedBlob(ctx, tx, blobID, candidate.purpose); err != nil {
					return err
				}
			}
			removed = true
			return nil
		})
		if err != nil {
			return purged, fmt.Errorf("purge request log payloads %d: %w", requestLogID, err)
		}
		if removed {
			purged++
		}
	}
	return purged, nil
}

func (s *Store) deleteDetachedEncryptedBlob(ctx context.Context, tx *sql.Tx, blobID uint64, expectedPurpose string) error {
	if tx == nil || blobID == 0 || expectedPurpose == "" {
		return ErrInvalidInput
	}
	var purpose string
	if err := tx.QueryRowContext(ctx, s.forUpdate(`SELECT purpose FROM encrypted_blobs WHERE id=?`), blobID).Scan(&purpose); err == sql.ErrNoRows {
		return ErrConflict
	} else if err != nil {
		return err
	}
	if purpose != expectedPurpose {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM encrypted_blob_key_wraps WHERE encrypted_blob_id=?`, blobID); err != nil {
		return err
	}
	return requireOneRow(tx.ExecContext(ctx, `DELETE FROM encrypted_blobs WHERE id=? AND purpose=?`, blobID, expectedPurpose))
}

func collectUint64Rows(rows *sql.Rows) ([]uint64, error) {
	if rows == nil {
		return nil, ErrInvalidInput
	}
	defer rows.Close()
	ids := make([]uint64, 0)
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

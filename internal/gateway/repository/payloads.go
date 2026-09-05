package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type CallPayloadInput struct {
	CallID          uint64
	Kind            string
	SchemaVersion   uint32
	EncryptedBlobID *uint64
	ContentHMAC     string
	ContentLength   uint64
	RetentionUntil  *time.Time
}

func (s *Store) CreateCallPayload(ctx context.Context, tx *sql.Tx, in CallPayloadInput) (uint64, error) {
	if tx == nil || in.CallID == 0 || in.SchemaVersion == 0 || in.ContentHMAC == "" || in.Kind != "request" && in.Kind != "result" {
		return 0, ErrInvalidInput
	}
	now := nowUTC()
	var retention any
	if in.RetentionUntil != nil {
		retention = in.RetentionUntil.UTC()
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_api_call_payloads(call_id,kind,schema_version,encrypted_blob_id,content_hmac,content_length,retention_until,created_at) VALUES (?,?,?,?,?,?,?,?)`, in.CallID, in.Kind, in.SchemaVersion, nullableID(in.EncryptedBlobID), in.ContentHMAC, in.ContentLength, retention, now)
	if err != nil {
		return 0, fmt.Errorf("create call payload: %w", err)
	}
	return lastID(result)
}

func (s *Store) PurgeCallPayload(ctx context.Context, tx *sql.Tx, payloadID uint64) error {
	if tx == nil || payloadID == 0 {
		return ErrInvalidInput
	}
	res, err := tx.ExecContext(ctx, `UPDATE gw_api_call_payloads SET encrypted_blob_id=NULL,purged_at=? WHERE id=? AND encrypted_blob_id IS NOT NULL AND retention_until IS NOT NULL AND retention_until<=UTC_TIMESTAMP(3)`, nowUTC(), payloadID)
	if err != nil {
		return err
	}
	ok, err := affected(res)
	if err != nil {
		return err
	}
	if !ok {
		return ErrConflict
	}
	return nil
}

type CallbackReceiptInput struct {
	TaskIdentityID, AsyncExecutionID *uint64
	EventScope                       string
	HMACKeyVersion                   uint32
	EventHMAC, PayloadHMAC           string
	VerifiedCredentialVersionID      uint64
	ExpiresAt                        time.Time
}

func (s *Store) CreateCallbackReceipt(ctx context.Context, tx *sql.Tx, in CallbackReceiptInput) (uint64, bool, error) {
	if tx == nil || in.EventScope == "" || len(in.EventScope) > 255 || in.HMACKeyVersion == 0 || !validHexDigest(in.EventHMAC, 32) || !validHexDigest(in.PayloadHMAC, 32) || in.VerifiedCredentialVersionID == 0 || in.ExpiresAt.IsZero() || !in.ExpiresAt.After(nowUTC()) || (in.TaskIdentityID == nil && in.AsyncExecutionID == nil) || (in.TaskIdentityID != nil && in.AsyncExecutionID != nil) {
		return 0, false, ErrInvalidInput
	}
	var id uint64
	err := tx.QueryRowContext(ctx, `SELECT id FROM gw_upstream_callback_receipts WHERE event_scope=? AND hmac_key_version=? AND event_hmac=? FOR UPDATE`, in.EventScope, in.HMACKeyVersion, in.EventHMAC).Scan(&id)
	if err == nil {
		return id, true, nil
	}
	if err != sql.ErrNoRows {
		return 0, false, err
	}
	now := nowUTC()
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_upstream_callback_receipts(task_identity_id,async_execution_id,event_scope,hmac_key_version,event_hmac,verified_credential_version_id,status,state_version,payload_hmac,expires_at,created_at) VALUES (?,?,?,?,?,?,'received',1,?,?,?)`, nullableID(in.TaskIdentityID), nullableID(in.AsyncExecutionID), in.EventScope, in.HMACKeyVersion, in.EventHMAC, in.VerifiedCredentialVersionID, in.PayloadHMAC, in.ExpiresAt.UTC(), now)
	if err != nil {
		// A concurrent callback may have won the unique event key between the
		// lookup and insert. Re-read it instead of turning a harmless replay
		// into a delivery failure.
		if readErr := tx.QueryRowContext(ctx, `SELECT id FROM gw_upstream_callback_receipts WHERE event_scope=? AND hmac_key_version=? AND event_hmac=? FOR UPDATE`, in.EventScope, in.HMACKeyVersion, in.EventHMAC).Scan(&id); readErr == nil {
			return id, true, nil
		}
		return 0, false, fmt.Errorf("create callback receipt: %w", err)
	}
	id, err = lastID(result)
	return id, false, err
}

func (s *Store) TransitionCallbackReceipt(ctx context.Context, tx *sql.Tx, receiptID uint64, from, to, reason string) error {
	if tx == nil || receiptID == 0 || reason == "" || !validReceiptState(from) || !validReceiptState(to) {
		return ErrInvalidInput
	}
	if from == to {
		return nil
	}
	if !(from == "received" && (to == "processed" || to == "manual_review" || to == "rejected") || from == "manual_review" && (to == "processed" || to == "rejected")) {
		return ErrConflict
	}
	res, err := tx.ExecContext(ctx, `UPDATE gw_upstream_callback_receipts SET status=?,state_version=state_version+1 WHERE id=? AND status=?`, to, receiptID, from)
	if err != nil {
		return err
	}
	ok, err := affected(res)
	if err != nil {
		return err
	}
	if !ok {
		return ErrConflict
	}
	var version uint64
	if err := tx.QueryRowContext(ctx, `SELECT state_version FROM gw_upstream_callback_receipts WHERE id=?`, receiptID).Scan(&version); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO gw_state_transition_events(callback_receipt_id,old_state,new_state,state_version,reason_code,created_at) VALUES (?,?,?,?,?,?)`, receiptID, from, to, version, reason, nowUTC())
	return err
}

func validReceiptState(value string) bool {
	switch value {
	case "received", "processed", "manual_review", "rejected":
		return true
	default:
		return false
	}
}

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
	res, err := tx.ExecContext(ctx, `UPDATE gw_api_call_payloads SET encrypted_blob_id=NULL,purged_at=? WHERE id=? AND encrypted_blob_id IS NOT NULL AND retention_until IS NOT NULL AND retention_until<=CURRENT_TIMESTAMP(3)`, nowUTC(), payloadID)
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
	EncryptedPayloadBlobID           uint64
	EventScope                       string
	HMACKeyVersion                   uint32
	EventHMAC, PayloadHMAC           string
	VerifiedCredentialVersionID      uint64
	ExpiresAt                        time.Time
}

type CallbackReceiptAlias struct {
	HMACKeyVersion uint32
	EventHMAC      string
}

// FindCallbackReceiptAlias resolves an event identity independently of the
// HMAC version used when the original callback arrived. It is intentionally a
// narrow lookup so callers can verify the execution and payload before
// treating the request as a replay.
func (s *Store) FindCallbackReceiptAlias(ctx context.Context, tx *sql.Tx, keyVersion uint32, eventHMAC string) (uint64, error) {
	if tx == nil || keyVersion == 0 || !validHexDigest(eventHMAC, 32) {
		return 0, ErrInvalidInput
	}
	var receiptID uint64
	err := tx.QueryRowContext(ctx, s.forUpdate(`SELECT a.receipt_id FROM gw_upstream_callback_receipt_aliases a JOIN gw_upstream_callback_receipts r ON r.id=a.receipt_id WHERE a.hmac_key_version=? AND a.event_hmac=? AND r.expires_at>CURRENT_TIMESTAMP(3) AND r.status IN ('received','processed')`), keyVersion, eventHMAC).Scan(&receiptID)
	if err == sql.ErrNoRows {
		return 0, ErrNotFound
	}
	return receiptID, err
}

func (s *Store) CreateCallbackReceipt(ctx context.Context, tx *sql.Tx, in CallbackReceiptInput) (uint64, bool, error) {
	if tx == nil || in.EventScope == "" || len(in.EventScope) > 255 || in.HMACKeyVersion == 0 || !validHexDigest(in.EventHMAC, 32) || !validHexDigest(in.PayloadHMAC, 32) || in.VerifiedCredentialVersionID == 0 || in.ExpiresAt.IsZero() || !in.ExpiresAt.After(nowUTC()) || (in.TaskIdentityID == nil && in.AsyncExecutionID == nil) || (in.TaskIdentityID != nil && in.AsyncExecutionID != nil) {
		return 0, false, ErrInvalidInput
	}
	var id, existingAsync, existingCredential uint64
	var existingPayload string
	var existingExpiry time.Time
	err := tx.QueryRowContext(ctx, `SELECT id,COALESCE(async_execution_id,0),verified_credential_version_id,payload_hmac,expires_at FROM gw_upstream_callback_receipts WHERE event_scope=? AND hmac_key_version=? AND event_hmac=? FOR UPDATE`, in.EventScope, in.HMACKeyVersion, in.EventHMAC).Scan(&id, &existingAsync, &existingCredential, &existingPayload, &existingExpiry)
	if err == nil {
		if !existingExpiry.After(nowUTC()) {
			return 0, false, ErrConflict
		}
		if (in.AsyncExecutionID != nil && existingAsync != *in.AsyncExecutionID) || existingCredential != in.VerifiedCredentialVersionID || existingPayload != in.PayloadHMAC {
			return 0, false, ErrConflict
		}
		return id, true, nil
	}
	if err != sql.ErrNoRows {
		return 0, false, err
	}
	now := nowUTC()
	var payloadBlob any
	if in.EncryptedPayloadBlobID != 0 {
		payloadBlob = in.EncryptedPayloadBlobID
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_upstream_callback_receipts(task_identity_id,async_execution_id,event_scope,hmac_key_version,event_hmac,verified_credential_version_id,status,state_version,payload_hmac,encrypted_payload_blob_id,expires_at,created_at) VALUES (?,?,?,?,?,?,'received',1,?,?,?,?)`, nullableID(in.TaskIdentityID), nullableID(in.AsyncExecutionID), in.EventScope, in.HMACKeyVersion, in.EventHMAC, in.VerifiedCredentialVersionID, in.PayloadHMAC, payloadBlob, in.ExpiresAt.UTC(), now)
	if err != nil {
		// A concurrent callback may have won the unique event key between the
		// lookup and insert. Re-read it instead of turning a harmless replay
		// into a delivery failure.
		if readErr := tx.QueryRowContext(ctx, `SELECT id,COALESCE(async_execution_id,0),verified_credential_version_id,payload_hmac,expires_at FROM gw_upstream_callback_receipts WHERE event_scope=? AND hmac_key_version=? AND event_hmac=? FOR UPDATE`, in.EventScope, in.HMACKeyVersion, in.EventHMAC).Scan(&id, &existingAsync, &existingCredential, &existingPayload, &existingExpiry); readErr == nil {
			if !existingExpiry.After(nowUTC()) {
				return 0, false, ErrConflict
			}
			if (in.AsyncExecutionID != nil && existingAsync != *in.AsyncExecutionID) || existingCredential != in.VerifiedCredentialVersionID || existingPayload != in.PayloadHMAC {
				return 0, false, ErrConflict
			}
			return id, true, nil
		}
		return 0, false, fmt.Errorf("create callback receipt: %w", err)
	}
	id, err = lastID(result)
	return id, false, err
}

// AddCallbackReceiptAliases appends all missing key-version aliases for one
// Receipt. Existing identical aliases are accepted, while an alias belonging
// to another Receipt is rejected by the database uniqueness constraint.
func (s *Store) AddCallbackReceiptAliases(ctx context.Context, tx *sql.Tx, receiptID uint64, aliases []CallbackReceiptAlias) error {
	if tx == nil || receiptID == 0 || len(aliases) == 0 {
		return ErrInvalidInput
	}
	seen := make(map[uint32]struct{}, len(aliases))
	for _, alias := range aliases {
		if alias.HMACKeyVersion == 0 || !validHexDigest(alias.EventHMAC, 32) {
			return ErrInvalidInput
		}
		if _, exists := seen[alias.HMACKeyVersion]; exists {
			continue
		}
		seen[alias.HMACKeyVersion] = struct{}{}
		if _, err := tx.ExecContext(ctx, `INSERT INTO gw_upstream_callback_receipt_aliases(receipt_id,hmac_key_version,event_hmac,created_at) VALUES (?,?,?,?)`, receiptID, alias.HMACKeyVersion, alias.EventHMAC, nowUTC()); err != nil {
			// A retry may have already inserted this exact alias. Verify the
			// owner before accepting the duplicate.
			var existing uint64
			readErr := tx.QueryRowContext(ctx, s.forUpdate(`SELECT receipt_id FROM gw_upstream_callback_receipt_aliases WHERE hmac_key_version=? AND event_hmac=?`), alias.HMACKeyVersion, alias.EventHMAC).Scan(&existing)
			if readErr == nil && existing == receiptID {
				continue
			}
			return fmt.Errorf("add callback receipt alias: %w", err)
		}
	}
	return nil
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
	var payloadBlob sql.NullInt64
	if err := tx.QueryRowContext(ctx, s.forUpdate(`SELECT encrypted_payload_blob_id FROM gw_upstream_callback_receipts WHERE id=? AND status=?`), receiptID, from).Scan(&payloadBlob); err == sql.ErrNoRows {
		return ErrConflict
	} else if err != nil {
		return err
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
	if err != nil {
		return err
	}
	if payloadBlob.Valid && payloadBlob.Int64 > 0 {
		return s.DetachCallbackReceiptPayload(ctx, tx, receiptID, uint64(payloadBlob.Int64))
	}
	return nil
}

// DetachCallbackReceiptPayload removes the encrypted callback body while
// retaining the immutable Receipt metadata.  The blob is deleted only after
// the FK reference is cleared and no other Receipt still points at it.
func (s *Store) DetachCallbackReceiptPayload(ctx context.Context, tx *sql.Tx, receiptID, blobID uint64) error {
	if s == nil || tx == nil || receiptID == 0 || blobID == 0 {
		return ErrInvalidInput
	}
	if err := requireOneRow(tx.ExecContext(ctx, `UPDATE gw_upstream_callback_receipts SET encrypted_payload_blob_id=NULL WHERE id=? AND encrypted_payload_blob_id=?`, receiptID, blobID)); err != nil {
		return err
	}
	var references uint64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_upstream_callback_receipts WHERE encrypted_payload_blob_id=?`, blobID).Scan(&references); err != nil {
		return err
	}
	if references != 0 {
		return nil
	}
	return s.deleteDetachedEncryptedBlob(ctx, tx, blobID, "gateway-callback-payload")
}

func validReceiptState(value string) bool {
	switch value {
	case "received", "processed", "manual_review", "rejected":
		return true
	default:
		return false
	}
}

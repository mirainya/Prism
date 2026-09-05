package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type ProviderStateRefInput struct {
	CreatedAttemptID, ProductTransportID, OfferingID, CredentialID uint64
	CompatibilityFingerprint, ScopeKind, ScopeKey                  string
	EncryptedStateBlobID                                           uint64
	ExpiresAt                                                      time.Time
}

func (s *Store) CreateProviderStateRef(ctx context.Context, tx *sql.Tx, in ProviderStateRefInput) (uint64, error) {
	if tx == nil || in.CreatedAttemptID == 0 || in.ProductTransportID == 0 || in.OfferingID == 0 || in.CredentialID == 0 || !validHexDigest(in.CompatibilityFingerprint, 32) || in.ScopeKind == "" || in.ScopeKey == "" || len(in.ScopeKey) > 255 || in.EncryptedStateBlobID == 0 || in.ExpiresAt.IsZero() || !in.ExpiresAt.After(nowUTC()) {
		return 0, ErrInvalidInput
	}
	switch in.ScopeKind {
	case "product_transport", "credential_pool", "credential", "credential_version":
	default:
		return 0, ErrInvalidInput
	}
	var attemptTransportID, attemptOfferingID, attemptCredentialID uint64
	if err := tx.QueryRowContext(ctx, `SELECT product_transport_id,offering_id,credential_id FROM gw_api_call_attempts WHERE id=? FOR SHARE`, in.CreatedAttemptID).Scan(&attemptTransportID, &attemptOfferingID, &attemptCredentialID); err == sql.ErrNoRows {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	} else if attemptTransportID != in.ProductTransportID || attemptOfferingID != in.OfferingID || attemptCredentialID != in.CredentialID {
		return 0, ErrConflict
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_provider_state_refs(created_attempt_id,product_transport_id,offering_id,credential_id,state_compatibility_fingerprint,scope_kind,scope_key,encrypted_state_blob_id,status,state_version,expires_at,created_at) VALUES (?,?,?,?,?,?,?,?, 'active',1,?,?)`, in.CreatedAttemptID, in.ProductTransportID, in.OfferingID, in.CredentialID, in.CompatibilityFingerprint, in.ScopeKind, in.ScopeKey, in.EncryptedStateBlobID, in.ExpiresAt.UTC(), nowUTC())
	if err != nil {
		return 0, fmt.Errorf("create provider state ref: %w", err)
	}
	return lastID(result)
}

func (s *Store) AddProviderStateAlias(ctx context.Context, tx *sql.Tx, refID uint64, scopeKind, scopeKey string, keyVersion uint32, valueHMAC string) (uint64, error) {
	if tx == nil || refID == 0 || scopeKind == "" || scopeKey == "" || len(scopeKey) > 255 || keyVersion == 0 || !validHexDigest(valueHMAC, 32) {
		return 0, ErrInvalidInput
	}
	if scopeKind != "product_transport" && scopeKind != "credential_pool" && scopeKind != "credential" && scopeKind != "credential_version" {
		return 0, ErrInvalidInput
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_provider_state_id_aliases(provider_state_ref_id,scope_kind,scope_key,hmac_key_version,value_hmac,matchable,created_at) VALUES (?,?,?,?,?,true,?)`, refID, scopeKind, scopeKey, keyVersion, valueHMAC, nowUTC())
	if err != nil {
		return 0, fmt.Errorf("add provider state alias: %w", err)
	}
	return lastID(result)
}

func (s *Store) TransitionProviderStateRef(ctx context.Context, tx *sql.Tx, refID uint64, from, to, reason string) error {
	if tx == nil || refID == 0 || reason == "" {
		return ErrInvalidInput
	}
	if from == to || !validProviderState(from) || !validProviderState(to) {
		return ErrInvalidInput
	}
	if !(from == "active" && (to == "expired" || to == "revoked")) {
		return ErrConflict
	}
	res, err := tx.ExecContext(ctx, `UPDATE gw_provider_state_refs SET status=?,state_version=state_version+1 WHERE id=? AND status=?`, to, refID, from)
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
	if err = tx.QueryRowContext(ctx, `SELECT state_version FROM gw_provider_state_refs WHERE id=?`, refID).Scan(&version); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO gw_state_transition_events(provider_state_ref_id,old_state,new_state,state_version,reason_code,created_at) VALUES (?,?,?,?,?,?)`, refID, from, to, version, reason, nowUTC())
	return err
}

func validProviderState(v string) bool { return v == "active" || v == "expired" || v == "revoked" }

func (s *Store) AddCallbackReceiptAlias(ctx context.Context, tx *sql.Tx, receiptID uint64, keyVersion uint32, eventHMAC string) (uint64, error) {
	if tx == nil || receiptID == 0 || keyVersion == 0 || !validHexDigest(eventHMAC, 32) {
		return 0, ErrInvalidInput
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_upstream_callback_receipt_aliases(receipt_id,hmac_key_version,event_hmac,created_at) VALUES (?,?,?,?)`, receiptID, keyVersion, eventHMAC, nowUTC())
	if err != nil {
		return 0, fmt.Errorf("add callback receipt alias: %w", err)
	}
	return lastID(result)
}

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type ResultDeliveryInput struct {
	CallID, AttemptID uint64
	Ordinal           uint32
	UserID, TokenID   uint64
	Mode, SourceKind  string
}

type CallbackTargetInput struct {
	CallID, UserID, TokenID, EncryptedConfigBlobID uint64
	TargetHMAC, Algorithm                          string
	PolicyVersion                                  uint32
}

func (s *Store) CreateCallbackTarget(ctx context.Context, tx *sql.Tx, in CallbackTargetInput) (uint64, error) {
	if tx == nil || in.CallID == 0 || in.UserID == 0 || in.TokenID == 0 || in.EncryptedConfigBlobID == 0 || !validHexDigest(in.TargetHMAC, 32) || in.Algorithm == "" || in.PolicyVersion == 0 {
		return 0, ErrInvalidInput
	}
	var callUserID, callTokenID uint64
	if err := tx.QueryRowContext(ctx, `SELECT user_id,token_id FROM gw_api_calls WHERE id=? FOR SHARE`, in.CallID).Scan(&callUserID, &callTokenID); err == sql.ErrNoRows {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	} else if callUserID != in.UserID || callTokenID != in.TokenID {
		return 0, ErrConflict
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_callback_targets(call_id,user_id,token_id,encrypted_config_blob_id,target_hmac,algorithm,policy_version,created_at) VALUES (?,?,?,?,?,?,?,?)`, in.CallID, in.UserID, in.TokenID, in.EncryptedConfigBlobID, in.TargetHMAC, in.Algorithm, in.PolicyVersion, nowUTC())
	if err != nil {
		return 0, fmt.Errorf("create callback target: %w", err)
	}
	return lastID(result)
}

func (s *Store) CreateResultDelivery(ctx context.Context, tx *sql.Tx, in ResultDeliveryInput) (uint64, error) {
	if tx == nil || in.CallID == 0 || in.AttemptID == 0 || in.UserID == 0 || in.TokenID == 0 || (in.Mode != "reference" && in.Mode != "managed_copy") || (in.SourceKind != "remote_url" && in.SourceKind != "inline_response") {
		return 0, ErrInvalidInput
	}
	if in.Mode == "reference" && in.SourceKind != "remote_url" {
		return 0, ErrInvalidInput
	}
	var callUserID, callTokenID uint64
	if err := tx.QueryRowContext(ctx, `SELECT user_id,token_id FROM gw_api_calls WHERE id=? FOR SHARE`, in.CallID).Scan(&callUserID, &callTokenID); err == sql.ErrNoRows {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	} else if callUserID != in.UserID || callTokenID != in.TokenID {
		return 0, ErrConflict
	}
	var attemptCallID uint64
	if err := tx.QueryRowContext(ctx, `SELECT call_id FROM gw_api_call_attempts WHERE id=? FOR SHARE`, in.AttemptID).Scan(&attemptCallID); err == sql.ErrNoRows {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	} else if attemptCallID != in.CallID {
		return 0, ErrConflict
	}
	now := nowUTC()
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_result_deliveries(call_id,attempt_id,result_ordinal,user_id,token_id,delivery_mode,source_kind,state,state_version,created_at,updated_at) VALUES (?,?,?,?,?,?,?,'pending',1,?,?)`, in.CallID, in.AttemptID, in.Ordinal, in.UserID, in.TokenID, in.Mode, in.SourceKind, now, now)
	if err != nil {
		return 0, fmt.Errorf("insert result delivery: %w", err)
	}
	return lastID(result)
}

type ResultSourceInput struct {
	DeliveryID                    uint64
	Sequence                      uint64
	EncryptedURLBlobID            uint64
	URLHMAC, ProviderIdentityHMAC string
	ExpiresAt                     *time.Time
}

func (s *Store) AddResultSource(ctx context.Context, tx *sql.Tx, in ResultSourceInput) (uint64, error) {
	if tx == nil || in.DeliveryID == 0 || in.Sequence == 0 || in.EncryptedURLBlobID == 0 || !validHexDigest(in.URLHMAC, 32) {
		return 0, ErrInvalidInput
	}
	if in.ProviderIdentityHMAC != "" && !validHexDigest(in.ProviderIdentityHMAC, 32) {
		return 0, ErrInvalidInput
	}
	if in.ExpiresAt != nil && !in.ExpiresAt.After(nowUTC()) {
		return 0, ErrInvalidInput
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_result_delivery_sources(result_delivery_id,source_seq,encrypted_url_blob_id,url_hmac,provider_result_identity_hmac,observed_at,expires_at,state) VALUES (?,?,?,?,?,? ,?,'superseded')`, in.DeliveryID, in.Sequence, in.EncryptedURLBlobID, in.URLHMAC, emptyAsNull(in.ProviderIdentityHMAC), nowUTC(), in.ExpiresAt)
	if err != nil {
		return 0, fmt.Errorf("insert result source: %w", err)
	}
	return lastID(result)
}

func (s *Store) ActivateResultSource(ctx context.Context, tx *sql.Tx, deliveryID, sourceID uint64) error {
	if tx == nil || deliveryID == 0 || sourceID == 0 {
		return ErrInvalidInput
	}
	now := nowUTC()
	var lockedID uint64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM gw_result_deliveries WHERE id=? FOR UPDATE`, deliveryID).Scan(&lockedID); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE gw_result_delivery_sources SET state='superseded' WHERE result_delivery_id=? AND state='active'`, deliveryID); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE gw_result_delivery_sources SET state='active' WHERE id=? AND result_delivery_id=? AND state='superseded'`, sourceID, deliveryID)
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
	res, err = tx.ExecContext(ctx, `UPDATE gw_result_deliveries SET current_source_id=?,state='ready',state_version=state_version+1,updated_at=? WHERE id=? AND state IN ('pending','delivery_failed')`, sourceID, now, deliveryID)
	if err != nil {
		return err
	}
	ok, err = affected(res)
	if err != nil {
		return err
	}
	if !ok {
		return ErrConflict
	}
	return nil
}

type CallbackDeliveryInput struct {
	CallID, TargetID, EventSeq, PayloadBlobID uint64
	PayloadHMAC                               string
	ReplayExpiresAt                           *time.Time
}

func (s *Store) CreateCallbackDelivery(ctx context.Context, tx *sql.Tx, in CallbackDeliveryInput) (uint64, error) {
	if tx == nil || in.CallID == 0 || in.TargetID == 0 || in.EventSeq == 0 || in.PayloadBlobID == 0 || !validHexDigest(in.PayloadHMAC, 32) || in.ReplayExpiresAt == nil || !in.ReplayExpiresAt.After(nowUTC()) {
		return 0, ErrInvalidInput
	}
	var targetCallID uint64
	if err := tx.QueryRowContext(ctx, `SELECT call_id FROM gw_callback_targets WHERE id=? FOR SHARE`, in.TargetID).Scan(&targetCallID); err == sql.ErrNoRows {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	} else if targetCallID != in.CallID {
		return 0, ErrConflict
	}
	now := nowUTC()
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_callback_deliveries(call_id,callback_target_id,callback_event_seq,encrypted_payload_blob_id,payload_hmac,state,state_version,replay_expires_at,created_at,updated_at) VALUES (?,?,?,?,?,'pending',1,?,?,?)`, in.CallID, in.TargetID, in.EventSeq, in.PayloadBlobID, in.PayloadHMAC, in.ReplayExpiresAt, now, now)
	if err != nil {
		return 0, fmt.Errorf("insert callback delivery: %w", err)
	}
	return lastID(result)
}

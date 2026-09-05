package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type IdempotencyInput struct {
	TokenID, OperationContractID   uint64
	KeyHMAC, RequestHMAC           string
	HMACKeyVersion                 uint32
	ReplayExpiresAt, KeyReuseAfter *time.Time
}

type IdempotencyReservation struct {
	ID     uint64
	CallID *uint64
	Reused bool
}

// ReserveIdempotency creates the one durable idempotency fact. A key can only
// be reused for byte-identical normalized input; a different request is a
// conflict and never reaches routing or billing.
func (s *Store) ReserveIdempotency(ctx context.Context, tx *sql.Tx, in IdempotencyInput) (IdempotencyReservation, error) {
	if tx == nil || in.TokenID == 0 || in.OperationContractID == 0 || !validHexDigest(in.KeyHMAC, 32) || !validHexDigest(in.RequestHMAC, 32) || in.HMACKeyVersion == 0 {
		return IdempotencyReservation{}, ErrInvalidInput
	}
	var id uint64
	var call sql.NullInt64
	var requestHMAC, status string
	err := tx.QueryRowContext(ctx, `SELECT id,call_id,request_hmac,status FROM gw_api_call_idempotencies WHERE token_id=? AND operation_contract_id=? AND hmac_key_version=? AND key_hmac=? FOR UPDATE`, in.TokenID, in.OperationContractID, in.HMACKeyVersion, in.KeyHMAC).Scan(&id, &call, &requestHMAC, &status)
	if err == nil {
		if requestHMAC != in.RequestHMAC || status != "active" {
			return IdempotencyReservation{}, ErrConflict
		}
		var callID *uint64
		if call.Valid {
			value := uint64(call.Int64)
			callID = &value
		}
		return IdempotencyReservation{ID: id, CallID: callID, Reused: true}, nil
	}
	if err != sql.ErrNoRows {
		return IdempotencyReservation{}, err
	}
	now := nowUTC()
	var replay, reuse any
	if in.ReplayExpiresAt != nil {
		replay = in.ReplayExpiresAt.UTC()
	}
	if in.KeyReuseAfter != nil {
		reuse = in.KeyReuseAfter.UTC()
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_api_call_idempotencies(token_id,operation_contract_id,key_hmac,hmac_key_version,request_hmac,status,replay_expires_at,key_reuse_after,created_at,updated_at) VALUES (?,?,?,?,?,'active',?,?,?,?)`, in.TokenID, in.OperationContractID, in.KeyHMAC, in.HMACKeyVersion, in.RequestHMAC, replay, reuse, now, now)
	if err != nil {
		// A concurrent insert can win the unique key between the lookup and
		// INSERT. Re-read the winner while holding its row lock.
		var existingID uint64
		var existingCall sql.NullInt64
		var existingHMAC, existingStatus string
		readErr := tx.QueryRowContext(ctx, `SELECT id,call_id,request_hmac,status FROM gw_api_call_idempotencies WHERE token_id=? AND operation_contract_id=? AND hmac_key_version=? AND key_hmac=? FOR UPDATE`, in.TokenID, in.OperationContractID, in.HMACKeyVersion, in.KeyHMAC).Scan(&existingID, &existingCall, &existingHMAC, &existingStatus)
		if readErr == nil {
			if existingHMAC != in.RequestHMAC || existingStatus != "active" {
				return IdempotencyReservation{}, ErrConflict
			}
			var callID *uint64
			if existingCall.Valid {
				value := uint64(existingCall.Int64)
				callID = &value
			}
			return IdempotencyReservation{ID: existingID, CallID: callID, Reused: true}, nil
		}
		return IdempotencyReservation{}, fmt.Errorf("reserve idempotency: %w", err)
	}
	id, err = lastID(result)
	if err != nil {
		return IdempotencyReservation{}, err
	}
	return IdempotencyReservation{ID: id}, nil
}

func (s *Store) AttachIdempotencyCall(ctx context.Context, tx *sql.Tx, id, callID uint64) error {
	if tx == nil || id == 0 || callID == 0 {
		return ErrInvalidInput
	}
	res, err := tx.ExecContext(ctx, `UPDATE gw_api_call_idempotencies SET call_id=?,updated_at=? WHERE id=? AND status='active' AND call_id IS NULL`, callID, nowUTC(), id)
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

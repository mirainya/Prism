package repository

import (
	"context"
	"database/sql"
	"fmt"
)

type CallbackAttemptInput struct {
	DeliveryID  uint64
	AttemptNo   uint64
	RequestHMAC string
}

func (s *Store) CreateCallbackAttempt(ctx context.Context, tx *sql.Tx, in CallbackAttemptInput) (uint64, error) {
	if tx == nil || in.DeliveryID == 0 || in.AttemptNo == 0 || !validHexDigest(in.RequestHMAC, 32) {
		return 0, ErrInvalidInput
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_callback_delivery_attempts(callback_delivery_id,attempt_no,state,request_hmac,created_at) VALUES (?,?, 'dispatching', ?, ?)`, in.DeliveryID, in.AttemptNo, in.RequestHMAC, nowUTC())
	if err != nil {
		return 0, fmt.Errorf("create callback attempt: %w", err)
	}
	return lastID(result)
}

type CallbackAttemptResult struct {
	State, ResponseHMAC, ErrorCode    string
	HTTPStatus                        uint32
	RequestComplete, ResponseComplete bool
}

func (s *Store) FinishCallbackAttempt(ctx context.Context, tx *sql.Tx, attemptID uint64, result CallbackAttemptResult) error {
	if tx == nil || attemptID == 0 || (result.State != "succeeded" && result.State != "failed" && result.State != "unknown") || result.ResponseHMAC != "" && !validHexDigest(result.ResponseHMAC, 32) {
		return ErrInvalidInput
	}
	res, err := tx.ExecContext(ctx, `UPDATE gw_callback_delivery_attempts SET state=?,response_hmac=?,request_bytes_complete=?,response_bytes_complete=?,http_status=?,error_code=? WHERE id=? AND state='dispatching'`, result.State, emptyAsNull(result.ResponseHMAC), result.RequestComplete, result.ResponseComplete, nullableUint32(result.HTTPStatus), result.ErrorCode, attemptID)
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

func (s *Store) TransitionCallbackDelivery(ctx context.Context, tx *sql.Tx, deliveryID uint64, from, to string) error {
	if tx == nil || deliveryID == 0 || !validCallbackDeliveryState(from) || !validCallbackDeliveryState(to) {
		return ErrInvalidInput
	}
	if from == to {
		return nil
	}
	if !(from == "pending" && to == "sending" || from == "sending" && (to == "succeeded" || to == "failed" || to == "dead_letter")) {
		return ErrConflict
	}
	res, err := tx.ExecContext(ctx, `UPDATE gw_callback_deliveries SET state=?,state_version=state_version+1,updated_at=? WHERE id=? AND state=?`, to, nowUTC(), deliveryID, from)
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

func validCallbackDeliveryState(v string) bool {
	return v == "pending" || v == "sending" || v == "succeeded" || v == "failed" || v == "dead_letter"
}
func nullableUint32(v uint32) any {
	if v == 0 {
		return nil
	}
	return v
}

package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const MaxCallbackDeliveryAttempts = uint64(5)

// ClaimedCallbackDelivery is the immutable dispatch material plus the lease
// fence authorizing one outbound HTTP exchange.
type ClaimedCallbackDelivery struct {
	ID, CallID, TargetID, EventSeq        uint64
	TargetBlobID, PayloadBlobID           uint64
	AttemptID, AttemptNo, MaxAttempts     uint64
	CallPublicID, TargetHMAC, PayloadHMAC string
	Algorithm, LeaseOwner                 string
	PolicyVersion                         uint32
	ReplayExpiresAt, LeaseExpiresAt       time.Time
}

// ClaimCallbackDelivery leases one due delivery and creates its immutable
// attempt before network I/O. Reclaiming an expired sender marks its previous
// attempt unknown; the stable event ID lets receivers deduplicate a replay.
func (s *Store) ClaimCallbackDelivery(ctx context.Context, tx *sql.Tx, owner string, lease time.Duration) (ClaimedCallbackDelivery, error) {
	if tx == nil || owner == "" || len(owner) > 128 || lease <= 0 {
		return ClaimedCallbackDelivery{}, ErrInvalidInput
	}
	var out ClaimedCallbackDelivery
	var state string
	var previousLease sql.NullTime
	err := tx.QueryRowContext(ctx, `SELECT d.id,d.call_id,d.callback_target_id,d.callback_event_seq,
t.encrypted_config_blob_id,t.target_hmac,t.algorithm,t.policy_version,
d.encrypted_payload_blob_id,d.payload_hmac,d.attempt_count,d.max_attempts,d.replay_expires_at,d.state,d.lease_expires_at,c.public_id
FROM gw_callback_deliveries d
JOIN gw_callback_targets t ON t.id=d.callback_target_id AND t.call_id=d.call_id
JOIN gw_api_calls c ON c.id=d.call_id
WHERE d.replay_expires_at>CURRENT_TIMESTAMP(3) AND d.attempt_count<d.max_attempts AND
 ((d.state IN ('pending','failed') AND d.available_at<=CURRENT_TIMESTAMP(3)) OR
  (d.state='sending' AND d.lease_expires_at<=CURRENT_TIMESTAMP(3)))
ORDER BY d.available_at,d.id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(
		&out.ID, &out.CallID, &out.TargetID, &out.EventSeq,
		&out.TargetBlobID, &out.TargetHMAC, &out.Algorithm, &out.PolicyVersion,
		&out.PayloadBlobID, &out.PayloadHMAC, &out.AttemptNo, &out.MaxAttempts, &out.ReplayExpiresAt,
		&state, &previousLease, &out.CallPublicID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ClaimedCallbackDelivery{}, ErrNotFound
	}
	if err != nil {
		return ClaimedCallbackDelivery{}, err
	}
	if state == "sending" {
		if !previousLease.Valid || previousLease.Time.After(nowUTC()) {
			return ClaimedCallbackDelivery{}, ErrConflict
		}
		if err := requireOneRow(tx.ExecContext(ctx, `UPDATE gw_callback_delivery_attempts
SET state='unknown',error_code='worker_lease_expired'
WHERE callback_delivery_id=? AND attempt_no=? AND state='dispatching'`, out.ID, out.AttemptNo)); err != nil {
			return ClaimedCallbackDelivery{}, err
		}
	}
	out.AttemptNo++
	out.LeaseOwner = owner
	out.LeaseExpiresAt = nowUTC().Add(lease)
	if err := requireOneRow(tx.ExecContext(ctx, `UPDATE gw_callback_deliveries
SET state='sending',state_version=state_version+1,attempt_count=?,lease_owner=?,lease_expires_at=?,last_error_code='',updated_at=?
WHERE id=? AND state=? AND attempt_count=?`, out.AttemptNo, owner, out.LeaseExpiresAt, nowUTC(), out.ID, state, out.AttemptNo-1)); err != nil {
		return ClaimedCallbackDelivery{}, err
	}
	attemptID, err := s.CreateCallbackAttempt(ctx, tx, CallbackAttemptInput{
		DeliveryID: out.ID, AttemptNo: out.AttemptNo, RequestHMAC: out.PayloadHMAC,
	})
	if err != nil {
		return ClaimedCallbackDelivery{}, err
	}
	out.AttemptID = attemptID
	return out, nil
}

type CallbackDeliveryCompletion struct {
	AttemptState                      string
	ResponseHMAC, ErrorCode           string
	HTTPStatus                        uint32
	RequestComplete, ResponseComplete bool
	Outcome                           string
	RetryAt                           time.Time
}

// CompleteCallbackDelivery commits the attempt evidence and delivery state
// under the exact claim fence. A late worker cannot overwrite a reclaimed
// attempt.
func (s *Store) CompleteCallbackDelivery(ctx context.Context, tx *sql.Tx, claim ClaimedCallbackDelivery, in CallbackDeliveryCompletion) error {
	if tx == nil || claim.ID == 0 || claim.AttemptID == 0 || claim.AttemptNo == 0 || claim.LeaseOwner == "" || len(in.ErrorCode) > 128 {
		return ErrInvalidInput
	}
	switch in.Outcome {
	case "succeeded":
		if in.AttemptState != "succeeded" || in.ErrorCode != "" || in.HTTPStatus < 200 || in.HTTPStatus >= 300 || !in.RequestComplete {
			return ErrInvalidInput
		}
	case "retry":
		if in.AttemptState != "failed" && in.AttemptState != "unknown" || in.ErrorCode == "" || in.RetryAt.IsZero() {
			return ErrInvalidInput
		}
	case "dead_letter":
		if in.AttemptState != "failed" && in.AttemptState != "unknown" || in.ErrorCode == "" {
			return ErrInvalidInput
		}
	default:
		return ErrInvalidInput
	}
	if err := s.FinishCallbackAttempt(ctx, tx, claim.AttemptID, CallbackAttemptResult{
		State: in.AttemptState, ResponseHMAC: in.ResponseHMAC, ErrorCode: in.ErrorCode,
		HTTPStatus: in.HTTPStatus, RequestComplete: in.RequestComplete, ResponseComplete: in.ResponseComplete,
	}); err != nil {
		return err
	}
	now := nowUTC()
	state := "dead_letter"
	var availableAt any = now
	var completedAt any = now
	if in.Outcome == "succeeded" {
		state = "succeeded"
	} else if in.Outcome == "retry" {
		state = "failed"
		availableAt = in.RetryAt.UTC()
		completedAt = nil
	}
	return requireOneRow(tx.ExecContext(ctx, `UPDATE gw_callback_deliveries
SET state=?,state_version=state_version+1,available_at=?,lease_owner='',lease_expires_at=NULL,
 last_error_code=?,completed_at=?,updated_at=?
WHERE id=? AND state='sending' AND lease_owner=? AND attempt_count=? AND lease_expires_at>CURRENT_TIMESTAMP(3)`,
		state, availableAt, in.ErrorCode, completedAt, now, claim.ID, claim.LeaseOwner, claim.AttemptNo))
}

// DeadLetterUndeliverableCallbacks closes expired or exhausted work that can
// no longer be sent. A live sending lease is never interrupted.
func (s *Store) DeadLetterUndeliverableCallbacks(ctx context.Context, limit int) (int64, error) {
	if s == nil || s.db == nil || limit <= 0 || limit > 1000 {
		return 0, ErrInvalidInput
	}
	now := nowUTC()
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM gw_callback_deliveries
WHERE state IN ('pending','failed','sending') AND
 (state<>'sending' OR lease_expires_at<=?) AND
 (replay_expires_at<=? OR attempt_count>=max_attempts)
	ORDER BY id LIMIT ?`, now, now, limit)
	if err != nil {
		return 0, err
	}
	ids, err := collectUint64Rows(rows)
	if err != nil {
		return 0, err
	}
	var completed int64
	for _, deliveryID := range ids {
		err := s.WithTx(ctx, func(tx *sql.Tx) error {
			var state string
			var attemptCount, maxAttempts uint64
			var replayExpires time.Time
			var leaseExpires sql.NullTime
			err := tx.QueryRowContext(ctx, `SELECT state,attempt_count,max_attempts,replay_expires_at,lease_expires_at
FROM gw_callback_deliveries WHERE id=? FOR UPDATE`, deliveryID).
				Scan(&state, &attemptCount, &maxAttempts, &replayExpires, &leaseExpires)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			if state != "pending" && state != "failed" && state != "sending" ||
				state == "sending" && (!leaseExpires.Valid || leaseExpires.Time.After(now)) ||
				replayExpires.After(now) && attemptCount < maxAttempts {
				return nil
			}
			if state == "sending" {
				if err := requireOneRow(tx.ExecContext(ctx, `UPDATE gw_callback_delivery_attempts
SET state='unknown',error_code='worker_lease_expired'
WHERE callback_delivery_id=? AND attempt_no=? AND state='dispatching'`, deliveryID, attemptCount)); err != nil {
					return err
				}
			}
			reason := "callback_attempts_exhausted"
			if !replayExpires.After(now) {
				reason = "callback_replay_window_expired"
			}
			if err := requireOneRow(tx.ExecContext(ctx, `UPDATE gw_callback_deliveries
SET state='dead_letter',state_version=state_version+1,lease_owner='',lease_expires_at=NULL,
 last_error_code=?,completed_at=?,updated_at=?
WHERE id=? AND state=? AND attempt_count=?`, reason, now, now, deliveryID, state, attemptCount)); err != nil {
				return err
			}
			completed++
			return nil
		})
		if err != nil {
			return completed, err
		}
	}
	return completed, nil
}

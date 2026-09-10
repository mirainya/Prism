package runtime

import (
	"context"
	"database/sql"
	"time"

	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/execution"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

// CancelQueuedResponse cancels only an execution that is proven not to have
// been dispatched. Once a worker has claimed the Outbox, cancellation cannot
// establish whether the provider accepted the request and must be rejected.
func (s *Service) CancelQueuedResponse(ctx context.Context, userID, tokenID uint64, publicID string) error {
	if s == nil || s.Store == nil || userID == 0 || tokenID == 0 || publicID == "" {
		return repository.ErrInvalidInput
	}
	var callID, attemptID uint64
	if err := s.Store.DB().QueryRowContext(ctx, `SELECT c.id,a.id
FROM gw_api_resources r
JOIN gw_ai_responses response ON response.resource_id=r.id
JOIN gw_api_calls c ON c.id=r.call_id
JOIN gw_api_call_attempts a ON a.call_id=c.id
WHERE r.public_id=? AND r.resource_kind='response' AND r.user_id=? AND r.token_id=? AND r.deleted_at IS NULL
ORDER BY a.attempt_no DESC LIMIT 1`, publicID, userID, tokenID).Scan(&callID, &attemptID); err == sql.ErrNoRows {
		return repository.ErrNotFound
	} else if err != nil {
		return err
	}
	return s.Store.WithTx(ctx, func(tx *sql.Tx) error {
		reservationID, err := s.Store.LockCallBillingAncestors(ctx, tx, callID)
		if err != nil {
			return err
		}
		var callStatus string
		var callVersion uint64
		var currentAttemptID sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT status,state_version,current_attempt_id FROM gw_api_calls WHERE id=? FOR UPDATE`, callID).Scan(&callStatus, &callVersion, &currentAttemptID); err != nil {
			return err
		}
		var attemptStatus string
		var attemptVersion uint64
		if err := tx.QueryRowContext(ctx, `SELECT state,state_version FROM gw_api_call_attempts WHERE id=? AND call_id=? FOR UPDATE`, attemptID, callID).Scan(&attemptStatus, &attemptVersion); err != nil {
			return err
		}
		var resourceID uint64
		var responseStatus string
		if err := tx.QueryRowContext(ctx, `SELECT r.id,response.status FROM gw_api_resources r JOIN gw_ai_responses response ON response.resource_id=r.id WHERE r.call_id=? AND r.public_id=? AND r.user_id=? AND r.token_id=? AND r.deleted_at IS NULL FOR UPDATE`, callID, publicID, userID, tokenID).Scan(&resourceID, &responseStatus); err != nil {
			return err
		}
		if callStatus == string(execution.CallCancelled) && attemptStatus == string(execution.AttemptCancelled) && responseStatus == "cancelled" {
			return nil
		}
		if callStatus != string(execution.CallInProgress) || !currentAttemptID.Valid || uint64(currentAttemptID.Int64) != attemptID || attemptStatus != string(execution.AttemptStarted) || responseStatus != "queued" {
			return repository.ErrConflict
		}
		var outboxID, attemptCount uint64
		var outboxStatus, leaseOwner string
		if err := tx.QueryRowContext(ctx, `SELECT id,status,attempt_count,lease_owner FROM gw_async_outbox WHERE attempt_id=? AND action='submit' ORDER BY action_seq DESC LIMIT 1 FOR UPDATE`, attemptID).Scan(&outboxID, &outboxStatus, &attemptCount, &leaseOwner); err != nil {
			return err
		}
		var requestID uint64
		requestErr := tx.QueryRowContext(ctx, `SELECT id FROM gw_channel_request_logs WHERE attempt_id=? ORDER BY request_seq DESC LIMIT 1 FOR UPDATE`, attemptID).Scan(&requestID)
		if requestErr != nil && requestErr != sql.ErrNoRows {
			return requestErr
		}
		if outboxStatus != "pending" || attemptCount != 0 || leaseOwner != "" || requestErr == nil {
			return repository.ErrConflict
		}
		if err := requireAffected(tx.ExecContext(ctx, `UPDATE gw_async_outbox SET status='failed',last_error_code='cancelled_before_send',updated_at=? WHERE id=? AND status='pending' AND attempt_count=0 AND lease_owner=''`, time.Now().UTC(), outboxID)); err != nil {
			return err
		}
		if err := s.Store.UpdateAIResponse(ctx, tx, resourceID, "cancelled", map[string]any{"error_code": "cancelled_before_send"}); err != nil {
			return err
		}
		if err := s.Store.TransitionAttempt(ctx, tx, attemptID, execution.AttemptStarted, execution.AttemptCancelled, attemptVersion, "cancelled_before_send"); err != nil {
			return err
		}
		if err := s.Store.TransitionCall(ctx, tx, callID, execution.CallInProgress, execution.CallCancelled, callVersion, "cancelled_before_send", &attemptID); err != nil {
			return err
		}
		if err := s.Store.FinalizeAttemptSlot(ctx, tx, attemptID); err != nil {
			return err
		}
		return s.finishBilling(ctx, tx, callID, reservationID, execution.CallCancelled, billing.Facts{})
	})
}

// DeleteResponse hides a terminal stored response from the public API while
// retaining its immutable execution, billing, and audit facts.
func (s *Service) DeleteResponse(ctx context.Context, userID, tokenID uint64, publicID string) error {
	if s == nil || s.Store == nil || userID == 0 || tokenID == 0 || publicID == "" {
		return repository.ErrInvalidInput
	}
	return s.Store.WithTx(ctx, func(tx *sql.Tx) error {
		var resourceID uint64
		var status string
		if err := tx.QueryRowContext(ctx, `SELECT r.id,response.status FROM gw_api_resources r JOIN gw_ai_responses response ON response.resource_id=r.id WHERE r.public_id=? AND r.resource_kind='response' AND r.user_id=? AND r.token_id=? AND r.deleted_at IS NULL FOR UPDATE`, publicID, userID, tokenID).Scan(&resourceID, &status); err == sql.ErrNoRows {
			return repository.ErrNotFound
		} else if err != nil {
			return err
		}
		if status != "completed" && status != "failed" && status != "cancelled" && status != "incomplete" {
			return repository.ErrConflict
		}
		return requireAffected(tx.ExecContext(ctx, `UPDATE gw_api_resources SET deleted_at=? WHERE id=? AND deleted_at IS NULL`, time.Now().UTC(), resourceID))
	})
}

func requireAffected(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return repository.ErrConflict
	}
	return nil
}

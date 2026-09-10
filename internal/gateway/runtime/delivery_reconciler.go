package runtime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mirainya/Prism/internal/gateway/repository"
)

type deliveryAction struct {
	attemptID         uint64
	state             string
	version, sequence uint64
	completed         bool
}

func (s *Service) lockDeliveryAction(ctx context.Context, tx *sql.Tx, item repository.OutboxItem, allowCompleted bool) (deliveryAction, error) {
	var action deliveryAction
	if tx == nil || item.ResultDeliveryID == 0 || item.Action != "reconcile_delivery" || item.CallID != 0 || item.AttemptID != 0 || item.AsyncExecutionID != 0 || item.CallbackReceiptID != 0 {
		return action, repository.ErrInvalidInput
	}
	if err := tx.QueryRowContext(ctx, `SELECT attempt_id FROM gw_result_deliveries WHERE id=?`, item.ResultDeliveryID).Scan(&action.attemptID); err == sql.ErrNoRows {
		return action, repository.ErrNotFound
	} else if err != nil {
		return action, err
	}
	var attemptID uint64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM gw_api_call_attempts WHERE id=? FOR UPDATE`, action.attemptID).Scan(&attemptID); err != nil {
		return action, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT state,state_version,action_seq FROM gw_result_deliveries WHERE id=? AND attempt_id=? FOR UPDATE`, item.ResultDeliveryID, action.attemptID).Scan(&action.state, &action.version, &action.sequence); err != nil {
		return action, err
	}
	if allowCompleted {
		var err error
		action.completed, err = s.Store.DeliveryOutboxSucceeded(ctx, tx, item)
		if err != nil || action.completed {
			return action, err
		}
	}
	return action, s.Store.AssertDeliveryOutboxLease(ctx, tx, item)
}

// PrepareDeliveryReconciliation closes an interrupted query log and consumes
// obsolete actions before another idempotent provider query is attempted.
func (s *Service) PrepareDeliveryReconciliation(ctx context.Context, item repository.OutboxItem) (bool, error) {
	handled := false
	err := s.Store.WithTx(ctx, func(tx *sql.Tx) error {
		action, err := s.lockDeliveryAction(ctx, tx, item, true)
		if err != nil || action.completed {
			handled = action.completed
			return err
		}
		var requestID uint64
		var status string
		err = tx.QueryRowContext(ctx, `SELECT id,status FROM gw_channel_request_logs WHERE outbox_id=? AND result_delivery_id=? AND outbox_attempt_count<? AND action='reconcile_delivery' ORDER BY request_seq DESC LIMIT 1 FOR UPDATE`, item.ID, item.ResultDeliveryID, item.Attempts).Scan(&requestID, &status)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if status == "dispatching" || status == "sent" {
			if err := s.finishRequest(ctx, tx, requestID, "unknown", repository.RequestLogResult{ErrorCode: "worker_exchange_lost"}); err != nil {
				return err
			}
		}
		if action.version != item.StateVersion || action.sequence != item.ActionSeq || action.state != "delivery_failed" && action.state != "expired" {
			handled = true
			return s.Store.CompleteDeliveryOutbox(ctx, tx, item, true, "superseded_action")
		}
		return nil
	})
	return handled, err
}

func (s *Service) BeginDeliveryReconcileRequest(ctx context.Context, item repository.OutboxItem, mappingHMAC, requestHMAC string) (uint64, error) {
	var requestID uint64
	err := s.Store.WithTx(ctx, func(tx *sql.Tx) error {
		action, err := s.lockDeliveryAction(ctx, tx, item, false)
		if err != nil {
			return fmt.Errorf("lock delivery reconciliation: %w", err)
		}
		if action.version != item.StateVersion || action.sequence != item.ActionSeq || action.state != "delivery_failed" && action.state != "expired" {
			return repository.ErrConflict
		}
		var requestSeq uint64
		err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(request_seq),0)+1 FROM gw_channel_request_logs WHERE attempt_id=? FOR UPDATE`, action.attemptID).Scan(&requestSeq)
		if err != nil {
			return fmt.Errorf("select delivery request sequence: %w", err)
		}
		requestID, err = s.beginRequest(ctx, tx, repository.RequestLogInput{AttemptID: &action.attemptID, RequestSeq: requestSeq, Action: "reconcile_delivery", MappingHMAC: mappingHMAC, RequestBytesHMAC: requestHMAC})
		if err != nil {
			return fmt.Errorf("begin delivery request: %w", err)
		}
		if err := requireDeliveryRequestLink(tx.ExecContext(ctx, `UPDATE gw_channel_request_logs SET result_delivery_id=?,outbox_id=?,outbox_attempt_count=? WHERE id=? AND attempt_id=? AND status='dispatching'`, item.ResultDeliveryID, item.ID, item.Attempts, requestID, action.attemptID)); err != nil {
			return fmt.Errorf("bind delivery request: %w", err)
		}
		if err := s.Store.AssertDeliveryOutboxLease(ctx, tx, item); err != nil {
			return fmt.Errorf("verify delivery request lease: %w", err)
		}
		return nil
	})
	return requestID, err
}

func requireDeliveryRequestLink(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return repository.ErrConflict
	}
	return nil
}

func (s *Service) FinishDeliveryReconcileRequest(ctx context.Context, item repository.OutboxItem, requestID uint64, status string, result repository.RequestLogResult) error {
	if requestID == 0 || status != "response_recorded" && status != "unknown" {
		return repository.ErrInvalidInput
	}
	return s.Store.WithTx(ctx, func(tx *sql.Tx) error {
		action, err := s.lockDeliveryAction(ctx, tx, item, true)
		if err != nil {
			return err
		}
		var linkedID uint64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM gw_channel_request_logs WHERE id=? AND attempt_id=? AND result_delivery_id=? AND outbox_id=? AND outbox_attempt_count=? AND action='reconcile_delivery' FOR UPDATE`, requestID, action.attemptID, item.ResultDeliveryID, item.ID, item.Attempts).Scan(&linkedID); err != nil {
			return err
		}
		if action.completed {
			return nil
		}
		if action.version != item.StateVersion || action.sequence != item.ActionSeq {
			return repository.ErrConflict
		}
		return s.finishRequest(ctx, tx, requestID, status, result)
	})
}

func (s *Service) ApplyDeliveryRefresh(ctx context.Context, item repository.OutboxItem, requestID uint64, response repository.RequestLogResult, source repository.BlobInput, expiresAt time.Time) error {
	if requestID == 0 || expiresAt.IsZero() || response.HTTPStatus == nil || *response.HTTPStatus < 200 || *response.HTTPStatus >= 300 || !response.ResponseComplete || response.ResponseBytesHMAC == "" {
		return repository.ErrInvalidInput
	}
	return s.Store.WithTx(ctx, func(tx *sql.Tx) error {
		action, err := s.lockDeliveryAction(ctx, tx, item, true)
		if err != nil {
			return err
		}
		var linkedID uint64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM gw_channel_request_logs WHERE id=? AND attempt_id=? AND result_delivery_id=? AND outbox_id=? AND outbox_attempt_count=? AND action='reconcile_delivery' FOR UPDATE`, requestID, action.attemptID, item.ResultDeliveryID, item.ID, item.Attempts).Scan(&linkedID); err != nil {
			return err
		}
		if action.completed {
			return nil
		}
		if action.version != item.StateVersion || action.sequence != item.ActionSeq || action.state != "delivery_failed" && action.state != "expired" {
			return repository.ErrConflict
		}
		if err := s.finishRequest(ctx, tx, requestID, "response_recorded", response); err != nil {
			return err
		}
		if err := s.Store.RefreshReferenceDelivery(ctx, tx, item.ResultDeliveryID, source, requestID, &expiresAt); err != nil {
			return err
		}
		return s.Store.CompleteDeliveryOutbox(ctx, tx, item, true, "")
	})
}

var errDeliveryRefreshPending = errors.New("provider result URL is not refreshable yet")

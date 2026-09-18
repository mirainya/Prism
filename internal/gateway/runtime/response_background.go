package runtime

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/execution"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

type BackgroundResponseResult struct {
	Item      repository.OutboxItem
	RequestID uint64
	Payload   repository.BlobInput
	Summary   any
	Facts     billing.Facts
	Exchange  repository.RequestLogResult
}

// CompleteBackgroundResponse commits the captured exchange, encrypted result,
// public projection, execution terminal state, settlement, Outbox completion,
// and lease release in one transaction.
func (s *Service) CompleteBackgroundResponse(ctx context.Context, in BackgroundResponseResult) error {
	if in.Item.AttemptID == 0 || in.RequestID == 0 || len(in.Payload.Plaintext) == 0 {
		return repository.ErrInvalidInput
	}
	return s.finishBackgroundResponse(ctx, in.Item, in.RequestID, "completed", execution.AttemptCompleted, execution.CallCompleted, "background_response_completed", in.Facts, &in.Payload, in.Summary, in.Exchange)
}

func (s *Service) FailBackgroundResponse(ctx context.Context, item repository.OutboxItem, requestID uint64, reason string, exchange repository.RequestLogResult) error {
	if item.AttemptID == 0 || reason == "" {
		return repository.ErrInvalidInput
	}
	return s.finishBackgroundResponse(ctx, item, requestID, "failed", execution.AttemptFailed, execution.CallFailed, reason, billing.Facts{}, nil, map[string]any{"error_code": reason}, exchange)
}

func (s *Service) MarkBackgroundResponseIndeterminate(ctx context.Context, item repository.OutboxItem, reason string) error {
	if item.AttemptID == 0 || reason == "" {
		return repository.ErrInvalidInput
	}
	return s.Store.WithTx(ctx, func(tx *sql.Tx) error {
		reservationID, err := s.lockBackgroundResponse(ctx, tx, item)
		if err != nil {
			return err
		}
		var requestID uint64
		err = tx.QueryRowContext(ctx, `SELECT id FROM gw_channel_request_logs WHERE attempt_id=? AND action IN ('submit','recover') ORDER BY request_seq DESC LIMIT 1 FOR UPDATE`, item.AttemptID).Scan(&requestID)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if requestID != 0 {
			if err := s.finishRequest(ctx, tx, requestID, "unknown", repository.RequestLogResult{ErrorCode: reason}); err != nil {
				return err
			}
		}
		return s.finishBackgroundResponseTx(ctx, tx, item, reservationID, "incomplete", execution.AttemptTerminatedUnknown, execution.CallIndeterminate, reason, billing.Facts{}, nil, map[string]any{"error_code": reason})
	})
}

func (s *Service) finishBackgroundResponse(ctx context.Context, item repository.OutboxItem, requestID uint64, responseStatus string, attemptState execution.AttemptState, callState execution.CallState, reason string, facts billing.Facts, payload *repository.BlobInput, summary any, exchange repository.RequestLogResult) error {
	return s.Store.WithTx(ctx, func(tx *sql.Tx) error {
		reservationID, err := s.lockBackgroundResponse(ctx, tx, item)
		if err != nil {
			return err
		}
		if requestID != 0 {
			var attemptID uint64
			var action string
			if err := tx.QueryRowContext(ctx, `SELECT attempt_id,action FROM gw_channel_request_logs WHERE id=? FOR UPDATE`, requestID).Scan(&attemptID, &action); err != nil {
				return err
			}
			if attemptID != item.AttemptID || action != item.Action {
				return repository.ErrConflict
			}
			status := "response_recorded"
			if !exchange.ResponseComplete {
				status = "unknown"
			}
			if err := s.finishRequest(ctx, tx, requestID, status, exchange); err != nil {
				return err
			}
		}
		return s.finishBackgroundResponseTx(ctx, tx, item, reservationID, responseStatus, attemptState, callState, reason, facts, payload, summary)
	})
}

func (s *Service) lockBackgroundResponse(ctx context.Context, tx *sql.Tx, item repository.OutboxItem) (uint64, error) {
	if item.AttemptID == 0 || item.CallLease.CallID == 0 {
		return 0, repository.ErrInvalidInput
	}
	var callID uint64
	if err := tx.QueryRowContext(ctx, `SELECT call_id FROM gw_api_call_attempts WHERE id=?`, item.AttemptID).Scan(&callID); err != nil {
		return 0, err
	}
	if callID != item.CallLease.CallID {
		return 0, repository.ErrConflict
	}
	reservationID, err := s.Store.LockCallBillingAncestors(ctx, tx, callID)
	if err != nil {
		return 0, err
	}
	if err := s.Store.AssertAttemptOutboxLease(ctx, tx, item); err != nil {
		return 0, err
	}
	return reservationID, nil
}

func (s *Service) finishBackgroundResponseTx(ctx context.Context, tx *sql.Tx, item repository.OutboxItem, reservationID uint64, responseStatus string, attemptState execution.AttemptState, callState execution.CallState, reason string, facts billing.Facts, payload *repository.BlobInput, summary any) error {
	var callID, attemptVersion, resourceID uint64
	var attemptFrom, callFrom string
	var callVersion uint64
	if err := tx.QueryRowContext(ctx, `SELECT a.call_id,a.state,a.state_version,c.status,c.state_version,r.id
FROM gw_api_call_attempts a JOIN gw_api_calls c ON c.id=a.call_id
JOIN gw_api_resources r ON r.call_id=c.id AND r.resource_kind='response'
WHERE a.id=? FOR UPDATE`, item.AttemptID).Scan(&callID, &attemptFrom, &attemptVersion, &callFrom, &callVersion, &resourceID); err != nil {
		return err
	}
	if callID != item.CallLease.CallID {
		return repository.ErrConflict
	}
	if payload != nil {
		if _, err := s.Store.PutCallPayload(ctx, tx, callID, "result", *payload); err != nil {
			return err
		}
	}
	if err := s.Store.UpdateAIResponse(ctx, tx, resourceID, responseStatus, summary); err != nil {
		return err
	}
	// The completion helper verifies both leases while the call is still active.
	// All following writes remain part of this transaction and roll it back on
	// failure, so marking the Outbox first cannot publish a partial terminal fact.
	if err := s.Store.CompleteAttemptOutbox(ctx, tx, item, ""); err != nil {
		return err
	}
	if execution.AttemptState(attemptFrom) != attemptState {
		if err := s.Store.TransitionAttempt(ctx, tx, item.AttemptID, execution.AttemptState(attemptFrom), attemptState, attemptVersion, reason); err != nil {
			return err
		}
	}
	if execution.CallState(callFrom) != callState {
		if err := s.Store.TransitionCall(ctx, tx, callID, execution.CallState(callFrom), callState, callVersion, reason, &item.AttemptID); err != nil {
			return err
		}
	}
	if err := s.finishBilling(ctx, tx, callID, reservationID, callState, facts); err != nil {
		return err
	}
	return nil
}

type AttemptOutboxDispatcher interface {
	DispatchAttempt(context.Context, repository.OutboxItem) error
	RecoverAttempt(context.Context, repository.OutboxItem) error
}

func (s *Service) ProcessAttemptOne(ctx context.Context, owner string, handler AttemptOutboxDispatcher) (bool, error) {
	if s == nil || s.Store == nil || owner == "" || handler == nil {
		return false, repository.ErrInvalidInput
	}
	if err := s.requireReadiness(ctx); err != nil {
		return false, err
	}
	var item repository.OutboxItem
	err := s.Store.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		item, err = s.Store.ClaimAttemptOutbox(ctx, tx, owner, 6*time.Minute)
		return err
	})
	if errors.Is(err, repository.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	workCtx, cancel := context.WithDeadline(ctx, item.LeaseExpiresAt)
	defer cancel()
	if item.MayHaveDispatched {
		err = handler.RecoverAttempt(workCtx, item)
	} else {
		err = handler.DispatchAttempt(workCtx, item)
	}
	if err == nil {
		return true, nil
	}
	markCtx, markCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer markCancel()
	markErr := s.Store.WithTx(markCtx, func(tx *sql.Tx) error {
		return s.Store.RetryAttemptOutbox(markCtx, tx, item, "worker_error", time.Now().UTC().Add(time.Second))
	})
	if markErr != nil {
		return true, markErr
	}
	return true, err
}

func (s *Service) RunAttemptOutbox(ctx context.Context, owner string, handler AttemptOutboxDispatcher, report func(error)) error {
	for ctx.Err() == nil {
		worked, err := s.ProcessAttemptOne(ctx, owner, handler)
		if errors.Is(err, ErrNotReady) {
			return err
		}
		if err != nil && report != nil {
			report(err)
		}
		delay := 250 * time.Millisecond
		if err != nil {
			delay = time.Second
		}
		if worked && err == nil {
			continue
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	return nil
}

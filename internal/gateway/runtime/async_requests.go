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

var ErrSubmissionUncertain = errors.New("gateway runtime: submission requires upstream reconciliation")

type asyncAction struct {
	attemptID         uint64
	state             execution.AsyncState
	version, sequence uint64
	completed         bool
}

// Lock order matches finalization: Attempt, AsyncExecution, Outbox, request.
func (s *Service) lockAsyncAction(ctx context.Context, tx *sql.Tx, item repository.OutboxItem, allowCompleted bool) (asyncAction, error) {
	var action asyncAction
	if item.AsyncExecutionID == 0 || item.CallID != 0 || item.AttemptID != 0 {
		return action, repository.ErrInvalidInput
	}
	if err := tx.QueryRowContext(ctx, `SELECT attempt_id FROM gw_async_executions WHERE id=?`, item.AsyncExecutionID).Scan(&action.attemptID); err != nil {
		return action, err
	}
	var id uint64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM gw_api_call_attempts WHERE id=? FOR UPDATE`, action.attemptID).Scan(&id); err != nil {
		return action, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT state,state_version,action_seq FROM gw_async_executions WHERE id=? FOR UPDATE`, item.AsyncExecutionID).Scan(&action.state, &action.version, &action.sequence); err != nil {
		return action, err
	}
	if allowCompleted {
		var err error
		action.completed, err = s.Store.AsyncOutboxSucceeded(ctx, tx, item)
		if err != nil || action.completed {
			return action, err
		}
	}
	return action, s.Store.AssertAsyncOutboxLease(ctx, tx, item)
}

type AsyncRequestEvidence struct {
	MappingHMAC, RequestHMAC string
	Payload                  *repository.BlobInput
}

// BeginAsyncRequest durably records the exact provider body and authorizes one
// HTTP exchange. A reclaimed submit must reconcile instead of being resent.
func (s *Service) BeginAsyncRequest(ctx context.Context, item repository.OutboxItem, evidence AsyncRequestEvidence) (uint64, error) {
	var requestID uint64
	err := s.Store.WithTx(ctx, func(tx *sql.Tx) error {
		action, err := s.lockAsyncAction(ctx, tx, item, false)
		if err != nil {
			return err
		}
		if action.version != item.StateVersion || action.sequence != item.ActionSeq || !asyncRequestAllowed(action.state, item.Action) {
			return repository.ErrConflict
		}
		if item.Action == "submit" {
			var id uint64
			err := tx.QueryRowContext(ctx, `SELECT id FROM gw_channel_request_logs WHERE attempt_id=? AND action='submit' AND status<>'not_sent' LIMIT 1 FOR UPDATE`, action.attemptID).Scan(&id)
			if err == nil {
				return ErrSubmissionUncertain
			}
			if err != sql.ErrNoRows {
				return err
			}
		}
		var sequence uint64
		err = tx.QueryRowContext(ctx, `SELECT request_seq FROM gw_channel_request_logs WHERE attempt_id=? ORDER BY request_seq DESC LIMIT 1 FOR UPDATE`, action.attemptID).Scan(&sequence)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		requestID, err = s.beginRequest(ctx, tx, repository.RequestLogInput{AttemptID: &action.attemptID, RequestSeq: sequence + 1, Action: item.Action, MappingHMAC: evidence.MappingHMAC, RequestBytesHMAC: evidence.RequestHMAC})
		if err != nil {
			return err
		}
		if evidence.Payload != nil {
			if _, err = s.Store.PutRequestLogPayload(ctx, tx, requestID, "request", *evidence.Payload); err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE gw_channel_request_logs SET async_execution_id=?,outbox_id=?,outbox_attempt_count=? WHERE id=?`, item.AsyncExecutionID, item.ID, item.Attempts, requestID); err != nil {
			return err
		}
		return s.Store.AssertAsyncOutboxLease(ctx, tx, item)
	})
	if err != nil {
		return 0, err
	}
	return requestID, nil
}

func asyncRequestAllowed(state execution.AsyncState, action string) bool {
	switch action {
	case "submit":
		return state == execution.AsyncSubmitting
	case "query":
		return state == execution.AsyncAccepted || state == execution.AsyncRunning
	case "recover":
		return state == execution.AsyncSubmissionUnknown || state == execution.AsyncManualReview
	case "cancel":
		return state == execution.AsyncCancelRequested || state == execution.AsyncCancelUnknown
	}
	return false
}

type AcceptAsyncInput struct {
	Item                       repository.OutboxItem
	RequestID                  uint64
	TaskIdentity               repository.BlobInput
	IdentityExpiresAt, QueryAt time.Time
	Response                   repository.RequestLogResult
}

// AcceptAsyncSubmission commits the HTTP outcome, encrypted provider identity,
// accepted state and next query atomically. A failed commit remains recoverable.
func (s *Service) AcceptAsyncSubmission(ctx context.Context, in AcceptAsyncInput) (uint64, error) {
	if in.RequestID == 0 || in.Item.Action != "submit" && in.Item.Action != "recover" || in.QueryAt.IsZero() || in.Response.HTTPStatus == nil || *in.Response.HTTPStatus < 200 || *in.Response.HTTPStatus >= 300 || !in.Response.ResponseComplete || in.Response.ResponseBytesHMAC == "" {
		return 0, repository.ErrInvalidInput
	}
	var identityID uint64
	err := s.Store.WithTx(ctx, func(tx *sql.Tx) error {
		action, err := s.lockAsyncAction(ctx, tx, in.Item, true)
		if err != nil {
			return err
		}
		var requestID uint64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM gw_channel_request_logs WHERE id=? AND outbox_id=? AND outbox_attempt_count=? AND attempt_id=? AND action=? FOR UPDATE`, in.RequestID, in.Item.ID, in.Item.Attempts, action.attemptID, in.Item.Action).Scan(&requestID); err != nil {
			return err
		}
		if action.completed {
			var responseHMAC string
			if err := tx.QueryRowContext(ctx, `SELECT response_bytes_hmac FROM gw_channel_request_logs WHERE id=? AND status='response_recorded'`, in.RequestID).Scan(&responseHMAC); err != nil {
				return err
			}
			if responseHMAC != in.Response.ResponseBytesHMAC {
				return repository.ErrConflict
			}
			identityID, err = s.Store.PutTaskIdentity(ctx, tx, in.Item.AsyncExecutionID, in.TaskIdentity, in.IdentityExpiresAt)
			if err != nil {
				return err
			}
			return s.updateAsyncResourceProjection(ctx, tx, in.Item.AsyncExecutionID, action.state)
		}
		if action.version != in.Item.StateVersion || action.sequence != in.Item.ActionSeq || !asyncRequestAllowed(action.state, in.Item.Action) {
			return repository.ErrConflict
		}
		identityID, err = s.Store.PutTaskIdentity(ctx, tx, in.Item.AsyncExecutionID, in.TaskIdentity, in.IdentityExpiresAt)
		if err != nil {
			return err
		}
		if err = s.finishRequest(ctx, tx, in.RequestID, "response_recorded", in.Response); err != nil {
			return err
		}
		seq, err := s.Store.TransitionAsync(ctx, tx, in.Item.AsyncExecutionID, action.state, execution.AsyncAccepted, action.version, "provider_accepted", "query")
		if err != nil {
			return err
		}
		if err := s.updateAsyncResourceProjection(ctx, tx, in.Item.AsyncExecutionID, execution.AsyncAccepted); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE gw_async_outbox SET available_at=? WHERE async_execution_id=? AND action_seq=? AND status='pending'`, in.QueryAt.UTC(), in.Item.AsyncExecutionID, seq); err != nil {
			return err
		}
		return s.Store.CompleteAsyncOutbox(ctx, tx, in.Item, true, "")
	})
	if err != nil {
		return 0, err
	}
	return identityID, nil
}

// Unknown is a recoverable execution fact, not proof of generation failure.
// The HTTP exchange has ended, so only its request slot is released here.
func (s *Service) RecordAsyncSubmissionUnknown(ctx context.Context, item repository.OutboxItem, requestID uint64, response repository.RequestLogResult) error {
	if item.Action != "submit" || requestID == 0 {
		return repository.ErrInvalidInput
	}
	return s.Store.WithTx(ctx, func(tx *sql.Tx) error {
		action, err := s.lockAsyncAction(ctx, tx, item, true)
		if err != nil {
			return err
		}
		var id uint64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM gw_channel_request_logs WHERE id=? AND outbox_id=? AND outbox_attempt_count=? AND attempt_id=? AND action='submit' FOR UPDATE`, requestID, item.ID, item.Attempts, action.attemptID).Scan(&id); err != nil {
			return err
		}
		if action.completed {
			return nil
		}
		if action.state != execution.AsyncSubmitting || action.version != item.StateVersion || action.sequence != item.ActionSeq {
			return repository.ErrConflict
		}
		if err := s.finishRequest(ctx, tx, requestID, "unknown", response); err != nil {
			return err
		}
		if _, err := s.Store.TransitionAsync(ctx, tx, item.AsyncExecutionID, action.state, execution.AsyncSubmissionUnknown, action.version, "submission_unverified", "recover"); err != nil {
			return err
		}
		if err := s.updateAsyncResourceProjection(ctx, tx, item.AsyncExecutionID, execution.AsyncSubmissionUnknown); err != nil {
			return err
		}
		return s.Store.CompleteAsyncOutbox(ctx, tx, item, true, "")
	})
}

// RecordAsyncSubmissionNotCreated commits a complete provider rejection. A
// fully received non-2xx response is authoritative evidence that this submit
// did not create a task, so it must not be held for unknown-result recovery.
func (s *Service) RecordAsyncSubmissionNotCreated(ctx context.Context, item repository.OutboxItem, requestID uint64, response repository.RequestLogResult) error {
	if item.Action != "submit" || item.AsyncExecutionID == 0 || requestID == 0 || response.HTTPStatus == nil || *response.HTTPStatus >= 200 && *response.HTTPStatus < 300 || !response.RequestComplete || !response.ResponseComplete || response.ResponseBytesHMAC == "" {
		return repository.ErrInvalidInput
	}
	return s.finishAsync(ctx, item.AsyncExecutionID, execution.AsyncNotCreated, "provider_rejected_submission", billing.Facts{}, func(ctx context.Context, tx *sql.Tx) error {
		var state execution.AsyncState
		var version, sequence uint64
		if err := tx.QueryRowContext(ctx, `SELECT state,state_version,action_seq FROM gw_async_executions WHERE id=?`, item.AsyncExecutionID).Scan(&state, &version, &sequence); err != nil {
			return err
		}
		if state != execution.AsyncSubmitting || version != item.StateVersion || sequence != item.ActionSeq {
			return repository.ErrConflict
		}
		if err := s.Store.AssertAsyncOutboxLease(ctx, tx, item); err != nil {
			return err
		}
		var id uint64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM gw_channel_request_logs WHERE id=? AND outbox_id=? AND outbox_attempt_count=? AND attempt_id=(SELECT attempt_id FROM gw_async_executions WHERE id=?) AND action='submit' FOR UPDATE`, requestID, item.ID, item.Attempts, item.AsyncExecutionID).Scan(&id); err != nil {
			return err
		}
		if err := s.finishRequest(ctx, tx, requestID, "response_recorded", response); err != nil {
			return err
		}
		return s.Store.CompleteAsyncOutbox(ctx, tx, item, true, "")
	})
}

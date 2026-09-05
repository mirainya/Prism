// Package runtime is the transaction-level orchestration for unified gateway
// calls. Network transports are deliberately outside these methods: workers
// record dispatch facts here before and after every exchange.
package runtime

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/mirainya/Prism/internal/gateway/execution"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

type Service struct{ Store *repository.Store }

func New(store *repository.Store) (*Service, error) {
	if store == nil {
		return nil, repository.ErrInvalidInput
	}
	return &Service{Store: store}, nil
}

type SubmitInput struct {
	Call                          repository.CreateCallInput
	Reservation                   repository.ReservationInput
	Attempt                       repository.BeginAttemptInput
	Idempotency                   *repository.IdempotencyInput
	AsyncScopeKind, AsyncScopeKey string
	Asynchronous                  bool
}

type Submission struct{ CallID, AttemptID, AsyncExecutionID, ReservationID uint64 }

// Submit creates call, reservation, and attempt in one transaction. No HTTP
// request is sent before this method commits; a worker consumes the outbox
// created for asynchronous calls afterwards.
func (s *Service) Submit(ctx context.Context, in SubmitInput) (Submission, error) {
	var out Submission
	err := s.Store.WithTx(ctx, func(tx *sql.Tx) error {
		var idempotencyID uint64
		if in.Idempotency != nil {
			reserved, err := s.Store.ReserveIdempotency(ctx, tx, *in.Idempotency)
			if err != nil {
				return err
			}
			if reserved.Reused {
				if reserved.CallID == nil {
					return repository.ErrConflict
				}
				out.CallID = *reserved.CallID
				return nil
			}
			idempotencyID = reserved.ID
		}
		callID, err := s.Store.CreateCall(ctx, tx, in.Call)
		if err != nil {
			return err
		}
		out.CallID = callID
		if idempotencyID != 0 {
			if err := s.Store.AttachIdempotencyCall(ctx, tx, idempotencyID, callID); err != nil {
				return err
			}
		}
		in.Reservation.CallID = callID
		reservationID, err := s.Store.ReserveBilling(ctx, tx, in.Reservation)
		if err != nil {
			return err
		}
		out.ReservationID = reservationID
		in.Attempt.CallID = callID
		attemptID, err := s.Store.BeginAttempt(ctx, tx, in.Attempt)
		if err != nil {
			return err
		}
		out.AttemptID = attemptID
		if in.Asynchronous {
			if in.AsyncScopeKind == "" || in.AsyncScopeKey == "" {
				return repository.ErrInvalidInput
			}
			asyncID, err := s.Store.CreateAsyncExecution(ctx, tx, repository.CreateAsyncInput{AttemptID: attemptID, ScopeKind: in.AsyncScopeKind, ScopeKey: in.AsyncScopeKey})
			if err != nil {
				return err
			}
			out.AsyncExecutionID = asyncID
			_, err = s.Store.TransitionAsync(ctx, tx, asyncID, execution.AsyncAllocated, execution.AsyncSubmitting, 1, "submit", "submit")
			return err
		}
		return nil
	})
	if err != nil {
		return Submission{}, fmt.Errorf("submit gateway call: %w", err)
	}
	return out, nil
}

// FinishAttempt applies the terminal attempt and call transition together.
// Settlement/release is part of the same transaction, so a retry cannot
// charge a call twice.
func (s *Service) FinishAttempt(ctx context.Context, attemptID uint64, attemptState execution.AttemptState, callState execution.CallState, reason string, settle bool) error {
	return s.Store.WithTx(ctx, func(tx *sql.Tx) error {
		if attemptID == 0 || reason == "" {
			return repository.ErrInvalidInput
		}
		var callID, attemptVersion uint64
		var attemptFrom string
		if err := tx.QueryRowContext(ctx, `SELECT call_id,state,state_version FROM gw_api_call_attempts WHERE id=? FOR UPDATE`, attemptID).Scan(&callID, &attemptFrom, &attemptVersion); err == sql.ErrNoRows {
			return repository.ErrNotFound
		} else if err != nil {
			return err
		}
		attemptCurrent := execution.AttemptState(attemptFrom)
		if attemptCurrent != attemptState {
			if err := s.Store.TransitionAttempt(ctx, tx, attemptID, attemptCurrent, attemptState, attemptVersion, reason); err != nil {
				return err
			}
		}
		var callFrom string
		var callVersion uint64
		if err := tx.QueryRowContext(ctx, `SELECT status,state_version FROM gw_api_calls WHERE id=? FOR UPDATE`, callID).Scan(&callFrom, &callVersion); err != nil {
			return err
		}
		callCurrent := execution.CallState(callFrom)
		if callCurrent != callState {
			if err := s.Store.TransitionCall(ctx, tx, callID, callCurrent, callState, callVersion, reason, &attemptID); err != nil {
				return err
			}
		}
		var reservationID uint64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM billing_reservations WHERE call_id=?`, callID).Scan(&reservationID); err == nil {
			target := "released"
			event := "reservation_released"
			if settle {
				target = "settled"
				event = "reservation_settled"
			}
			if err := s.Store.ResolveReservation(ctx, tx, reservationID, target, event); err != nil {
				return err
			}
		} else if err != sql.ErrNoRows {
			return err
		}
		return nil
	})
}

// FinishAsync applies the provider terminal fact to AsyncExecution, its
// parent Attempt and the Call in one transaction. This prevents a callback
// or poller from leaving an accepted task with a terminal parent mismatch.
func (s *Service) FinishAsync(ctx context.Context, asyncID uint64, target execution.AsyncState, reason string) error {
	if asyncID == 0 || reason == "" {
		return repository.ErrInvalidInput
	}
	var attemptState execution.AttemptState
	var callState execution.CallState
	settle := false
	switch target {
	case execution.AsyncSucceeded:
		attemptState, callState, settle = execution.AttemptCompleted, execution.CallCompleted, true
	case execution.AsyncFailed:
		attemptState, callState = execution.AttemptFailed, execution.CallFailed
	case execution.AsyncCancelled:
		attemptState, callState = execution.AttemptCancelled, execution.CallCancelled
	case execution.AsyncNotCreated:
		attemptState, callState = execution.AttemptNotCreated, execution.CallFailed
	case execution.AsyncTerminatedUnknown:
		attemptState, callState = execution.AttemptTerminatedUnknown, execution.CallIndeterminate
	default:
		return repository.ErrInvalidInput
	}
	return s.Store.WithTx(ctx, func(tx *sql.Tx) error {
		var attemptID, asyncVersion, attemptVersion uint64
		var fromAsync, fromAttempt string
		if err := tx.QueryRowContext(ctx, `SELECT attempt_id,state,state_version FROM gw_async_executions WHERE id=? FOR UPDATE`, asyncID).Scan(&attemptID, &fromAsync, &asyncVersion); err == sql.ErrNoRows {
			return repository.ErrNotFound
		} else if err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT state,state_version FROM gw_api_call_attempts WHERE id=? FOR UPDATE`, attemptID).Scan(&fromAttempt, &attemptVersion); err != nil {
			return err
		}
		if execution.AsyncState(fromAsync) != target {
			if _, err := s.Store.TransitionAsync(ctx, tx, asyncID, execution.AsyncState(fromAsync), target, asyncVersion, reason, ""); err != nil {
				return err
			}
		}
		if execution.AttemptState(fromAttempt) != attemptState {
			if err := s.Store.TransitionAttempt(ctx, tx, attemptID, execution.AttemptState(fromAttempt), attemptState, attemptVersion, reason); err != nil {
				return err
			}
		}
		var callID uint64
		var fromCall string
		var callVersion uint64
		if err := tx.QueryRowContext(ctx, `SELECT call_id FROM gw_api_call_attempts WHERE id=?`, attemptID).Scan(&callID); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT status,state_version FROM gw_api_calls WHERE id=? FOR UPDATE`, callID).Scan(&fromCall, &callVersion); err != nil {
			return err
		}
		if execution.CallState(fromCall) != callState {
			if err := s.Store.TransitionCall(ctx, tx, callID, execution.CallState(fromCall), callState, callVersion, reason, &attemptID); err != nil {
				return err
			}
		}
		var reservationID uint64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM billing_reservations WHERE call_id=?`, callID).Scan(&reservationID); err == nil {
			targetReservation, event := "released", "reservation_released"
			if settle {
				targetReservation, event = "settled", "reservation_settled"
			}
			if target == execution.AsyncTerminatedUnknown {
				targetReservation, event = "unknown_hold", "reservation_held_unknown"
			}
			if err := s.Store.ResolveReservation(ctx, tx, reservationID, targetReservation, event); err != nil {
				return err
			}
		} else if err != sql.ErrNoRows {
			return err
		}
		return nil
	})
}

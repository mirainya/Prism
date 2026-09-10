package runtime

import (
	"context"
	"database/sql"

	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/delivery"
	"github.com/mirainya/Prism/internal/gateway/execution"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

type CapabilitySuccessInput struct {
	AttemptID, RequestID uint64
	Exchange             repository.RequestLogResult
	Result               repository.BlobInput
	Sources              []delivery.RemoteResult
	SourceURLPolicy      string
	Facts                billing.Facts
}

type CapabilityFailureInput struct {
	AttemptID, RequestID uint64
	Exchange             repository.RequestLogResult
	Reason               string
	Facts                billing.Facts
	Uncertain            bool
}

// RejectCapability closes an Attempt that could not produce a valid provider
// request. Recovery may first fence a stale Attempt as recovery_pending; the
// absence of a request log still proves that no network exchange was authorized.
func (s *Service) RejectCapability(ctx context.Context, attemptID uint64, reason string) error {
	if s == nil || s.Store == nil || attemptID == 0 || reason == "" || len(reason) > 64 {
		return repository.ErrInvalidInput
	}
	return s.Store.WithTx(ctx, func(tx *sql.Tx) error {
		var callID uint64
		if err := tx.QueryRowContext(ctx, `SELECT call_id FROM gw_api_call_attempts WHERE id=?`, attemptID).Scan(&callID); err != nil {
			return err
		}
		reservationID, err := s.Store.LockCallBillingAncestors(ctx, tx, callID)
		if err != nil {
			return err
		}
		var callState execution.CallState
		var callVersion uint64
		if err := tx.QueryRowContext(ctx, `SELECT status,state_version FROM gw_api_calls WHERE id=? FOR UPDATE`, callID).Scan(&callState, &callVersion); err != nil {
			return err
		}
		var attemptCallID, attemptVersion uint64
		var attemptState execution.AttemptState
		if err := tx.QueryRowContext(ctx, `SELECT call_id,state,state_version FROM gw_api_call_attempts WHERE id=? FOR UPDATE`, attemptID).Scan(&attemptCallID, &attemptState, &attemptVersion); err != nil {
			return err
		}
		if attemptCallID != callID ||
			(attemptState != execution.AttemptStarted && attemptState != execution.AttemptRecoveryPending) ||
			callState != execution.CallInProgress {
			return repository.ErrConflict
		}
		var resourceID uint64
		if err := tx.QueryRowContext(ctx, `SELECT r.id FROM gw_api_resources r JOIN gw_capability_tasks task ON task.resource_id=r.id WHERE r.call_id=? AND r.resource_kind='capability_task' FOR UPDATE`, callID).Scan(&resourceID); err != nil {
			return err
		}
		if err := s.Store.UpdateCapabilityTaskState(ctx, tx, resourceID, "not_created", 0); err != nil {
			return err
		}
		if err := s.Store.TransitionAttempt(ctx, tx, attemptID, attemptState, execution.AttemptNotCreated, attemptVersion, reason); err != nil {
			return err
		}
		if err := s.Store.TransitionCall(ctx, tx, callID, callState, execution.CallFailed, callVersion, reason, &attemptID); err != nil {
			return err
		}
		if err := s.Store.FinalizeAttemptSlot(ctx, tx, attemptID); err != nil {
			return err
		}
		return s.finishBilling(ctx, tx, callID, reservationID, execution.CallFailed, billing.Facts{})
	})
}

// CompleteCapability commits the HTTP response, protected deliveries, result
// payload, public task projection, terminal execution state, and settlement
// as one unit. Managed copies are uploaded first and deleted if the database
// transaction cannot commit.
func (s *Service) CompleteCapability(ctx context.Context, in CapabilitySuccessInput) error {
	if s == nil || s.Store == nil || in.AttemptID == 0 || in.RequestID == 0 || in.Exchange.HTTPStatus == nil || *in.Exchange.HTTPStatus < 200 || *in.Exchange.HTTPStatus >= 300 || !in.Exchange.RequestComplete || !in.Exchange.ResponseComplete || in.Exchange.ResponseBytesHMAC == "" || len(in.Result.Plaintext) == 0 || len(in.Sources) == 0 {
		return repository.ErrInvalidInput
	}
	deliveryMode, err := s.capabilityDeliveryMode(ctx, in.AttemptID)
	if err != nil {
		return err
	}
	if in.SourceURLPolicy != "fixed" && in.SourceURLPolicy != "refreshable" || delivery.ValidateResult(delivery.ResourceCapabilityTask, in.Result.Plaintext, in.Sources) != nil {
		return repository.ErrInvalidInput
	}
	managedCopies, err := s.prepareManagedResultCopies(ctx, in.AttemptID, deliveryMode, in.Sources)
	if err != nil {
		return err
	}
	err = s.Store.WithTx(ctx, func(tx *sql.Tx) error {
		callID, resourceID, attemptState, attemptVersion, callState, callVersion, reservationID, err := s.lockCapabilityCompletion(ctx, tx, in.AttemptID, in.RequestID)
		if err != nil {
			return err
		}
		if attemptState != execution.AttemptStarted && attemptState != execution.AttemptRecoveryPending || callState != execution.CallInProgress {
			return repository.ErrConflict
		}
		if err := s.finishRequest(ctx, tx, in.RequestID, "response_recorded", in.Exchange); err != nil {
			return err
		}
		if err := s.persistAsyncResult(ctx, tx, in.AttemptID, in.RequestID, in.Result, in.Sources, in.SourceURLPolicy, managedCopies); err != nil {
			return err
		}
		if err := s.Store.UpdateCapabilityTaskState(ctx, tx, resourceID, "completed", 100); err != nil {
			return err
		}
		if err := s.Store.TransitionAttempt(ctx, tx, in.AttemptID, attemptState, execution.AttemptCompleted, attemptVersion, "provider_completed"); err != nil {
			return err
		}
		if err := s.Store.TransitionCall(ctx, tx, callID, callState, execution.CallCompleted, callVersion, "provider_completed", &in.AttemptID); err != nil {
			return err
		}
		if err := s.Store.FinalizeAttemptSlot(ctx, tx, in.AttemptID); err != nil {
			return err
		}
		return s.finishBilling(ctx, tx, callID, reservationID, execution.CallCompleted, in.Facts)
	})
	return err
}

// FailCapability records either a definitive synchronous failure or an
// unknown outcome. A complete exchange remains response_recorded even when a
// later local persistence failure makes the overall outcome indeterminate.
func (s *Service) FailCapability(ctx context.Context, in CapabilityFailureInput) error {
	if s == nil || s.Store == nil || in.AttemptID == 0 || in.RequestID == 0 || in.Reason == "" || len(in.Reason) > 64 {
		return repository.ErrInvalidInput
	}
	exchangeComplete := in.Exchange.HTTPStatus != nil && in.Exchange.RequestComplete && in.Exchange.ResponseComplete && in.Exchange.ResponseBytesHMAC != ""
	requestStatus := "response_recorded"
	if !exchangeComplete {
		requestStatus = "unknown"
	}
	if !in.Uncertain && !exchangeComplete {
		return repository.ErrInvalidInput
	}
	return s.Store.WithTx(ctx, func(tx *sql.Tx) error {
		callID, resourceID, attemptState, attemptVersion, callState, callVersion, reservationID, err := s.lockCapabilityCompletion(ctx, tx, in.AttemptID, in.RequestID)
		if err != nil {
			return err
		}
		if attemptState != execution.AttemptStarted && attemptState != execution.AttemptRecoveryPending || callState != execution.CallInProgress {
			return repository.ErrConflict
		}
		if err := s.finishRequest(ctx, tx, in.RequestID, requestStatus, in.Exchange); err != nil {
			return err
		}
		targetAttempt, targetCall, projection := execution.AttemptFailed, execution.CallFailed, "failed"
		if in.Uncertain {
			targetAttempt, targetCall, projection = execution.AttemptTerminatedUnknown, execution.CallIndeterminate, "terminated_unknown"
		}
		if err := s.Store.UpdateCapabilityTaskState(ctx, tx, resourceID, projection, 0); err != nil {
			return err
		}
		if err := s.Store.TransitionAttempt(ctx, tx, in.AttemptID, attemptState, targetAttempt, attemptVersion, in.Reason); err != nil {
			return err
		}
		if err := s.Store.TransitionCall(ctx, tx, callID, callState, targetCall, callVersion, in.Reason, &in.AttemptID); err != nil {
			return err
		}
		if err := s.Store.FinalizeAttemptSlot(ctx, tx, in.AttemptID); err != nil {
			return err
		}
		return s.finishBilling(ctx, tx, callID, reservationID, targetCall, in.Facts)
	})
}

func (s *Service) capabilityDeliveryMode(ctx context.Context, attemptID uint64) (string, error) {
	var mode, kind string
	err := s.Store.DB().QueryRowContext(ctx, `SELECT c.delivery_mode,r.resource_kind FROM gw_api_call_attempts a JOIN gw_api_calls c ON c.id=a.call_id JOIN gw_api_resources r ON r.call_id=c.id WHERE a.id=?`, attemptID).Scan(&mode, &kind)
	if err != nil {
		return "", err
	}
	if kind != delivery.ResourceCapabilityTask || mode != "reference" && mode != "managed_copy" {
		return "", repository.ErrConflict
	}
	return mode, nil
}

// Lock order: billing ancestors, Call, Attempt, RequestLog, Resource.
func (s *Service) lockCapabilityCompletion(ctx context.Context, tx *sql.Tx, attemptID, requestID uint64) (uint64, uint64, execution.AttemptState, uint64, execution.CallState, uint64, uint64, error) {
	var callID uint64
	if err := tx.QueryRowContext(ctx, `SELECT call_id FROM gw_api_call_attempts WHERE id=?`, attemptID).Scan(&callID); err != nil {
		return 0, 0, "", 0, "", 0, 0, err
	}
	reservationID, err := s.Store.LockCallBillingAncestors(ctx, tx, callID)
	if err != nil {
		return 0, 0, "", 0, "", 0, 0, err
	}
	var callState execution.CallState
	var callVersion uint64
	if err := tx.QueryRowContext(ctx, `SELECT status,state_version FROM gw_api_calls WHERE id=? FOR UPDATE`, callID).Scan(&callState, &callVersion); err != nil {
		return 0, 0, "", 0, "", 0, 0, err
	}
	var attemptCallID, attemptVersion uint64
	var attemptState execution.AttemptState
	if err := tx.QueryRowContext(ctx, `SELECT call_id,state,state_version FROM gw_api_call_attempts WHERE id=? FOR UPDATE`, attemptID).Scan(&attemptCallID, &attemptState, &attemptVersion); err != nil {
		return 0, 0, "", 0, "", 0, 0, err
	}
	if attemptCallID != callID {
		return 0, 0, "", 0, "", 0, 0, repository.ErrConflict
	}
	var asyncID uint64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM gw_async_executions WHERE attempt_id=? FOR UPDATE`, attemptID).Scan(&asyncID); err == nil {
		return 0, 0, "", 0, "", 0, 0, repository.ErrConflict
	} else if err != sql.ErrNoRows {
		return 0, 0, "", 0, "", 0, 0, err
	}
	var requestAttemptID uint64
	if err := tx.QueryRowContext(ctx, `SELECT attempt_id FROM gw_channel_request_logs WHERE id=? FOR UPDATE`, requestID).Scan(&requestAttemptID); err != nil {
		return 0, 0, "", 0, "", 0, 0, err
	}
	if requestAttemptID != attemptID {
		return 0, 0, "", 0, "", 0, 0, repository.ErrConflict
	}
	var resourceID uint64
	var resourceKind string
	if err := tx.QueryRowContext(ctx, `SELECT id,resource_kind FROM gw_api_resources WHERE call_id=? AND user_id=(SELECT user_id FROM gw_api_calls WHERE id=?) AND token_id=(SELECT token_id FROM gw_api_calls WHERE id=?) FOR UPDATE`, callID, callID, callID).Scan(&resourceID, &resourceKind); err != nil {
		return 0, 0, "", 0, "", 0, 0, err
	}
	if resourceKind != delivery.ResourceCapabilityTask {
		return 0, 0, "", 0, "", 0, 0, repository.ErrConflict
	}
	return callID, resourceID, attemptState, attemptVersion, callState, callVersion, reservationID, nil
}

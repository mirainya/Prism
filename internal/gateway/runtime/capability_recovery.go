package runtime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mirainya/Prism/internal/gateway/execution"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

const (
	defaultCapabilityRecoveryInterval = 30 * time.Second
	defaultCapabilityRecoveryGrace    = 6 * time.Minute
	defaultCapabilityRecoveryBatch    = 100
)

type CapabilityRecoveryPolicy struct {
	Interval  time.Duration
	Grace     time.Duration
	BatchSize int
}

type capabilityRecoveryAction uint8

const (
	capabilityRecoveryNone capabilityRecoveryAction = iota
	capabilityRecoveryNotSent
	capabilityRecoveryUnknown
)

type capabilityRecoveryClaim struct {
	AttemptID uint64
	RequestID uint64
	Action    capabilityRecoveryAction
}

func normalizeCapabilityRecoveryPolicy(policy CapabilityRecoveryPolicy) (CapabilityRecoveryPolicy, error) {
	if policy.Interval == 0 {
		policy.Interval = defaultCapabilityRecoveryInterval
	}
	if policy.Grace == 0 {
		policy.Grace = defaultCapabilityRecoveryGrace
	}
	if policy.BatchSize == 0 {
		policy.BatchSize = defaultCapabilityRecoveryBatch
	}
	if policy.Interval < time.Second || policy.Interval > time.Hour ||
		policy.Grace < time.Minute || policy.Grace > 24*time.Hour ||
		policy.BatchSize < 1 || policy.BatchSize > 1000 {
		return CapabilityRecoveryPolicy{}, repository.ErrInvalidInput
	}
	return policy, nil
}

// RunCapabilityRecovery resolves synchronous capability Calls whose serving
// process disappeared. A missing request log is proof that the provider was
// never contacted. Once a request was authorized, the outcome is preserved as
// indeterminate instead of retrying or refunding an operation that may exist.
func (s *Service) RunCapabilityRecovery(ctx context.Context, policy CapabilityRecoveryPolicy, onError func(error)) error {
	if s == nil || s.Store == nil {
		return repository.ErrInvalidInput
	}
	policy, err := normalizeCapabilityRecoveryPolicy(policy)
	if err != nil {
		return err
	}
	run := func() error {
		if err := s.requireReadiness(ctx); err != nil {
			return err
		}
		if err := s.recoverStaleCapabilities(ctx, time.Now().UTC().Add(-policy.Grace), policy.BatchSize); err != nil &&
			!errors.Is(err, context.Canceled) && onError != nil {
			onError(err)
		}
		return nil
	}
	if err := run(); err != nil {
		return err
	}
	ticker := time.NewTicker(policy.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := run(); err != nil {
				return err
			}
		}
	}
}

func (s *Service) recoverStaleCapabilities(ctx context.Context, staleBefore time.Time, batchSize int) error {
	if staleBefore.IsZero() || batchSize < 1 || batchSize > 1000 {
		return repository.ErrInvalidInput
	}
	rows, err := s.Store.DB().QueryContext(ctx, `SELECT a.id
FROM gw_api_call_attempts a
JOIN gw_api_calls c ON c.id=a.call_id AND c.status='in_progress'
JOIN gw_api_resources r ON r.call_id=c.id AND r.resource_kind='capability_task'
LEFT JOIN gw_async_executions x ON x.attempt_id=a.id
WHERE x.id IS NULL AND a.state IN ('started','recovery_pending') AND a.updated_at<=?
ORDER BY a.updated_at,a.id LIMIT ?`, staleBefore, batchSize)
	if err != nil {
		return fmt.Errorf("list stale capability attempts: %w", err)
	}
	defer rows.Close()
	ids := make([]uint64, 0, batchSize)
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	var joined error
	for _, id := range ids {
		if err := s.recoverStaleCapability(ctx, id, staleBefore); err != nil &&
			!errors.Is(err, repository.ErrConflict) && !errors.Is(err, repository.ErrNotFound) {
			joined = errors.Join(joined, fmt.Errorf("recover capability attempt %d: %w", id, err))
		}
	}
	return joined
}

func (s *Service) recoverStaleCapability(ctx context.Context, attemptID uint64, staleBefore time.Time) error {
	claim, err := s.claimStaleCapability(ctx, attemptID, staleBefore)
	if err != nil || claim.Action == capabilityRecoveryNone {
		return err
	}
	switch claim.Action {
	case capabilityRecoveryNotSent:
		return s.RejectCapability(ctx, claim.AttemptID, "process_stopped_before_send")
	case capabilityRecoveryUnknown:
		return s.FailCapability(ctx, CapabilityFailureInput{
			AttemptID: claim.AttemptID,
			RequestID: claim.RequestID,
			Exchange: repository.RequestLogResult{
				ErrorCode: "process_interrupted",
			},
			Reason:    "process_interrupted",
			Uncertain: true,
		})
	default:
		return repository.ErrConflict
	}
}

func (s *Service) claimStaleCapability(ctx context.Context, attemptID uint64, staleBefore time.Time) (capabilityRecoveryClaim, error) {
	if attemptID == 0 || staleBefore.IsZero() {
		return capabilityRecoveryClaim{}, repository.ErrInvalidInput
	}
	claim := capabilityRecoveryClaim{AttemptID: attemptID}
	err := s.Store.WithTx(ctx, func(tx *sql.Tx) error {
		var callID uint64
		if err := tx.QueryRowContext(ctx, `SELECT call_id FROM gw_api_call_attempts WHERE id=?`, attemptID).Scan(&callID); err == sql.ErrNoRows {
			return repository.ErrNotFound
		} else if err != nil {
			return err
		}
		if _, err := s.Store.LockCallBillingAncestors(ctx, tx, callID); err != nil {
			return err
		}
		var callState execution.CallState
		if err := tx.QueryRowContext(ctx, `SELECT status FROM gw_api_calls WHERE id=? FOR UPDATE`, callID).Scan(&callState); err != nil {
			return err
		}
		var attemptCallID, stateVersion uint64
		var state execution.AttemptState
		var updatedAt time.Time
		if err := tx.QueryRowContext(ctx, `SELECT call_id,state,state_version,updated_at FROM gw_api_call_attempts WHERE id=? FOR UPDATE`, attemptID).Scan(&attemptCallID, &state, &stateVersion, &updatedAt); err != nil {
			return err
		}
		if attemptCallID != callID || callState != execution.CallInProgress ||
			(state != execution.AttemptStarted && state != execution.AttemptRecoveryPending) ||
			updatedAt.After(staleBefore) {
			return repository.ErrConflict
		}
		var resourceID uint64
		if err := tx.QueryRowContext(ctx, `SELECT r.id FROM gw_api_resources r
JOIN gw_capability_tasks task ON task.resource_id=r.id
LEFT JOIN gw_async_executions x ON x.attempt_id=?
WHERE r.call_id=? AND r.resource_kind='capability_task' AND x.id IS NULL FOR UPDATE`, attemptID, callID).Scan(&resourceID); err == sql.ErrNoRows {
			return repository.ErrConflict
		} else if err != nil {
			return err
		}
		var requestID sql.NullInt64
		var requestStatus sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT id,status FROM gw_channel_request_logs
WHERE attempt_id=? ORDER BY request_seq DESC LIMIT 1 FOR UPDATE`, attemptID).Scan(&requestID, &requestStatus); err != nil && err != sql.ErrNoRows {
			return err
		}
		if !requestID.Valid {
			claim.Action = capabilityRecoveryNotSent
		} else {
			switch requestStatus.String {
			case "dispatching", "sent", "unknown":
				claim.Action = capabilityRecoveryUnknown
				claim.RequestID = uint64(requestID.Int64)
			default:
				return repository.ErrConflict
			}
		}
		if state == execution.AttemptStarted {
			return s.Store.TransitionAttempt(ctx, tx, attemptID, state, execution.AttemptRecoveryPending, stateVersion, "process_recovery_claimed")
		}
		return nil
	})
	return claim, err
}

package repository

import (
	"context"
	"database/sql"

	"github.com/mirainya/Prism/internal/gateway/execution"
)

// FinalizeAttemptSlot belongs to the same transaction as the terminal attempt.
// Unknown outcomes retain capacity until a separate reconciliation proves an end.
func (s *Store) FinalizeAttemptSlot(ctx context.Context, tx *sql.Tx, attemptID uint64) error {
	if tx == nil || attemptID == 0 {
		return ErrInvalidInput
	}
	var state execution.AttemptState
	if err := tx.QueryRowContext(ctx, `SELECT state FROM gw_api_call_attempts WHERE id=? FOR UPDATE`, attemptID).Scan(&state); err != nil {
		return err
	}
	switch state {
	case execution.AttemptCompleted, execution.AttemptFailed, execution.AttemptCancelled, execution.AttemptNotCreated, execution.AttemptTerminatedUnknown:
	default:
		return ErrConflict
	}
	var slotID uint64
	var slotState string
	if err := tx.QueryRowContext(ctx, `SELECT id,state FROM gw_credential_slots WHERE active_attempt_id=? FOR UPDATE`, attemptID).Scan(&slotID, &slotState); err == sql.ErrNoRows {
		return nil
	} else if err != nil {
		return err
	}
	unknown := state == execution.AttemptTerminatedUnknown
	if unknown && slotState == "recovery_required" {
		return nil
	}
	return s.ReleaseCredentialSlot(ctx, tx, slotID, unknown)
}

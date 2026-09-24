package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/mirainya/Prism/internal/gateway/execution"
)

// A repeated queued/running observation still consumes a unique action and
// advances the execution revision; an already-dispatched event is immutable.
func (s *Store) ScheduleAsyncQuery(ctx context.Context, tx *sql.Tx, asyncID uint64, from, to execution.AsyncState, version uint64, at time.Time) error {
	if tx == nil || asyncID == 0 || version == 0 || at.IsZero() {
		return ErrInvalidInput
	}
	// A terminated-unknown execution may be resolved only by terminal evidence.
	// Never let a late non-terminal query reopen it as accepted/running.
	if from == execution.AsyncTerminatedUnknown {
		return ErrConflict
	}
	if from != execution.AsyncAccepted && from != execution.AsyncRunning || to != execution.AsyncAccepted && to != execution.AsyncRunning || from == execution.AsyncRunning && to == execution.AsyncAccepted {
		return ErrInvalidInput
	}
	if err := requireOneRow(tx.ExecContext(ctx, `UPDATE gw_async_executions SET state=?,state_version=state_version+1,action_seq=action_seq+1,updated_at=? WHERE id=? AND state=? AND state_version=?`, to, nowUTC(), asyncID, from, version)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO gw_state_transition_events(async_execution_id,old_state,new_state,state_version,reason_code,created_at) VALUES (?,?,?,?,?,?)`, asyncID, from, to, version+1, "provider_progress", nowUTC()); err != nil {
		return err
	}
	return requireOneRow(tx.ExecContext(ctx, `INSERT INTO gw_async_outbox(async_execution_id,action_seq,action,status,state_version,available_at,attempt_count,created_at,updated_at) SELECT id,action_seq,'query','pending',state_version,?,0,?,? FROM gw_async_executions WHERE id=? AND state_version=?`, at.UTC(), nowUTC(), nowUTC(), asyncID, version+1))
}

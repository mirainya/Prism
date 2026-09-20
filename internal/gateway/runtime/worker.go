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

// OutboxDispatcher makes lease recovery explicit. A reclaimed item must query
// the upstream fact first; it must never silently repeat a submit request.
type OutboxDispatcher interface {
	Dispatch(context.Context, repository.OutboxItem) error
	Recover(context.Context, repository.OutboxItem) error
}

// PermanentDispatchError is reserved for malformed actions that cannot recover
// without an operator repair. Upstream outages and capacity limits are transient.
type PermanentDispatchError struct{ Code string }

func (e *PermanentDispatchError) Error() string { return e.Code }

// ProcessOne leases exactly one outbox action, executes network work outside
// the database transaction, then records success or a bounded retry. The
// handler must be idempotent and use the item's action sequence as its fence.
func (s *Service) ProcessOne(ctx context.Context, owner string, lease time.Duration, retryDelay time.Duration, handler OutboxDispatcher) (bool, error) {
	if handler == nil || owner == "" || lease <= 0 || retryDelay <= 0 {
		return false, repository.ErrInvalidInput
	}
	if err := s.requireReadiness(ctx); err != nil {
		return false, err
	}
	var item repository.OutboxItem
	claimed := false
	err := s.Store.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		item, err = s.Store.ClaimAsyncOutbox(ctx, tx, owner, lease)
		if err == repository.ErrNotFound {
			return err
		}
		claimed = err == nil
		return err
	})
	if err == repository.ErrNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !claimed {
		return false, nil
	}
	workCtx, cancel := context.WithDeadline(ctx, item.LeaseExpiresAt)
	defer cancel()
	var workErr error
	if item.MayHaveDispatched {
		workErr = handler.Recover(workCtx, item)
	} else {
		workErr = handler.Dispatch(workCtx, item)
	}
	if workErr != nil {
		// Persist the retry fact with a fresh context. A cancelled request must
		// not prevent the worker from releasing or fencing its lease.
		markCtx, markCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer markCancel()
		markErr := s.Store.WithTx(markCtx, func(tx *sql.Tx) error {
			var permanent *PermanentDispatchError
			if errors.As(workErr, &permanent) && permanent.Code != "" {
				return s.finishPermanentAsyncDispatchTx(markCtx, tx, item, permanent.Code)
			}
			return s.Store.RetryAsyncOutbox(markCtx, tx, item, "worker_error", time.Now().UTC().Add(outboxRetryDelay(retryDelay, item.Attempts)))
		})
		if markErr != nil {
			return true, markErr
		}
		return true, workErr
	}
	markCtx, markCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer markCancel()
	err = s.Store.WithTx(markCtx, func(tx *sql.Tx) error {
		done, err := s.Store.AsyncOutboxSucceeded(markCtx, tx, item)
		if err != nil || done {
			return err
		}
		return s.Store.CompleteAsyncOutbox(markCtx, tx, item, true, "")
	})
	return true, err
}

func (s *Service) finishPermanentAsyncDispatchTx(ctx context.Context, tx *sql.Tx, item repository.OutboxItem, errorCode string) error {
	target, ok := permanentAsyncDispatchTarget(item.Action)
	if !ok {
		return s.Store.DeadLetterAsyncOutbox(ctx, tx, item, errorCode)
	}
	return s.finishAsyncTx(ctx, tx, item.AsyncExecutionID, target, errorCode, billing.Facts{}, func(ctx context.Context, tx *sql.Tx) error {
		var state execution.AsyncState
		var version, sequence uint64
		if err := tx.QueryRowContext(ctx, `SELECT state,state_version,action_seq FROM gw_async_executions WHERE id=?`, item.AsyncExecutionID).Scan(&state, &version, &sequence); err != nil {
			return err
		}
		if version != item.StateVersion || sequence != item.ActionSeq || !asyncRequestAllowed(state, item.Action) {
			return repository.ErrConflict
		}
		return s.Store.DeadLetterAsyncOutbox(ctx, tx, item, errorCode)
	})
}

func permanentAsyncDispatchTarget(action string) (execution.AsyncState, bool) {
	switch action {
	case "submit", "query", "cancel":
		return execution.AsyncFailed, true
	case "recover":
		return execution.AsyncTerminatedUnknown, true
	default:
		return "", false
	}
}

func outboxRetryDelay(base time.Duration, attempts uint64) time.Duration {
	const maximum = 5 * time.Minute
	if base >= maximum {
		return maximum
	}
	for attempts > 1 && base < maximum/2 {
		base *= 2
		attempts--
	}
	if attempts > 1 {
		return maximum
	}
	return base
}

// RunOutbox reads durable intents directly; a Redis outage cannot prevent
// execution recovery. Cancellation waits for the current exchange to be logged.
func (s *Service) RunOutbox(ctx context.Context, owner string, handler OutboxDispatcher, report func(error)) error {
	if s == nil || s.Store == nil || owner == "" || handler == nil {
		return repository.ErrInvalidInput
	}
	for ctx.Err() == nil {
		worked, err := s.ProcessOne(ctx, owner, 6*time.Minute, time.Second, handler)
		if ctx.Err() != nil {
			break
		}
		if errors.Is(err, ErrNotReady) {
			return err
		}
		if err != nil && report != nil {
			report(err)
		}
		if worked && err == nil {
			continue
		}
		delay := 250 * time.Millisecond
		if err != nil {
			delay = time.Second
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

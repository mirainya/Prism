package runtime

import (
	"context"
	"database/sql"
	"time"

	"github.com/mirainya/Prism/internal/gateway/repository"
)

// OutboxDispatcher makes lease recovery explicit. A reclaimed item must query
// the upstream fact first; it must never silently repeat a submit request.
type OutboxDispatcher interface {
	Dispatch(context.Context, repository.OutboxItem) error
	Recover(context.Context, repository.OutboxItem) error
}

// ProcessOne leases exactly one outbox action, executes network work outside
// the database transaction, then records success or a bounded retry. The
// handler must be idempotent and use the item's action sequence as its fence.
func (s *Service) ProcessOne(ctx context.Context, owner string, lease time.Duration, retryDelay time.Duration, handler OutboxDispatcher) (bool, error) {
	if handler == nil || owner == "" || lease <= 0 || retryDelay <= 0 {
		return false, repository.ErrInvalidInput
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
	workCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lease)
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
			if item.Attempts >= 10 {
				return s.Store.DeadLetterAsyncOutbox(markCtx, tx, item, "retry_limit_exceeded")
			}
			return s.Store.RetryAsyncOutbox(markCtx, tx, item, "worker_error", time.Now().UTC().Add(retryDelay))
		})
		if markErr != nil {
			return true, markErr
		}
		return true, workErr
	}
	markCtx, markCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer markCancel()
	err = s.Store.WithTx(markCtx, func(tx *sql.Tx) error { return s.Store.CompleteAsyncOutbox(markCtx, tx, item, true, "") })
	return true, err
}

package runtime

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/mirainya/Prism/internal/gateway/execution"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

// CallbackOutboxHandler decodes and applies a callback Receipt. Returning
// ErrCallbackNeedsQuery asks the worker to use the authoritative poll path.
type CallbackOutboxHandler interface {
	HandleCallback(context.Context, repository.OutboxItem) error
}

// ProcessCallbackOne consumes one callback Receipt outbox entry using the
// conservative query-only behavior. Deployments with a decoder should use
// RunCallbackOutboxWithHandler for direct terminal application.
func (s *Service) ProcessCallbackOne(ctx context.Context, owner string, lease time.Duration) (bool, error) {
	if s == nil || s.Store == nil || owner == "" || lease <= 0 {
		return false, repository.ErrInvalidInput
	}
	if err := s.requireReadiness(ctx); err != nil {
		return false, err
	}
	var item repository.OutboxItem
	err := s.Store.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		item, err = s.Store.ClaimCallbackOutbox(ctx, tx, owner, lease)
		return err
	})
	if errors.Is(err, repository.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	markCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = s.Store.WithTx(markCtx, func(tx *sql.Tx) error {
		locked, err := s.lockCallbackProcessing(markCtx, tx, item)
		if err != nil {
			return err
		}
		if locked.ReceiptStatus != "received" {
			// A prior worker may have committed the receipt and query before
			// losing its lease. Acknowledge the stale outbox safely.
			return s.Store.CompleteCallbackOutbox(markCtx, tx, item, true, "")
		}
		if locked.State == execution.AsyncAccepted || locked.State == execution.AsyncRunning {
			if err := s.Store.ScheduleCallbackQuery(markCtx, tx, item, locked.AsyncID, time.Now().UTC()); err != nil {
				return err
			}
			if err := s.updateAsyncResourceProjection(markCtx, tx, locked.AsyncID, locked.State); err != nil {
				return err
			}
		} else {
			if err := s.Store.TransitionCallbackReceipt(markCtx, tx, item.CallbackReceiptID, "received", "processed", "late_callback"); err != nil {
				return err
			}
		}
		return s.Store.CompleteCallbackOutbox(markCtx, tx, item, true, "")
	})
	return true, err
}

func (s *Service) RunCallbackOutbox(ctx context.Context, owner string, report func(error)) error {
	if s == nil || s.Store == nil || owner == "" {
		return repository.ErrInvalidInput
	}
	for ctx.Err() == nil {
		worked, err := s.ProcessCallbackOne(ctx, owner, 2*time.Minute)
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
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	return nil
}

// RunCallbackOutboxWithHandler is the production callback loop. The legacy
// loop above remains useful for deployments without a registered decoder.
func (s *Service) RunCallbackOutboxWithHandler(ctx context.Context, owner string, handler CallbackOutboxHandler, report func(error)) error {
	if s == nil || s.Store == nil || owner == "" || handler == nil {
		return repository.ErrInvalidInput
	}
	for ctx.Err() == nil {
		if err := s.requireReadiness(ctx); err != nil {
			return err
		}
		var item repository.OutboxItem
		err := s.Store.WithTx(ctx, func(tx *sql.Tx) error {
			var err error
			item, err = s.Store.ClaimCallbackOutbox(ctx, tx, owner, 2*time.Minute)
			return err
		})
		if errors.Is(err, repository.ErrNotFound) {
			time.Sleep(250 * time.Millisecond)
			continue
		}
		if err != nil {
			if report != nil {
				report(err)
			}
			time.Sleep(time.Second)
			continue
		}
		handleErr := handler.HandleCallback(ctx, item)
		markCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		markErr := s.Store.WithTx(markCtx, func(tx *sql.Tx) error {
			if errors.Is(handleErr, ErrCallbackNeedsQuery) {
				locked, err := s.lockCallbackProcessing(markCtx, tx, item)
				if err != nil {
					return err
				}
				if locked.ReceiptStatus != "received" {
					return s.Store.CompleteCallbackOutbox(markCtx, tx, item, true, "")
				}
				switch locked.State {
				case execution.AsyncAccepted, execution.AsyncRunning:
					if err := s.Store.ScheduleCallbackQuery(markCtx, tx, item, locked.AsyncID, time.Now().UTC()); err != nil {
						return err
					}
					if err := s.updateAsyncResourceProjection(markCtx, tx, locked.AsyncID, locked.State); err != nil {
						return err
					}
				case execution.AsyncSucceeded, execution.AsyncFailed, execution.AsyncCancelled:
					if err := s.Store.TransitionCallbackReceipt(markCtx, tx, item.CallbackReceiptID, "received", "processed", "late_callback"); err != nil {
						return err
					}
				case execution.AsyncNotCreated, execution.AsyncTerminatedUnknown:
					const code = "callback_conflicts_with_terminal_execution"
					if err := s.Store.TransitionCallbackReceipt(markCtx, tx, item.CallbackReceiptID, "received", "manual_review", code); err != nil {
						return err
					}
					return s.Store.DeadLetterAsyncOutbox(markCtx, tx, item, code)
				default:
					return s.Store.RetryAsyncOutbox(markCtx, tx, item, "callback_waiting_for_submission", time.Now().UTC().Add(outboxRetryDelay(time.Second, item.Attempts)))
				}
				return s.Store.CompleteCallbackOutbox(markCtx, tx, item, true, "")
			}
			if handleErr != nil {
				locked, err := s.lockCallbackProcessing(markCtx, tx, item)
				if err != nil {
					return err
				}
				if locked.ReceiptStatus != "received" {
					return s.Store.CompleteCallbackOutbox(markCtx, tx, item, true, "")
				}
				var permanent *PermanentDispatchError
				if !errors.As(handleErr, &permanent) || permanent.Code == "" {
					return s.Store.RetryAsyncOutbox(markCtx, tx, item, "callback_processing_failed", time.Now().UTC().Add(outboxRetryDelay(time.Second, item.Attempts)))
				}
				// Invalid or unverifiable callback evidence is retained for
				// review; it must not remain indistinguishable from a pending
				// callback after the outbox item is acknowledged.
				if err := s.Store.TransitionCallbackReceipt(markCtx, tx, item.CallbackReceiptID, "received", "manual_review", permanent.Code); err != nil && !errors.Is(err, repository.ErrConflict) {
					return err
				}
				return s.Store.DeadLetterAsyncOutbox(markCtx, tx, item, permanent.Code)
			}
			done, err := s.Store.AsyncOutboxSucceeded(markCtx, tx, item)
			if err != nil || done {
				return err
			}
			return s.Store.CompleteCallbackOutbox(markCtx, tx, item, true, "")
		})
		cancel()
		if handleErr != nil && report != nil {
			report(handleErr)
		}
		if markErr != nil && report != nil {
			report(markErr)
		}
	}
	return nil
}

package runtime

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/mirainya/Prism/internal/gateway/repository"
)

type DeliveryReconciler interface {
	ReconcileDelivery(context.Context, repository.OutboxItem) error
}

func (s *Service) ProcessDeliveryOne(ctx context.Context, owner string, lease, retryDelay time.Duration, handler DeliveryReconciler) (bool, error) {
	if s == nil || s.Store == nil || handler == nil || owner == "" || lease <= 0 || retryDelay <= 0 {
		return false, repository.ErrInvalidInput
	}
	if err := s.requireReadiness(ctx); err != nil {
		return false, err
	}
	var item repository.OutboxItem
	err := s.Store.WithTx(ctx, func(tx *sql.Tx) error {
		if err := s.lockClaimDeployment(ctx, tx); err != nil {
			return err
		}
		var err error
		item, err = s.Store.ClaimDeliveryOutbox(ctx, tx, owner, lease)
		return err
	})
	if errors.Is(err, repository.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	workCtx, cancel := context.WithDeadline(ctx, item.LeaseExpiresAt)
	workErr := handler.ReconcileDelivery(workCtx, item)
	cancel()
	markCtx, markCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer markCancel()
	if workErr != nil {
		markErr := s.Store.WithTx(markCtx, func(tx *sql.Tx) error {
			var permanent *PermanentDispatchError
			if errors.As(workErr, &permanent) && permanent.Code != "" {
				return s.Store.DeadLetterDeliveryOutbox(markCtx, tx, item, permanent.Code)
			}
			at := time.Now().UTC().Add(outboxRetryDelay(retryDelay, item.Attempts))
			return s.Store.RetryDeliveryOutbox(markCtx, tx, item, "worker_error", at)
		})
		if markErr != nil {
			return true, markErr
		}
		return true, workErr
	}
	err = s.Store.WithTx(markCtx, func(tx *sql.Tx) error {
		done, err := s.Store.DeliveryOutboxSucceeded(markCtx, tx, item)
		if err != nil || done {
			return err
		}
		return s.Store.CompleteDeliveryOutbox(markCtx, tx, item, true, "")
	})
	return true, err
}

func (s *Service) RunDeliveryRecovery(ctx context.Context, owner string, handler DeliveryReconciler, report func(error)) error {
	if s == nil || s.Store == nil || owner == "" || handler == nil {
		return repository.ErrInvalidInput
	}
	for ctx.Err() == nil {
		worked, err := s.ProcessDeliveryOne(ctx, owner, 2*time.Minute, time.Second, handler)
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

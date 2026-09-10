package runtime

import (
	"context"
	"time"

	"github.com/mirainya/Prism/internal/gateway/repository"
)

type SensitiveRetentionPolicy struct {
	RequestLogAge time.Duration
	Interval      time.Duration
	BatchSize     int
}

// RunSensitiveRetention periodically removes expired encrypted execution
// bodies without deleting immutable call, task, or request-log facts.
func (s *Service) RunSensitiveRetention(ctx context.Context, policy SensitiveRetentionPolicy, onError func(error)) error {
	if s == nil || s.Store == nil || policy.RequestLogAge <= 0 || policy.Interval <= 0 || policy.BatchSize <= 0 || policy.BatchSize > 5000 {
		return repository.ErrInvalidInput
	}
	run := func() error {
		if err := s.requireReadiness(ctx); err != nil {
			return err
		}
		if err := s.purgeSensitivePayloads(ctx, policy); err != nil && ctx.Err() == nil && onError != nil {
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
			return ctx.Err()
		case <-ticker.C:
			if err := run(); err != nil {
				return err
			}
		}
	}
}

func (s *Service) purgeSensitivePayloads(ctx context.Context, policy SensitiveRetentionPolicy) error {
	now := time.Now().UTC()
	// Callback receipts and binding aliases have shorter, execution-scoped
	// lifetimes than ordinary request logs.  Process them first so a token or
	// provider body cannot survive merely because its parent call is still
	// retained for observability.
	for {
		count, err := s.Store.PurgeExpiredCallbackReceipts(ctx, now, policy.BatchSize)
		if err != nil {
			return err
		}
		if count < policy.BatchSize {
			break
		}
	}
	for {
		count, err := s.Store.PurgeExpiredCallbackBindings(ctx, now, policy.BatchSize)
		if err != nil {
			return err
		}
		if count < policy.BatchSize {
			break
		}
	}
	for {
		count, err := s.Store.PurgeExpiredCallPayloads(ctx, now, policy.BatchSize)
		if err != nil {
			return err
		}
		if count < policy.BatchSize {
			break
		}
	}
	cutoff := now.Add(-policy.RequestLogAge)
	for {
		count, err := s.Store.PurgeExpiredRequestLogPayloads(ctx, cutoff, now, policy.BatchSize)
		if err != nil {
			return err
		}
		if count < policy.BatchSize {
			return nil
		}
	}
}

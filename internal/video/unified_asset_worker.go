package video

import (
	"context"
	"time"
)

// UnifiedAssetCleanupPolicy controls cleanup of expired or deleting media
// assets. A small batch bounds each database transaction while the worker
// drains all currently eligible rows before sleeping.
type UnifiedAssetCleanupPolicy struct {
	Interval  time.Duration
	BatchSize int
}

// RunCleanup removes eligible media assets immediately and then at each
// interval. Storage or database failures are reported and retried on the next
// tick; shutdown waits for the current deletion attempt to return.
func (s *UnifiedAssetService) RunCleanup(ctx context.Context, policy UnifiedAssetCleanupPolicy, onError func(error)) error {
	if ctx == nil || s == nil || s.db == nil || policy.Interval <= 0 || policy.BatchSize <= 0 || policy.BatchSize > maxUnifiedAssetCleanupBatchSize {
		return ErrInvalidAsset
	}
	run := func() {
		for ctx.Err() == nil {
			count, err := s.PurgeExpired(ctx, policy.BatchSize)
			if err != nil {
				if ctx.Err() == nil && onError != nil {
					onError(err)
				}
				return
			}
			if count < policy.BatchSize {
				return
			}
		}
	}

	run()
	ticker := time.NewTicker(policy.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			run()
		}
	}
}

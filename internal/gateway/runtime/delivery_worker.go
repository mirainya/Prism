package runtime

import (
	"context"
	"time"
)

// RunDeliveryExpiry periodically advances expired reference deliveries. It
// is intentionally independent from the async outbox so delivery expiry can
// continue even when no provider task is being polled.
func (s *Service) RunDeliveryExpiry(ctx context.Context, report func(error)) error {
	if s == nil || s.Store == nil {
		return nil
	}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		if err := s.requireReadiness(ctx); err != nil {
			return err
		}
		if _, err := s.Store.ExpireReadyDeliveries(ctx, 100); err != nil && ctx.Err() == nil && report != nil {
			report(err)
		}
		if _, err := s.Store.ScheduleDueDeliveryReconciliations(ctx, 100); err != nil && ctx.Err() == nil && report != nil {
			report(err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

package runtime

import (
	"context"
	"database/sql"
	"errors"

	"github.com/mirainya/Prism/internal/gateway/execution"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

// updateAsyncResourceProjection mirrors a provider-independent execution state
// into its optional public domain projection. Transport-only calls do not have
// a projection, so ErrNotFound is intentionally treated as a no-op here.
func (s *Service) updateAsyncResourceProjection(ctx context.Context, tx *sql.Tx, asyncID uint64, state execution.AsyncState) error {
	if s == nil || s.Store == nil || tx == nil || asyncID == 0 {
		return repository.ErrInvalidInput
	}
	status, progress := asyncProjectionState(state)
	err := s.Store.UpdateAsyncResourceProjection(ctx, tx, asyncID, status, progress, nil)
	if errors.Is(err, repository.ErrNotFound) {
		return nil
	}
	return err
}

// asyncProjectionState keeps the public video/capability task vocabulary
// stable while preserving internal recovery states for operator visibility.
func asyncProjectionState(state execution.AsyncState) (string, uint8) {
	switch state {
	case execution.AsyncAllocated, execution.AsyncSubmitting:
		return "queued", 0
	case execution.AsyncAccepted:
		return "submitted", 0
	case execution.AsyncRunning:
		return "tracking", 0
	case execution.AsyncSucceeded:
		return "completed", 100
	case execution.AsyncFailed:
		return "failed", 0
	case execution.AsyncCancelled:
		return "cancelled", 0
	case execution.AsyncSubmissionUnknown:
		return "submission_unknown", 0
	case execution.AsyncManualReview:
		return "manual_review", 0
	case execution.AsyncCancelRequested:
		return "cancel_requested", 0
	case execution.AsyncCancelUnknown:
		return "cancel_unknown", 0
	case execution.AsyncNotCreated:
		return "not_created", 0
	case execution.AsyncTerminatedUnknown:
		return "terminated_unknown", 0
	default:
		return "unknown", 0
	}
}

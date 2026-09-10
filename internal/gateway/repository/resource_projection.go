package repository

import (
	"context"
	"database/sql"
	"time"
)

// AsyncResource identifies the public resource projection attached to an
// asynchronous execution. A call may have no projection (for example a
// transport-only operation), so callers must treat ErrNotFound as an
// intentional absence rather than as a database failure.
type AsyncResource struct {
	ID, CallID, UserID, TokenID uint64
	PublicID, Kind              string
}

// ReadAsyncResource resolves the resource through the immutable async ->
// attempt -> call relationship. Keeping this lookup here prevents runtime
// code from joining user-controlled public IDs and accidentally crossing
// tenant boundaries.
func (s *Store) ReadAsyncResource(ctx context.Context, db DB, asyncID uint64) (AsyncResource, error) {
	if db == nil || asyncID == 0 {
		return AsyncResource{}, ErrInvalidInput
	}
	var out AsyncResource
	err := db.QueryRowContext(ctx, `SELECT r.id,r.call_id,r.user_id,r.token_id,r.public_id,r.resource_kind
FROM gw_async_executions x
JOIN gw_api_call_attempts a ON a.id=x.attempt_id
JOIN gw_api_resources r ON r.call_id=a.call_id
WHERE x.id=?`, asyncID).Scan(&out.ID, &out.CallID, &out.UserID, &out.TokenID, &out.PublicID, &out.Kind)
	if err == sql.ErrNoRows {
		return AsyncResource{}, ErrNotFound
	}
	if err != nil {
		return AsyncResource{}, err
	}
	if out.ID == 0 || out.CallID == 0 || out.UserID == 0 || out.TokenID == 0 || out.PublicID == "" {
		return AsyncResource{}, ErrConflict
	}
	if out.Kind != "video_task" && out.Kind != "capability_task" && out.Kind != "response" && out.Kind != "file" {
		return AsyncResource{}, ErrConflict
	}
	return out, nil
}

// UpdateAsyncResourceProjection applies a canonical async observation to its
// domain projection in the same transaction as the execution transition.
// The summary is optional: nil preserves the original request summary and
// prevents a terminal result from replacing it with an empty JSON value.
func (s *Store) UpdateAsyncResourceProjection(ctx context.Context, tx *sql.Tx, asyncID uint64, status string, progress uint8, summary any) error {
	if tx == nil || asyncID == 0 || status == "" || len(status) > 24 || progress > 100 {
		return ErrInvalidInput
	}
	if !validProjectionStatus(status) {
		return ErrInvalidInput
	}
	resource, err := s.ReadAsyncResource(ctx, tx, asyncID)
	if err != nil {
		return err
	}
	var current string
	var currentProgress uint8
	var query string
	switch resource.Kind {
	case "video_task":
		query = `SELECT status,progress FROM gw_video_tasks WHERE resource_id=? FOR UPDATE`
	case "capability_task":
		query = `SELECT status,progress FROM gw_capability_tasks WHERE resource_id=? FOR UPDATE`
	default:
		// Responses and files do not have an async task projection yet. They
		// still resolve successfully, but changing them here would invent a
		// domain state that is not represented by their schema.
		return nil
	}
	if err := tx.QueryRowContext(ctx, query, resource.ID).Scan(&current, &currentProgress); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	// Projection states use the public domain vocabulary (submitted,
	// tracking, completed, ...), while the execution table uses its own
	// vocabulary.  Once a domain projection is terminal it may only receive
	// the same terminal state again; an older or different observation must
	// never reopen it.
	if isProjectionTerminal(current) {
		if current != status {
			return ErrConflict
		}
	}
	if progress < currentProgress {
		return ErrConflict
	}
	value, err := jsonValue(summary)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	var update string
	if resource.Kind == "video_task" {
		update = `UPDATE gw_video_tasks SET status=?,progress=?,specification_summary=COALESCE(?,specification_summary),updated_at=? WHERE resource_id=?`
	} else {
		update = `UPDATE gw_capability_tasks SET status=?,progress=?,parameter_summary=COALESCE(?,parameter_summary),updated_at=? WHERE resource_id=?`
	}
	result, err := tx.ExecContext(ctx, update, status, progress, value, now, resource.ID)
	if err != nil {
		return err
	}
	// RowsAffected may be zero when an idempotent replay writes the same
	// values, so existence was deliberately checked above and zero is valid.
	_, err = result.RowsAffected()
	return err
}

func validProjectionStatus(status string) bool {
	switch status {
	case "allocated", "submitting", "submission_unknown", "accepted", "submitted", "running", "tracking", "manual_review", "cancel_requested", "cancel_unknown", "succeeded", "completed", "failed", "cancelled", "not_created", "terminated_unknown", "queued", "unknown":
		return true
	default:
		return false
	}
}

func isProjectionTerminal(status string) bool {
	switch status {
	case "succeeded", "completed", "failed", "cancelled", "not_created", "terminated_unknown":
		return true
	default:
		return false
	}
}

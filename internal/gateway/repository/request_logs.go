package repository

import (
	"context"
	"database/sql"
	"fmt"
)

type RequestLogInput struct {
	AttemptID, ControlPlaneRunID  *uint64
	RequestSeq                    uint64
	Action                        string
	MappingHMAC, RequestBytesHMAC string
}

func (s *Store) CreateRequestLog(ctx context.Context, tx *sql.Tx, in RequestLogInput) (uint64, error) {
	if tx == nil || in.RequestSeq == 0 || !validHexDigest(in.MappingHMAC, 32) || in.RequestBytesHMAC != "" && !validHexDigest(in.RequestBytesHMAC, 32) || (in.AttemptID == nil) == (in.ControlPlaneRunID == nil) {
		return 0, ErrInvalidInput
	}
	if !validRequestAction(in.Action) {
		return 0, ErrInvalidInput
	}
	now := nowUTC()
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_channel_request_logs(attempt_id,control_plane_run_id,request_seq,action,status,request_mapping_hmac,request_bytes_hmac,created_at) VALUES (?,?,?,?,'prepared',?,?,?)`, nullableID(in.AttemptID), nullableID(in.ControlPlaneRunID), in.RequestSeq, in.Action, in.MappingHMAC, emptyAsNull(in.RequestBytesHMAC), now)
	if err != nil {
		return 0, fmt.Errorf("insert request log: %w", err)
	}
	return lastID(result)
}

type RequestLogResult struct {
	ResponseBytesHMAC                 string
	HTTPStatus                        *uint16
	DurationMS                        *uint64
	ErrorCode                         string
	RequestComplete, ResponseComplete bool
	ResponsePayload                   *BlobInput `json:"-"`
	Diagnostic                        *BlobInput `json:"-"`
}

func (s *Store) CompleteRequestLog(ctx context.Context, tx *sql.Tx, id uint64, status string, result RequestLogResult) error {
	if tx == nil || id == 0 || status == "" || result.ResponseBytesHMAC != "" && !validHexDigest(result.ResponseBytesHMAC, 32) {
		return ErrInvalidInput
	}
	if status != "dispatching" && status != "not_sent" && status != "sent" && status != "response_recorded" && status != "unknown" {
		return ErrInvalidInput
	}
	var current string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM gw_channel_request_logs WHERE id=? FOR UPDATE`, id).Scan(&current); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if !validRequestLogTransition(current, status) {
		return ErrConflict
	}
	if result.ResponsePayload != nil {
		if _, err := s.PutRequestLogPayload(ctx, tx, id, "response", *result.ResponsePayload); err != nil {
			return err
		}
	}
	if result.Diagnostic != nil {
		if _, err := s.PutRequestLogDiagnostic(ctx, tx, id, *result.Diagnostic); err != nil {
			return err
		}
	}
	now := nowUTC()
	var completedAt any = now
	if status == "dispatching" || status == "sent" {
		completedAt = nil
	}
	res, err := tx.ExecContext(ctx, `UPDATE gw_channel_request_logs SET status=?,response_bytes_hmac=?,request_bytes_complete=?,response_bytes_complete=?,http_status=?,duration_ms=?,error_code=?,completed_at=? WHERE id=? AND status IN ('prepared','dispatching','sent','unknown')`, status, emptyAsNull(result.ResponseBytesHMAC), result.RequestComplete, result.ResponseComplete, nullableUint16(result.HTTPStatus), nullableUint64(result.DurationMS), result.ErrorCode, completedAt, id)
	if err != nil {
		return fmt.Errorf("complete request log: %w", err)
	}
	ok, err := affected(res)
	if err != nil {
		return err
	}
	if !ok {
		return ErrConflict
	}
	return nil
}

func (s *Store) ReleaseCredentialSlot(ctx context.Context, tx *sql.Tx, id uint64, recoveryRequired bool) error {
	if tx == nil || id == 0 {
		return ErrInvalidInput
	}
	state := "released"
	if recoveryRequired {
		state = "recovery_required"
	}
	var current, scope string
	if err := tx.QueryRowContext(ctx, `SELECT state,scope FROM gw_credential_slots WHERE id=? FOR UPDATE`, id).Scan(&current, &scope); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if recoveryRequired {
		if current != "active" || scope != "task" {
			return ErrConflict
		}
	} else if current != "active" && current != "recovery_required" {
		return ErrConflict
	}
	var released any = nowUTC()
	if recoveryRequired {
		released = nil
	}
	res, err := tx.ExecContext(ctx, `UPDATE gw_credential_slots SET state=?,state_version=state_version+1,released_at=? WHERE id=? AND state IN ('active','recovery_required')`, state, released, id)
	if err != nil {
		return fmt.Errorf("release credential slot: %w", err)
	}
	ok, err := affected(res)
	if err != nil {
		return err
	}
	if !ok {
		return ErrConflict
	}
	return nil
}

func emptyAsNull(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func validRequestAction(value string) bool {
	switch value {
	case "submit", "recover", "query", "cancel", "reconcile_delivery", "named_action", "result_fetch", "catalog_discovery", "entitlement_probe", "commercial_check":
		return true
	default:
		return false
	}
}

func validRequestLogTransition(from, to string) bool {
	switch from {
	case "prepared":
		return to == "dispatching" || to == "not_sent"
	case "dispatching":
		return to == "sent" || to == "not_sent" || to == "unknown"
	case "sent":
		return to == "response_recorded" || to == "unknown"
	case "unknown":
		return to == "response_recorded" || to == "unknown"
	default:
		return false
	}
}
func nullableUint16(value *uint16) any {
	if value == nil {
		return nil
	}
	return *value
}
func nullableUint64(value *uint64) any {
	if value == nil {
		return nil
	}
	return *value
}

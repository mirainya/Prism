package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type ExecutionHealthInput struct {
	Scope               string
	ChannelID           uint64
	Fingerprint         string
	CredentialVersionID *uint64
}

func (s *Store) EnsureExecutionHealth(ctx context.Context, tx *sql.Tx, in ExecutionHealthInput) (uint64, error) {
	if tx == nil || in.ChannelID == 0 || in.Fingerprint == "" || (in.Scope != "shared" && in.Scope != "credential") {
		return 0, ErrInvalidInput
	}
	if in.Scope == "shared" && in.CredentialVersionID != nil || in.Scope == "credential" && in.CredentialVersionID == nil {
		return 0, ErrInvalidInput
	}
	var id uint64
	var err error
	if in.CredentialVersionID == nil {
		err = tx.QueryRowContext(ctx, `SELECT id FROM gw_execution_health WHERE scope='shared' AND channel_id=? AND execution_fingerprint=? AND credential_version_id IS NULL FOR UPDATE`, in.ChannelID, in.Fingerprint).Scan(&id)
	} else {
		err = tx.QueryRowContext(ctx, `SELECT id FROM gw_execution_health WHERE scope='credential' AND channel_id=? AND execution_fingerprint=? AND credential_version_id=? FOR UPDATE`, in.ChannelID, in.Fingerprint, *in.CredentialVersionID).Scan(&id)
	}
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_execution_health(scope,channel_id,execution_fingerprint,credential_version_id,state,state_version,updated_at) VALUES (?,?,?,?, 'closed',1,?)`, in.Scope, in.ChannelID, in.Fingerprint, nullableID(in.CredentialVersionID), nowUTC())
	if err != nil {
		return 0, fmt.Errorf("create execution health: %w", err)
	}
	return lastID(result)
}

func (s *Store) TransitionExecutionHealth(ctx context.Context, tx *sql.Tx, healthID uint64, from, to, evidenceHMAC string, requestLogID *uint64, retryAt *time.Time) error {
	if tx == nil || healthID == 0 || evidenceHMAC == "" || !validHealthState(from) || !validHealthState(to) {
		return ErrInvalidInput
	}
	if from == to {
		return nil
	}
	if !(from == "closed" && to == "open" || from == "open" && to == "half_open" || from == "half_open" && (to == "closed" || to == "open")) {
		return ErrConflict
	}
	res, err := tx.ExecContext(ctx, `UPDATE gw_execution_health SET state=?,state_version=state_version+1,retry_at=?,updated_at=? WHERE id=? AND state=?`, to, nullableTime(retryAt), nowUTC(), healthID, from)
	if err != nil {
		return err
	}
	ok, err := affected(res)
	if err != nil {
		return err
	}
	if !ok {
		return ErrConflict
	}
	var version uint64
	if err := tx.QueryRowContext(ctx, `SELECT state_version FROM gw_execution_health WHERE id=?`, healthID).Scan(&version); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO gw_execution_health_events(execution_health_id,health_version,old_state,new_state,request_log_id,evidence_hmac,created_at) VALUES (?,?,?,?,?,?,?)`, healthID, version, from, to, nullableID(requestLogID), evidenceHMAC, nowUTC())
	return err
}
func validHealthState(v string) bool { return v == "closed" || v == "open" || v == "half_open" }
func nullableTime(v *time.Time) any {
	if v == nil {
		return nil
	}
	return v.UTC()
}

package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var ErrConcurrencyLimit = errors.New("gateway repository: credential concurrency limit reached")

type SlotInput struct {
	CredentialID, CredentialPoolID uint64
	Scope                          string
	RequestLogID, AttemptID        *uint64
}

func (s *Store) AcquireCredentialSlot(ctx context.Context, tx *sql.Tx, in SlotInput) (uint64, error) {
	if tx == nil || in.CredentialID == 0 || in.CredentialPoolID == 0 || (in.Scope != "request" && in.Scope != "task") || (in.Scope == "request" && (in.RequestLogID == nil || *in.RequestLogID == 0 || in.AttemptID != nil)) || (in.Scope == "task" && (in.AttemptID == nil || *in.AttemptID == 0 || in.RequestLogID != nil)) {
		return 0, ErrInvalidInput
	}
	// Owners precede pool/credential and slot locks, as they do in finalization.
	owner, err := lockSlotOwner(ctx, tx, in)
	if err != nil {
		return 0, err
	}
	if err := lockAdmissionChannel(ctx, tx, in.CredentialPoolID); err != nil {
		return 0, err
	}
	var poolState, credentialState string
	var poolRequest, poolTask, keyRequest, keyTask sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT status,request_limit,task_limit FROM gw_credential_pools WHERE id=? FOR UPDATE`, in.CredentialPoolID).Scan(&poolState, &poolRequest, &poolTask); err == sql.ErrNoRows {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT status,request_limit,task_limit FROM gw_credentials WHERE id=? AND credential_pool_id=? FOR UPDATE`, in.CredentialID, in.CredentialPoolID).Scan(&credentialState, &keyRequest, &keyTask); err == sql.ErrNoRows {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	}
	if err := owner.validateAuthorization(ctx, tx, in); err != nil {
		return 0, err
	}
	if !slotStateAllows(poolState, owner.recovery) || !slotStateAllows(credentialState, owner.recovery) {
		return 0, ErrConflict
	}
	poolLimit, keyLimit := poolRequest, keyRequest
	if in.Scope == "task" {
		poolLimit, keyLimit = poolTask, keyTask
	}
	// The pool and credential locks serialize admission across processes. Use
	// current reads so callers using REPEATABLE READ cannot admit from a snapshot.
	if err := checkSlotLimit(ctx, tx, "credential_pool_id", in.CredentialPoolID, in.Scope, poolLimit); err != nil {
		return 0, err
	}
	if err := checkSlotLimit(ctx, tx, "credential_id", in.CredentialID, in.Scope, keyLimit); err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_credential_slots(credential_id,credential_pool_id,scope,request_log_id,attempt_id,state,state_version,acquired_at) VALUES (?,?,?,?,?,'active',1,?)`, in.CredentialID, in.CredentialPoolID, in.Scope, nullableID(in.RequestLogID), nullableID(in.AttemptID), nowUTC())
	if err != nil {
		return 0, fmt.Errorf("acquire credential slot: %w", err)
	}
	return lastID(result)
}

func slotStateAllows(state string, recovery bool) bool {
	return state == "active" || recovery && state == "draining"
}

func lockAdmissionChannel(ctx context.Context, tx *sql.Tx, poolID uint64) error {
	// Pool ownership is immutable. Channel locks precede pool locks in both
	// configuration writes and admission, including an attempt's first send.
	var channelID uint64
	if err := tx.QueryRowContext(ctx, `SELECT channel_id FROM gw_credential_pools WHERE id=?`, poolID).Scan(&channelID); err != nil {
		return err
	}
	var state string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM gateway_channels WHERE id=? FOR SHARE`, channelID).Scan(&state); err != nil {
		return err
	}
	if state != "active" {
		return ErrConflict
	}
	return nil
}

func checkSlotLimit(ctx context.Context, tx *sql.Tx, column string, id uint64, scope string, limit sql.NullInt64) error {
	if !limit.Valid {
		return nil
	}
	if limit.Int64 <= 0 {
		return ErrConcurrencyLimit
	}
	if column != "credential_pool_id" && column != "credential_id" {
		return ErrInvalidInput
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM gw_credential_slots WHERE `+column+`=? AND scope=? AND state IN ('active','recovery_required') LIMIT ? FOR UPDATE`, id, scope, limit.Int64)
	if err != nil {
		return err
	}
	defer rows.Close()
	var count int64
	for rows.Next() {
		count++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if count >= limit.Int64 {
		return ErrConcurrencyLimit
	}
	return nil
}

type slotOwner struct {
	attemptID, controlRunID sql.NullInt64
	versionID, grantID      uint64
	action                  string
	recovery                bool
}

func lockSlotOwner(ctx context.Context, tx *sql.Tx, in SlotInput) (slotOwner, error) {
	var owner slotOwner
	attemptID := in.AttemptID
	if in.Scope == "request" {
		// Read immutable ancestry here; lock and recheck the log after its owner.
		if err := tx.QueryRowContext(ctx, `SELECT attempt_id,control_plane_run_id,action FROM gw_channel_request_logs WHERE id=?`, *in.RequestLogID).Scan(&owner.attemptID, &owner.controlRunID, &owner.action); err != nil {
			return owner, err
		}
		if owner.attemptID.Valid == owner.controlRunID.Valid {
			return owner, ErrConflict
		}
		if owner.controlRunID.Valid {
			var key uint64
			var state, action string
			if err := tx.QueryRowContext(ctx, `SELECT credential_id,auth_credential_version_id,auth_purpose_grant_id,state,action FROM gw_control_plane_runs WHERE id=? FOR UPDATE`, owner.controlRunID.Int64).Scan(&key, &owner.versionID, &owner.grantID, &state, &action); err != nil {
				return owner, err
			}
			if key != in.CredentialID || state != "running" || action != owner.action {
				return owner, ErrConflict
			}
			return owner, nil
		}
		if owner.attemptID.Int64 <= 0 {
			return owner, ErrConflict
		}
		value := uint64(owner.attemptID.Int64)
		attemptID = &value
		owner.recovery = owner.action == "query" || owner.action == "recover" || owner.action == "cancel" || owner.action == "result_fetch" || owner.action == "reconcile_delivery"
	}
	var state string
	var key, pool uint64
	if err := tx.QueryRowContext(ctx, `SELECT credential_id,credential_pool_id,credential_version_id,purpose_grant_id,state FROM gw_api_call_attempts WHERE id=? FOR UPDATE`, *attemptID).Scan(&key, &pool, &owner.versionID, &owner.grantID, &state); err != nil {
		return owner, err
	}
	if key != in.CredentialID || pool != in.CredentialPoolID || !slotAttemptAllows(state, owner.action) {
		return owner, ErrConflict
	}
	return owner, nil
}

func slotAttemptAllows(state, action string) bool {
	switch action {
	case "", "submit":
		return state == "started"
	case "reconcile_delivery":
		return state == "completed"
	case "query", "recover", "cancel", "result_fetch", "named_action":
	default:
		return false
	}
	if state == "started" || state == "recovery_pending" {
		return true
	}
	if state == "completed" {
		return action == "result_fetch"
	}
	return state == "terminated_unknown" && (action == "query" || action == "recover")
}

func (o slotOwner) validateAuthorization(ctx context.Context, tx *sql.Tx, in SlotInput) error {
	var versionState, secretState, grantState, purpose string
	var validUntil sql.NullTime
	if err := tx.QueryRowContext(ctx, `SELECT v.status,v.valid_until,i.status FROM gw_credential_versions v JOIN gw_credential_secret_identities i ON i.id=v.secret_identity_id WHERE v.id=? AND v.credential_id=? FOR SHARE`, o.versionID, in.CredentialID).Scan(&versionState, &validUntil, &secretState); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT status,purpose FROM gw_credential_purpose_grants WHERE id=? AND credential_id=? FOR SHARE`, o.grantID, in.CredentialID).Scan(&grantState, &purpose); err != nil {
		return err
	}
	wantPurpose := "execution"
	if o.controlRunID.Valid {
		wantPurpose = "catalog_discovery"
	}
	if secretState != "active" || purpose != wantPurpose || !slotStateAllows(grantState, o.recovery) || validUntil.Valid && !validUntil.Time.After(nowUTC()) || versionState != "active" && (!o.recovery || versionState != "superseded") {
		return ErrConflict
	}
	if in.Scope == "request" {
		var parent, control sql.NullInt64
		var action, status string
		if err := tx.QueryRowContext(ctx, `SELECT attempt_id,control_plane_run_id,action,status FROM gw_channel_request_logs WHERE id=? FOR UPDATE`, *in.RequestLogID).Scan(&parent, &control, &action, &status); err != nil {
			return err
		}
		if parent != o.attemptID || control != o.controlRunID || action != o.action || status != "prepared" {
			return ErrConflict
		}
	}
	return nil
}

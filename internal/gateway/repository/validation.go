package repository

import (
	"context"
	"database/sql"
	"time"
)

type ValidationResultInput struct {
	ControlPlaneRunID uint64
	ActorID           uint64
	State             string
	EvidenceHMAC      string
	ValidUntil        *time.Time
}

func (in ValidationResultInput) validate(now time.Time) error {
	if in.ControlPlaneRunID == 0 || in.ActorID == 0 || !validValidationState(in.State) || !validHexDigest(in.EvidenceHMAC, 32) {
		return ErrInvalidInput
	}
	if in.State == "valid" {
		if in.ValidUntil == nil || !in.ValidUntil.After(now) {
			return ErrInvalidInput
		}
	} else if in.ValidUntil != nil {
		return ErrInvalidInput
	}
	return nil
}

// CompleteCredentialValidation appends the immutable probe result, advances
// its materialized state and completes the associated control-plane run in one
// transaction. The run pins the credential version and entitlement identity.
func (s *Store) CompleteCredentialValidation(ctx context.Context, tx *sql.Tx, in ValidationResultInput) (uint64, error) {
	if tx == nil {
		return 0, ErrInvalidInput
	}
	now := nowUTC()
	if err := in.validate(now); err != nil {
		return 0, err
	}
	var credentialID, credentialVersionID, configVersion uint64
	var action, runState, fingerprint string
	if err := tx.QueryRowContext(ctx, `SELECT r.action,r.state,r.credential_id,r.auth_credential_version_id,r.target_fingerprint,r.target_config_version FROM gw_control_plane_runs r WHERE r.id=? FOR UPDATE`, in.ControlPlaneRunID).Scan(&action, &runState, &credentialID, &credentialVersionID, &fingerprint, &configVersion); err == sql.ErrNoRows {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	}
	if action != "entitlement_probe" || runState != "running" || credentialID == 0 || credentialVersionID == 0 || configVersion == 0 || !validHexDigest(fingerprint, 32) {
		return 0, ErrConflict
	}
	var currentVersionID, currentConfigVersion uint64
	var credentialState, versionState string
	if err := tx.QueryRowContext(ctx, `SELECT c.current_version_id,c.config_version,c.status,v.status FROM gw_credentials c JOIN gw_credential_versions v ON v.id=? AND v.credential_id=c.id WHERE c.id=? FOR SHARE`, credentialVersionID, credentialID).Scan(&currentVersionID, &currentConfigVersion, &credentialState, &versionState); err != nil {
		return 0, err
	}
	if currentVersionID != credentialVersionID || currentConfigVersion != configVersion || credentialState != "active" || versionState != "active" {
		return 0, ErrConflict
	}
	stateVersion, exists, err := lockEntitlementState(ctx, tx, credentialID, credentialVersionID, fingerprint)
	if err != nil {
		return 0, err
	}
	sequence := stateVersion + 1
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_credential_validation_events(credential_id,credential_version_id,entitlement_fingerprint,validation_seq,control_plane_run_id,state,response_hmac,checked_at,valid_until) VALUES (?,?,?,?,?,?,?,?,?)`, credentialID, credentialVersionID, fingerprint, sequence, in.ControlPlaneRunID, in.State, in.EvidenceHMAC, now, nullableTimePtr(in.ValidUntil))
	if err != nil {
		return 0, err
	}
	eventID, err := lastID(result)
	if err != nil {
		return 0, err
	}
	if exists {
		if err := requireOneRow(tx.ExecContext(ctx, `UPDATE gw_credential_entitlement_state SET state=?,state_version=?,latest_event_id=?,updated_at=? WHERE credential_id=? AND credential_version_id=? AND entitlement_fingerprint=? AND state_version=?`, in.State, sequence, eventID, now, credentialID, credentialVersionID, fingerprint, stateVersion)); err != nil {
			return 0, err
		}
	} else if _, err := tx.ExecContext(ctx, `INSERT INTO gw_credential_entitlement_state(credential_id,credential_version_id,entitlement_fingerprint,state,state_version,latest_event_id,updated_at) VALUES (?,?,?,?,1,?,?)`, credentialID, credentialVersionID, fingerprint, in.State, eventID, now); err != nil {
		return 0, err
	}
	if err := s.FinishControlPlaneRun(ctx, tx, in.ControlPlaneRunID, "running", "completed", "validation_recorded"); err != nil {
		return 0, err
	}
	if err := recordCatalogAdminChange(ctx, tx, in.ActorID, "unified.entitlement.validate", "credential_validation", eventID, ginSafeMetadata{"control_plane_run_id": in.ControlPlaneRunID, "state": in.State}); err != nil {
		return 0, err
	}
	return eventID, nil
}

// CompleteCommercialValidation applies the same append-only protocol to the
// Offering commercial identity pinned by a commercial_check run.
func (s *Store) CompleteCommercialValidation(ctx context.Context, tx *sql.Tx, in ValidationResultInput) (uint64, error) {
	if tx == nil {
		return 0, ErrInvalidInput
	}
	now := nowUTC()
	if err := in.validate(now); err != nil {
		return 0, err
	}
	var offeringID uint64
	var action, runState, fingerprint string
	if err := tx.QueryRowContext(ctx, `SELECT r.action,r.state,r.offering_id,r.target_fingerprint FROM gw_control_plane_runs r WHERE r.id=? FOR UPDATE`, in.ControlPlaneRunID).Scan(&action, &runState, &offeringID, &fingerprint); err == sql.ErrNoRows {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	}
	if action != "commercial_check" || runState != "running" || offeringID == 0 || !validHexDigest(fingerprint, 32) {
		return 0, ErrConflict
	}
	var currentFingerprint string
	if err := tx.QueryRowContext(ctx, `SELECT commercial_fingerprint FROM gw_offerings WHERE id=? FOR SHARE`, offeringID).Scan(&currentFingerprint); err != nil {
		return 0, err
	}
	if currentFingerprint != fingerprint {
		return 0, ErrConflict
	}
	stateVersion, exists, err := lockCommercialState(ctx, tx, fingerprint)
	if err != nil {
		return 0, err
	}
	sequence := stateVersion + 1
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_commercial_validation_events(commercial_fingerprint,validation_seq,control_plane_run_id,state,observation_hmac,checked_at,valid_until) VALUES (?,?,?,?,?,?,?)`, fingerprint, sequence, in.ControlPlaneRunID, in.State, in.EvidenceHMAC, now, nullableTimePtr(in.ValidUntil))
	if err != nil {
		return 0, err
	}
	eventID, err := lastID(result)
	if err != nil {
		return 0, err
	}
	if exists {
		if err := requireOneRow(tx.ExecContext(ctx, `UPDATE gw_commercial_state SET state=?,state_version=?,latest_event_id=?,updated_at=? WHERE commercial_fingerprint=? AND state_version=?`, in.State, sequence, eventID, now, fingerprint, stateVersion)); err != nil {
			return 0, err
		}
	} else if _, err := tx.ExecContext(ctx, `INSERT INTO gw_commercial_state(commercial_fingerprint,state,state_version,latest_event_id,updated_at) VALUES (?,?,1,?,?)`, fingerprint, in.State, eventID, now); err != nil {
		return 0, err
	}
	if err := s.FinishControlPlaneRun(ctx, tx, in.ControlPlaneRunID, "running", "completed", "validation_recorded"); err != nil {
		return 0, err
	}
	if err := recordCatalogAdminChange(ctx, tx, in.ActorID, "unified.commercial.validate", "commercial_validation", eventID, ginSafeMetadata{"control_plane_run_id": in.ControlPlaneRunID, "state": in.State}); err != nil {
		return 0, err
	}
	return eventID, nil
}

func lockEntitlementState(ctx context.Context, tx *sql.Tx, credentialID, credentialVersionID uint64, fingerprint string) (uint64, bool, error) {
	var version uint64
	err := tx.QueryRowContext(ctx, `SELECT state_version FROM gw_credential_entitlement_state WHERE credential_id=? AND credential_version_id=? AND entitlement_fingerprint=? FOR UPDATE`, credentialID, credentialVersionID, fingerprint).Scan(&version)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	return version, err == nil, err
}

func lockCommercialState(ctx context.Context, tx *sql.Tx, fingerprint string) (uint64, bool, error) {
	var version uint64
	err := tx.QueryRowContext(ctx, `SELECT state_version FROM gw_commercial_state WHERE commercial_fingerprint=? FOR UPDATE`, fingerprint).Scan(&version)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	return version, err == nil, err
}

func validValidationState(value string) bool {
	switch value {
	case "valid", "drift", "unknown", "expired":
		return true
	default:
		return false
	}
}

func nullableTimePtr(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC()
}

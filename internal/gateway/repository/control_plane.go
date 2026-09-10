package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

type ControlPlaneRunInput struct {
	Action                                           string
	CatalogReleaseSourceID, CredentialID, OfferingID *uint64
	TargetFingerprint                                string
	TargetConfigVersion                              *uint64
	AuthCredentialVersionID, AuthPurposeGrantID      uint64
}

func (s *Store) CreateControlPlaneRun(ctx context.Context, tx *sql.Tx, in ControlPlaneRunInput) (uint64, error) {
	if tx == nil || in.Action == "" || in.AuthCredentialVersionID == 0 || in.AuthPurposeGrantID == 0 || !validControlPlaneAction(in.Action) {
		return 0, ErrInvalidInput
	}
	if in.CredentialID == nil {
		return 0, ErrInvalidInput
	}
	if in.Action == "catalog_discovery" && (in.CatalogReleaseSourceID == nil || in.OfferingID != nil) {
		return 0, ErrInvalidInput
	}
	if in.Action != "catalog_discovery" && (in.CatalogReleaseSourceID != nil && in.OfferingID != nil) {
		return 0, ErrInvalidInput
	}
	if in.TargetFingerprint != "" && !validHexDigest(in.TargetFingerprint, 32) {
		return 0, ErrInvalidInput
	}
	var credentialID uint64
	var credentialStatus, versionStatus, grantStatus, grantPurpose string
	if err := tx.QueryRowContext(ctx, `SELECT c.id,c.status,cv.status,pg.status,pg.purpose FROM gw_credentials c JOIN gw_credential_versions cv ON cv.credential_id=c.id JOIN gw_credential_purpose_grants pg ON pg.credential_id=c.id WHERE c.id=? AND cv.id=? AND pg.id=? FOR SHARE`, *in.CredentialID, in.AuthCredentialVersionID, in.AuthPurposeGrantID).Scan(&credentialID, &credentialStatus, &versionStatus, &grantStatus, &grantPurpose); err == sql.ErrNoRows {
		return 0, ErrConflict
	} else if err != nil {
		return 0, err
	}
	if credentialID != *in.CredentialID || credentialStatus != "active" || versionStatus != "active" || grantStatus != "active" || grantPurpose != "catalog_discovery" {
		return 0, ErrConflict
	}
	now := nowUTC()
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_control_plane_runs(action,catalog_release_source_id,credential_id,offering_id,target_fingerprint,target_config_version,auth_credential_version_id,auth_purpose_grant_id,state,state_version,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?, 'scheduled',1,?,?)`, in.Action, nullableID(in.CatalogReleaseSourceID), nullableID(in.CredentialID), nullableID(in.OfferingID), emptyAsNull(in.TargetFingerprint), nullableUint64(in.TargetConfigVersion), in.AuthCredentialVersionID, in.AuthPurposeGrantID, now, now)
	if err != nil {
		return 0, fmt.Errorf("create control plane run: %w", err)
	}
	runID, err := lastID(result)
	if err != nil {
		return 0, err
	}
	if err := appendControlPlaneRunEvent(ctx, tx, runID, 1, "", "scheduled", "created", now); err != nil {
		return 0, err
	}
	return runID, nil
}

func (s *Store) FinishControlPlaneRun(ctx context.Context, tx *sql.Tx, runID uint64, from, to, reason string) error {
	if tx == nil || runID == 0 || reason == "" || !validControlPlaneState(from) || !validControlPlaneState(to) {
		return ErrInvalidInput
	}
	if from == to {
		return nil
	}
	if !(from == "scheduled" && to == "running" || from == "running" && (to == "completed" || to == "failed" || to == "manual_review")) {
		return ErrConflict
	}
	var current string
	var version uint64
	if err := tx.QueryRowContext(ctx, `SELECT state,state_version FROM gw_control_plane_runs WHERE id=? FOR UPDATE`, runID).Scan(&current, &version); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if current != from {
		return ErrConflict
	}
	now := nowUTC()
	res, err := tx.ExecContext(ctx, `UPDATE gw_control_plane_runs SET state=?,state_version=?,updated_at=? WHERE id=? AND state=? AND state_version=?`, to, version+1, now, runID, from, version)
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
	return appendControlPlaneRunEvent(ctx, tx, runID, version+1, from, to, reason, now)
}

func appendControlPlaneRunEvent(ctx context.Context, tx *sql.Tx, runID, sequence uint64, oldState, newState, reason string, at time.Time) error {
	reason = strings.ToLower(strings.TrimSpace(reason))
	if tx == nil || runID == 0 || sequence == 0 || !validControlPlaneState(newState) ||
		oldState != "" && !validControlPlaneState(oldState) || reason == "" ||
		!utf8.ValidString(reason) || utf8.RuneCountInString(reason) > 128 || strings.ContainsAny(reason, "\x00\r\n\t") || at.IsZero() {
		return ErrInvalidInput
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO gw_control_plane_run_events(control_plane_run_id,event_seq,old_state,new_state,reason_code,created_at) VALUES (?,?,?,?,?,?)`, runID, sequence, emptyAsNull(oldState), newState, reason, at.UTC())
	return err
}

func validControlPlaneAction(value string) bool {
	switch value {
	case "catalog_discovery", "entitlement_probe", "commercial_check":
		return true
	default:
		return false
	}
}
func validControlPlaneState(value string) bool {
	switch value {
	case "scheduled", "running", "completed", "failed", "manual_review":
		return true
	default:
		return false
	}
}

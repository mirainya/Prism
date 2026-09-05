package repository

import (
	"context"
	"database/sql"
	"fmt"
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
	return lastID(result)
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
	now := nowUTC()
	res, err := tx.ExecContext(ctx, `UPDATE gw_control_plane_runs SET state=?,state_version=state_version+1,updated_at=? WHERE id=? AND state=?`, to, now, runID, from)
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
	return nil
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

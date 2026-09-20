package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strings"
	"unicode/utf8"
)

type CatalogSourceInput struct {
	ChannelID        uint64              `json:"channel_id"`
	CredentialID     uint64              `json:"credential_id"`
	SourceCode       string              `json:"source_code"`
	ContractCode     string              `json:"contract_code"`
	BaseURL          string              `json:"base_url"`
	ExternalGroup    string              `json:"external_group"`
	RequestTimeoutMS uint32              `json:"request_timeout_ms"`
	Adapter          CatalogAdapterInput `json:"-"`
}

func (in *CatalogSourceInput) Normalize() {
	in.SourceCode = strings.ToLower(strings.TrimSpace(in.SourceCode))
	in.ContractCode = strings.ToLower(strings.TrimSpace(in.ContractCode))
	in.BaseURL = strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
	in.ExternalGroup = strings.TrimSpace(in.ExternalGroup)
}

func (in CatalogSourceInput) Validate() error {
	if in.ChannelID == 0 || in.CredentialID == 0 || !channelCodePattern.MatchString(in.SourceCode) ||
		!validCatalogSourceContract(in.ContractCode) || !validCatalogSourceText(in.ExternalGroup, 128) ||
		in.RequestTimeoutMS < 1000 || in.RequestTimeoutMS > 120000 || in.Adapter.Code != in.ContractCode ||
		in.Adapter.Version != 1 || in.Adapter.Protocol != "catalog_discovery" {
		return ErrInvalidInput
	}
	parsed, err := validateCatalogBaseURL(in.BaseURL)
	if err != nil || parsed.Path != "" && parsed.Path != "/" {
		return ErrInvalidInput
	}
	return nil
}

func validCatalogSourceContract(value string) bool {
	return utf8.RuneCountInString(value) <= 64 && catalogIdentityPattern.MatchString(value)
}

func validCatalogSourceText(value string, maxRunes int) bool {
	return value != "" && utf8.ValidString(value) && utf8.RuneCountInString(value) <= maxRunes &&
		!strings.ContainsAny(value, "\x00\r\n\t")
}

func (s *Store) CreateManagedCatalogSource(ctx context.Context, tx *sql.Tx, in CatalogSourceInput, actorID uint64) (uint64, error) {
	if tx == nil || actorID == 0 {
		return 0, ErrInvalidInput
	}
	in.Normalize()
	if err := in.Validate(); err != nil {
		return 0, err
	}
	var channelStatus, credentialStatus, versionStatus, grantStatus, purpose string
	var credentialChannelID, grantID, credentialVersionID uint64
	err := tx.QueryRowContext(ctx, `SELECT c.channel_id,c.status,c.current_version_id,cv.status,g.id,g.status,g.purpose
FROM gw_credentials c
JOIN gw_credential_versions cv ON cv.id=c.current_version_id AND cv.credential_id=c.id
JOIN gw_credential_purpose_grants g ON g.credential_id=c.id AND g.purpose='catalog_discovery'
WHERE c.id=? ORDER BY g.grant_seq DESC LIMIT 1 FOR SHARE`, in.CredentialID).
		Scan(&credentialChannelID, &credentialStatus, &credentialVersionID, &versionStatus, &grantID, &grantStatus, &purpose)
	if err == sql.ErrNoRows {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT status FROM gateway_channels WHERE id=? FOR SHARE`, in.ChannelID).Scan(&channelStatus); err != nil {
		return 0, err
	}
	if credentialChannelID != in.ChannelID || channelStatus != "active" || credentialStatus != "active" ||
		versionStatus != "active" || grantStatus != "active" || purpose != "catalog_discovery" {
		return 0, ErrConflict
	}
	adapterID, err := ensureCatalogAdapter(ctx, tx, in.Adapter)
	if err != nil {
		return 0, err
	}
	now := nowUTC()
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_catalog_sources(channel_id,source_code,adapter_implementation_id,credential_id,credential_purpose_grant_id,status,state_version,nonterminal_run_count,created_at,updated_at) VALUES (?,?,?,?,?,'active',1,0,?,?)`, in.ChannelID, in.SourceCode, adapterID, in.CredentialID, grantID, now, now)
	if err != nil {
		return 0, err
	}
	id, err := lastID(result)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO gw_catalog_source_profiles(catalog_source_id,contract_code,base_url,external_group,request_timeout_ms,created_at) VALUES (?,?,?,?,?,?)`, id, in.ContractCode, in.BaseURL, in.ExternalGroup, in.RequestTimeoutMS, now); err != nil {
		return 0, err
	}
	return id, recordCatalogAdminChange(ctx, tx, actorID, "unified.catalog_source.create", "catalog_source", id, ginSafeMetadata{
		"channel_id": in.ChannelID, "credential_id": in.CredentialID, "source_code": in.SourceCode,
		"contract_code": in.ContractCode, "base_url": in.BaseURL, "external_group": in.ExternalGroup,
		"request_timeout_ms": in.RequestTimeoutMS,
	})
}

func (s *Store) TransitionManagedCatalogSource(ctx context.Context, tx *sql.Tx, sourceID, expectedVersion uint64, from, to, reason string, actorID uint64) error {
	if tx == nil || sourceID == 0 || expectedVersion == 0 || actorID == 0 {
		return ErrInvalidInput
	}
	var current string
	var version uint64
	if err := tx.QueryRowContext(ctx, `SELECT status,state_version FROM gw_catalog_sources WHERE id=? FOR UPDATE`, sourceID).Scan(&current, &version); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if current != from || version != expectedVersion {
		return ErrConflict
	}
	if from == "active" && to == "draining" {
		var scheduled uint64
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_control_plane_runs r JOIN gw_catalog_release_sources rs ON rs.id=r.catalog_release_source_id WHERE rs.catalog_source_id=? AND r.state='scheduled' FOR UPDATE`, sourceID).Scan(&scheduled); err != nil {
			return err
		}
		if scheduled > 0 {
			now := nowUTC()
			if _, err := tx.ExecContext(ctx, `INSERT INTO gw_control_plane_run_events(control_plane_run_id,event_seq,old_state,new_state,reason_code,created_at)
SELECT r.id,r.state_version+1,'scheduled','failed','source_draining',?
FROM gw_control_plane_runs r JOIN gw_catalog_release_sources rs ON rs.id=r.catalog_release_source_id
WHERE rs.catalog_source_id=? AND r.state='scheduled'`, now, sourceID); err != nil {
				return err
			}
			result, err := tx.ExecContext(ctx, `UPDATE gw_control_plane_runs r JOIN gw_catalog_release_sources rs ON rs.id=r.catalog_release_source_id SET r.state='failed',r.state_version=r.state_version+1,r.lease_owner='',r.lease_expires_at=NULL,r.updated_at=? WHERE rs.catalog_source_id=? AND r.state='scheduled'`, now, sourceID)
			if err != nil {
				return err
			}
			updated, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if uint64(updated) != scheduled {
				return ErrConflict
			}
			result, err = tx.ExecContext(ctx, `UPDATE gw_catalog_sources SET nonterminal_run_count=nonterminal_run_count-?,updated_at=? WHERE id=? AND nonterminal_run_count>=?`, scheduled, now, sourceID, scheduled)
			if err != nil {
				return err
			}
			updated, err = result.RowsAffected()
			if err != nil {
				return err
			}
			if updated != 1 {
				return ErrConflict
			}
		}
	}
	if err := s.TransitionCatalogSource(ctx, tx, sourceID, from, to, reason); err != nil {
		return err
	}
	return recordCatalogAdminChange(ctx, tx, actorID, "unified.catalog_source.transition", "catalog_source", sourceID, ginSafeMetadata{"from": from, "to": to, "reason": reason})
}

func (s *Store) ScheduleCatalogDiscovery(ctx context.Context, tx *sql.Tx, releaseID, sourceID, actorID uint64) (uint64, error) {
	if tx == nil || releaseID == 0 || sourceID == 0 || actorID == 0 {
		return 0, ErrInvalidInput
	}
	var releaseStatus string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM gw_catalog_releases WHERE id=? FOR UPDATE`, releaseID).Scan(&releaseStatus); err == sql.ErrNoRows {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	}
	if releaseStatus != "draft" {
		return 0, ErrConflict
	}
	var sourceState, contractCode, baseURL, externalGroup string
	var sourceVersion, credentialID, grantID, credentialVersionID uint64
	var timeout uint32
	err := tx.QueryRowContext(ctx, `SELECT s.status,s.state_version,s.credential_id,s.credential_purpose_grant_id,c.current_version_id,p.contract_code,p.base_url,p.external_group,p.request_timeout_ms
FROM gw_catalog_sources s JOIN gw_catalog_source_profiles p ON p.catalog_source_id=s.id
JOIN gw_credentials c ON c.id=s.credential_id
WHERE s.id=? FOR UPDATE`, sourceID).Scan(&sourceState, &sourceVersion, &credentialID, &grantID, &credentialVersionID, &contractCode, &baseURL, &externalGroup, &timeout)
	if err == sql.ErrNoRows {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if sourceState != "active" || credentialVersionID == 0 {
		return 0, ErrConflict
	}
	canonical, _ := json.Marshal(ginSafeMetadata{"source_id": sourceID, "source_version": sourceVersion, "credential_version_id": credentialVersionID, "contract_code": contractCode, "base_url": baseURL, "external_group": externalGroup, "timeout_ms": timeout})
	digest := sha256.Sum256(canonical)
	targetFingerprint := hex.EncodeToString(digest[:])
	now := nowUTC()
	var releaseSourceID, observedSourceVersion uint64
	err = tx.QueryRowContext(ctx, `SELECT id,source_state_version FROM gw_catalog_release_sources WHERE release_id=? AND catalog_source_id=? FOR UPDATE`, releaseID, sourceID).Scan(&releaseSourceID, &observedSourceVersion)
	if err == sql.ErrNoRows {
		result, insertErr := tx.ExecContext(ctx, `INSERT INTO gw_catalog_release_sources(release_id,catalog_source_id,source_state_version,created_at) VALUES (?,?,?,?)`, releaseID, sourceID, sourceVersion, now)
		if insertErr != nil {
			return 0, insertErr
		}
		releaseSourceID, err = lastID(result)
		if err != nil {
			return 0, err
		}
	} else if err != nil {
		return 0, err
	} else if observedSourceVersion != sourceVersion {
		return 0, ErrConflict
	}

	var existingRunID, existingVersion, existingAuthVersion, existingGrant uint64
	var existingState string
	err = tx.QueryRowContext(ctx, `SELECT id,state,state_version,auth_credential_version_id,auth_purpose_grant_id
FROM gw_control_plane_runs
WHERE catalog_release_source_id=? AND action='catalog_discovery' AND target_fingerprint=? AND target_config_version=?
FOR UPDATE`, releaseSourceID, targetFingerprint, sourceVersion).Scan(&existingRunID, &existingState, &existingVersion, &existingAuthVersion, &existingGrant)
	if err == nil {
		if existingState != "failed" || existingAuthVersion != credentialVersionID || existingGrant != grantID {
			return 0, ErrConflict
		}
		var snapshotCount uint64
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_catalog_discovery_snapshots WHERE release_source_id=?`, releaseSourceID).Scan(&snapshotCount); err != nil {
			return 0, err
		}
		if snapshotCount != 0 {
			return 0, ErrConflict
		}
		if err := s.AdjustCatalogSourceRunCount(ctx, tx, sourceID, 1); err != nil {
			return 0, err
		}
		result, err := tx.ExecContext(ctx, `UPDATE gw_control_plane_runs SET state='scheduled',state_version=state_version+1,next_action_at=NULL,lease_owner='',lease_expires_at=NULL,updated_at=? WHERE id=? AND state='failed' AND state_version=?`, now, existingRunID, existingVersion)
		if err != nil {
			return 0, err
		}
		ok, err := affected(result)
		if err != nil {
			return 0, err
		}
		if !ok {
			return 0, ErrConflict
		}
		if err := appendControlPlaneRunEvent(ctx, tx, existingRunID, existingVersion+1, "failed", "scheduled", "admin_retry", now); err != nil {
			return 0, err
		}
		if err := recordCatalogAdminChange(ctx, tx, actorID, "unified.catalog_source.discovery_retry", "control_plane_run", existingRunID, ginSafeMetadata{"release_id": releaseID, "catalog_source_id": sourceID}); err != nil {
			return 0, err
		}
		return existingRunID, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	var blocked uint64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_control_plane_runs WHERE catalog_release_source_id=? AND state IN ('scheduled','running','manual_review')`, releaseSourceID).Scan(&blocked); err != nil {
		return 0, err
	}
	var snapshotCount uint64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_catalog_discovery_snapshots WHERE release_source_id=?`, releaseSourceID).Scan(&snapshotCount); err != nil {
		return 0, err
	}
	if blocked != 0 || snapshotCount != 0 {
		return 0, ErrConflict
	}
	if err := s.AdjustCatalogSourceRunCount(ctx, tx, sourceID, 1); err != nil {
		return 0, err
	}
	runID, err := s.CreateControlPlaneRun(ctx, tx, ControlPlaneRunInput{
		Action: "catalog_discovery", CatalogReleaseSourceID: &releaseSourceID, CredentialID: &credentialID,
		TargetFingerprint: targetFingerprint, TargetConfigVersion: &sourceVersion,
		AuthCredentialVersionID: credentialVersionID, AuthPurposeGrantID: grantID,
	})
	if err != nil {
		return 0, err
	}
	if err := recordCatalogAdminChange(ctx, tx, actorID, "unified.catalog_source.discover", "control_plane_run", runID, ginSafeMetadata{"release_id": releaseID, "catalog_source_id": sourceID}); err != nil {
		return 0, err
	}
	return runID, nil
}

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/mirainya/Prism/internal/gateway/credentials"
)

type SecretIdentityInput struct {
	ChannelID      uint64
	SecretHMAC     string
	HMACKeyVersion uint32
}

func (s *Store) EnsureSecretIdentity(ctx context.Context, tx *sql.Tx, in SecretIdentityInput) (uint64, error) {
	if tx == nil || in.ChannelID == 0 || !validHexDigest(in.SecretHMAC, 32) || in.HMACKeyVersion == 0 {
		return 0, ErrInvalidInput
	}
	var id uint64
	err := tx.QueryRowContext(ctx, `SELECT id FROM gw_credential_secret_identities WHERE channel_id=? AND hmac_key_version=? AND secret_hmac=? FOR UPDATE`, in.ChannelID, in.HMACKeyVersion, in.SecretHMAC).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_credential_secret_identities(channel_id,secret_hmac,hmac_key_version,status,created_at,updated_at) VALUES (?,?,?,'active',?,?)`, in.ChannelID, in.SecretHMAC, in.HMACKeyVersion, nowUTC(), nowUTC())
	if err != nil {
		if readErr := tx.QueryRowContext(ctx, `SELECT id FROM gw_credential_secret_identities WHERE channel_id=? AND hmac_key_version=? AND secret_hmac=? FOR UPDATE`, in.ChannelID, in.HMACKeyVersion, in.SecretHMAC).Scan(&id); readErr == nil {
			return id, nil
		}
		return 0, fmt.Errorf("insert secret identity: %w", err)
	}
	return lastID(result)
}

type CredentialVersionInput struct {
	ChannelID, CredentialID, SecretIdentityID, EncryptedBlobID uint64
	VersionNo                                                  uint64
	ValidUntil                                                 *time.Time
}

func (s *Store) CreateCredentialVersion(ctx context.Context, tx *sql.Tx, in CredentialVersionInput) (uint64, error) {
	if tx == nil || in.ChannelID == 0 || in.CredentialID == 0 || in.SecretIdentityID == 0 || in.EncryptedBlobID == 0 || in.VersionNo == 0 {
		return 0, ErrInvalidInput
	}
	var credentialSecretID uint64
	var credentialStatus string
	if err := tx.QueryRowContext(ctx, `SELECT secret_identity_id,status FROM gw_credentials WHERE id=? AND channel_id=? FOR SHARE`, in.CredentialID, in.ChannelID).Scan(&credentialSecretID, &credentialStatus); err == sql.ErrNoRows {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	} else if credentialStatus == string(credentials.CredentialDisabled) || credentialSecretID != in.SecretIdentityID {
		return 0, ErrConflict
	}
	var latestVersion uint64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version_no),0) FROM gw_credential_versions WHERE credential_id=?`, in.CredentialID).Scan(&latestVersion); err != nil {
		return 0, err
	}
	if in.VersionNo != latestVersion+1 {
		return 0, ErrConflict
	}
	if in.ValidUntil != nil && !in.ValidUntil.After(nowUTC()) {
		return 0, ErrInvalidInput
	}
	now := nowUTC()
	var valid any
	if in.ValidUntil != nil {
		valid = in.ValidUntil.UTC()
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_credential_versions(channel_id,credential_id,secret_identity_id,version_no,encrypted_blob_id,status,valid_until,created_at) VALUES (?,?,?,?,?,'preparing',?,?)`, in.ChannelID, in.CredentialID, in.SecretIdentityID, in.VersionNo, in.EncryptedBlobID, valid, now)
	if err != nil {
		return 0, fmt.Errorf("insert credential version: %w", err)
	}
	return lastID(result)
}

func (s *Store) ActivateCredentialVersion(ctx context.Context, tx *sql.Tx, credentialID, versionID uint64) error {
	if tx == nil || credentialID == 0 || versionID == 0 {
		return ErrInvalidInput
	}
	var lockedID, credentialSecretID, credentialChannelID uint64
	var currentVersion sql.NullInt64
	var credentialStatus string
	if err := tx.QueryRowContext(ctx, `SELECT id,channel_id,secret_identity_id,current_version_id,status FROM gw_credentials WHERE id=? FOR UPDATE`, credentialID).Scan(&lockedID, &credentialChannelID, &credentialSecretID, &currentVersion, &credentialStatus); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if credentialStatus == "disabled" {
		return ErrConflict
	}
	var state string
	var versionSecretID uint64
	var channelID uint64
	var validUntil sql.NullTime
	if err := tx.QueryRowContext(ctx, `SELECT status,channel_id,secret_identity_id,valid_until FROM gw_credential_versions WHERE id=? AND credential_id=? FOR UPDATE`, versionID, credentialID).Scan(&state, &channelID, &versionSecretID, &validUntil); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if versionSecretID == 0 || channelID != credentialChannelID || validUntil.Valid && !validUntil.Time.After(nowUTC()) {
		return ErrConflict
	}
	var secretID uint64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM gw_credential_secret_identities WHERE id=? AND status='active' FOR SHARE`, credentialSecretID).Scan(&secretID); err == sql.ErrNoRows {
		return ErrConflict
	} else if err != nil {
		return err
	}
	if versionSecretID != credentialSecretID {
		return ErrConflict
	}
	if state == "active" && currentVersion.Valid && uint64(currentVersion.Int64) == versionID {
		return nil
	}
	if err := credentials.TransitionVersion(credentials.VersionState(state), credentials.VersionActive); err != nil {
		return err
	}
	now := nowUTC()
	var previousID uint64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM gw_credential_versions WHERE credential_id=? AND status='active' FOR UPDATE`, credentialID).Scan(&previousID); err != nil && err != sql.ErrNoRows {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE gw_credential_versions SET status='superseded',retired_at=? WHERE credential_id=? AND status='active'`, now, credentialID); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE gw_credential_versions SET status='active' WHERE id=? AND credential_id=? AND status='preparing'`, versionID, credentialID)
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
	if previousID != 0 {
		if err := appendCredentialVersionAudit(ctx, tx, previousID, "active", "superseded", "rotation", now); err != nil {
			return err
		}
	}
	if err := appendCredentialVersionAudit(ctx, tx, versionID, "preparing", "active", "activate", now); err != nil {
		return err
	}
	if previousID != 0 {
		res, err = tx.ExecContext(ctx, `UPDATE gw_credentials SET current_version_id=?,config_version=config_version+1,updated_at=? WHERE id=? AND current_version_id=?`, versionID, now, credentialID, previousID)
	} else {
		res, err = tx.ExecContext(ctx, `UPDATE gw_credentials SET current_version_id=?,config_version=config_version+1,updated_at=? WHERE id=? AND current_version_id IS NULL`, versionID, now, credentialID)
	}
	if err != nil {
		return err
	}
	ok, err = affected(res)
	if err != nil {
		return err
	}
	if !ok {
		return ErrConflict
	}
	return nil
}

func (s *Store) TransitionCredential(ctx context.Context, tx *sql.Tx, credentialID uint64, from, to credentials.CredentialState, reason string) error {
	if tx == nil || credentialID == 0 || reason == "" {
		return ErrInvalidInput
	}
	if err := credentials.TransitionCredential(from, to); err != nil {
		return err
	}
	var current string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM gw_credentials WHERE id=? FOR UPDATE`, credentialID).Scan(&current); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	} else if current != string(from) {
		return ErrConflict
	}
	if to == credentials.CredentialDisabled {
		var active uint64
		if err := tx.QueryRowContext(
			ctx,
			`SELECT COUNT(*) FROM gw_credential_slots WHERE credential_id=? AND state IN ('active','recovery_required')`,
			credentialID,
		).Scan(&active); err != nil {
			return err
		}
		if active != 0 {
			return ErrConflict
		}
	}
	now := nowUTC()
	res, err := tx.ExecContext(ctx, `UPDATE gw_credentials SET status=?,config_version=config_version+1,updated_at=? WHERE id=? AND status=?`, string(to), now, credentialID, string(from))
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
	if err := tx.QueryRowContext(ctx, `SELECT config_version FROM gw_credentials WHERE id=?`, credentialID).Scan(&version); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO gw_credential_state_events(credential_id,state_version,old_state,new_state,reason_code,created_at) VALUES (?,?,?,?,?,?)`, credentialID, version, string(from), string(to), reason, now)
	return err
}

func appendCredentialVersionAudit(ctx context.Context, tx *sql.Tx, versionID uint64, oldState, newState, reason string, at time.Time) error {
	var next uint64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(state_version),0)+1 FROM gw_credential_version_state_events WHERE credential_version_id=?`, versionID).Scan(&next); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO gw_credential_version_state_events(credential_version_id,state_version,old_state,new_state,reason_code,created_at) VALUES (?,?,?,?,?,?)`, versionID, next, oldState, newState, reason, at)
	return err
}

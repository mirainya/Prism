package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/mirainya/Prism/internal/gateway/credentials"
)

type PoolInput struct {
	ChannelID               uint64
	PoolCode, DisplayName   string
	RequestLimit, TaskLimit *uint64
}

func (s *Store) CreateCredentialPool(ctx context.Context, tx *sql.Tx, in PoolInput) (uint64, error) {
	if tx == nil || in.ChannelID == 0 || in.PoolCode == "" || len(in.PoolCode) > 128 || in.DisplayName == "" || len(in.DisplayName) > 128 {
		return 0, ErrInvalidInput
	}
	now := nowUTC()
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_credential_pools(channel_id,pool_code,display_name,status,config_version,request_limit,task_limit,created_at,updated_at) VALUES (?,?,?,'active',1,?,?,?,?)`, in.ChannelID, in.PoolCode, in.DisplayName, nullableUint64(in.RequestLimit), nullableUint64(in.TaskLimit), now, now)
	if err != nil {
		return 0, fmt.Errorf("create credential pool: %w", err)
	}
	return lastID(result)
}

type CredentialInput struct {
	ChannelID, PoolID, SecretIdentityID uint64
	Code                                string
	RequestLimit, TaskLimit, Weight     *uint64
}

func (s *Store) CreateCredential(ctx context.Context, tx *sql.Tx, in CredentialInput) (uint64, error) {
	if tx == nil || in.ChannelID == 0 || in.PoolID == 0 || in.SecretIdentityID == 0 || in.Code == "" || len(in.Code) > 128 {
		return 0, ErrInvalidInput
	}
	var poolStatus string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM gw_credential_pools WHERE id=? AND channel_id=? FOR SHARE`, in.PoolID, in.ChannelID).Scan(&poolStatus); err == sql.ErrNoRows {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	} else if poolStatus != string(credentials.PoolActive) {
		return 0, ErrConflict
	}
	weight := uint64(1)
	if in.Weight != nil {
		weight = *in.Weight
	}
	if weight == 0 {
		return 0, ErrInvalidInput
	}
	now := nowUTC()
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_credentials(channel_id,credential_pool_id,secret_identity_id,credential_code,status,config_version,request_limit,task_limit,weight,created_at,updated_at) VALUES (?,?,?,?, 'active',1,?,?,?,?,?)`, in.ChannelID, in.PoolID, in.SecretIdentityID, in.Code, nullableUint64(in.RequestLimit), nullableUint64(in.TaskLimit), weight, now, now)
	if err != nil {
		return 0, fmt.Errorf("create credential: %w", err)
	}
	return lastID(result)
}

func (s *Store) GrantCredentialPurpose(ctx context.Context, tx *sql.Tx, credentialID uint64, purpose credentials.Purpose, seq uint64) (uint64, error) {
	if tx == nil || credentialID == 0 || seq == 0 || !validPurpose(purpose) {
		return 0, ErrInvalidInput
	}
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM gw_credentials WHERE id=? FOR SHARE`, credentialID).Scan(&status); err == sql.ErrNoRows {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	} else if status != string(credentials.CredentialActive) {
		return 0, ErrConflict
	}
	var latestSeq uint64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(grant_seq),0) FROM gw_credential_purpose_grants WHERE credential_id=? AND purpose=?`, credentialID, string(purpose)).Scan(&latestSeq); err != nil {
		return 0, err
	}
	if seq != latestSeq+1 {
		return 0, ErrConflict
	}
	now := nowUTC()
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_credential_purpose_grants(credential_id,purpose,grant_seq,status,state_version,created_at) VALUES (?,?,?,'active',1,?)`, credentialID, string(purpose), seq, now)
	if err != nil {
		return 0, fmt.Errorf("grant credential purpose: %w", err)
	}
	return lastID(result)
}

func validPurpose(value credentials.Purpose) bool {
	switch value {
	case credentials.PurposeExecution, credentials.PurposeCatalogDiscovery, credentials.PurposeCallbackVerify:
		return true
	default:
		return false
	}
}

func (s *Store) TransitionCredentialPool(ctx context.Context, tx *sql.Tx, poolID uint64, from, to credentials.PoolState, reason string) error {
	if tx == nil || poolID == 0 || reason == "" {
		return ErrInvalidInput
	}
	if err := credentials.TransitionPool(from, to); err != nil {
		return err
	}
	var current string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM gw_credential_pools WHERE id=? FOR UPDATE`, poolID).Scan(&current); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	} else if current != string(from) {
		return ErrConflict
	}
	if to == credentials.PoolDisabled {
		var active uint64
		if err := tx.QueryRowContext(
			ctx,
			`SELECT COUNT(*) FROM gw_credential_slots WHERE credential_pool_id=? AND state IN ('active','recovery_required')`,
			poolID,
		).Scan(&active); err != nil {
			return err
		}
		if active != 0 {
			return ErrConflict
		}
	}
	now := nowUTC()
	res, err := tx.ExecContext(ctx, `UPDATE gw_credential_pools SET status=?,config_version=config_version+1,updated_at=? WHERE id=? AND status=?`, string(to), now, poolID, string(from))
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
	if err := tx.QueryRowContext(ctx, `SELECT config_version FROM gw_credential_pools WHERE id=?`, poolID).Scan(&version); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO gw_credential_pool_state_events(credential_pool_id,state_version,old_state,new_state,reason_code,created_at) VALUES (?,?,?,?,?,?)`, poolID, version, string(from), string(to), reason, now)
	return err
}

func (s *Store) TransitionCredentialGrant(ctx context.Context, tx *sql.Tx, grantID uint64, from, to credentials.GrantState, reason string) error {
	if tx == nil || grantID == 0 || reason == "" {
		return ErrInvalidInput
	}
	if err := credentials.TransitionGrant(from, to); err != nil {
		return err
	}
	now := nowUTC()
	res, err := tx.ExecContext(ctx, `UPDATE gw_credential_purpose_grants SET status=?,state_version=state_version+1,revoked_at=CASE WHEN ?='revoked' THEN ? ELSE revoked_at END WHERE id=? AND status=?`, string(to), string(to), now, grantID, string(from))
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
	if err := tx.QueryRowContext(ctx, `SELECT state_version FROM gw_credential_purpose_grants WHERE id=?`, grantID).Scan(&version); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO gw_credential_purpose_grant_state_events(grant_id,state_version,old_state,new_state,reason_code,created_at) VALUES (?,?,?,?,?,?)`, grantID, version, string(from), string(to), reason, now)
	return err
}

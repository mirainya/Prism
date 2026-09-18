package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/mirainya/Prism/internal/gateway/credentials"
	"github.com/mirainya/Prism/internal/gateway/security"
)

var ErrCredentialEncryptionUnavailable = errors.New("credential encryption configuration is unavailable")
var ErrDuplicateCredentialSecret = errors.New("credential secret already exists in this channel")

type ManagedCredentialInput struct {
	Code         string                `json:"credential_code"`
	RequestLimit *uint64               `json:"request_limit"`
	TaskLimit    *uint64               `json:"task_limit"`
	Weight       uint64                `json:"weight"`
	Purposes     []credentials.Purpose `json:"purposes"`
	Secret       []byte                `json:"-"`
}

// CreateManagedCredentialPlaintext is the simplified operator path. It keeps
// the identity and active-version rows required by existing validation and
// audit paths, while the upstream key itself lives directly on the credential.
func (s *Store) CreateManagedCredentialPlaintext(ctx context.Context, tx *sql.Tx, poolID uint64, in ManagedCredentialInput, actorID uint64) (uint64, error) {
	if tx == nil || poolID == 0 || actorID == 0 {
		return 0, ErrInvalidInput
	}
	if err := in.Validate(); err != nil {
		return 0, err
	}
	var channelID uint64
	if err := tx.QueryRowContext(ctx, `SELECT channel_id FROM gw_credential_pools WHERE id=?`, poolID).Scan(&channelID); err == sql.ErrNoRows {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	}
	var state string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM gateway_channels WHERE id=? FOR SHARE`, channelID).Scan(&state); err != nil {
		return 0, err
	}
	if state != "active" {
		return 0, ErrConflict
	}
	if err := tx.QueryRowContext(ctx, `SELECT status FROM gw_credential_pools WHERE id=? AND channel_id=? FOR UPDATE`, poolID, channelID).Scan(&state); err != nil {
		return 0, err
	}
	if state != "active" {
		return 0, ErrConflict
	}
	// Keep the identity and version rows populated because runtime admission
	// and validation records are still pinned to them.
	identityID, err := s.ensureActivePlaintextSecretIdentity(ctx, tx, channelID, in.Secret)
	if err != nil {
		return 0, err
	}
	var existing uint64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM gw_credentials WHERE channel_id=? AND credential_code=? FOR UPDATE`, channelID, in.Code).Scan(&existing); err == nil {
		return 0, ErrDuplicateCredentialSecret
	} else if err != sql.ErrNoRows {
		return 0, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT id FROM gw_credentials WHERE channel_id=? AND (BINARY secret=BINARY ? OR ((secret IS NULL OR secret='') AND secret_identity_id=?)) FOR UPDATE`, channelID, string(in.Secret), identityID).Scan(&existing); err == nil {
		return 0, ErrDuplicateCredentialSecret
	} else if err != sql.ErrNoRows {
		return 0, err
	}
	id, err := s.CreateCredential(ctx, tx, CredentialInput{ChannelID: channelID, PoolID: poolID, SecretIdentityID: identityID, Code: in.Code, Secret: in.Secret, RequestLimit: in.RequestLimit, TaskLimit: in.TaskLimit, Weight: &in.Weight})
	if err != nil {
		return 0, err
	}
	versionID, err := s.CreateCredentialVersion(ctx, tx, CredentialVersionInput{ChannelID: channelID, CredentialID: id, SecretIdentityID: identityID, VersionNo: 1})
	if err != nil {
		return 0, err
	}
	if err := s.ActivateCredentialVersion(ctx, tx, id, versionID); err != nil {
		return 0, err
	}
	for _, purpose := range in.Purposes {
		if _, err := s.GrantCredentialPurpose(ctx, tx, id, purpose, 1); err != nil {
			return 0, err
		}
	}
	return id, recordCatalogAdminChange(ctx, tx, actorID, "unified.credential.create", "credential", id, ginSafeMetadata{"credential_code": in.Code, "purposes": in.Purposes})
}

func (in ManagedCredentialInput) Validate() error {
	if !channelCodePattern.MatchString(in.Code) || !validPoolLimit(in.RequestLimit) || !validPoolLimit(in.TaskLimit) || in.Weight == 0 || in.Weight > 1000000 || len(in.Secret) == 0 || len(in.Secret) > 8192 || len(in.Purposes) == 0 || len(in.Purposes) > 3 {
		return ErrInvalidInput
	}
	for _, b := range in.Secret {
		if b < 33 || b > 126 {
			return ErrInvalidInput
		}
	}
	seen := make(map[credentials.Purpose]bool, len(in.Purposes))
	for _, purpose := range in.Purposes {
		if !validPurpose(purpose) || seen[purpose] {
			return ErrInvalidInput
		}
		seen[purpose] = true
	}
	return nil
}

func (s *Store) CreateManagedCredential(ctx context.Context, tx *sql.Tx, poolID uint64, in ManagedCredentialInput, kek, hmacKey []byte, actorID uint64) (uint64, error) {
	if tx == nil || poolID == 0 || actorID == 0 || len(kek) != security.KeySize || len(hmacKey) != security.KeySize {
		return 0, ErrInvalidInput
	}
	if err := in.Validate(); err != nil {
		return 0, err
	}
	var channelID uint64
	if err := tx.QueryRowContext(ctx, `SELECT channel_id FROM gw_credential_pools WHERE id=?`, poolID).Scan(&channelID); err == sql.ErrNoRows {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	}
	var state string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM gateway_channels WHERE id=? FOR SHARE`, channelID).Scan(&state); err != nil {
		return 0, err
	}
	if state != "active" {
		return 0, ErrConflict
	}
	if err := tx.QueryRowContext(ctx, `SELECT status FROM gw_credential_pools WHERE id=? AND channel_id=? FOR UPDATE`, poolID, channelID).Scan(&state); err != nil {
		return 0, err
	}
	if state != "active" {
		return 0, ErrConflict
	}
	keyringID, version, err := s.credentialWriteKeyring(ctx, tx, kek, hmacKey)
	if err != nil {
		return 0, err
	}
	digest := security.HMACSHA256(hmacKey, in.Secret)
	identityID, err := s.EnsureSecretIdentity(ctx, tx, SecretIdentityInput{ChannelID: channelID, SecretHMAC: hex.EncodeToString(digest[:]), HMACKeyVersion: 1})
	if err != nil {
		return 0, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT status FROM gw_credential_secret_identities WHERE id=? FOR UPDATE`, identityID).Scan(&state); err != nil {
		return 0, err
	}
	if state != "active" {
		return 0, ErrConflict
	}
	var existing uint64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM gw_credentials WHERE secret_identity_id=? FOR UPDATE`, identityID).Scan(&existing); err == nil {
		return 0, ErrDuplicateCredentialSecret
	} else if err != sql.ErrNoRows {
		return 0, err
	}
	id, err := s.CreateCredential(ctx, tx, CredentialInput{ChannelID: channelID, PoolID: poolID, SecretIdentityID: identityID, Code: in.Code, Secret: in.Secret, RequestLimit: in.RequestLimit, TaskLimit: in.TaskLimit, Weight: &in.Weight})
	if err != nil {
		return 0, err
	}
	blobID, err := s.PutEncryptedBlob(ctx, tx, BlobInput{KeyringID: keyringID, KEKVersion: version, Purpose: "credential", SchemaVersion: 1, Owner: []byte(fmt.Sprintf("credential:%d", id)), Plaintext: in.Secret, KEK: kek, HMACKey: hmacKey})
	if err != nil {
		return 0, err
	}
	versionID, err := s.CreateCredentialVersion(ctx, tx, CredentialVersionInput{ChannelID: channelID, CredentialID: id, SecretIdentityID: identityID, EncryptedBlobID: blobID, VersionNo: 1})
	if err != nil {
		return 0, err
	}
	if err := s.ActivateCredentialVersion(ctx, tx, id, versionID); err != nil {
		return 0, err
	}
	for _, purpose := range in.Purposes {
		if _, err := s.GrantCredentialPurpose(ctx, tx, id, purpose, 1); err != nil {
			return 0, err
		}
	}
	return id, recordCatalogAdminChange(ctx, tx, actorID, "unified.credential.create", "credential", id, in)
}

// Validate the configured key against a retained credential before using it for
// new writes. The current environment provider supports HMAC version 1 only.
func (s *Store) credentialWriteKeyring(ctx context.Context, tx *sql.Tx, kek, hmacKey []byte) (uint64, uint32, error) {
	var keyringID uint64
	var version uint32
	err := tx.QueryRowContext(ctx, `SELECT k.id,k.current_version FROM crypto_keyring_state k JOIN crypto_key_versions v ON v.keyring_id=k.id AND v.key_version=k.current_version WHERE k.purpose='gateway-credential' AND v.status='current' AND v.provider_key_ref='env:PRISM_GATEWAY_KEK_B64' AND v.algorithm='aes-256-gcm' FOR SHARE`).Scan(&keyringID, &version)
	if err == sql.ErrNoRows {
		return 0, 0, ErrCredentialEncryptionUnavailable
	} else if err != nil {
		return 0, 0, err
	}
	var otherHMACVersions uint64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_credential_secret_identities WHERE hmac_key_version<>1`).Scan(&otherHMACVersions); err != nil {
		return 0, 0, err
	}
	if otherHMACVersions != 0 {
		return 0, 0, ErrCredentialEncryptionUnavailable
	}
	var blobID, credentialID uint64
	err = tx.QueryRowContext(ctx, `SELECT v.encrypted_blob_id,v.credential_id FROM gw_credential_versions v JOIN encrypted_blobs b ON b.id=v.encrypted_blob_id WHERE b.keyring_id=? AND b.purged_at IS NULL ORDER BY v.id LIMIT 1`, keyringID).Scan(&blobID, &credentialID)
	if err == sql.ErrNoRows {
		return keyringID, version, nil
	} else if err != nil {
		return 0, 0, err
	}
	envelope, err := s.ReadEncryptedBlob(ctx, tx, blobID)
	if err != nil {
		return 0, 0, err
	}
	if envelope.KEKVersion != version {
		return 0, 0, ErrCredentialEncryptionUnavailable
	}
	plain, err := OpenBlob(envelope, blobID, []byte(fmt.Sprintf("credential:%d", credentialID)), kek, hmacKey)
	clear(plain)
	if err != nil {
		return 0, 0, ErrCredentialEncryptionUnavailable
	}
	return keyringID, version, nil
}

type CredentialUpdate struct {
	// Secret replaces the upstream key in place. Nil leaves the key unchanged.
	Secret          *string `json:"secret"`
	RequestLimit    *uint64 `json:"request_limit"`
	TaskLimit       *uint64 `json:"task_limit"`
	Weight          uint64  `json:"weight"`
	ExpectedVersion uint64  `json:"expected_version"`
}

func (in CredentialUpdate) Validate() error {
	if !validPoolLimit(in.RequestLimit) || !validPoolLimit(in.TaskLimit) || in.Weight == 0 || in.Weight > 1000000 || in.ExpectedVersion == 0 {
		return ErrInvalidInput
	}
	if in.Secret != nil {
		value := []byte(*in.Secret)
		if len(value) == 0 || len(value) > 8192 {
			return ErrInvalidInput
		}
		for _, b := range value {
			if b < 33 || b > 126 {
				return ErrInvalidInput
			}
		}
	}
	return nil
}

func (s *Store) UpdateManagedCredential(ctx context.Context, tx *sql.Tx, id uint64, in CredentialUpdate, actorID uint64) error {
	if tx == nil || id == 0 || actorID == 0 {
		return ErrInvalidInput
	}
	if err := in.Validate(); err != nil {
		return err
	}
	var state string
	var version, channelID uint64
	if err := tx.QueryRowContext(ctx, `SELECT status,config_version,channel_id FROM gw_credentials WHERE id=? FOR UPDATE`, id).Scan(&state, &version, &channelID); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if state != "active" || version != in.ExpectedVersion {
		return ErrConflict
	}
	if in.Secret != nil {
		var duplicateID uint64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM gw_credentials WHERE channel_id=? AND id<>? AND BINARY secret=BINARY ? FOR UPDATE`, channelID, id, *in.Secret).Scan(&duplicateID); err == nil {
			return ErrDuplicateCredentialSecret
		} else if err != sql.ErrNoRows {
			return err
		}
		if err := requireOneRow(tx.ExecContext(ctx, `UPDATE gw_credentials SET secret=?,request_limit=?,task_limit=?,weight=?,config_version=config_version+1,updated_at=? WHERE id=? AND config_version=?`, *in.Secret, nullableUint64(in.RequestLimit), nullableUint64(in.TaskLimit), in.Weight, nowUTC(), id, in.ExpectedVersion)); err != nil {
			return err
		}
		// Route breakers are namespaced with bit 31 and keyed by credential ID.
		// A replacement secret fixes the failed identity represented by those
		// rows, so retaining them would keep the new key out of the next request.
		if _, err := tx.ExecContext(ctx, `DELETE FROM gw_route_states WHERE (key_id & 2147483648)<>0 AND (key_id & 2147483647)=?`, id); err != nil {
			return err
		}
	} else if err := requireOneRow(tx.ExecContext(ctx, `UPDATE gw_credentials SET request_limit=?,task_limit=?,weight=?,config_version=config_version+1,updated_at=? WHERE id=? AND config_version=?`, nullableUint64(in.RequestLimit), nullableUint64(in.TaskLimit), in.Weight, nowUTC(), id, in.ExpectedVersion)); err != nil {
		return err
	}
	metadata := in
	metadata.Secret = nil
	return recordCatalogAdminChange(ctx, tx, actorID, "unified.credential.update", "credential", id, metadata)
}

func (s *Store) ensureActivePlaintextSecretIdentity(ctx context.Context, tx *sql.Tx, channelID uint64, secret []byte) (uint64, error) {
	digest := sha256.Sum256(secret)
	identityID, err := s.EnsureSecretIdentity(ctx, tx, SecretIdentityInput{
		ChannelID:      channelID,
		SecretHMAC:     hex.EncodeToString(digest[:]),
		HMACKeyVersion: 1,
	})
	if err != nil {
		return 0, err
	}
	var state string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM gw_credential_secret_identities WHERE id=? FOR UPDATE`, identityID).Scan(&state); err != nil {
		return 0, err
	}
	if state != "active" {
		return 0, ErrConflict
	}
	return identityID, nil
}

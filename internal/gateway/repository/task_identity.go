package repository

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/mirainya/Prism/internal/gateway/security"
)

// PutTaskIdentity compares decrypted identities on replay; randomized
// encryption must not turn the same provider task ID into a conflict.
func (s *Store) PutTaskIdentity(ctx context.Context, tx *sql.Tx, asyncID uint64, blob BlobInput, expiresAt time.Time) (uint64, error) {
	if tx == nil || asyncID == 0 || len(blob.Plaintext) == 0 || len(blob.Plaintext) > 4096 || len(blob.KEK) != security.KeySize || len(blob.HMACKey) != security.KeySize || !expiresAt.After(nowUTC()) {
		return 0, ErrInvalidInput
	}
	var scopeKind, scopeKey string
	if err := tx.QueryRowContext(ctx, `SELECT upstream_scope_kind,upstream_scope_key FROM gw_async_executions WHERE id=? FOR UPDATE`, asyncID).Scan(&scopeKind, &scopeKey); err != nil {
		return 0, err
	}
	var keyringID uint64
	if err := tx.QueryRowContext(ctx, `SELECT k.id FROM crypto_keyring_state k JOIN crypto_key_versions v ON v.keyring_id=k.id AND v.key_version=k.current_version WHERE k.id=? AND k.purpose='gateway-payload' AND k.current_version=? AND v.status='current' FOR SHARE`, blob.KeyringID, blob.KEKVersion).Scan(&keyringID); err != nil {
		return 0, err
	}
	owner := []byte(fmt.Sprintf("async:%d:task-identity", asyncID))
	var identityID, blobID uint64
	err := tx.QueryRowContext(ctx, `SELECT id,encrypted_blob_id FROM gw_upstream_task_identities WHERE async_execution_id=? FOR UPDATE`, asyncID).Scan(&identityID, &blobID)
	if err == nil {
		envelope, err := s.ReadEncryptedBlob(ctx, tx, blobID)
		if err != nil {
			return 0, err
		}
		plain, err := OpenBlob(envelope, blobID, owner, blob.KEK, blob.HMACKey)
		if err != nil {
			return 0, err
		}
		defer clear(plain)
		if !bytes.Equal(plain, blob.Plaintext) {
			return 0, ErrConflict
		}
		return identityID, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	blob.Purpose, blob.SchemaVersion, blob.Owner = "gateway-task-identity", 1, owner
	blobID, err = s.PutEncryptedBlob(ctx, tx, blob)
	if err != nil {
		return 0, err
	}
	// Alias HMAC version 1 uses the deployment's retained payload HMAC key.
	// KEK rotation changes wrapping, not the alias identity.
	digest := security.DomainDigest(blob.HMACKey, "upstream-task-id-v1", []byte(scopeKind), []byte(scopeKey), blob.Plaintext)
	return s.BindTaskIdentity(ctx, tx, TaskIdentityInput{AsyncExecutionID: asyncID, ScopeKind: scopeKind, ScopeKey: scopeKey, EncryptedBlobID: blobID, ExpiresAt: expiresAt}, []TaskAliasInput{{ScopeKind: scopeKind, ScopeKey: scopeKey, HMACKeyVersion: 1, ValueHMAC: fmt.Sprintf("%x", digest[:])}})
}

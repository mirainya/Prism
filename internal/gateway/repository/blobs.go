package repository

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"fmt"

	"github.com/mirainya/Prism/internal/gateway/security"
)

type BlobInput struct {
	KeyringID     uint64
	KEKVersion    uint32
	Purpose       string
	SchemaVersion uint32
	Owner         []byte
	Plaintext     []byte
	KEK           []byte
	HMACKey       []byte
}

// PutEncryptedBlob creates the row and its key-wrap in one transaction. A
// temporary empty row obtains the auto-increment identity needed by the AAD;
// it is never visible outside the transaction and is filled before commit.
func (s *Store) PutEncryptedBlob(ctx context.Context, tx *sql.Tx, in BlobInput) (uint64, error) {
	if tx == nil || in.KeyringID == 0 || in.KEKVersion == 0 || in.Purpose == "" || in.SchemaVersion == 0 || len(in.Owner) == 0 || len(in.Plaintext) == 0 || len(in.KEK) != security.KeySize || len(in.HMACKey) == 0 {
		return 0, ErrInvalidInput
	}
	now := nowUTC()
	result, err := tx.ExecContext(ctx, `INSERT INTO encrypted_blobs(keyring_id,purpose,schema_version,aad_hash,nonce,ciphertext,content_hmac,content_length,created_at) VALUES (?,?,?,?,?,?,?,?,?)`, in.KeyringID, in.Purpose, in.SchemaVersion, "", []byte{0}, []byte{}, "", len(in.Plaintext), now)
	if err != nil {
		return 0, fmt.Errorf("allocate encrypted blob: %w", err)
	}
	id, err := lastID(result)
	if err != nil {
		return 0, err
	}
	aad, err := security.CanonicalAAD(id, in.Purpose, in.SchemaVersion, in.Owner)
	if err != nil {
		return 0, err
	}
	envelope, err := security.Seal(in.Plaintext, aad, in.KEK, in.KEKVersion)
	if err != nil {
		return 0, err
	}
	aadHash := sha256.Sum256(aad)
	contentHMAC := security.HMACSHA256(in.HMACKey, in.Plaintext)
	if _, err = tx.ExecContext(ctx, `UPDATE encrypted_blobs SET aad_hash=?,nonce=?,ciphertext=?,content_hmac=? WHERE id=?`, hex.EncodeToString(aadHash[:]), envelope.Nonce, envelope.Ciphertext, hex.EncodeToString(contentHMAC[:]), id); err != nil {
		return 0, fmt.Errorf("write encrypted blob: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO encrypted_blob_key_wraps(encrypted_blob_id,keyring_id,kek_version,wrap_nonce,wrapped_dek,created_at) VALUES (?,?,?,?,?,?)`, id, in.KeyringID, in.KEKVersion, envelope.WrapNonce, envelope.WrappedDEK, now); err != nil {
		return 0, fmt.Errorf("write encrypted blob wrap: %w", err)
	}
	return id, nil
}

type BlobEnvelope struct {
	KeyringID                                uint64
	KEKVersion                               uint32
	Purpose                                  string
	SchemaVersion                            uint32
	AADHash                                  string
	Nonce, Ciphertext, WrapNonce, WrappedDEK []byte
	ContentHMAC                              string
}

func (s *Store) ReadEncryptedBlob(ctx context.Context, db DB, id uint64) (BlobEnvelope, error) {
	if db == nil || id == 0 {
		return BlobEnvelope{}, ErrInvalidInput
	}
	var b BlobEnvelope
	err := db.QueryRowContext(ctx, `SELECT b.keyring_id,b.purpose,b.schema_version,b.aad_hash,b.nonce,b.ciphertext,b.content_hmac,w.kek_version,w.wrap_nonce,w.wrapped_dek FROM encrypted_blobs b JOIN encrypted_blob_key_wraps w ON w.encrypted_blob_id=b.id AND w.keyring_id=b.keyring_id WHERE b.id=? AND b.purged_at IS NULL ORDER BY w.kek_version DESC LIMIT 1`, id).Scan(&b.KeyringID, &b.Purpose, &b.SchemaVersion, &b.AADHash, &b.Nonce, &b.Ciphertext, &b.ContentHMAC, &b.KEKVersion, &b.WrapNonce, &b.WrappedDEK)
	if err == sql.ErrNoRows {
		return BlobEnvelope{}, ErrNotFound
	}
	if err != nil {
		return BlobEnvelope{}, err
	}
	return b, nil
}

func OpenBlob(envelope BlobEnvelope, blobID uint64, owner, kek, hmacKey []byte) ([]byte, error) {
	if blobID == 0 || len(kek) != security.KeySize || len(hmacKey) == 0 {
		return nil, ErrInvalidInput
	}
	aad, err := security.CanonicalAAD(blobID, envelope.Purpose, envelope.SchemaVersion, owner)
	if err != nil {
		return nil, err
	}
	plain, err := (security.Envelope{Version: security.EnvelopeVersion, AADVersion: 1, KEKVersion: envelope.KEKVersion, Nonce: envelope.Nonce, Ciphertext: envelope.Ciphertext, WrapNonce: envelope.WrapNonce, WrappedDEK: envelope.WrappedDEK}).Open(aad, kek)
	if err != nil {
		return nil, err
	}
	digest := security.HMACSHA256(hmacKey, plain)
	if len(envelope.ContentHMAC) != len(digest)*2 {
		return nil, security.ErrAuthentication
	}
	encoded := make([]byte, len(digest)*2)
	hex.Encode(encoded, digest[:])
	if subtle.ConstantTimeCompare(encoded, []byte(envelope.ContentHMAC)) != 1 {
		return nil, security.ErrAuthentication
	}
	return plain, nil
}

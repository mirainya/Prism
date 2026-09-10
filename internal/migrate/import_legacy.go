package migrate

// This importer is intentionally one-way and transactional. It reads the
// legacy gateway tables, creates encrypted credential versions, and builds a
// draft catalog. It never deletes or modifies legacy rows; deletion is a
// separate, reviewed cutover step.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/gateway/security"
)

var ErrImportRequiresKeyring = errors.New("legacy import requires PRISM_GATEWAY_KEK_B64 and PRISM_GATEWAY_HMAC_B64")

type ImportOptions struct {
	KEK     []byte
	HMACKey []byte
}

type ImportReport struct {
	RunID, SourceRows, Skipped                    int64
	Channels, CredentialPools, Credentials        int64
	Models, Operations, SKUs, Abilities, Products int64
	Transports, Offerings, Routes, Endpoints      int64
	GatewayAbilities, VideoChannels, Issues       int64
	ReleaseID                                     int64
	SourceRevisionHMAC                            string
	Reused                                        bool
}

// VerifyEncryptedCredentials reads every active credential blob through the
// configured KEK and verifies its authenticated owner binding. It does not
// expose plaintext and is safe to run repeatedly after deployment.
func VerifyEncryptedCredentials(ctx context.Context, db *sql.DB, kek []byte) error {
	if db == nil || len(kek) != security.KeySize {
		return ErrImportRequiresKeyring
	}
	rows, err := db.QueryContext(ctx, `SELECT v.id,v.credential_id,b.id,b.nonce,b.ciphertext,w.kek_version,w.wrap_nonce,w.wrapped_dek FROM gw_credential_versions v LEFT JOIN encrypted_blobs b ON b.id=v.encrypted_blob_id LEFT JOIN crypto_keyring_state ks ON ks.id=b.keyring_id LEFT JOIN encrypted_blob_key_wraps w ON w.encrypted_blob_id=b.id AND w.keyring_id=b.keyring_id AND w.kek_version=ks.current_version ORDER BY v.id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var checked int
	for rows.Next() {
		var versionID, credentialID int64
		var blobID, keyVersion sql.NullInt64
		var nonce, ciphertext, wrapNonce, wrappedDEK []byte
		if err := rows.Scan(&versionID, &credentialID, &blobID, &nonce, &ciphertext, &keyVersion, &wrapNonce, &wrappedDEK); err != nil {
			return err
		}
		if !blobID.Valid || blobID.Int64 <= 0 || !keyVersion.Valid || keyVersion.Int64 <= 0 || keyVersion.Int64 > 1<<32-1 {
			return fmt.Errorf("credential version %d is missing its encrypted blob or current key wrap", versionID)
		}
		aad, err := security.CanonicalAAD(uint64(blobID.Int64), "credential", 1, []byte(fmt.Sprintf("credential:%d", credentialID)))
		if err != nil {
			return err
		}
		plaintext, err := (security.Envelope{Version: security.EnvelopeVersion, AADVersion: 1, KEKVersion: uint32(keyVersion.Int64), Nonce: nonce, Ciphertext: ciphertext, WrapNonce: wrapNonce, WrappedDEK: wrappedDEK}).Open(aad, kek)
		if err != nil {
			return fmt.Errorf("credential version %d crypto verification failed: %w", versionID, err)
		}
		clear(plaintext)
		checked++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if checked == 0 {
		return fmt.Errorf("no encrypted credential versions found")
	}
	return nil
}

type legacyChannel struct {
	ID       int64
	Name     string
	Protocol string
	BaseURL  string
	Status   int
}

type legacyKey struct {
	ID, ChannelID int64
	Name, APIKey  string
	Weight        int64
	Status        int
}

type legacyAbility struct {
	ID, ChannelID, KeyID   int64
	ModelName, VendorModel string
	Priority               int64
	Status                 int
}

// ImportLegacyGateway imports every legacy catalog domain through the
// revision-aware catalog importer. The old name remains the command/API entry
// point so operators cannot accidentally invoke the incomplete first pass.
func ImportLegacyGateway(ctx context.Context, db *sql.DB, options ImportOptions) (ImportReport, error) {
	return ImportLegacyCatalog(ctx, db, options)
}

type legacyCatalogQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func loadLegacyChannels(ctx context.Context, db legacyCatalogQueryer) ([]legacyChannel, error) {
	rows, err := db.QueryContext(ctx, `SELECT id,name,protocol,base_url,status FROM gw_channels WHERE deleted_at IS NULL ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []legacyChannel
	for rows.Next() {
		var v legacyChannel
		if err := rows.Scan(&v.ID, &v.Name, &v.Protocol, &v.BaseURL, &v.Status); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func loadLegacyKeys(ctx context.Context, db legacyCatalogQueryer) ([]legacyKey, error) {
	rows, err := db.QueryContext(ctx, `SELECT id,channel_id,COALESCE(name,''),api_key,COALESCE(weight,1),status FROM gw_channel_keys WHERE deleted_at IS NULL ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []legacyKey
	for rows.Next() {
		var v legacyKey
		if err := rows.Scan(&v.ID, &v.ChannelID, &v.Name, &v.APIKey, &v.Weight, &v.Status); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func loadLegacyAbilities(ctx context.Context, db legacyCatalogQueryer) ([]legacyAbility, error) {
	rows, err := db.QueryContext(ctx, `SELECT id,model_name,channel_id,key_id,vendor_model,COALESCE(priority,0),status FROM gw_abilities WHERE status<>0 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []legacyAbility
	for rows.Next() {
		var v legacyAbility
		if err := rows.Scan(&v.ID, &v.ModelName, &v.ChannelID, &v.KeyID, &v.VendorModel, &v.Priority, &v.Status); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func ensureKeyring(ctx context.Context, tx *sql.Tx, now time.Time) (int64, int64, error) {
	if _, err := tx.ExecContext(ctx, `INSERT IGNORE INTO crypto_keyring_state (purpose,current_version,created_at,updated_at) VALUES ('gateway-credential',1,?,?)`, now, now); err != nil {
		return 0, 0, err
	}
	var keyringID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM crypto_keyring_state WHERE purpose='gateway-credential'`).Scan(&keyringID); err != nil {
		return 0, 0, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT IGNORE INTO crypto_key_versions (keyring_id,key_version,status,provider_key_ref,algorithm,created_at) VALUES (?,1,'current','env:PRISM_GATEWAY_KEK_B64','aes-256-gcm',?)`, keyringID, now); err != nil {
		return 0, 0, err
	}
	return keyringID, 1, nil
}

func insertEncryptedBlob(ctx context.Context, tx *sql.Tx, keyringID, keyVersion, ownerID int64, plaintext, kek, hmacKey []byte, now time.Time) (int64, error) {
	contentHMAC := security.HMACSHA256(hmacKey, plaintext)
	result, err := tx.ExecContext(ctx, `INSERT INTO encrypted_blobs (keyring_id,purpose,schema_version,aad_hash,nonce,ciphertext,content_hmac,content_length,created_at) VALUES (?, ?, 1, ?, ?, ?, ?, ?, ?)`, keyringID, "credential", strings.Repeat("0", 64), []byte{0}, []byte{0}, hex.EncodeToString(contentHMAC[:]), len(plaintext), now)
	if err != nil {
		return 0, err
	}
	blobID, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	aad, err := security.CanonicalAAD(uint64(blobID), "credential", 1, []byte(fmt.Sprintf("credential:%d", ownerID)))
	if err != nil {
		return 0, err
	}
	envelope, err := security.Seal(plaintext, aad, kek, uint32(keyVersion))
	if err != nil {
		return 0, err
	}
	aadHash := sha256.Sum256(aad)
	if _, err = tx.ExecContext(ctx, `UPDATE encrypted_blobs SET aad_hash=?,nonce=?,ciphertext=? WHERE id=?`, hex.EncodeToString(aadHash[:]), envelope.Nonce, envelope.Ciphertext, blobID); err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO encrypted_blob_key_wraps (encrypted_blob_id,keyring_id,kek_version,wrap_nonce,wrapped_dek,created_at) VALUES (?,?,?,?,?,?)`, blobID, keyringID, keyVersion, envelope.WrapNonce, envelope.WrappedDEK, now); err != nil {
		return 0, err
	}
	return blobID, nil
}

func insertID(ctx context.Context, tx *sql.Tx, query string, args ...any) (int64, error) {
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func importDigest(channels []legacyChannel, keys []legacyKey, abilities []legacyAbility) string {
	parts := make([]string, 0, len(channels)+len(keys)+len(abilities))
	for _, v := range channels {
		parts = append(parts, fmt.Sprintf("c:%d:%s:%s:%s", v.ID, v.Name, v.Protocol, v.BaseURL))
	}
	for _, v := range keys {
		parts = append(parts, fmt.Sprintf("k:%d:%d:%s", v.ID, v.ChannelID, v.Name))
	}
	for _, v := range abilities {
		parts = append(parts, fmt.Sprintf("a:%d:%s:%d:%s", v.ID, v.ModelName, v.ChannelID, v.VendorModel))
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])
}

func legacyStatus(status int) string {
	if status == 0 {
		return "disabled"
	}
	return "active"
}
func legacyCredentialStatus(status int) string {
	if status == 0 {
		return "disabled"
	}
	return "active"
}
func maxOne(v int64) int64 {
	if v < 1 {
		return 1
	}
	return v
}
func maxZero(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}

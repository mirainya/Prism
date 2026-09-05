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
	Channels, Credentials, Models, Abilities int64
	ReleaseID                                int64
}

// VerifyEncryptedCredentials reads every active credential blob through the
// configured KEK and verifies its authenticated owner binding. It does not
// expose plaintext and is safe to run repeatedly after deployment.
func VerifyEncryptedCredentials(ctx context.Context, db *sql.DB, kek []byte) error {
	if db == nil || len(kek) != security.KeySize {
		return ErrImportRequiresKeyring
	}
	rows, err := db.QueryContext(ctx, `SELECT v.id,v.credential_id,b.id,b.nonce,b.ciphertext,w.kek_version,w.wrap_nonce,w.wrapped_dek FROM gw_credential_versions v JOIN encrypted_blobs b ON b.id=v.encrypted_blob_id JOIN encrypted_blob_key_wraps w ON w.encrypted_blob_id=b.id AND w.keyring_id=b.keyring_id ORDER BY v.id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var checked int
	for rows.Next() {
		var versionID, credentialID, blobID int64
		var nonce, ciphertext, wrapNonce, wrappedDEK []byte
		var keyVersion int64
		if err := rows.Scan(&versionID, &credentialID, &blobID, &nonce, &ciphertext, &keyVersion, &wrapNonce, &wrappedDEK); err != nil {
			return err
		}
		aad, err := security.CanonicalAAD(uint64(blobID), "credential", 1, []byte(fmt.Sprintf("credential:%d", credentialID)))
		if err != nil {
			return err
		}
		if _, err = (security.Envelope{Version: security.EnvelopeVersion, AADVersion: 1, KEKVersion: uint32(keyVersion), Nonce: nonce, Ciphertext: ciphertext, WrapNonce: wrapNonce, WrappedDEK: wrappedDEK}).Open(aad, kek); err != nil {
			return fmt.Errorf("credential version %d crypto verification failed: %w", versionID, err)
		}
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

// ImportLegacyGateway performs the first, non-destructive import pass. It
// refuses to run once target rows exist so a partial/conflicting migration is
// never silently merged with a second source snapshot.
func ImportLegacyGateway(ctx context.Context, db *sql.DB, options ImportOptions) (ImportReport, error) {
	if db == nil || len(options.KEK) != security.KeySize || len(options.HMACKey) != security.KeySize {
		return ImportReport{}, ErrImportRequiresKeyring
	}
	var targetCount int64
	for _, table := range []string{"gateway_channels", "gw_models", "gw_credentials", "gw_catalog_releases"} {
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM `"+table+"`").Scan(&targetCount); err != nil {
			return ImportReport{}, err
		}
		if targetCount != 0 {
			return ImportReport{}, fmt.Errorf("legacy import refused: target table %s is not empty", table)
		}
	}
	channels, err := loadLegacyChannels(ctx, db)
	if err != nil {
		return ImportReport{}, err
	}
	keys, err := loadLegacyKeys(ctx, db)
	if err != nil {
		return ImportReport{}, err
	}
	abs, err := loadLegacyAbilities(ctx, db)
	if err != nil {
		return ImportReport{}, err
	}
	if len(channels) == 0 || len(keys) == 0 || len(abs) == 0 {
		return ImportReport{}, fmt.Errorf("legacy import refused: source snapshot is incomplete (channels=%d keys=%d abilities=%d)", len(channels), len(keys), len(abs))
	}
	channelSet := make(map[int64]bool, len(channels))
	for _, channel := range channels {
		channelSet[channel.ID] = true
	}
	keySet := make(map[int64]bool, len(keys))
	for _, key := range keys {
		if !channelSet[key.ChannelID] {
			return ImportReport{}, fmt.Errorf("legacy key %d references unavailable channel %d", key.ID, key.ChannelID)
		}
		keySet[key.ID] = true
	}
	for _, ability := range abs {
		if !channelSet[ability.ChannelID] || !keySet[ability.KeyID] {
			return ImportReport{}, fmt.Errorf("legacy ability %d references unavailable channel/key (%d/%d)", ability.ID, ability.ChannelID, ability.KeyID)
		}
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return ImportReport{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	keyringID, keyVersion, err := ensureKeyring(ctx, tx, now)
	if err != nil {
		return ImportReport{}, err
	}
	channelIDs := make(map[int64]int64, len(channels))
	for _, channel := range channels {
		code := fmt.Sprintf("legacy-channel-%d", channel.ID)
		id, err := insertID(ctx, tx, `INSERT INTO gateway_channels (channel_code,display_name,status,created_at) VALUES (?,?,?,?)`, code, channel.Name, legacyStatus(channel.Status), now)
		if err != nil {
			return ImportReport{}, err
		}
		channelIDs[channel.ID] = id
	}

	poolIDs := make(map[int64]int64, len(channels))
	for _, channel := range channels {
		id, err := insertID(ctx, tx, `INSERT INTO gw_credential_pools (channel_id,pool_code,display_name,status,config_version,created_at,updated_at) VALUES (?,?,?,?,?,?,?)`, channelIDs[channel.ID], fmt.Sprintf("legacy-pool-%d", channel.ID), channel.Name+" keys", legacyStatus(channel.Status), 1, now, now)
		if err != nil {
			return ImportReport{}, err
		}
		poolIDs[channel.ID] = id
	}

	credentialIDs := make(map[int64]int64, len(keys))
	versionIDs := make(map[int64]int64, len(keys))
	for _, key := range keys {
		if strings.TrimSpace(key.APIKey) == "" {
			return ImportReport{}, fmt.Errorf("legacy key %d is empty", key.ID)
		}
		channelID := channelIDs[key.ChannelID]
		poolID := poolIDs[key.ChannelID]
		fingerprint := security.HMACSHA256(options.HMACKey, []byte(key.APIKey))
		identityID, err := insertID(ctx, tx, `INSERT INTO gw_credential_secret_identities (channel_id,secret_hmac,hmac_key_version,status,created_at,updated_at) VALUES (?,?,?,?,?,?)`, channelID, hex.EncodeToString(fingerprint[:]), 1, "active", now, now)
		if err != nil {
			return ImportReport{}, err
		}
		credentialID, err := insertID(ctx, tx, `INSERT INTO gw_credentials (channel_id,credential_pool_id,secret_identity_id,credential_code,status,config_version,weight,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?)`, channelID, poolID, identityID, fmt.Sprintf("legacy-key-%d", key.ID), legacyCredentialStatus(key.Status), 1, maxOne(key.Weight), now, now)
		if err != nil {
			return ImportReport{}, err
		}
		blobID, err := insertEncryptedBlob(ctx, tx, keyringID, keyVersion, credentialID, []byte(key.APIKey), options.KEK, options.HMACKey, now)
		if err != nil {
			return ImportReport{}, err
		}
		versionID, err := insertID(ctx, tx, `INSERT INTO gw_credential_versions (channel_id,credential_id,secret_identity_id,version_no,encrypted_blob_id,status,created_at) VALUES (?,?,?,?,?,?,?)`, channelID, credentialID, identityID, 1, blobID, "active", now)
		if err != nil {
			return ImportReport{}, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE gw_credentials SET current_version_id=? WHERE id=?`, versionID, credentialID); err != nil {
			return ImportReport{}, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO gw_credential_purpose_grants (credential_id,purpose,grant_seq,status,state_version,created_at) VALUES (?,?,?,?,?,?)`, credentialID, "execution", 1, "active", 1, now); err != nil {
			return ImportReport{}, err
		}
		credentialIDs[key.ID], versionIDs[key.ID] = credentialID, versionID
	}

	modelIDs := make(map[string]int64)
	for _, ability := range abs {
		if _, ok := modelIDs[ability.ModelName]; ok {
			continue
		}
		modelID, err := insertID(ctx, tx, `INSERT INTO gw_models (model_code,created_at) VALUES (?,?)`, ability.ModelName, now)
		if err != nil {
			return ImportReport{}, err
		}
		if _, err = insertID(ctx, tx, `INSERT INTO gw_model_names (model_id,api_name,is_primary,created_at) VALUES (?,?,?,?)`, modelID, ability.ModelName, true, now); err != nil {
			return ImportReport{}, err
		}
		modelIDs[ability.ModelName] = modelID
	}

	contentHash := importDigest(channels, keys, abs)
	releaseID, err := insertID(ctx, tx, `INSERT INTO gw_catalog_releases (release_no,status,serialization_version,content_hash_algorithm,content_hash,semantic_version,semantic_digest,created_at) VALUES (?,?,?,?,?,?,?,?)`, 1, "draft", 1, "sha256", contentHash, "legacy-import-1", contentHash, now)
	if err != nil {
		return ImportReport{}, err
	}
	contractID, err := insertID(ctx, tx, `INSERT INTO gw_operation_contracts (operation_code,contract_version,status,created_at) VALUES (?,?,?,?)`, "chat.completions", 1, "active", now)
	if err != nil {
		return ImportReport{}, err
	}
	if _, err = insertID(ctx, tx, `INSERT INTO gw_operation_routes (operation_contract_id,http_method,route_template,created_at) VALUES (?,?,?,?)`, contractID, "POST", "/v1/chat/completions", now); err != nil {
		return ImportReport{}, err
	}
	adapterIDs := make(map[string]int64)
	transportIDs := make(map[int64]int64)
	for _, channel := range channels {
		protocol := channel.Protocol
		if protocol == "" {
			protocol = "openai"
		}
		adapterID := adapterIDs[protocol]
		if adapterID == 0 {
			adapterDigest := sha256.Sum256([]byte("legacy-adapter:" + protocol))
			adapterID, err = insertID(ctx, tx, `INSERT INTO gw_adapter_implementations (adapter_code,contract_version,implementation_digest,minimum_semantic_version,created_at) VALUES (?,?,?,?,?)`, "legacy-"+protocol, 1, hex.EncodeToString(adapterDigest[:]), "1.0.0", now)
			if err != nil {
				return ImportReport{}, err
			}
			adapterIDs[protocol] = adapterID
		}
		transportID, err := insertID(ctx, tx, `INSERT INTO gw_channel_transports (release_id,channel_id,adapter_implementation_id,transport_code,base_url,protocol,request_method,request_path,auth_scheme,execution_fingerprint,timeout_ms,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, releaseID, channelIDs[channel.ID], adapterID, fmt.Sprintf("legacy-transport-%d", channel.ID), strings.TrimRight(channel.BaseURL, "/"), protocol, "POST", "/v1/chat/completions", "bearer", contentHash, 30000, now)
		if err != nil {
			return ImportReport{}, err
		}
		transportIDs[channel.ID] = transportID
	}
	catalogModelIDs := make(map[string]int64, len(modelIDs))
	operationIDs := make(map[string]int64, len(modelIDs))
	skuIDs := make(map[string]int64, len(modelIDs))
	for modelName, modelID := range modelIDs {
		catalogModelID, err := insertID(ctx, tx, `INSERT INTO gw_catalog_models (release_id,model_id,display_name,description,capability_tags,sort_order,visibility,created_at) VALUES (?,?,?,?,?,?,?,?)`, releaseID, modelID, modelName, "Imported from legacy gateway", `{"source":"legacy"}`, 0, "visible", now)
		if err != nil {
			return ImportReport{}, err
		}
		if _, err = insertID(ctx, tx, `INSERT INTO gw_catalog_model_names (release_id,catalog_model_id,model_id,model_name_id,is_primary,created_at) SELECT ?,?,?,id,TRUE,? FROM gw_model_names WHERE model_id=?`, releaseID, catalogModelID, modelID, now, modelID); err != nil {
			return ImportReport{}, err
		}
		operationID, err := insertID(ctx, tx, `INSERT INTO gw_model_operations (release_id,catalog_model_id,operation_contract_id,normalization_version,semantic_digest,created_at) VALUES (?,?,?,?,?,?)`, releaseID, catalogModelID, contractID, 1, contentHash, now)
		if err != nil {
			return ImportReport{}, err
		}
		skuID, err := insertID(ctx, tx, `INSERT INTO gw_skus (release_id,model_operation_id,sku_code,delivery_mode,max_results,idempotency_mode,created_at) VALUES (?,?,?,?,?,?,?)`, releaseID, operationID, "legacy-"+modelName, "reference", 1, "optional", now)
		if err != nil {
			return ImportReport{}, err
		}
		catalogModelIDs[modelName], operationIDs[modelName], skuIDs[modelName] = catalogModelID, operationID, skuID
	}
	for _, ability := range abs {
		productID, err := insertID(ctx, tx, `INSERT INTO gw_products (release_id,channel_id,product_code,vendor_model,capability_constraints,constraints_schema_version,created_at) VALUES (?,?,?,?,?,?,?)`, releaseID, channelIDs[ability.ChannelID], fmt.Sprintf("legacy-product-%d", ability.ID), ability.VendorModel, `{"source":"legacy"}`, 1, now)
		if err != nil {
			return ImportReport{}, err
		}
		productTransportID, err := insertID(ctx, tx, `INSERT INTO gw_product_transports (release_id,product_id,channel_transport_id,task_scope,cancel_mode,source_url_policy,upstream_scope_kind,upstream_scope_key,execution_fingerprint,timeout_ms,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`, releaseID, productID, transportIDs[ability.ChannelID], "none", "none", "fixed", "none", "", contentHash, 30000, now)
		if err != nil {
			return ImportReport{}, err
		}
		offeringID, err := insertID(ctx, tx, `INSERT INTO gw_offerings (release_id,product_transport_id,credential_pool_id,commercial_fingerprint,cost_plan_code,created_at) VALUES (?,?,?,?,?,?)`, releaseID, productTransportID, poolIDs[ability.ChannelID], contentHash, "legacy-unpriced", now)
		if err != nil {
			return ImportReport{}, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO gw_offering_runtime_state (release_id,offering_id,state,state_version,reason_code,updated_at) VALUES (?,?, 'active',1,'legacy_import',?)`, releaseID, offeringID, now); err != nil {
			return ImportReport{}, err
		}
		if _, err = insertID(ctx, tx, `INSERT INTO gw_routes (release_id,sku_id,offering_id,priority,weight,created_at) VALUES (?,?,?,?,?,?)`, releaseID, skuIDs[ability.ModelName], offeringID, maxZero(ability.Priority), 1, now); err != nil {
			return ImportReport{}, err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO gw_catalog_runtime_state (id,state_version,updated_at) VALUES (1,1,?)`, now); err != nil {
		return ImportReport{}, err
	}
	if err = tx.Commit(); err != nil {
		return ImportReport{}, err
	}
	return ImportReport{Channels: int64(len(channels)), Credentials: int64(len(keys)), Models: int64(len(modelIDs)), Abilities: int64(len(abs)), ReleaseID: releaseID}, nil
}

func loadLegacyChannels(ctx context.Context, db *sql.DB) ([]legacyChannel, error) {
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

func loadLegacyKeys(ctx context.Context, db *sql.DB) ([]legacyKey, error) {
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

func loadLegacyAbilities(ctx context.Context, db *sql.DB) ([]legacyAbility, error) {
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

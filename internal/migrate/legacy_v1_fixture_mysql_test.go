//go:build integration

package migrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/gateway/security"
)

// importLegacyGatewayV1 recreates the retired first-pass importer so the
// replacement path can be verified against its exact persisted graph.
type legacyV1FixtureOptions struct {
	omitMappingProofSourceTable string
	omitMappingEvidence         bool
}

func importLegacyGatewayV1(ctx context.Context, db *sql.DB, options ImportOptions) (ImportReport, error) {
	return importLegacyGatewayV1WithOptions(ctx, db, options, legacyV1FixtureOptions{})
}

func importLegacyGatewayV1WithOptions(ctx context.Context, db *sql.DB, options ImportOptions, fixtureOptions legacyV1FixtureOptions) (ImportReport, error) {
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
	importRunID, err := insertID(ctx, tx, `INSERT INTO gw_migration_runs (operation,source_revision_hmac,status,started_at) VALUES ('legacy_gateway_import',?,'running',?)`, contentHash, now)
	if err != nil {
		return ImportReport{}, err
	}
	releaseID, err := insertID(ctx, tx, `INSERT INTO gw_catalog_releases (release_no,status,serialization_version,content_hash_algorithm,content_hash,semantic_version,semantic_digest,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?)`, 1, "draft", 1, "sha256", contentHash, "legacy-import-1", contentHash, now, now)
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
	protocols := make(map[int64]string, len(channels))
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
		protocols[channel.ID] = protocol
	}
	catalogModelIDs := make(map[string]int64, len(modelIDs))
	operationIDs := make(map[string]int64, len(modelIDs))
	skuIDs := make(map[string]int64, len(modelIDs))
	productIDs := make(map[int64]int64, len(abs))
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
		skuID, err := insertID(ctx, tx, `INSERT INTO gw_skus (release_id,model_operation_id,sku_code,delivery_mode,max_results,idempotency_mode,service_tiers,created_at) VALUES (?,?,?,?,?,?,JSON_ARRAY('standard'),?)`, releaseID, operationID, "legacy-"+modelName, "reference", 1, "optional", now)
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
		productIDs[ability.ID] = productID
		productTransportID, err := insertID(ctx, tx, `INSERT INTO gw_product_transports (release_id,product_id,channel_transport_id,task_scope,cancel_mode,source_url_policy,upstream_scope_kind,upstream_scope_key,execution_fingerprint,timeout_ms,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`, releaseID, productID, transportIDs[ability.ChannelID], "none", "none", "fixed", "none", "", contentHash, 30000, now)
		if err != nil {
			return ImportReport{}, err
		}
		entitlementDigest := sha256.Sum256([]byte(strings.Join([]string{
			strconv.FormatInt(channelIDs[ability.ChannelID], 10), ability.VendorModel,
			protocols[ability.ChannelID], "none", "",
		}, "\x00")))
		entitlementHash := hex.EncodeToString(entitlementDigest[:])
		offeringID, err := insertID(ctx, tx, `INSERT INTO gw_offerings (release_id,product_transport_id,credential_pool_id,entitlement_fingerprint,commercial_fingerprint,cost_plan_code,created_at) VALUES (?,?,?,?,?,?,?)`, releaseID, productTransportID, poolIDs[ability.ChannelID], entitlementHash, contentHash, "legacy-unpriced", now)
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
	var activeRelease sql.NullInt64
	var runtimeVersion uint64
	if err = tx.QueryRowContext(ctx, `SELECT active_release_id,state_version FROM gw_catalog_runtime_state WHERE id=1 FOR UPDATE`).Scan(&activeRelease, &runtimeVersion); err != nil {
		return ImportReport{}, err
	}
	if activeRelease.Valid || runtimeVersion != 1 {
		return ImportReport{}, fmt.Errorf("legacy import requires an unused catalog runtime state")
	}
	if err := recordLegacyImportAudit(ctx, tx, importRunID, contentHash, fixtureOptions, channels, keys, abs, channelIDs, credentialIDs, productIDs, now); err != nil {
		return ImportReport{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE gw_migration_runs SET status='succeeded',finished_at=? WHERE id=? AND status='running'`, now, importRunID); err != nil {
		return ImportReport{}, err
	}
	if err = tx.Commit(); err != nil {
		return ImportReport{}, err
	}
	return ImportReport{Channels: int64(len(channels)), Credentials: int64(len(keys)), Models: int64(len(modelIDs)), Abilities: int64(len(abs)), ReleaseID: releaseID}, nil
}

func recordLegacyImportAudit(
	ctx context.Context,
	tx *sql.Tx,
	runID int64,
	sourceRevision string,
	fixtureOptions legacyV1FixtureOptions,
	channels []legacyChannel,
	keys []legacyKey,
	abilities []legacyAbility,
	channelIDs map[int64]int64,
	credentialIDs map[int64]int64,
	productIDs map[int64]int64,
	now time.Time,
) error {
	for _, channel := range channels {
		if err := recordLegacyImportMapping(ctx, tx, runID, fixtureOptions, "gw_channels", channel.ID, "gateway_channel", channelIDs[channel.ID], fmt.Sprintf("legacy-channel-%d", channel.ID), sourceRevision, now); err != nil {
			return err
		}
	}
	for _, key := range keys {
		if err := recordLegacyImportMapping(ctx, tx, runID, fixtureOptions, "gw_channel_keys", key.ID, "credential", credentialIDs[key.ID], fmt.Sprintf("legacy-key-%d", key.ID), sourceRevision, now); err != nil {
			return err
		}
	}
	for _, ability := range abilities {
		if err := recordLegacyImportMapping(ctx, tx, runID, fixtureOptions, "gw_abilities", ability.ID, "product", productIDs[ability.ID], fmt.Sprintf("legacy-product-%d", ability.ID), sourceRevision, now); err != nil {
			return err
		}
	}
	return nil
}

func recordLegacyImportMapping(ctx context.Context, tx *sql.Tx, runID int64, fixtureOptions legacyV1FixtureOptions, sourceTable string, sourceID int64, targetType string, targetID int64, discriminator, sourceRevision string, now time.Time) error {
	if targetID <= 0 {
		return fmt.Errorf("legacy import mapping %s/%d has no target", sourceTable, sourceID)
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_migration_object_map (run_id,source_table,source_pk,target_type,target_id,target_discriminator,created_at) VALUES (?,?,?,?,?,?,?)`, runID, sourceTable, strconv.FormatInt(sourceID, 10), targetType, targetID, discriminator, now)
	if err != nil {
		return err
	}
	mappingID, err := result.LastInsertId()
	if err != nil {
		return err
	}
	if fixtureOptions.omitMappingEvidence {
		return nil
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO gw_migration_source_revisions (object_map_id,source_hmac,observed_at) VALUES (?,?,?)`, mappingID, sourceRevision, now); err != nil {
		return err
	}
	if sourceTable == fixtureOptions.omitMappingProofSourceTable {
		return nil
	}
	return recordMappingProof(ctx, tx, runID, mappingID, sourceRevision, now)
}

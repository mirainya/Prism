package migrate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/mirainya/Prism/internal/gateway/security"
)

const legacyGatewayImportOperation = "legacy_gateway_import"

type legacyDraftReplacement struct {
	releaseID int64
	blobIDs   []int64
}

// replaceLegacyGatewayDraft removes only the incomplete V1 draft. It is
// intentionally strict: every source root must have a proof from the successful
// V1 import, the generated graph must still match that source, and no runtime
// or control-plane fact may reference the graph.
func replaceLegacyGatewayDraft(ctx context.Context, tx *sql.Tx, options ImportOptions) (bool, string, error) {
	channels, err := loadLegacyChannels(ctx, tx)
	if err != nil {
		return false, "", fmt.Errorf("load legacy channels for replacement: %w", err)
	}
	keys, err := loadLegacyKeys(ctx, tx)
	if err != nil {
		return false, "", fmt.Errorf("load legacy credentials for replacement: %w", err)
	}
	abilities, err := loadLegacyAbilities(ctx, tx)
	if err != nil {
		return false, "", fmt.Errorf("load legacy abilities for replacement: %w", err)
	}
	if len(channels) == 0 || len(keys) == 0 || len(abilities) == 0 {
		return false, "the existing target cannot be matched to a complete V1 source snapshot", nil
	}

	draft, reason, err := inspectLegacyGatewayDraft(ctx, tx, options, channels, keys, abilities)
	if err != nil || draft == nil {
		return false, reason, err
	}
	if err := deleteLegacyGatewayDraft(ctx, tx, draft); err != nil {
		return false, "", fmt.Errorf("delete verified V1 catalog draft: %w", err)
	}
	return true, "", nil
}

func inspectLegacyGatewayDraft(
	ctx context.Context,
	tx *sql.Tx,
	options ImportOptions,
	channels []legacyChannel,
	keys []legacyKey,
	abilities []legacyAbility,
) (*legacyDraftReplacement, string, error) {
	unsafe := func(reason string) (*legacyDraftReplacement, string, error) { return nil, reason, nil }

	var activeRelease sql.NullInt64
	var runtimeVersion uint64
	if err := tx.QueryRowContext(ctx, `SELECT active_release_id,state_version FROM gw_catalog_runtime_state WHERE id=1 FOR UPDATE`).Scan(&activeRelease, &runtimeVersion); err != nil {
		return nil, "", err
	}
	if activeRelease.Valid || runtimeVersion != 1 {
		return unsafe("the catalog runtime state has already been used")
	}
	for _, check := range []struct {
		query  string
		reason string
	}{
		{`SELECT COUNT(*) FROM gw_api_calls`, "unified calls already reference the target catalog"},
		{`SELECT COUNT(*) FROM gw_deployment_generations WHERE status='active'`, "an active deployment exists"},
		{`SELECT COUNT(*) FROM gw_control_plane_runs`, "control-plane runs already reference the target graph"},
		{`SELECT COUNT(*) FROM gw_catalog_sources`, "catalog sources already reference the target graph"},
		{`SELECT COUNT(*) FROM gw_credential_slots`, "credential slots already reference the target graph"},
		{`SELECT COUNT(*) FROM gw_provider_state_refs`, "provider state already references the target graph"},
		{`SELECT COUNT(*) FROM gw_execution_health`, "execution health already references the target graph"},
		{`SELECT COUNT(*) FROM gw_upstream_cost_evidence`, "upstream cost evidence already references the target graph"},
	} {
		var count int64
		if err := tx.QueryRowContext(ctx, check.query).Scan(&count); err != nil {
			return nil, "", err
		}
		if count != 0 {
			return unsafe(check.reason)
		}
	}

	var releaseID, releaseNo, configVersion, serializationVersion int64
	var status, hashAlgorithm, contentHash, semanticVersion, semanticDigest string
	var reviewedBy sql.NullInt64
	var publishedAt sql.NullTime
	var unchanged bool
	if err := tx.QueryRowContext(ctx, `SELECT id,release_no,status,config_version,serialization_version,content_hash_algorithm,
			content_hash,semantic_version,semantic_digest,reviewed_by,published_at,created_at=updated_at
		FROM gw_catalog_releases FOR UPDATE`).Scan(
		&releaseID, &releaseNo, &status, &configVersion, &serializationVersion, &hashAlgorithm,
		&contentHash, &semanticVersion, &semanticDigest, &reviewedBy, &publishedAt, &unchanged,
	); err == sql.ErrNoRows {
		return unsafe("the existing target has no V1 catalog release")
	} else if err != nil {
		return nil, "", err
	}
	var releaseCount int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_catalog_releases`).Scan(&releaseCount); err != nil {
		return nil, "", err
	}
	if releaseCount != 1 || releaseNo != 1 || status != "draft" || configVersion != 1 || serializationVersion != 1 ||
		hashAlgorithm != "sha256" || semanticVersion != "legacy-import-1" || reviewedBy.Valid || publishedAt.Valid || !unchanged {
		return unsafe("the existing release is not an untouched V1 draft")
	}
	expectedRevision := importDigest(channels, keys, abilities)
	if contentHash != expectedRevision || semanticDigest != expectedRevision {
		return unsafe("the V1 release digest no longer matches the legacy source")
	}

	var runID int64
	if err := tx.QueryRowContext(ctx, `SELECT r.id FROM gw_migration_runs r
		WHERE r.operation=? AND r.status='succeeded' AND r.source_revision_hmac=?
		  AND NOT EXISTS (SELECT 1 FROM gw_migration_runs newer WHERE newer.operation=r.operation AND newer.id>r.id)
		FOR UPDATE`, legacyGatewayImportOperation, expectedRevision).Scan(&runID); err == sql.ErrNoRows {
		return unsafe("the V1 draft has no current successful import run proof")
	} else if err != nil {
		return nil, "", err
	}
	var mappingCount int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_migration_object_map WHERE run_id=?`, runID).Scan(&mappingCount); err != nil {
		return nil, "", err
	}
	if mappingCount != int64(len(channels)+len(keys)+len(abilities)) {
		return unsafe("the V1 import object mapping set is incomplete")
	}

	draft := &legacyDraftReplacement{releaseID: releaseID}
	channelIDs := make(map[int64]int64, len(channels))
	poolIDs := make(map[int64]int64, len(channels))
	for _, channel := range channels {
		discriminator := fmt.Sprintf("legacy-channel-%d", channel.ID)
		targetID, ok, err := verifiedLegacyMapping(ctx, tx, runID, expectedRevision, "gw_channels", channel.ID, "gateway_channel", discriminator)
		if err != nil {
			return nil, "", err
		}
		if !ok {
			return unsafe(fmt.Sprintf("legacy channel %d has no verified V1 target", channel.ID))
		}
		var code, name, targetStatus string
		if err := tx.QueryRowContext(ctx, `SELECT channel_code,display_name,status FROM gateway_channels WHERE id=? FOR UPDATE`, targetID).Scan(&code, &name, &targetStatus); err != nil {
			if err == sql.ErrNoRows {
				return unsafe(fmt.Sprintf("legacy channel %d target is missing", channel.ID))
			}
			return nil, "", err
		}
		if code != discriminator || name != channel.Name || targetStatus != legacyStatus(channel.Status) {
			return unsafe(fmt.Sprintf("legacy channel %d target was modified", channel.ID))
		}
		var poolID int64
		var poolCode, poolName, poolStatus string
		var poolVersion int64
		var requestLimit, taskLimit sql.NullInt64
		var poolUnchanged bool
		if err := tx.QueryRowContext(ctx, `SELECT id,pool_code,display_name,status,config_version,request_limit,task_limit,created_at=updated_at
			FROM gw_credential_pools WHERE channel_id=? FOR UPDATE`, targetID).Scan(
			&poolID, &poolCode, &poolName, &poolStatus, &poolVersion, &requestLimit, &taskLimit, &poolUnchanged,
		); err != nil {
			return unsafe(fmt.Sprintf("legacy channel %d credential pool is not uniquely owned by the V1 draft", channel.ID))
		}
		if poolCode != fmt.Sprintf("legacy-pool-%d", channel.ID) || poolName != channel.Name+" keys" ||
			poolStatus != legacyStatus(channel.Status) || poolVersion != 1 || requestLimit.Valid || taskLimit.Valid || !poolUnchanged {
			return unsafe(fmt.Sprintf("legacy channel %d credential pool was modified", channel.ID))
		}
		transportValid, err := verifyLegacyChannelTransport(ctx, tx, releaseID, expectedRevision, channel, targetID)
		if err != nil {
			return nil, "", err
		}
		if !transportValid {
			return unsafe(fmt.Sprintf("legacy channel %d transport was modified", channel.ID))
		}
		channelIDs[channel.ID] = targetID
		poolIDs[channel.ID] = poolID
	}

	for _, key := range keys {
		targetID, ok, err := verifiedLegacyMapping(ctx, tx, runID, expectedRevision, "gw_channel_keys", key.ID, "credential", fmt.Sprintf("legacy-key-%d", key.ID))
		if err != nil {
			return nil, "", err
		}
		if !ok {
			return unsafe(fmt.Sprintf("legacy credential %d has no verified V1 target", key.ID))
		}
		blobID, valid, err := verifyLegacyCredentialTarget(ctx, tx, options, key, targetID, channelIDs[key.ChannelID], poolIDs[key.ChannelID])
		if err != nil {
			return nil, "", err
		}
		if !valid {
			return unsafe(fmt.Sprintf("legacy credential %d target was modified", key.ID))
		}
		draft.blobIDs = append(draft.blobIDs, blobID)
	}

	if reason, err := verifyLegacyModelTargets(ctx, tx, releaseID, expectedRevision, abilities); err != nil {
		return nil, "", err
	} else if reason != "" {
		return unsafe(reason)
	}

	for _, ability := range abilities {
		targetID, ok, err := verifiedLegacyMapping(ctx, tx, runID, expectedRevision, "gw_abilities", ability.ID, "product", fmt.Sprintf("legacy-product-%d", ability.ID))
		if err != nil {
			return nil, "", err
		}
		if !ok {
			return unsafe(fmt.Sprintf("legacy ability %d has no verified V1 target", ability.ID))
		}
		valid, err := verifyLegacyProductTarget(ctx, tx, releaseID, expectedRevision, ability, targetID, channelIDs[ability.ChannelID], poolIDs[ability.ChannelID])
		if err != nil {
			return nil, "", err
		}
		if !valid {
			return unsafe(fmt.Sprintf("legacy ability %d target was modified", ability.ID))
		}
	}

	if reason, err := verifyLegacyDraftCardinality(ctx, tx, releaseID, len(channels), len(keys), len(abilities), distinctLegacyModels(abilities)); err != nil {
		return nil, "", err
	} else if reason != "" {
		return unsafe(reason)
	}
	return draft, "", nil
}

func verifiedLegacyMapping(ctx context.Context, tx *sql.Tx, runID int64, revision, sourceTable string, sourceID int64, targetType, discriminator string) (int64, bool, error) {
	var targetID int64
	err := tx.QueryRowContext(ctx, `SELECT m.target_id FROM gw_migration_object_map m
		JOIN gw_migration_source_revisions sr ON sr.object_map_id=m.id AND sr.source_hmac=?
		JOIN gw_migration_mapping_proofs p ON p.run_id=m.run_id AND p.object_map_id=m.id AND p.source_hmac=sr.source_hmac
		WHERE m.run_id=? AND m.source_table=? AND m.source_pk=? AND m.target_type=? AND m.target_discriminator=?`,
		revision, runID, sourceTable, strconv.FormatInt(sourceID, 10), targetType, discriminator).Scan(&targetID)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	return targetID, err == nil, err
}

func verifyLegacyChannelTransport(ctx context.Context, tx *sql.Tx, releaseID int64, revision string, channel legacyChannel, channelID int64) (bool, error) {
	protocol := channel.Protocol
	if protocol == "" {
		protocol = "openai"
	}
	var transportCode, baseURL, targetProtocol, method, path, authScheme, executionFingerprint string
	var compatibilityFingerprint sql.NullString
	var timeoutMS, adapterContractVersion int64
	var adapterCode, adapterDigest, minimumSemanticVersion string
	err := tx.QueryRowContext(ctx, `SELECT ct.transport_code,ct.base_url,ct.protocol,ct.request_method,ct.request_path,
			ct.auth_scheme,ct.execution_fingerprint,ct.state_compatibility_fingerprint,ct.timeout_ms,
			adapter.adapter_code,adapter.contract_version,adapter.implementation_digest,adapter.minimum_semantic_version
		FROM gw_channel_transports ct
		JOIN gw_adapter_implementations adapter ON adapter.id=ct.adapter_implementation_id
		WHERE ct.release_id=? AND ct.channel_id=? FOR UPDATE`, releaseID, channelID).Scan(
		&transportCode, &baseURL, &targetProtocol, &method, &path,
		&authScheme, &executionFingerprint, &compatibilityFingerprint, &timeoutMS,
		&adapterCode, &adapterContractVersion, &adapterDigest, &minimumSemanticVersion,
	)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	expectedAdapterDigest := sha256.Sum256([]byte("legacy-adapter:" + protocol))
	return legacyV1TransportCodeMatches(transportCode, protocol, channel.ID, channelID) &&
		baseURL == strings.TrimRight(channel.BaseURL, "/") &&
		targetProtocol == protocol && method == "POST" && path == "/v1/chat/completions" &&
		authScheme == "bearer" && executionFingerprint == revision && !compatibilityFingerprint.Valid &&
		timeoutMS == 30000 && adapterCode == "legacy-"+protocol && adapterContractVersion == 1 &&
		adapterDigest == hex.EncodeToString(expectedAdapterDigest[:]) && minimumSemanticVersion == "1.0.0", nil
}

func legacyV1TransportCodeMatches(code, protocol string, sourceChannelID, targetChannelID int64) bool {
	if code == fmt.Sprintf("legacy-transport-%d", sourceChannelID) {
		return true
	}
	prefix := "openai_chat"
	switch protocol {
	case "anthropic":
		prefix = "anthropic_messages"
	case "google":
		prefix = "google_generate_content"
	case "volcengine":
		prefix = "volcengine_responses_v3"
	}
	return code == fmt.Sprintf("%s-%d", prefix, targetChannelID)
}

func verifyLegacyModelTargets(ctx context.Context, tx *sql.Tx, releaseID int64, revision string, abilities []legacyAbility) (string, error) {
	contractID, valid, err := verifyLegacyOperationContract(ctx, tx)
	if err != nil {
		return "", err
	}
	if !valid {
		return "the V1 operation contract was modified", nil
	}
	seen := make(map[string]struct{}, len(abilities))
	for _, ability := range abilities {
		modelName := ability.ModelName
		if _, ok := seen[modelName]; ok {
			continue
		}
		seen[modelName] = struct{}{}

		var modelCode, apiName, displayName, description, sourceTag, visibility string
		var sortOrder, operationContractID, normalizationVersion, maxResults int64
		var tagCount, tierCount int64
		var modelNamePrimary, catalogNamePrimary bool
		var semanticDigest, skuCode, deliveryMode, idempotencyMode, serviceTier string
		err := tx.QueryRowContext(ctx, `SELECT model.model_code,name.api_name,name.is_primary,
				catalog.display_name,catalog.description,
				COALESCE(JSON_UNQUOTE(JSON_EXTRACT(catalog.capability_tags,'$.source')),''),
				COALESCE(JSON_LENGTH(catalog.capability_tags),0),catalog.sort_order,catalog.visibility,
				catalog_name.is_primary,operation.operation_contract_id,operation.normalization_version,operation.semantic_digest,
				sku.sku_code,sku.delivery_mode,sku.max_results,sku.idempotency_mode,
				COALESCE(JSON_UNQUOTE(JSON_EXTRACT(sku.service_tiers,'$[0]')),''),
				COALESCE(JSON_LENGTH(sku.service_tiers),0)
			FROM gw_models model
			JOIN gw_model_names name ON name.model_id=model.id
			JOIN gw_catalog_models catalog ON catalog.model_id=model.id AND catalog.release_id=?
			JOIN gw_catalog_model_names catalog_name ON catalog_name.release_id=catalog.release_id
			  AND catalog_name.catalog_model_id=catalog.id AND catalog_name.model_id=model.id AND catalog_name.model_name_id=name.id
			JOIN gw_model_operations operation ON operation.release_id=catalog.release_id AND operation.catalog_model_id=catalog.id
			JOIN gw_skus sku ON sku.release_id=operation.release_id AND sku.model_operation_id=operation.id
			WHERE model.model_code=? FOR UPDATE`, releaseID, modelName).Scan(
			&modelCode, &apiName, &modelNamePrimary, &displayName, &description,
			&sourceTag, &tagCount, &sortOrder, &visibility, &catalogNamePrimary,
			&operationContractID, &normalizationVersion, &semanticDigest,
			&skuCode, &deliveryMode, &maxResults, &idempotencyMode, &serviceTier, &tierCount,
		)
		if err == sql.ErrNoRows {
			return fmt.Sprintf("legacy model %q target is missing", modelName), nil
		}
		if err != nil {
			return "", err
		}
		if modelCode != modelName || apiName != modelName || !modelNamePrimary ||
			displayName != modelName || description != "Imported from legacy gateway" ||
			sourceTag != "legacy" || tagCount != 1 || sortOrder != 0 || visibility != "visible" || !catalogNamePrimary ||
			operationContractID != contractID || normalizationVersion != 1 || semanticDigest != revision ||
			skuCode != "legacy-"+modelName || deliveryMode != "reference" || maxResults != 1 ||
			idempotencyMode != "optional" || serviceTier != "standard" || tierCount != 1 {
			return fmt.Sprintf("legacy model %q target was modified", modelName), nil
		}
	}
	return "", nil
}

func verifyLegacyOperationContract(ctx context.Context, tx *sql.Tx) (int64, bool, error) {
	var contractID, version int64
	var status string
	err := tx.QueryRowContext(ctx, `SELECT id,contract_version,status FROM gw_operation_contracts
		WHERE operation_code='chat.completions' AND contract_version=1 FOR UPDATE`).Scan(&contractID, &version, &status)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT http_method,route_template FROM gw_operation_routes
		WHERE operation_contract_id=? FOR UPDATE`, contractID)
	if err != nil {
		return 0, false, err
	}
	defer rows.Close()
	var routes [][2]string
	for rows.Next() {
		var route [2]string
		if err := rows.Scan(&route[0], &route[1]); err != nil {
			return 0, false, err
		}
		routes = append(routes, route)
	}
	if err := rows.Err(); err != nil {
		return 0, false, err
	}
	return contractID, version == 1 && status == "active" && len(routes) == 1 &&
		routes[0][0] == "POST" && routes[0][1] == "/v1/chat/completions", nil
}

func verifyLegacyCredentialTarget(ctx context.Context, tx *sql.Tx, options ImportOptions, key legacyKey, credentialID, channelID, poolID int64) (int64, bool, error) {
	var targetChannelID, targetPoolID, identityID, versionID, blobID, configVersion, weight int64
	var hmacVersion, versionNo, keyringID, blobSchemaVersion, contentLength, kekVersion int64
	var code, status, identityHMAC, identityStatus, versionStatus, blobPurpose, contentHMAC, aadHash string
	var requestLimit, taskLimit sql.NullInt64
	var validUntil, retiredAt, purgedAt sql.NullTime
	var credentialUnchanged, identityUnchanged bool
	var nonce, ciphertext, wrapNonce, wrappedDEK []byte
	err := tx.QueryRowContext(ctx, `SELECT c.channel_id,c.credential_pool_id,c.secret_identity_id,COALESCE(c.current_version_id,0),
			c.credential_code,c.status,c.config_version,c.request_limit,c.task_limit,c.weight,c.created_at=c.updated_at,
			i.secret_hmac,i.hmac_key_version,i.status,i.created_at=i.updated_at,
			v.encrypted_blob_id,v.version_no,v.status,v.valid_until,v.retired_at,
			b.keyring_id,b.purpose,b.schema_version,b.content_length,b.purged_at,b.content_hmac,b.aad_hash,b.nonce,b.ciphertext,
			w.kek_version,w.wrap_nonce,w.wrapped_dek
		FROM gw_credentials c
		JOIN gw_credential_secret_identities i ON i.id=c.secret_identity_id
		JOIN gw_credential_versions v ON v.id=c.current_version_id AND v.credential_id=c.id
		JOIN encrypted_blobs b ON b.id=v.encrypted_blob_id AND b.purpose='credential'
		JOIN encrypted_blob_key_wraps w ON w.encrypted_blob_id=b.id AND w.keyring_id=b.keyring_id AND w.kek_version=1
		WHERE c.id=? FOR UPDATE`, credentialID).Scan(
		&targetChannelID, &targetPoolID, &identityID, &versionID,
		&code, &status, &configVersion, &requestLimit, &taskLimit, &weight, &credentialUnchanged,
		&identityHMAC, &hmacVersion, &identityStatus, &identityUnchanged,
		&blobID, &versionNo, &versionStatus, &validUntil, &retiredAt,
		&keyringID, &blobPurpose, &blobSchemaVersion, &contentLength, &purgedAt, &contentHMAC, &aadHash, &nonce, &ciphertext,
		&kekVersion, &wrapNonce, &wrappedDEK,
	)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	expectedHMAC := security.HMACSHA256(options.HMACKey, []byte(key.APIKey))
	expectedHMACHex := hex.EncodeToString(expectedHMAC[:])
	if targetChannelID != channelID || targetPoolID != poolID || versionID <= 0 || identityID <= 0 ||
		code != fmt.Sprintf("legacy-key-%d", key.ID) || status != legacyCredentialStatus(key.Status) ||
		configVersion != 1 || requestLimit.Valid || taskLimit.Valid || weight != maxOne(key.Weight) || !credentialUnchanged ||
		identityHMAC != expectedHMACHex || hmacVersion != 1 || identityStatus != "active" || !identityUnchanged ||
		versionNo != 1 || versionStatus != "active" || validUntil.Valid || retiredAt.Valid ||
		keyringID <= 0 || blobPurpose != "credential" || blobSchemaVersion != 1 ||
		contentLength != int64(len(key.APIKey)) || purgedAt.Valid || contentHMAC != expectedHMACHex || kekVersion != 1 {
		return 0, false, nil
	}
	var grantCount int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_credential_purpose_grants
		WHERE credential_id=? AND purpose='execution' AND grant_seq=1 AND status='active'
		  AND state_version=1 AND revoked_at IS NULL`, credentialID).Scan(&grantCount); err != nil {
		return 0, false, err
	}
	if grantCount != 1 {
		return 0, false, nil
	}
	aad, err := security.CanonicalAAD(uint64(blobID), "credential", 1, []byte(fmt.Sprintf("credential:%d", credentialID)))
	if err != nil {
		return 0, false, err
	}
	expectedAADHash := sha256.Sum256(aad)
	if aadHash != hex.EncodeToString(expectedAADHash[:]) {
		return 0, false, nil
	}
	plaintext, err := (security.Envelope{
		Version: security.EnvelopeVersion, AADVersion: 1, KEKVersion: uint32(kekVersion),
		Nonce: nonce, Ciphertext: ciphertext, WrapNonce: wrapNonce, WrappedDEK: wrappedDEK,
	}).Open(aad, options.KEK)
	if err != nil {
		return 0, false, nil
	}
	defer clear(plaintext)
	if !bytes.Equal(plaintext, []byte(key.APIKey)) {
		return 0, false, nil
	}
	return blobID, true, nil
}

func verifyLegacyProductTarget(ctx context.Context, tx *sql.Tx, releaseID int64, revision string, ability legacyAbility, productID, channelID, poolID int64) (bool, error) {
	var targetReleaseID, targetChannelID, transportChannelID, targetPoolID int64
	var constraintsCount, constraintsVersion, transportTimeout, offeringStateVersion, priority, weight int64
	var productCode, vendorModel, constraintsSource, transportCode, transportProtocol string
	var taskScope, cancelMode, sourceURLPolicy, scopeKind, scopeKey, executionFingerprint string
	var compatibilityFingerprint sql.NullString
	var entitlementFingerprint, commercialFingerprint, costPlanCode, offeringState, offeringReason, modelCode string
	err := tx.QueryRowContext(ctx, `SELECT p.release_id,p.channel_id,p.product_code,p.vendor_model,
			COALESCE(JSON_UNQUOTE(JSON_EXTRACT(p.capability_constraints,'$.source')),''),
			COALESCE(JSON_LENGTH(p.capability_constraints),0),p.constraints_schema_version,
			ct.channel_id,ct.transport_code,ct.protocol,
			pt.task_scope,pt.cancel_mode,pt.source_url_policy,pt.upstream_scope_kind,pt.upstream_scope_key,
			pt.execution_fingerprint,pt.state_compatibility_fingerprint,pt.timeout_ms,
			o.credential_pool_id,o.entitlement_fingerprint,o.commercial_fingerprint,o.cost_plan_code,
			offering_state.state,offering_state.state_version,offering_state.reason_code,
			r.priority,r.weight,m.model_code
		FROM gw_products p
		JOIN gw_product_transports pt ON pt.release_id=p.release_id AND pt.product_id=p.id
		JOIN gw_channel_transports ct ON ct.release_id=pt.release_id AND ct.id=pt.channel_transport_id
		JOIN gw_offerings o ON o.release_id=pt.release_id AND o.product_transport_id=pt.id
		JOIN gw_offering_runtime_state offering_state ON offering_state.release_id=o.release_id AND offering_state.offering_id=o.id
		JOIN gw_routes r ON r.release_id=o.release_id AND r.offering_id=o.id
		JOIN gw_skus s ON s.release_id=r.release_id AND s.id=r.sku_id
		JOIN gw_model_operations mo ON mo.release_id=s.release_id AND mo.id=s.model_operation_id
		JOIN gw_catalog_models cm ON cm.release_id=mo.release_id AND cm.id=mo.catalog_model_id
		JOIN gw_models m ON m.id=cm.model_id
		WHERE p.id=? FOR UPDATE`, productID).Scan(
		&targetReleaseID, &targetChannelID, &productCode, &vendorModel,
		&constraintsSource, &constraintsCount, &constraintsVersion,
		&transportChannelID, &transportCode, &transportProtocol,
		&taskScope, &cancelMode, &sourceURLPolicy, &scopeKind, &scopeKey,
		&executionFingerprint, &compatibilityFingerprint, &transportTimeout,
		&targetPoolID, &entitlementFingerprint, &commercialFingerprint, &costPlanCode,
		&offeringState, &offeringStateVersion, &offeringReason,
		&priority, &weight, &modelCode,
	)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	expectedEntitlement := sha256.Sum256([]byte(strings.Join([]string{
		strconv.FormatInt(channelID, 10), ability.VendorModel, transportProtocol, "none", "",
	}, "\x00")))
	return targetReleaseID == releaseID && targetChannelID == channelID && transportChannelID == channelID && targetPoolID == poolID &&
		productCode == fmt.Sprintf("legacy-product-%d", ability.ID) && vendorModel == ability.VendorModel &&
		constraintsSource == "legacy" && constraintsCount == 1 && constraintsVersion == 1 &&
		legacyV1TransportCodeMatches(transportCode, transportProtocol, ability.ChannelID, channelID) &&
		taskScope == "none" && cancelMode == "none" && sourceURLPolicy == "fixed" &&
		scopeKind == "none" && scopeKey == "" && executionFingerprint == revision &&
		!compatibilityFingerprint.Valid && transportTimeout == 30000 &&
		entitlementFingerprint == hex.EncodeToString(expectedEntitlement[:]) && commercialFingerprint == revision &&
		costPlanCode == "legacy-unpriced" && offeringState == "active" && offeringStateVersion == 1 &&
		(offeringReason == "legacy_import" || offeringReason == "legacy_import_backfill") &&
		priority == maxZero(ability.Priority) && weight == 1 &&
		modelCode == ability.ModelName, nil
}

func distinctLegacyModels(abilities []legacyAbility) int {
	models := make(map[string]struct{}, len(abilities))
	for _, ability := range abilities {
		models[ability.ModelName] = struct{}{}
	}
	return len(models)
}

func verifyLegacyDraftCardinality(ctx context.Context, tx *sql.Tx, releaseID int64, channels, credentials, abilities, models int) (string, error) {
	checks := []struct {
		query    string
		args     []any
		expected int64
		label    string
	}{
		{`SELECT COUNT(*) FROM gateway_channels`, nil, int64(channels), "channels"},
		{`SELECT COUNT(*) FROM gw_credential_pools`, nil, int64(channels), "credential pools"},
		{`SELECT COUNT(*) FROM gw_credential_secret_identities`, nil, int64(credentials), "credential identities"},
		{`SELECT COUNT(*) FROM gw_credentials`, nil, int64(credentials), "credentials"},
		{`SELECT COUNT(*) FROM gw_credential_versions`, nil, int64(credentials), "credential versions"},
		{`SELECT COUNT(*) FROM gw_credential_purpose_grants`, nil, int64(credentials), "credential grants"},
		{`SELECT COUNT(*) FROM gw_models`, nil, int64(models), "models"},
		{`SELECT COUNT(*) FROM gw_model_names`, nil, int64(models), "model names"},
		{`SELECT COUNT(*) FROM gw_catalog_models WHERE release_id=?`, []any{releaseID}, int64(models), "catalog models"},
		{`SELECT COUNT(*) FROM gw_catalog_model_names WHERE release_id=?`, []any{releaseID}, int64(models), "catalog model names"},
		{`SELECT COUNT(*) FROM gw_model_operations WHERE release_id=?`, []any{releaseID}, int64(models), "model operations"},
		{`SELECT COUNT(*) FROM gw_skus WHERE release_id=?`, []any{releaseID}, int64(models), "SKUs"},
		{`SELECT COUNT(*) FROM gw_channel_transports WHERE release_id=?`, []any{releaseID}, int64(channels), "channel transports"},
		{`SELECT COUNT(*) FROM gw_products WHERE release_id=?`, []any{releaseID}, int64(abilities), "products"},
		{`SELECT COUNT(*) FROM gw_product_transports WHERE release_id=?`, []any{releaseID}, int64(abilities), "product transports"},
		{`SELECT COUNT(*) FROM gw_offerings WHERE release_id=?`, []any{releaseID}, int64(abilities), "offerings"},
		{`SELECT COUNT(*) FROM gw_routes WHERE release_id=?`, []any{releaseID}, int64(abilities), "routes"},
		{`SELECT COUNT(*) FROM gw_offering_runtime_state WHERE release_id=?`, []any{releaseID}, int64(abilities), "offering states"},
		{`SELECT COUNT(*) FROM encrypted_blob_key_wraps w JOIN gw_credential_versions v ON v.encrypted_blob_id=w.encrypted_blob_id`, nil, int64(credentials), "credential key wraps"},
	}
	for _, check := range checks {
		var count int64
		if err := tx.QueryRowContext(ctx, check.query, check.args...).Scan(&count); err != nil {
			return "", err
		}
		if count != check.expected {
			return "the V1 draft " + check.label + " cardinality does not match its source proof", nil
		}
	}
	for _, table := range []string{
		"gw_transport_allowed_hosts", "gw_product_transport_actions", "gw_sell_rates", "gw_cost_plans", "gw_cost_rates",
		"gw_offering_state_events", "gw_catalog_release_sources", "gw_catalog_imports", "gw_catalog_readiness",
		"gw_catalog_release_state_events", "gw_runtime_requirements", "gw_credential_fingerprints",
		"gw_credential_pool_state_events", "gw_credential_state_events", "gw_credential_version_events",
		"gw_credential_version_state_events", "gw_credential_purpose_grant_events", "gw_credential_purpose_grant_state_events",
		"gw_credential_secret_identity_events", "gw_credential_validation_events", "gw_credential_entitlement_state",
	} {
		var count int64
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM `"+table+"`").Scan(&count); err != nil {
			return "", err
		}
		if count != 0 {
			return "the V1 draft has additional " + strings.TrimPrefix(table, "gw_") + " facts", nil
		}
	}
	return "", nil
}

func deleteLegacyGatewayDraft(ctx context.Context, tx *sql.Tx, draft *legacyDraftReplacement) error {
	for _, table := range []string{
		"gw_cost_rates", "gw_cost_plans", "gw_sell_rates", "gw_routes", "gw_offering_state_events", "gw_offering_runtime_state",
		"gw_product_transport_actions", "gw_offerings", "gw_product_transports", "gw_products", "gw_transport_allowed_hosts",
		"gw_channel_transports", "gw_skus", "gw_model_operations", "gw_catalog_model_names", "gw_catalog_models",
		"gw_catalog_release_state_events", "gw_catalog_imports", "gw_catalog_release_sources", "gw_catalog_readiness", "gw_runtime_requirements",
	} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM `"+table+"` WHERE release_id=?", draft.releaseID); err != nil {
			return fmt.Errorf("delete %s: %w", table, err)
		}
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM gw_catalog_releases WHERE id=? AND status='draft' AND config_version=1`, draft.releaseID)
	if err != nil {
		return err
	}
	if rows, err := result.RowsAffected(); err != nil {
		return err
	} else if rows != 1 {
		return fmt.Errorf("delete V1 catalog release affected %d rows", rows)
	}

	for _, table := range []string{
		"gw_credential_purpose_grant_state_events", "gw_credential_purpose_grant_events",
	} {
		if _, err := tx.ExecContext(ctx, "DELETE e FROM `"+table+"` e JOIN gw_credential_purpose_grants g ON g.id=e."+legacyGrantEventColumn(table)+" JOIN gw_credentials c ON c.id=g.credential_id"); err != nil {
			return fmt.Errorf("delete %s: %w", table, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM gw_credential_purpose_grants`); err != nil {
		return err
	}
	for _, table := range []string{"gw_credential_version_state_events", "gw_credential_version_events"} {
		if _, err := tx.ExecContext(ctx, "DELETE e FROM `"+table+"` e JOIN gw_credential_versions v ON v.id=e.credential_version_id"); err != nil {
			return fmt.Errorf("delete %s: %w", table, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE gw_credentials SET current_version_id=NULL`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM gw_credential_versions`); err != nil {
		return err
	}
	for _, table := range []string{"gw_credential_state_events", "gw_credential_fingerprints"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM `"+table+"`"); err != nil {
			return fmt.Errorf("delete %s: %w", table, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM gw_credentials`); err != nil {
		return err
	}
	for _, table := range []string{"gw_credential_secret_identity_events", "gw_credential_secret_identities", "gw_credential_pool_state_events", "gw_credential_pools"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM `"+table+"`"); err != nil {
			return fmt.Errorf("delete %s: %w", table, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM gateway_channels`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM gw_model_names`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM gw_models`); err != nil {
		return err
	}
	if len(draft.blobIDs) > 0 {
		placeholders := strings.TrimRight(strings.Repeat("?,", len(draft.blobIDs)), ",")
		args := make([]any, len(draft.blobIDs))
		for index, id := range draft.blobIDs {
			args[index] = id
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM encrypted_blob_key_wraps WHERE encrypted_blob_id IN (`+placeholders+`)`, args...); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM encrypted_blobs WHERE id IN (`+placeholders+`)`, args...); err != nil {
			return err
		}
	}
	return nil
}

func legacyGrantEventColumn(table string) string {
	if table == "gw_credential_purpose_grant_events" {
		return "purpose_grant_id"
	}
	return "grant_id"
}

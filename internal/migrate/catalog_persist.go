package migrate

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mirainya/Prism/internal/gateway/adapter"
	"github.com/mirainya/Prism/internal/gateway/security"
)

type catalogPersistedIDs struct {
	channels          map[string]int64
	pools             map[string]int64
	credentials       map[string]int64
	models            map[string]int64
	catalogModels     map[string]int64
	modelOperations   map[string]int64
	skus              map[string]int64
	transports        map[string]int64
	products          map[string]int64
	productTransports map[string]int64
	offerings         map[string]int64
	routes            map[string]int64
}

func newCatalogPersistedIDs() *catalogPersistedIDs {
	return &catalogPersistedIDs{
		channels: make(map[string]int64), pools: make(map[string]int64), credentials: make(map[string]int64),
		models: make(map[string]int64), catalogModels: make(map[string]int64), modelOperations: make(map[string]int64),
		skus: make(map[string]int64), transports: make(map[string]int64), products: make(map[string]int64),
		productTransports: make(map[string]int64), offerings: make(map[string]int64), routes: make(map[string]int64),
	}
}

func (m *catalogImporter) persist() error {
	if m == nil || m.tx == nil || m.plan == nil || m.runID <= 0 || len(m.snapshot.HMAC) != 64 {
		return fmt.Errorf("invalid catalog persistence state")
	}
	ids := newCatalogPersistedIDs()
	keyringID, keyVersion, err := ensureKeyring(m.ctx, m.tx, m.now)
	if err != nil {
		return fmt.Errorf("initialize credential keyring: %w", err)
	}
	if err := m.persistCatalogChannels(ids); err != nil {
		return err
	}
	if err := m.persistCatalogCredentials(ids, keyringID, keyVersion); err != nil {
		return err
	}
	if err := m.persistCatalogModelIdentities(ids); err != nil {
		return err
	}
	releaseID, err := m.persistCatalogRelease()
	if err != nil {
		return err
	}
	if err := m.persistCatalogModels(ids, releaseID); err != nil {
		return err
	}
	if err := m.persistCatalogSKUs(ids, releaseID); err != nil {
		return err
	}
	if err := m.persistCatalogProducts(ids, releaseID); err != nil {
		return err
	}
	if err := m.persistCatalogMappings(ids, releaseID); err != nil {
		return err
	}
	m.report.ReleaseID = releaseID
	m.report.Channels = int64(len(ids.channels))
	m.report.CredentialPools = int64(len(ids.pools))
	m.report.Credentials = int64(len(ids.credentials))
	m.report.Models = int64(len(ids.models))
	m.report.Operations = int64(len(ids.modelOperations))
	m.report.SKUs = int64(len(ids.skus))
	m.report.Products = int64(len(ids.products))
	m.report.Transports = int64(len(ids.transports))
	m.report.Offerings = int64(len(ids.offerings))
	m.report.Routes = int64(len(ids.routes))
	return nil
}

func (m *catalogImporter) persistCatalogChannels(ids *catalogPersistedIDs) error {
	for _, channel := range m.plan.channels {
		id, err := insertID(m.ctx, m.tx, `INSERT INTO gateway_channels(channel_code,display_name,status,created_at) VALUES (?,?,?,?)`, channel.Code, channel.Name, channel.Status, m.now)
		if err != nil {
			return fmt.Errorf("insert channel %s: %w", channel.Code, err)
		}
		ids.channels[channel.Key] = id
	}
	return nil
}

func (m *catalogImporter) persistCatalogCredentials(ids *catalogPersistedIDs, keyringID, keyVersion int64) error {
	for _, credential := range m.plan.credentials {
		channelID := ids.channels[credential.ChannelKey]
		if channelID <= 0 {
			return fmt.Errorf("credential %s has no persisted channel", credential.Code)
		}
		poolID, err := insertID(m.ctx, m.tx, `INSERT INTO gw_credential_pools(channel_id,pool_code,display_name,status,config_version,request_limit,task_limit,created_at,updated_at) VALUES (?,?,?,?,1,?,?,?,?)`, channelID, credential.PoolCode, credential.Name, credential.Status, credential.RequestLimit, credential.TaskLimit, m.now, m.now)
		if err != nil {
			return fmt.Errorf("insert credential pool %s: %w", credential.PoolCode, err)
		}
		if _, err = m.tx.ExecContext(m.ctx, `INSERT INTO gw_credential_pool_state_events(credential_pool_id,state_version,old_state,new_state,reason_code,created_at) VALUES (?,1,NULL,?,'legacy_catalog_import',?)`, poolID, credential.Status, m.now); err != nil {
			return fmt.Errorf("insert credential pool state %s: %w", credential.PoolCode, err)
		}

		secretHMAC := security.HMACSHA256(m.options.HMACKey, credential.Secret)
		secretHex := hex.EncodeToString(secretHMAC[:])
		identityID, err := insertID(m.ctx, m.tx, `INSERT INTO gw_credential_secret_identities(channel_id,secret_hmac,hmac_key_version,status,created_at,updated_at) VALUES (?,?,1,'active',?,?)`, channelID, secretHex, m.now, m.now)
		if err != nil {
			return fmt.Errorf("insert credential identity %s: %w", credential.Code, err)
		}
		if _, err = m.tx.ExecContext(m.ctx, `INSERT INTO gw_credential_fingerprints(secret_identity_id,hmac_key_version,secret_hmac,created_at) VALUES (?,1,?,?)`, identityID, secretHex, m.now); err != nil {
			return fmt.Errorf("insert credential fingerprint %s: %w", credential.Code, err)
		}
		credentialID, err := insertID(m.ctx, m.tx, `INSERT INTO gw_credentials(channel_id,credential_pool_id,secret_identity_id,credential_code,status,config_version,request_limit,task_limit,weight,created_at,updated_at) VALUES (?,?,?,?,?,1,?,?,?,?,?)`, channelID, poolID, identityID, credential.Code, credential.Status, credential.RequestLimit, credential.TaskLimit, credential.Weight, m.now, m.now)
		if err != nil {
			return fmt.Errorf("insert credential %s: %w", credential.Code, err)
		}
		if _, err = m.tx.ExecContext(m.ctx, `INSERT INTO gw_credential_state_events(credential_id,state_version,old_state,new_state,reason_code,created_at) VALUES (?,1,NULL,?,'legacy_catalog_import',?)`, credentialID, credential.Status, m.now); err != nil {
			return fmt.Errorf("insert credential state %s: %w", credential.Code, err)
		}
		blobID, err := insertEncryptedBlob(m.ctx, m.tx, keyringID, keyVersion, credentialID, credential.Secret, m.options.KEK, m.options.HMACKey, m.now)
		if err != nil {
			return fmt.Errorf("encrypt credential %s: %w", credential.Code, err)
		}
		versionID, err := insertID(m.ctx, m.tx, `INSERT INTO gw_credential_versions(channel_id,credential_id,secret_identity_id,version_no,encrypted_blob_id,status,created_at) VALUES (?,?,?,?,?,'active',?)`, channelID, credentialID, identityID, 1, blobID, m.now)
		if err != nil {
			return fmt.Errorf("insert credential version %s: %w", credential.Code, err)
		}
		if _, err = m.tx.ExecContext(m.ctx, `INSERT INTO gw_credential_version_state_events(credential_version_id,state_version,old_state,new_state,reason_code,created_at) VALUES (?,1,NULL,'active','legacy_catalog_import',?)`, versionID, m.now); err != nil {
			return fmt.Errorf("insert credential version state %s: %w", credential.Code, err)
		}
		if _, err = m.tx.ExecContext(m.ctx, `UPDATE gw_credentials SET current_version_id=? WHERE id=?`, versionID, credentialID); err != nil {
			return fmt.Errorf("activate credential version %s: %w", credential.Code, err)
		}
		grantID, err := insertID(m.ctx, m.tx, `INSERT INTO gw_credential_purpose_grants(credential_id,purpose,grant_seq,status,state_version,created_at) VALUES (?,'execution',1,'active',1,?)`, credentialID, m.now)
		if err != nil {
			return fmt.Errorf("insert execution grant %s: %w", credential.Code, err)
		}
		if _, err = m.tx.ExecContext(m.ctx, `INSERT INTO gw_credential_purpose_grant_state_events(grant_id,state_version,old_state,new_state,reason_code,created_at) VALUES (?,1,NULL,'active','legacy_catalog_import',?)`, grantID, m.now); err != nil {
			return fmt.Errorf("insert execution grant state %s: %w", credential.Code, err)
		}
		ids.pools[credential.Key] = poolID
		ids.credentials[credential.Key] = credentialID
	}
	return nil
}

func (m *catalogImporter) persistCatalogModelIdentities(ids *catalogPersistedIDs) error {
	for _, model := range m.plan.models {
		modelID, err := insertID(m.ctx, m.tx, `INSERT INTO gw_models(model_code,created_at) VALUES (?,?)`, model.Code, m.now)
		if err != nil {
			return fmt.Errorf("insert model %s: %w", model.Code, err)
		}
		for _, name := range model.Names {
			if _, err = insertID(m.ctx, m.tx, `INSERT INTO gw_model_names(model_id,api_name,is_primary,created_at) VALUES (?,?,?,?)`, modelID, name, name == model.Code, m.now); err != nil {
				return fmt.Errorf("insert model name %s: %w", name, err)
			}
		}
		ids.models[model.Key] = modelID
	}
	return nil
}

func (m *catalogImporter) persistCatalogRelease() (int64, error) {
	var releaseNo int64
	if err := m.tx.QueryRowContext(m.ctx, `SELECT COALESCE(MAX(release_no),0)+1 FROM gw_catalog_releases`).Scan(&releaseNo); err != nil {
		return 0, fmt.Errorf("allocate catalog release number: %w", err)
	}
	semanticDigest := catalogSemanticDigest()
	releaseID, err := insertID(m.ctx, m.tx, `INSERT INTO gw_catalog_releases(release_no,status,config_version,serialization_version,content_hash_algorithm,content_hash,semantic_version,semantic_digest,created_at,updated_at) VALUES (?,'draft',1,1,'sha256',?,'legacy-catalog-import-1',?,?,?)`, releaseNo, m.snapshot.HMAC, semanticDigest, m.now, m.now)
	if err != nil {
		return 0, fmt.Errorf("insert catalog release: %w", err)
	}
	if _, err = m.tx.ExecContext(m.ctx, `INSERT INTO gw_catalog_release_state_events(release_id,old_state,new_state,reason_code,created_at) VALUES (?,NULL,'draft','legacy_catalog_import',?)`, releaseID, m.now); err != nil {
		return 0, fmt.Errorf("insert catalog release state: %w", err)
	}
	return releaseID, nil
}

func (m *catalogImporter) persistCatalogModels(ids *catalogPersistedIDs, releaseID int64) error {
	for _, model := range m.plan.models {
		modelID := ids.models[model.Key]
		tags, err := catalogMarshalStrings(model.Tags)
		if err != nil {
			return err
		}
		catalogModelID, err := insertID(m.ctx, m.tx, `INSERT INTO gw_catalog_models(release_id,model_id,display_name,description,capability_tags,sort_order,visibility,created_at) VALUES (?,?,?,?,?,?,?,?)`, releaseID, modelID, model.DisplayName, model.Description, tags, model.Sort, model.Visibility, m.now)
		if err != nil {
			return fmt.Errorf("insert catalog model %s: %w", model.Code, err)
		}
		for _, name := range model.Names {
			var nameID int64
			if err := m.tx.QueryRowContext(m.ctx, `SELECT id FROM gw_model_names WHERE model_id=? AND api_name=?`, modelID, name).Scan(&nameID); err != nil {
				return fmt.Errorf("resolve catalog model name %s: %w", name, err)
			}
			if _, err := m.tx.ExecContext(m.ctx, `INSERT INTO gw_catalog_model_names(release_id,catalog_model_id,model_id,model_name_id,is_primary,created_at) VALUES (?,?,?,?,?,?)`, releaseID, catalogModelID, modelID, nameID, name == model.Code, m.now); err != nil {
				return fmt.Errorf("insert catalog model name %s: %w", name, err)
			}
		}
		ids.catalogModels[model.Key] = catalogModelID
	}
	return nil
}

func (m *catalogImporter) persistCatalogSKUs(ids *catalogPersistedIDs, releaseID int64) error {
	contractIDs := make(map[string]int64)
	semanticDigest := catalogSemanticDigest()
	for _, sku := range m.plan.skus {
		contractID := contractIDs[sku.Operation]
		if contractID == 0 {
			var err error
			contractID, err = m.ensureCatalogOperationContract(sku)
			if err != nil {
				return err
			}
			contractIDs[sku.Operation] = contractID
		}
		operationKey := sku.ModelKey + "\x00" + sku.Operation
		modelOperationID := ids.modelOperations[operationKey]
		if modelOperationID == 0 {
			var err error
			modelOperationID, err = insertID(m.ctx, m.tx, `INSERT INTO gw_model_operations(release_id,catalog_model_id,operation_contract_id,normalization_version,semantic_digest,created_at) VALUES (?,?,?,?,?,?)`, releaseID, ids.catalogModels[sku.ModelKey], contractID, 1, semanticDigest, m.now)
			if err != nil {
				return fmt.Errorf("insert model operation %s: %w", sku.Operation, err)
			}
			ids.modelOperations[operationKey] = modelOperationID
		}
		tiers, err := catalogMarshalStrings(sku.ServiceTiers)
		if err != nil {
			return err
		}
		skuID, err := insertID(m.ctx, m.tx, `INSERT INTO gw_skus(release_id,model_operation_id,sku_code,delivery_mode,max_results,idempotency_mode,service_tiers,created_at) VALUES (?,?,?,?,?,?,?,?)`, releaseID, modelOperationID, sku.Code, sku.DeliveryMode, sku.MaxResults, sku.IdempotencyMode, tiers, m.now)
		if err != nil {
			return fmt.Errorf("insert SKU %s: %w", sku.Code, err)
		}
		if _, err := m.tx.ExecContext(m.ctx, `INSERT INTO gw_sku_downstream_paths(release_id,sku_id,path,created_at) VALUES (?,?,?,?)`, releaseID, skuID, sku.Route, m.now); err != nil {
			return fmt.Errorf("insert downstream path for SKU %s: %w", sku.Code, err)
		}
		ids.skus[sku.Key] = skuID
	}
	return nil
}

func (m *catalogImporter) ensureCatalogOperationContract(sku *catalogPlanSKU) (int64, error) {
	var id int64
	var status string
	err := m.tx.QueryRowContext(m.ctx, `SELECT id,status FROM gw_operation_contracts WHERE operation_code=? AND contract_version=1`, sku.Operation).Scan(&id, &status)
	if err == sql.ErrNoRows {
		id, err = insertID(m.ctx, m.tx, `INSERT INTO gw_operation_contracts(operation_code,contract_version,status,created_at) VALUES (?,1,'active',?)`, sku.Operation, m.now)
	} else if err == nil && status != "active" {
		return 0, fmt.Errorf("operation contract %s is not active", sku.Operation)
	}
	if err != nil {
		return 0, fmt.Errorf("ensure operation contract %s: %w", sku.Operation, err)
	}
	var routeContractID int64
	err = m.tx.QueryRowContext(m.ctx, `SELECT operation_contract_id FROM gw_operation_routes WHERE http_method=? AND route_template=?`, sku.Method, sku.Route).Scan(&routeContractID)
	if err == sql.ErrNoRows {
		_, err = m.tx.ExecContext(m.ctx, `INSERT INTO gw_operation_routes(operation_contract_id,http_method,route_template,created_at) VALUES (?,?,?,?)`, id, sku.Method, sku.Route, m.now)
	} else if err == nil && routeContractID != id {
		return 0, fmt.Errorf("operation route %s belongs to another contract", sku.Route)
	}
	if err != nil {
		return 0, fmt.Errorf("ensure operation route %s: %w", sku.Route, err)
	}
	return id, nil
}

func (m *catalogImporter) persistCatalogProducts(ids *catalogPersistedIDs, releaseID int64) error {
	adapterIDs := make(map[string]int64)
	for _, product := range m.plan.products {
		channelID, poolID := ids.channels[product.ChannelKey], ids.pools[product.CredentialKey]
		if channelID <= 0 || poolID <= 0 {
			return fmt.Errorf("product %s has unresolved channel or credential pool", product.Code)
		}
		adapterKey := product.Adapter.Code + "\x00" + strconv.FormatUint(uint64(product.Adapter.Version), 10)
		adapterID := adapterIDs[adapterKey]
		if adapterID == 0 {
			var err error
			adapterID, err = m.ensureCatalogAdapter(product.Adapter)
			if err != nil {
				return err
			}
			adapterIDs[adapterKey] = adapterID
		}
		executionDigest, err := catalogPlanExecutionDigest(product)
		if err != nil {
			return fmt.Errorf("hash product %s: %w", product.Code, err)
		}
		compatibilityDigest := sha256.Sum256([]byte(strings.Join([]string{product.Protocol, product.UpstreamScopeKind, product.UpstreamScopeKey}, "\x00")))
		compatibility := hex.EncodeToString(compatibilityDigest[:])
		transportID, err := insertID(m.ctx, m.tx, `INSERT INTO gw_channel_transports(release_id,channel_id,adapter_implementation_id,transport_code,base_url,protocol,request_method,request_path,auth_scheme,execution_fingerprint,state_compatibility_fingerprint,timeout_ms,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, releaseID, channelID, adapterID, product.TransportCode, product.BaseURL, product.Protocol, product.Method, product.Path, product.AuthScheme, executionDigest, compatibility, product.TimeoutMS, m.now)
		if err != nil {
			return fmt.Errorf("insert transport %s: %w", product.TransportCode, err)
		}
		hosts, err := catalogPlanAllowedHosts(product)
		if err != nil {
			return fmt.Errorf("resolve allowed hosts for %s: %w", product.Code, err)
		}
		for _, host := range hosts {
			if _, err := m.tx.ExecContext(m.ctx, `INSERT INTO gw_transport_allowed_hosts(release_id,channel_transport_id,protocol,host_pattern,port,created_at) VALUES (?,?,?,?,?,?)`, releaseID, transportID, host.Protocol, host.Host, host.Port, m.now); err != nil {
				return fmt.Errorf("insert allowed host for %s: %w", product.Code, err)
			}
		}
		constraints := product.Constraints
		if len(constraints) == 0 {
			constraints = json.RawMessage(`{}`)
		}
		productID, err := insertID(m.ctx, m.tx, `INSERT INTO gw_products(release_id,channel_id,product_code,vendor_model,capability_constraints,constraints_schema_version,created_at) VALUES (?,?,?,?,?,1,?)`, releaseID, channelID, product.Code, product.VendorModel, constraints, m.now)
		if err != nil {
			return fmt.Errorf("insert product %s: %w", product.Code, err)
		}
		productTransportID, err := insertID(m.ctx, m.tx, `INSERT INTO gw_product_transports(release_id,product_id,channel_transport_id,task_scope,cancel_mode,source_url_policy,upstream_scope_kind,upstream_scope_key,execution_fingerprint,state_compatibility_fingerprint,timeout_ms,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, releaseID, productID, transportID, product.TaskScope, product.CancelMode, product.SourceURLPolicy, product.UpstreamScopeKind, product.UpstreamScopeKey, executionDigest, compatibility, product.TimeoutMS, m.now)
		if err != nil {
			return fmt.Errorf("insert product transport %s: %w", product.Code, err)
		}
		for _, action := range product.Actions {
			if _, err := m.tx.ExecContext(m.ctx, `INSERT INTO gw_product_transport_actions(release_id,product_transport_id,action_code,allowed_source_state,idempotency_mode,request_schema_version,response_schema_version,created_at) VALUES (?,?,?,?,?,1,1,?)`, releaseID, productTransportID, action.Code, action.AllowedSourceState, action.IdempotencyMode, m.now); err != nil {
				return fmt.Errorf("insert product action %s/%s: %w", product.Code, action.Code, err)
			}
		}
		entitlementDigest := sha256.Sum256([]byte(strings.Join([]string{strconv.FormatInt(channelID, 10), product.VendorModel, product.Protocol, product.UpstreamScopeKind, product.UpstreamScopeKey}, "\x00")))
		commercialDigest := sha256.Sum256([]byte(strings.Join([]string{product.Code, strconv.FormatInt(poolID, 10), product.CostPlanCode, executionDigest}, "\x00")))
		offeringID, err := insertID(m.ctx, m.tx, `INSERT INTO gw_offerings(release_id,product_transport_id,credential_pool_id,entitlement_fingerprint,commercial_fingerprint,cost_plan_code,created_at) VALUES (?,?,?,?,?,?,?)`, releaseID, productTransportID, poolID, hex.EncodeToString(entitlementDigest[:]), hex.EncodeToString(commercialDigest[:]), product.CostPlanCode, m.now)
		if err != nil {
			return fmt.Errorf("insert offering %s: %w", product.Code, err)
		}
		state := "disabled"
		if product.Active {
			state = "active"
		}
		if _, err := m.tx.ExecContext(m.ctx, `INSERT INTO gw_offering_runtime_state(release_id,offering_id,state,state_version,reason_code,updated_at) VALUES (?,?,?,1,'legacy_catalog_import',?)`, releaseID, offeringID, state, m.now); err != nil {
			return fmt.Errorf("insert offering state %s: %w", product.Code, err)
		}
		if _, err := m.tx.ExecContext(m.ctx, `INSERT INTO gw_offering_state_events(release_id,offering_id,state_version,old_state,new_state,reason_code,created_at) VALUES (?,?,1,NULL,?,'legacy_catalog_import',?)`, releaseID, offeringID, state, m.now); err != nil {
			return fmt.Errorf("insert offering state event %s: %w", product.Code, err)
		}
		if _, err := m.tx.ExecContext(m.ctx, `INSERT INTO gw_cost_plans(release_id,offering_id,plan_code,created_at) VALUES (?,?,?,?)`, releaseID, offeringID, product.CostPlanCode, m.now); err != nil {
			return fmt.Errorf("insert empty cost plan %s: %w", product.CostPlanCode, err)
		}
		for _, skuKey := range product.SKUKeys {
			skuID := ids.skus[skuKey]
			if skuID <= 0 {
				return fmt.Errorf("product %s references an unknown SKU", product.Code)
			}
			routeID, err := insertID(m.ctx, m.tx, `INSERT INTO gw_routes(release_id,sku_id,offering_id,priority,weight,created_at) VALUES (?,?,?,?,?,?)`, releaseID, skuID, offeringID, product.Priority, product.Weight, m.now)
			if err != nil {
				return fmt.Errorf("insert route for %s: %w", product.Code, err)
			}
			ids.routes[product.Key+"\x00"+skuKey] = routeID
			if len(product.SKUKeys) == 1 {
				ids.routes[product.Key] = routeID
			}
		}
		ids.transports[product.Key] = transportID
		ids.products[product.Key] = productID
		ids.productTransports[product.Key] = productTransportID
		ids.offerings[product.Key] = offeringID
	}
	return nil
}

func (m *catalogImporter) ensureCatalogAdapter(descriptor adapter.Descriptor) (int64, error) {
	var id int64
	var digest, minimum string
	err := m.tx.QueryRowContext(m.ctx, `SELECT id,implementation_digest,minimum_semantic_version FROM gw_adapter_implementations WHERE adapter_code=? AND contract_version=?`, descriptor.Code, descriptor.Version).Scan(&id, &digest, &minimum)
	if err == sql.ErrNoRows {
		id, err = insertID(m.ctx, m.tx, `INSERT INTO gw_adapter_implementations(adapter_code,contract_version,implementation_digest,minimum_semantic_version,created_at) VALUES (?,?,?,?,?)`, descriptor.Code, descriptor.Version, descriptor.ImplementationDigest, descriptor.MinimumSemanticVersion, m.now)
	} else if err == nil && (digest != descriptor.ImplementationDigest || minimum != descriptor.MinimumSemanticVersion) {
		return 0, fmt.Errorf("adapter implementation %s@%d conflicts with the running binary", descriptor.Code, descriptor.Version)
	}
	if err != nil {
		return 0, fmt.Errorf("ensure adapter implementation %s@%d: %w", descriptor.Code, descriptor.Version, err)
	}
	return id, nil
}

func (m *catalogImporter) persistCatalogMappings(ids *catalogPersistedIDs, releaseID int64) error {
	snapshotRow := runtimeSourceRow{Table: catalogSnapshotMappingTable, PrimaryKey: m.snapshot.HMAC, Revision: m.snapshot.HMAC}
	if err := m.persistCatalogMapping(snapshotRow, "catalog_release", releaseID, "legacy-catalog-import-1"); err != nil {
		return err
	}
	for _, mapping := range m.plan.mappings {
		targetID := ids.mappingTarget(mapping.TargetType, mapping.TargetKey)
		if targetID <= 0 {
			return fmt.Errorf("mapping %s/%s has no persisted %s target", mapping.Row.Table, mapping.Row.PrimaryKey, mapping.TargetType)
		}
		if err := m.persistCatalogMapping(mapping.Row, mapping.TargetType, targetID, mapping.TargetDiscriminator); err != nil {
			return err
		}
	}
	return nil
}

func (m *catalogImporter) persistCatalogMapping(row runtimeSourceRow, targetType string, targetID int64, discriminator string) error {
	mapID, err := insertID(m.ctx, m.tx, `INSERT INTO gw_migration_object_map(run_id,source_table,source_pk,target_type,target_id,target_discriminator,created_at) VALUES (?,?,?,?,?,?,?)`, m.runID, row.Table, row.PrimaryKey, targetType, targetID, discriminator, m.now)
	if err != nil {
		return fmt.Errorf("insert object mapping %s/%s: %w", row.Table, row.PrimaryKey, err)
	}
	if _, err := m.tx.ExecContext(m.ctx, `INSERT INTO gw_migration_source_revisions(object_map_id,source_hmac,observed_at) VALUES (?,?,?)`, mapID, row.Revision, m.now); err != nil {
		return fmt.Errorf("insert source revision %s/%s: %w", row.Table, row.PrimaryKey, err)
	}
	if err := recordMappingProof(m.ctx, m.tx, m.runID, mapID, row.Revision, m.now); err != nil {
		return fmt.Errorf("record mapping proof %s/%s: %w", row.Table, row.PrimaryKey, err)
	}
	return nil
}

func (ids *catalogPersistedIDs) mappingTarget(targetType, key string) int64 {
	switch targetType {
	case "gateway_channel":
		return ids.channels[key]
	case "credential_pool":
		return ids.pools[key]
	case "credential":
		return ids.credentials[key]
	case "model":
		return ids.models[key]
	case "channel_transport":
		return ids.transports[key]
	case "product":
		return ids.products[key]
	case "offering":
		return ids.offerings[key]
	case "route":
		return ids.routes[key]
	default:
		return 0
	}
}

func catalogPlanExecutionDigest(product *catalogPlanProduct) (string, error) {
	constraints := product.Constraints
	if len(constraints) == 0 {
		constraints = json.RawMessage(`{}`)
	}
	canonicalAdapter := struct {
		Code                   string `json:"code"`
		Version                uint32 `json:"version"`
		Protocol               string `json:"protocol"`
		MinimumSemanticVersion string `json:"minimum_semantic_version"`
		ImplementationDigest   string `json:"implementation_digest"`
	}{
		Code: product.Adapter.Code, Version: product.Adapter.Version, Protocol: product.Adapter.Protocol,
		MinimumSemanticVersion: product.Adapter.MinimumSemanticVersion, ImplementationDigest: product.Adapter.ImplementationDigest,
	}
	canonical, err := json.Marshal(map[string]any{
		"adapter": canonicalAdapter, "base_url": product.BaseURL, "protocol": product.Protocol,
		"request_method": product.Method, "request_path": product.Path, "auth_scheme": product.AuthScheme,
		"task_scope": product.TaskScope, "cancel_mode": product.CancelMode, "source_url_policy": product.SourceURLPolicy,
		"constraints": json.RawMessage(constraints),
	})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}

func catalogPlanAllowedHosts(product *catalogPlanProduct) ([]catalogAllowedHost, error) {
	base, err := catalogBaseHost(product.BaseURL)
	if err != nil {
		return nil, err
	}
	values := append([]catalogAllowedHost{base}, product.AllowedHosts...)
	seen := make(map[string]bool, len(values))
	out := make([]catalogAllowedHost, 0, len(values))
	for _, value := range values {
		value.Protocol = strings.ToLower(strings.TrimSpace(value.Protocol))
		value.Host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value.Host), "."))
		if value.Port == 0 || value.Protocol != "http" && value.Protocol != "https" || !catalogValidRemoteHost(value.Host) {
			return nil, fmt.Errorf("invalid allowed host")
		}
		key := value.Protocol + "\x00" + value.Host + "\x00" + strconv.FormatUint(uint64(value.Port), 10)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	if len(out) > 32 {
		return nil, fmt.Errorf("too many allowed hosts")
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Protocol != out[j].Protocol {
			return out[i].Protocol < out[j].Protocol
		}
		if out[i].Host != out[j].Host {
			return out[i].Host < out[j].Host
		}
		return out[i].Port < out[j].Port
	})
	return out, nil
}

func catalogMarshalStrings(values []string) ([]byte, error) {
	if values == nil {
		values = []string{}
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("encode catalog string list: %w", err)
	}
	return encoded, nil
}

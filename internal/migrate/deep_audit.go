package migrate

import (
	"context"
	"database/sql"
	"fmt"
)

// DeepAuditReport is the read-only evidence used immediately before the
// one-way legacy cleanup. It deliberately reports blockers instead of
// treating missing optional migration metadata as success.
type DeepAuditReport struct {
	MissingTargetTables     []string
	MissingAuditTables      []string
	LegacyTablesPresent     []string
	UnverifiedLegacyHistory []string
	UnmappedLegacyChannels  int64
	UnmappedLegacyKeys      int64
	UnmappedLegacyAbilities int64
	OpenMigrationIssues     int64
	MigrationRunCount       int64
	SucceededMigrationRuns  int64
	RunningMigrationRuns    int64
}

var deepAuditTargetTables = []string{
	// Catalog and transport.
	"gw_models", "gw_model_names", "gw_operation_contracts", "gw_operation_routes",
	"gw_adapter_implementations", "gateway_channels", "gw_catalog_releases",
	"gw_catalog_models", "gw_catalog_model_names", "gw_model_operations", "gw_skus",
	"gw_channel_transports", "gw_transport_allowed_hosts", "gw_products",
	"gw_product_transports", "gw_offerings", "gw_routes", "gw_offering_runtime_state",
	// Credentials and cryptography.
	"crypto_keyring_state", "crypto_key_versions", "encrypted_blobs", "encrypted_blob_key_wraps",
	"crypto_key_readiness", "gw_credential_pools", "gw_credential_secret_identities",
	"gw_credentials", "gw_credential_versions", "gw_credential_purpose_grants",
	"gw_credential_fingerprints", "gw_credential_pool_state_events", "gw_credential_state_events",
	"gw_credential_version_events", "gw_credential_purpose_grant_events",
	"gw_credential_secret_identity_events", "gw_credential_validation_events",
	"gw_credential_entitlement_state", "gw_credential_purpose_grant_state_events",
	"gw_credential_version_state_events",
	// Calls, resources, asynchronous execution and request facts.
	"gw_api_calls", "gw_api_call_payloads", "gw_api_call_idempotencies",
	"gw_api_call_idempotency_keys", "gw_api_resources", "gw_api_call_attempts",
	"gw_async_executions", "gw_upstream_task_identities", "gw_upstream_task_id_aliases",
	"gw_callback_binding_token_aliases", "gw_async_outbox", "gw_channel_request_logs",
	"gw_credential_slots", "gw_capability_tasks", "gw_ai_responses", "gw_video_tasks",
	"gw_provider_state_refs", "gw_provider_state_id_aliases",
	// Callbacks, media and delivery.
	"gw_callback_targets", "gw_callback_deliveries", "gw_callback_delivery_attempts",
	"gw_upstream_callback_receipts", "gw_upstream_callback_receipt_aliases",
	"gw_media_assets", "gw_media_asset_refs", "gw_media_asset_state_events", "gw_file_resources",
	"gw_result_deliveries", "gw_result_delivery_sources",
	// Control plane, pricing, routing and runtime health.
	"gw_catalog_sources", "gw_catalog_release_sources", "gw_control_plane_runs",
	"gw_catalog_imports", "gw_catalog_runtime_state", "gw_offering_state_events",
	"gw_deployment_generations", "gw_deployment_members", "gw_catalog_readiness",
	"gw_commercial_validation_events", "gw_commercial_state", "gw_rate_evidence",
	"gw_rate_evidence_review_events", "gw_rate_evidence_review_state", "gw_sell_rates",
	"gw_cost_plans", "gw_cost_rates", "gw_routing_policies", "gw_routing_policy_versions",
	"gw_routing_policy_entries", "gw_state_transition_events", "gw_product_transport_actions",
	"gw_catalog_source_state_events", "gw_execution_health", "gw_execution_health_events",
	"gw_runtime_requirement_guards", "gw_runtime_requirements", "gw_runtime_requirement_refs",
	"gw_catalog_release_state_events", "gw_routing_policy_version_events",
	// Billing and immutable accounting.
	"billing_currency_definitions", "billing_system_state", "token_budget_policies",
	"token_budget_policy_activations", "token_budget_windows", "billing_accounts",
	"billing_reservations", "billing_events", "ledger_accounts", "ledger_transactions",
	"ledger_entries", "billing_posting_rules", "billing_account_state_events",
	"token_budget_adjustment_events", "fx_rate_snapshots", "gw_upstream_cost_evidence",
	"gw_upstream_cost_events", "billing_settlements", "gw_legacy_account_snapshots",
	"gw_legacy_billing_evidence",
	// Public API authentication.
	"token_auth_state_events",
}

var deepAuditLegacyTables = []string{"gw_channels", "gw_channel_keys", "gw_abilities"}
var deepAuditMetadataTables = []string{"gw_migration_runs", "gw_migration_issues", "gw_migration_object_map", "gw_migration_source_revisions", "gw_migration_mapping_proofs"}

// These histories are not handled by ImportLegacyGateway. Their presence must
// block destructive cleanup until their dedicated import and verification exist.
var deepAuditHistoryTables = []string{
	"tasks", "video_tasks", "ai_responses", "api_calls", "api_call_attempts",
	"api_call_payloads", "channel_request_logs", "request_logs", "video_assets", "ai_files",
	"billing_logs", "balance_entries",
}

type runtimeHistoryAuditSpec struct {
	SourceTable, PrimaryKey, TargetType, TargetTable string
	ProjectionTable, ResourceKind                    string
}

var runtimeHistoryAuditSpecs = map[string]runtimeHistoryAuditSpec{
	"api_calls":            {"api_calls", "id", "api_call", "gw_api_calls", "", ""},
	"api_call_attempts":    {"api_call_attempts", "id", "api_call_attempt", "gw_api_call_attempts", "", ""},
	"api_call_payloads":    {"api_call_payloads", "id", "api_call_payload", "gw_api_call_payloads", "", ""},
	"tasks":                {"tasks", "id", "capability_task", "gw_api_resources", "gw_capability_tasks", "capability_task"},
	"video_tasks":          {"video_tasks", "id", "video_task", "gw_api_resources", "gw_video_tasks", "video_task"},
	"ai_responses":         {"ai_responses", "id", "response", "gw_api_resources", "gw_ai_responses", "response"},
	"channel_request_logs": {"channel_request_logs", "id", "request_log", "gw_channel_request_logs", "", ""},
	"request_logs":         {"request_logs", "id", "request_log", "gw_channel_request_logs", "", ""},
	"billing_logs":         {"billing_logs", "id", "legacy_billing_evidence", "gw_legacy_billing_evidence", "", ""},
	"balance_entries":      {"balance_entries", "id", "legacy_billing_evidence", "gw_legacy_billing_evidence", "", ""},
}

func DeepAudit(ctx context.Context, db *sql.DB) (DeepAuditReport, error) {
	if db == nil {
		return DeepAuditReport{}, fmt.Errorf("database is required")
	}
	report := DeepAuditReport{}
	for _, table := range deepAuditTargetTables {
		present, err := tableExists(ctx, db, table)
		if err != nil {
			return DeepAuditReport{}, err
		}
		if !present {
			report.MissingTargetTables = append(report.MissingTargetTables, table)
		}
	}
	for _, table := range deepAuditLegacyTables {
		present, err := tableExists(ctx, db, table)
		if err != nil {
			return DeepAuditReport{}, err
		}
		if present {
			report.LegacyTablesPresent = append(report.LegacyTablesPresent, table)
		}
	}
	for _, table := range deepAuditMetadataTables {
		present, err := tableExists(ctx, db, table)
		if err != nil {
			return DeepAuditReport{}, err
		}
		if !present {
			report.MissingAuditTables = append(report.MissingAuditTables, table)
		}
	}
	for _, table := range deepAuditHistoryTables {
		present, err := tableExists(ctx, db, table)
		if err != nil {
			return DeepAuditReport{}, err
		}
		if present {
			var hasRows bool
			if err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM `"+table+"` LIMIT 1)").Scan(&hasRows); err != nil {
				return DeepAuditReport{}, err
			}
			verified := false
			if hasRows && len(report.MissingAuditTables) == 0 {
				var auditErr error
				switch table {
				case "video_assets":
					if !stringSliceContains(report.MissingTargetTables, "gw_media_assets") {
						verified, auditErr = auditVideoAssetHistory(ctx, db)
					}
				case "ai_files":
					if !stringSliceContains(report.MissingTargetTables, "gw_file_resources") &&
						!stringSliceContains(report.MissingTargetTables, "gw_media_assets") &&
						!stringSliceContains(report.MissingTargetTables, "gw_media_asset_refs") {
						verified, auditErr = auditAIFileHistory(ctx, db)
					}
				default:
					if spec, ok := runtimeHistoryAuditSpecs[table]; ok &&
						!stringSliceContains(report.MissingTargetTables, spec.TargetTable) &&
						(spec.ProjectionTable == "" || !stringSliceContains(report.MissingTargetTables, spec.ProjectionTable)) {
						verified, auditErr = auditRuntimeHistory(ctx, db, spec)
					}
				}
				if auditErr != nil {
					return DeepAuditReport{}, auditErr
				}
			}
			if hasRows && !verified {
				report.UnverifiedLegacyHistory = append(report.UnverifiedLegacyHistory, table)
			}
		}
	}
	if present, err := tableExists(ctx, db, "gw_migration_runs"); err != nil {
		return DeepAuditReport{}, err
	} else if present {
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*), COALESCE(SUM(status='succeeded'),0), COALESCE(SUM(status='running'),0) FROM gw_migration_runs").Scan(&report.MigrationRunCount, &report.SucceededMigrationRuns, &report.RunningMigrationRuns); err != nil {
			return DeepAuditReport{}, err
		}
	}
	if present, err := tableExists(ctx, db, "gw_migration_issues"); err != nil {
		return DeepAuditReport{}, err
	} else if present {
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_migration_issues i
			JOIN gw_migration_runs r ON r.id=i.run_id
			WHERE i.status='open' AND NOT EXISTS (
				SELECT 1 FROM gw_migration_runs newer
				WHERE newer.operation=r.operation AND newer.id>r.id
			)`).Scan(&report.OpenMigrationIssues); err != nil {
			return DeepAuditReport{}, err
		}
	}
	if present, err := tableExists(ctx, db, "gw_migration_object_map"); err != nil {
		return DeepAuditReport{}, err
	} else if present && len(report.MissingAuditTables) == 0 && len(report.MissingTargetTables) == 0 {
		for _, check := range []struct {
			table  string
			target *int64
			query  string
		}{
			{"gw_channels", &report.UnmappedLegacyChannels, "SELECT COUNT(*) FROM gw_channels c WHERE NOT EXISTS (SELECT 1 FROM gw_migration_object_map m JOIN gw_migration_mapping_proofs p ON p.object_map_id=m.id JOIN gateway_channels t ON t.id=m.target_id JOIN gw_migration_runs r ON r.id=p.run_id AND r.status='succeeded' AND NOT EXISTS (SELECT 1 FROM gw_migration_runs newer WHERE newer.operation=r.operation AND newer.id>r.id) WHERE m.source_table='gw_channels' AND m.source_pk COLLATE utf8mb4_unicode_ci=CAST(c.id AS CHAR) COLLATE utf8mb4_unicode_ci AND m.target_type='gateway_channel')"},
			{"gw_channel_keys", &report.UnmappedLegacyKeys, "SELECT COUNT(*) FROM gw_channel_keys k WHERE NOT EXISTS (SELECT 1 FROM gw_migration_object_map m JOIN gw_migration_mapping_proofs p ON p.object_map_id=m.id JOIN gw_credentials t ON t.id=m.target_id JOIN gw_migration_runs r ON r.id=p.run_id AND r.status='succeeded' AND NOT EXISTS (SELECT 1 FROM gw_migration_runs newer WHERE newer.operation=r.operation AND newer.id>r.id) WHERE m.source_table='gw_channel_keys' AND m.source_pk COLLATE utf8mb4_unicode_ci=CAST(k.id AS CHAR) COLLATE utf8mb4_unicode_ci AND m.target_type='credential')"},
			{"gw_abilities", &report.UnmappedLegacyAbilities, "SELECT COUNT(*) FROM gw_abilities a WHERE NOT EXISTS (SELECT 1 FROM gw_migration_object_map m JOIN gw_migration_mapping_proofs p ON p.object_map_id=m.id JOIN gw_products t ON t.id=m.target_id JOIN gw_migration_runs r ON r.id=p.run_id AND r.status='succeeded' AND NOT EXISTS (SELECT 1 FROM gw_migration_runs newer WHERE newer.operation=r.operation AND newer.id>r.id) WHERE m.source_table='gw_abilities' AND m.source_pk COLLATE utf8mb4_unicode_ci=CAST(a.id AS CHAR) COLLATE utf8mb4_unicode_ci AND m.target_type='product')"},
		} {
			present, err := tableExists(ctx, db, check.table)
			if err != nil {
				return DeepAuditReport{}, err
			}
			if present {
				if err := db.QueryRowContext(ctx, check.query).Scan(check.target); err != nil {
					return DeepAuditReport{}, err
				}
			}
		}
	}
	return report, nil
}

func stringSliceContains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

// auditRuntimeHistory verifies that each source row has exactly one mapping
// created by a successful runtime import, a recorded source revision, and a
// live target projection. The import command must be rerun immediately before
// this audit so a changed source snapshot receives a new failed run.
func auditRuntimeHistory(ctx context.Context, db *sql.DB, spec runtimeHistoryAuditSpec) (bool, error) {
	sourceTable := quoteRuntimeIdentifier(spec.SourceTable)
	primaryKey := quoteRuntimeIdentifier(spec.PrimaryKey)
	targetTable := quoteRuntimeIdentifier(spec.TargetTable)
	projectionJoin := ""
	projectionLeftJoin := ""
	resourcePredicate := ""
	orphanPredicate := ""
	if spec.ProjectionTable != "" {
		projectionTable := quoteRuntimeIdentifier(spec.ProjectionTable)
		projectionJoin = " JOIN " + projectionTable + " p ON p.resource_id=t.id"
		projectionLeftJoin = " LEFT JOIN " + projectionTable + " p ON p.resource_id=t.id"
		resourcePredicate = " AND t.resource_kind=?"
		orphanPredicate = " OR p.resource_id IS NULL OR t.resource_kind<>?"
	}
	args := []any{runtimeImportOperation, spec.SourceTable, spec.TargetType}
	if spec.ResourceKind != "" {
		args = append(args, spec.ResourceKind)
	}
	query := `SELECT COUNT(*) FROM ` + sourceTable + ` s WHERE NOT EXISTS (
		SELECT 1 FROM gw_migration_object_map m
		JOIN gw_migration_mapping_proofs mp ON mp.object_map_id=m.id
		JOIN gw_migration_runs r ON r.id=mp.run_id
		JOIN ` + targetTable + ` t ON t.id=m.target_id` + projectionJoin + `
		WHERE r.operation=? AND r.status='succeeded'
		  AND NOT EXISTS (SELECT 1 FROM gw_migration_runs newer WHERE newer.operation=r.operation AND newer.id>r.id)
		  AND m.source_table=? AND m.target_type=?
		  AND m.source_pk COLLATE utf8mb4_unicode_ci=CAST(s.` + primaryKey + ` AS CHAR CHARACTER SET utf8mb4) COLLATE utf8mb4_unicode_ci
		  AND EXISTS (SELECT 1 FROM gw_migration_source_revisions sr WHERE sr.object_map_id=m.id AND sr.source_hmac=mp.source_hmac)` + resourcePredicate + `)`
	var unmigrated int64
	if err := db.QueryRowContext(ctx, query, args...).Scan(&unmigrated); err != nil {
		return false, fmt.Errorf("audit %s source mappings: %w", spec.SourceTable, err)
	}
	if unmigrated != 0 {
		return false, nil
	}
	var duplicateMappings int64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM (
		SELECT m.source_pk FROM gw_migration_object_map m
		JOIN gw_migration_mapping_proofs mp ON mp.object_map_id=m.id
		JOIN gw_migration_runs r ON r.id=mp.run_id
		WHERE r.operation=? AND r.status='succeeded' AND m.source_table=? AND m.target_type=?
		  AND NOT EXISTS (SELECT 1 FROM gw_migration_runs newer WHERE newer.operation=r.operation AND newer.id>r.id)
		GROUP BY m.source_pk HAVING COUNT(*) <> 1
	) duplicates`, runtimeImportOperation, spec.SourceTable, spec.TargetType).Scan(&duplicateMappings); err != nil {
		return false, fmt.Errorf("audit %s duplicate mappings: %w", spec.SourceTable, err)
	}
	if duplicateMappings != 0 {
		return false, nil
	}
	args = []any{runtimeImportOperation, spec.SourceTable, spec.TargetType}
	if spec.ResourceKind != "" {
		args = append(args, spec.ResourceKind)
	}
	query = `SELECT COUNT(*) FROM gw_migration_object_map m
		JOIN gw_migration_mapping_proofs mp ON mp.object_map_id=m.id
		JOIN gw_migration_runs r ON r.id=mp.run_id
		LEFT JOIN ` + sourceTable + ` s ON m.source_pk COLLATE utf8mb4_unicode_ci=CAST(s.` + primaryKey + ` AS CHAR CHARACTER SET utf8mb4) COLLATE utf8mb4_unicode_ci
		LEFT JOIN ` + targetTable + ` t ON t.id=m.target_id` + projectionLeftJoin + `
		WHERE r.operation=? AND r.status='succeeded' AND m.source_table=? AND m.target_type=?
		  AND NOT EXISTS (SELECT 1 FROM gw_migration_runs newer WHERE newer.operation=r.operation AND newer.id>r.id)
		  AND (s.` + primaryKey + ` IS NULL OR t.id IS NULL` + orphanPredicate + `)`
	var orphanMappings int64
	if err := db.QueryRowContext(ctx, query, args...).Scan(&orphanMappings); err != nil {
		return false, fmt.Errorf("audit %s orphan mappings: %w", spec.SourceTable, err)
	}
	return orphanMappings == 0, nil
}

// auditVideoAssetHistory verifies the complete source-to-target relation for
// every legacy asset. A row counts as migrated only when its mapping belongs to
// a successful video-asset import, has a source revision, points at an existing
// media asset, and all immutable ownership/integrity fields match. Counting
// mapping rows alone is insufficient because orphaned, duplicate or stale maps
// can otherwise make a partial import appear complete.
func auditVideoAssetHistory(ctx context.Context, db *sql.DB) (bool, error) {
	var unmigrated int64
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM video_assets a
		LEFT JOIN tokens t ON t.id=a.token_id
		WHERE NOT EXISTS (
			SELECT 1
			FROM gw_migration_object_map m
			JOIN gw_migration_mapping_proofs mp ON mp.object_map_id=m.id
			JOIN gw_migration_runs r ON r.id=mp.run_id
			JOIN gw_media_assets ma ON ma.id=m.target_id
			WHERE r.operation='legacy_video_assets_import'
			  AND r.status='succeeded'
			  AND NOT EXISTS (SELECT 1 FROM gw_migration_runs newer WHERE newer.operation=r.operation AND newer.id>r.id)
			  AND m.source_table='video_assets'
			  AND m.source_pk COLLATE utf8mb4_unicode_ci=a.id COLLATE utf8mb4_unicode_ci
			  AND m.target_type='media_asset'
			  AND m.target_discriminator COLLATE utf8mb4_unicode_ci=TRIM(COALESCE(a.storage_path,'')) COLLATE utf8mb4_unicode_ci
			  AND ma.user_id=COALESCE(t.user_id,0)
			  AND ma.token_id=a.token_id
			  AND ma.purpose='input'
			  AND ma.object_key COLLATE utf8mb4_unicode_ci=TRIM(COALESCE(a.storage_path,'')) COLLATE utf8mb4_unicode_ci
			  AND LOWER(ma.content_type) COLLATE utf8mb4_unicode_ci=LOWER(TRIM(COALESCE(a.content_type,''))) COLLATE utf8mb4_unicode_ci
			  AND ma.content_length=a.size_bytes
			  AND LOWER(ma.sha256) COLLATE utf8mb4_unicode_ci=LOWER(TRIM(COALESCE(a.sha256,''))) COLLATE utf8mb4_unicode_ci
			  AND EXISTS (SELECT 1 FROM gw_migration_source_revisions sr WHERE sr.object_map_id=m.id AND sr.source_hmac=mp.source_hmac)
		)`).Scan(&unmigrated); err != nil {
		return false, fmt.Errorf("audit video asset source mappings: %w", err)
	}
	if unmigrated != 0 {
		return false, nil
	}
	var duplicateMappings int64
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM (
			SELECT m.source_pk
			FROM gw_migration_object_map m
			JOIN gw_migration_mapping_proofs mp ON mp.object_map_id=m.id
			JOIN gw_migration_runs r ON r.id=mp.run_id
			WHERE r.operation='legacy_video_assets_import' AND r.status='succeeded'
			  AND NOT EXISTS (SELECT 1 FROM gw_migration_runs newer WHERE newer.operation=r.operation AND newer.id>r.id)
			  AND m.source_table='video_assets' AND m.target_type='media_asset'
			GROUP BY m.source_pk
			HAVING COUNT(*) <> 1
		) duplicates`).Scan(&duplicateMappings); err != nil {
		return false, fmt.Errorf("audit video asset duplicate mappings: %w", err)
	}
	if duplicateMappings != 0 {
		return false, nil
	}
	var orphanMappings int64
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM gw_migration_object_map m
		JOIN gw_migration_mapping_proofs mp ON mp.object_map_id=m.id
		JOIN gw_migration_runs r ON r.id=mp.run_id
		LEFT JOIN video_assets a ON a.id COLLATE utf8mb4_unicode_ci=m.source_pk COLLATE utf8mb4_unicode_ci
		LEFT JOIN gw_media_assets ma ON ma.id=m.target_id
		WHERE r.operation='legacy_video_assets_import' AND r.status='succeeded'
		  AND NOT EXISTS (SELECT 1 FROM gw_migration_runs newer WHERE newer.operation=r.operation AND newer.id>r.id)
		  AND m.source_table='video_assets' AND m.target_type='media_asset'
		  AND (a.id IS NULL OR ma.id IS NULL)`).Scan(&orphanMappings); err != nil {
		return false, fmt.Errorf("audit video asset orphan mappings: %w", err)
	}
	return orphanMappings == 0, nil
}

// auditAIFileHistory requires a one-to-one source mapping whose current
// metadata and byte digest still match the published file resource and object.
func auditAIFileHistory(ctx context.Context, db *sql.DB) (bool, error) {
	var unmigrated int64
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM ai_files f
		WHERE NOT EXISTS (
			SELECT 1
			FROM gw_migration_object_map m
			JOIN gw_migration_mapping_proofs mp ON mp.object_map_id=m.id
			JOIN gw_migration_runs run ON run.id=mp.run_id
			JOIN gw_file_resources target_file ON target_file.id=m.target_discriminator
			JOIN gw_media_assets asset ON asset.id=m.target_id
			JOIN gw_media_asset_refs ref ON ref.media_asset_id=asset.id
			WHERE run.operation='legacy_ai_files_import'
			  AND run.status='succeeded'
			  AND NOT EXISTS (SELECT 1 FROM gw_migration_runs newer WHERE newer.operation=run.operation AND newer.id>run.id)
			  AND m.source_table='ai_files'
			  AND m.target_type='file_resource'
			  AND m.source_pk COLLATE utf8mb4_unicode_ci=f.id COLLATE utf8mb4_unicode_ci
			  AND m.target_discriminator COLLATE utf8mb4_unicode_ci=f.id COLLATE utf8mb4_unicode_ci
			  AND target_file.user_id=f.user_id
			  AND target_file.token_id=f.token_id
			  AND target_file.filename COLLATE utf8mb4_unicode_ci=f.filename COLLATE utf8mb4_unicode_ci
			  AND target_file.purpose COLLATE utf8mb4_unicode_ci=f.purpose COLLATE utf8mb4_unicode_ci
			  AND target_file.bytes=f.bytes
			  AND LOWER(target_file.mime_type) COLLATE utf8mb4_unicode_ci=LOWER(f.mime_type) COLLATE utf8mb4_unicode_ci
			  AND target_file.status='processed'
			  AND target_file.created_at=f.created_at
			  AND asset.user_id=f.user_id
			  AND asset.token_id=f.token_id
			  AND asset.purpose='file'
			  AND asset.content_length=OCTET_LENGTH(f.content)
			  AND LOWER(asset.content_type) COLLATE utf8mb4_unicode_ci=LOWER(f.mime_type) COLLATE utf8mb4_unicode_ci
			  AND asset.sha256 COLLATE utf8mb4_unicode_ci=LOWER(SHA2(f.content,256)) COLLATE utf8mb4_unicode_ci
			  AND asset.state='active'
			  AND ref.ai_file_id=target_file.id
			  AND ref.user_id=f.user_id
			  AND ref.token_id=f.token_id
			  AND ref.role='file'
			  AND ref.ordinal=0
			  AND EXISTS (SELECT 1 FROM gw_migration_source_revisions sr WHERE sr.object_map_id=m.id AND sr.source_hmac=mp.source_hmac)
		)`).Scan(&unmigrated); err != nil {
		return false, fmt.Errorf("audit AI file source mappings: %w", err)
	}
	if unmigrated != 0 {
		return false, nil
	}
	var duplicateMappings int64
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM (
			SELECT m.source_pk
			FROM gw_migration_object_map m
			JOIN gw_migration_mapping_proofs mp ON mp.object_map_id=m.id
			JOIN gw_migration_runs run ON run.id=mp.run_id
			WHERE run.operation='legacy_ai_files_import' AND run.status='succeeded'
			  AND NOT EXISTS (SELECT 1 FROM gw_migration_runs newer WHERE newer.operation=run.operation AND newer.id>run.id)
			  AND m.source_table='ai_files' AND m.target_type='file_resource'
			GROUP BY m.source_pk
			HAVING COUNT(*) <> 1
		) duplicates`).Scan(&duplicateMappings); err != nil {
		return false, fmt.Errorf("audit AI file duplicate mappings: %w", err)
	}
	if duplicateMappings != 0 {
		return false, nil
	}
	var orphanMappings int64
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM gw_migration_object_map m
		JOIN gw_migration_mapping_proofs mp ON mp.object_map_id=m.id
		JOIN gw_migration_runs run ON run.id=mp.run_id
		LEFT JOIN ai_files f ON f.id COLLATE utf8mb4_unicode_ci=m.source_pk COLLATE utf8mb4_unicode_ci
		LEFT JOIN gw_file_resources target_file ON target_file.id=m.target_discriminator
		LEFT JOIN gw_media_assets asset ON asset.id=m.target_id
		LEFT JOIN gw_media_asset_refs ref ON ref.media_asset_id=asset.id AND ref.ai_file_id=target_file.id AND ref.role='file' AND ref.ordinal=0
		WHERE run.operation='legacy_ai_files_import' AND run.status='succeeded'
		  AND NOT EXISTS (SELECT 1 FROM gw_migration_runs newer WHERE newer.operation=run.operation AND newer.id>run.id)
		  AND m.source_table='ai_files' AND m.target_type='file_resource'
		  AND (f.id IS NULL OR target_file.id IS NULL OR asset.id IS NULL OR ref.id IS NULL)`).Scan(&orphanMappings); err != nil {
		return false, fmt.Errorf("audit AI file orphan mappings: %w", err)
	}
	return orphanMappings == 0, nil
}

func (r DeepAuditReport) ReadyForCleanup() bool {
	return len(r.MissingTargetTables) == 0 &&
		len(r.MissingAuditTables) == 0 && len(r.UnverifiedLegacyHistory) == 0 &&
		r.UnmappedLegacyChannels == 0 && r.UnmappedLegacyKeys == 0 &&
		r.UnmappedLegacyAbilities == 0 && r.OpenMigrationIssues == 0 &&
		r.MigrationRunCount > 0 && r.SucceededMigrationRuns > 0 && r.RunningMigrationRuns == 0
}

// CleanupLegacyGateway removes only the three legacy gateway configuration
// tables after a successful deep audit. History tables are never touched here;
// their migration must be completed and verified before this guard can pass.
func CleanupLegacyGateway(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("database is required")
	}
	report, err := DeepAudit(ctx, db)
	if err != nil {
		return fmt.Errorf("deep audit before cleanup: %w", err)
	}
	if !report.ReadyForCleanup() {
		return fmt.Errorf("legacy cleanup blocked: missing_target=%v missing_audit=%v unverified_history=%v unmapped_channels=%d unmapped_keys=%d unmapped_abilities=%d open_issues=%d migration_runs=%d succeeded_runs=%d", report.MissingTargetTables, report.MissingAuditTables, report.UnverifiedLegacyHistory, report.UnmappedLegacyChannels, report.UnmappedLegacyKeys, report.UnmappedLegacyAbilities, report.OpenMigrationIssues, report.MigrationRunCount, report.SucceededMigrationRuns)
	}
	if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS gw_abilities, gw_channel_keys, gw_channels"); err != nil {
		return fmt.Errorf("drop legacy gateway tables: %w", err)
	}
	return nil
}

func tableExists(ctx context.Context, db *sql.DB, table string) (bool, error) {
	var count int
	err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name=? AND table_type='BASE TABLE'", table).Scan(&count)
	return count > 0, err
}

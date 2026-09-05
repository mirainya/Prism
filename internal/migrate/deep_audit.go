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
	LegacyTablesPresent     []string
	UnmappedLegacyChannels  int64
	UnmappedLegacyKeys      int64
	UnmappedLegacyAbilities int64
	OpenMigrationIssues     int64
	MigrationRunCount       int64
	SucceededMigrationRuns  int64
}

var deepAuditTargetTables = []string{
	"gateway_channels", "gw_models", "gw_model_names", "gw_operation_contracts",
	"gw_catalog_releases", "gw_catalog_models", "gw_model_operations", "gw_skus",
	"gw_channel_transports", "gw_products", "gw_product_transports", "gw_offerings",
	"gw_routes", "gw_sell_rates", "gw_cost_plans", "gw_cost_rates", "gw_credentials",
	"gw_credential_versions", "gw_credential_purpose_grants", "gw_api_calls",
	"gw_api_call_attempts", "gw_async_executions", "gw_catalog_runtime_state",
	"gw_deployment_generations", "gw_deployment_members", "gw_catalog_readiness",
	"crypto_key_readiness", "encrypted_blobs", "encrypted_blob_key_wraps",
}

var deepAuditLegacyTables = []string{"gw_channels", "gw_channel_keys", "gw_abilities"}

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
	if present, err := tableExists(ctx, db, "gw_migration_runs"); err != nil {
		return DeepAuditReport{}, err
	} else if present {
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*), COALESCE(SUM(status='succeeded'),0) FROM gw_migration_runs").Scan(&report.MigrationRunCount, &report.SucceededMigrationRuns); err != nil {
			return DeepAuditReport{}, err
		}
	}
	if present, err := tableExists(ctx, db, "gw_migration_issues"); err != nil {
		return DeepAuditReport{}, err
	} else if present {
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM gw_migration_issues WHERE status='open'").Scan(&report.OpenMigrationIssues); err != nil {
			return DeepAuditReport{}, err
		}
	}
	if present, err := tableExists(ctx, db, "gw_migration_object_map"); err != nil {
		return DeepAuditReport{}, err
	} else if present {
		for _, check := range []struct {
			table  string
			target *int64
			query  string
		}{
			{"gw_channels", &report.UnmappedLegacyChannels, "SELECT COUNT(*) FROM gw_channels c WHERE c.deleted_at IS NULL AND NOT EXISTS (SELECT 1 FROM gw_migration_object_map m WHERE m.source_table='gw_channels' AND m.source_pk=CAST(c.id AS CHAR) AND m.target_type='gateway_channel')"},
			{"gw_channel_keys", &report.UnmappedLegacyKeys, "SELECT COUNT(*) FROM gw_channel_keys k WHERE k.deleted_at IS NULL AND NOT EXISTS (SELECT 1 FROM gw_migration_object_map m WHERE m.source_table='gw_channel_keys' AND m.source_pk=CAST(k.id AS CHAR) AND m.target_type='credential')"},
			{"gw_abilities", &report.UnmappedLegacyAbilities, "SELECT COUNT(*) FROM gw_abilities a WHERE a.status<>0 AND NOT EXISTS (SELECT 1 FROM gw_migration_object_map m WHERE m.source_table='gw_abilities' AND m.source_pk=CAST(a.id AS CHAR) AND m.target_type='product')"},
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

func (r DeepAuditReport) ReadyForCleanup() bool {
	return len(r.MissingTargetTables) == 0 &&
		r.UnmappedLegacyChannels == 0 && r.UnmappedLegacyKeys == 0 &&
		r.UnmappedLegacyAbilities == 0 && r.OpenMigrationIssues == 0 &&
		r.MigrationRunCount > 0 && r.MigrationRunCount == r.SucceededMigrationRuns
}

func tableExists(ctx context.Context, db *sql.DB, table string) (bool, error) {
	var count int
	err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name=? AND table_type='BASE TABLE'", table).Scan(&count)
	return count > 0, err
}

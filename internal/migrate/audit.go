package migrate

import (
	"context"
	"database/sql"
)

// AuditReport describes whether the database is ready for the unified gateway
// cutover. It never changes data and reports legacy rows explicitly.
type AuditReport struct {
	LegacyChannels, LegacyAbilities                int64
	TargetChannels, TargetModels                   int64
	TargetCredentials, TargetReleases, TargetCalls int64
	TargetOfferings, TargetRoutes                  int64
	SellRates, CostRates, Currencies               int64
	ActiveReleaseID                                sql.NullInt64
	DeploymentStatus                               string
}

func Audit(ctx context.Context, db *sql.DB) (AuditReport, error) {
	var report AuditReport
	counts := []struct {
		table    string
		target   *int64
		optional bool
	}{
		{"gw_channels", &report.LegacyChannels, true},
		{"gw_abilities", &report.LegacyAbilities, true},
		{"gateway_channels", &report.TargetChannels, false},
		{"gw_models", &report.TargetModels, false},
		{"gw_credentials", &report.TargetCredentials, false},
		{"gw_catalog_releases", &report.TargetReleases, false},
		{"gw_api_calls", &report.TargetCalls, false},
		{"gw_offerings", &report.TargetOfferings, false},
		{"gw_routes", &report.TargetRoutes, false},
		{"gw_sell_rates", &report.SellRates, false},
		{"gw_cost_rates", &report.CostRates, false},
		{"billing_currency_definitions", &report.Currencies, false},
	}
	for _, count := range counts {
		table, target := count.table, count.target
		if count.optional {
			present, err := tableExists(ctx, db, table)
			if err != nil {
				return AuditReport{}, err
			}
			if !present {
				continue
			}
		}
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM `"+table+"`").Scan(target); err != nil {
			return AuditReport{}, err
		}
	}
	if err := db.QueryRowContext(ctx, "SELECT active_release_id FROM gw_catalog_runtime_state WHERE id=1").Scan(&report.ActiveReleaseID); err != nil && err != sql.ErrNoRows {
		return AuditReport{}, err
	}
	if err := db.QueryRowContext(ctx, "SELECT status FROM gw_deployment_generations ORDER BY id DESC LIMIT 1").Scan(&report.DeploymentStatus); err != nil && err != sql.ErrNoRows {
		return AuditReport{}, err
	}
	return report, nil
}

func (r AuditReport) ReadyForCutover() bool {
	// Calls are runtime history, not a prerequisite for accepting the first
	// request. A newly provisioned, fully configured catalog is therefore
	// eligible even when gw_api_calls is still empty.
	return r.LegacyChannels == 0 && r.LegacyAbilities == 0 && r.TargetChannels > 0 && r.TargetModels > 0 && r.TargetCredentials > 0 && r.TargetReleases > 0 && r.TargetOfferings > 0 && r.TargetRoutes > 0 && r.SellRates > 0 && r.CostRates > 0 && r.Currencies > 0 && r.ActiveReleaseID.Valid && r.DeploymentStatus == "active"
}

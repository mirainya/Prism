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
		table  string
		target *int64
	}{
		{"gw_channels", &report.LegacyChannels},
		{"gw_abilities", &report.LegacyAbilities},
		{"gateway_channels", &report.TargetChannels},
		{"gw_models", &report.TargetModels},
		{"gw_credentials", &report.TargetCredentials},
		{"gw_catalog_releases", &report.TargetReleases},
		{"gw_api_calls", &report.TargetCalls},
		{"gw_offerings", &report.TargetOfferings},
		{"gw_routes", &report.TargetRoutes},
		{"gw_sell_rates", &report.SellRates},
		{"gw_cost_rates", &report.CostRates},
		{"billing_currency_definitions", &report.Currencies},
	}
	for _, count := range counts {
		table, target := count.table, count.target
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
	return r.LegacyChannels == 0 && r.LegacyAbilities == 0 && r.TargetChannels > 0 && r.TargetModels > 0 && r.TargetCredentials > 0 && r.TargetReleases > 0 && r.TargetOfferings > 0 && r.TargetRoutes > 0 && r.SellRates > 0 && r.CostRates > 0 && r.Currencies > 0 && r.ActiveReleaseID.Valid && r.DeploymentStatus == "active"
}

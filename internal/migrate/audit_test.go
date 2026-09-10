package migrate

import (
	"context"
	"database/sql"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestAuditReportsCutoverReadiness(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	queries := []struct {
		table string
		value int64
	}{
		{"gw_channels", 0},
		{"gw_abilities", 0},
		{"gateway_channels", 2},
		{"gw_models", 3},
		{"gw_credentials", 4},
		{"gw_catalog_releases", 1},
		{"gw_api_calls", 7},
		{"gw_offerings", 2},
		{"gw_routes", 2},
		{"gw_sell_rates", 2},
		{"gw_cost_rates", 2},
		{"billing_currency_definitions", 1},
	}
	for _, query := range queries {
		if query.table == "gw_channels" || query.table == "gw_abilities" {
			mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name=? AND table_type='BASE TABLE'")).WithArgs(query.table).WillReturnRows(sqlmock.NewRows([]string{"COUNT(*)"}).AddRow(1))
		}
		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM `" + query.table + "`")).WillReturnRows(sqlmock.NewRows([]string{"COUNT(*)"}).AddRow(query.value))
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT active_release_id FROM gw_catalog_runtime_state WHERE id=1")).WillReturnRows(sqlmock.NewRows([]string{"active_release_id"}).AddRow(int64(12)))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT status FROM gw_deployment_generations ORDER BY id DESC LIMIT 1")).WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("active"))

	report, err := Audit(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if !report.ReadyForCutover() {
		t.Fatalf("report should be ready: %+v", report)
	}
	if report.TargetCalls != 7 || !report.ActiveReleaseID.Valid || report.ActiveReleaseID.Int64 != 12 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAuditDoesNotClaimReadinessWithLegacyRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for _, query := range []struct {
		table string
		value int64
	}{
		{"gw_channels", 1},
		{"gw_abilities", 0},
		{"gateway_channels", 2},
		{"gw_models", 3},
		{"gw_credentials", 4},
		{"gw_catalog_releases", 1},
		{"gw_api_calls", 7},
		{"gw_offerings", 2},
		{"gw_routes", 2},
		{"gw_sell_rates", 2},
		{"gw_cost_rates", 2},
		{"billing_currency_definitions", 1},
	} {
		if query.table == "gw_channels" || query.table == "gw_abilities" {
			mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name=? AND table_type='BASE TABLE'")).WithArgs(query.table).WillReturnRows(sqlmock.NewRows([]string{"COUNT(*)"}).AddRow(1))
		}
		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM `" + query.table + "`")).WillReturnRows(sqlmock.NewRows([]string{"COUNT(*)"}).AddRow(query.value))
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT active_release_id FROM gw_catalog_runtime_state WHERE id=1")).WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT status FROM gw_deployment_generations ORDER BY id DESC LIMIT 1")).WillReturnError(sql.ErrNoRows)

	report, err := Audit(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if report.ReadyForCutover() {
		t.Fatalf("report must not be ready with legacy rows: %+v", report)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAuditTreatsCleanedLegacyTablesAsEmpty(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for _, table := range []string{"gw_channels", "gw_abilities"} {
		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name=? AND table_type='BASE TABLE'")).WithArgs(table).WillReturnRows(sqlmock.NewRows([]string{"COUNT(*)"}).AddRow(0))
	}
	for _, table := range []string{"gateway_channels", "gw_models", "gw_credentials", "gw_catalog_releases", "gw_api_calls", "gw_offerings", "gw_routes", "gw_sell_rates", "gw_cost_rates", "billing_currency_definitions"} {
		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM `" + table + "`")).WillReturnRows(sqlmock.NewRows([]string{"COUNT(*)"}).AddRow(1))
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT active_release_id FROM gw_catalog_runtime_state WHERE id=1")).WillReturnRows(sqlmock.NewRows([]string{"active_release_id"}).AddRow(int64(12)))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT status FROM gw_deployment_generations ORDER BY id DESC LIMIT 1")).WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("active"))

	report, err := Audit(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if !report.ReadyForCutover() || report.LegacyChannels != 0 || report.LegacyAbilities != 0 {
		t.Fatalf("cleaned legacy tables should be ready: %+v", report)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

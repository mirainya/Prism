//go:build integration

package migrate

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/mirainya/Prism/internal/model"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type legacyDraftGuardFixture struct {
	db            *sql.DB
	options       ImportOptions
	legacyRelease int64
}

func TestMySQLLegacyDraftReplacementGuards(t *testing.T) {
	tests := []struct {
		name           string
		wantReason     string
		fixtureOptions legacyV1FixtureOptions
		mutate         func(*testing.T, legacyDraftGuardFixture)
	}{
		{
			name:       "active catalog",
			wantReason: "catalog runtime state has already been used",
			mutate: func(t *testing.T, fixture legacyDraftGuardFixture) {
				now := time.Now().UTC()
				// The runtime-state trigger intentionally rejects a draft release.
				// Publish the fixture first, then model a prior activation.
				mustExecCatalogGuard(t, fixture.db, `UPDATE gw_catalog_releases SET status='published',published_at=?,updated_at=? WHERE id=?`, now, now, fixture.legacyRelease)
				mustExecCatalogGuard(t, fixture.db, `UPDATE gw_catalog_runtime_state SET active_release_id=?,state_version=2,updated_at=? WHERE id=1`, fixture.legacyRelease, now)
			},
		},
		{
			name:       "active deployment",
			wantReason: "an active deployment exists",
			mutate: func(t *testing.T, fixture legacyDraftGuardFixture) {
				mustExecCatalogGuard(t, fixture.db, `INSERT INTO gw_deployment_generations(generation_no,status,semantic_version,semantic_digest,created_at) VALUES (1,'active','guard-test',?,?)`, strings.Repeat("a", 64), time.Now().UTC())
			},
		},
		{
			name:       "unified call",
			wantReason: "unified calls already reference the target catalog",
			mutate: func(t *testing.T, fixture legacyDraftGuardFixture) {
				var contractID, operationID, skuID int64
				if err := fixture.db.QueryRow(`SELECT mo.operation_contract_id,mo.id,s.id FROM gw_model_operations mo JOIN gw_skus s ON s.release_id=mo.release_id AND s.model_operation_id=mo.id WHERE mo.release_id=? LIMIT 1`, fixture.legacyRelease).Scan(&contractID, &operationID, &skuID); err != nil {
					t.Fatal(err)
				}
				now := time.Now().UTC()
				mustExecCatalogGuard(t, fixture.db, `INSERT INTO gw_api_calls(public_id,user_id,token_id,operation_contract_id,catalog_release_id,model_operation_id,sku_id,status,price_currency,price_currency_version,delivery_mode,created_at,updated_at) VALUES ('00000000-0000-0000-0000-000000000001',1,1,?,?,?,?,'completed','CREDIT',1,'reference',?,?)`, contractID, fixture.legacyRelease, operationID, skuID, now, now)
			},
		},
		{
			name:           "missing mapping proof",
			wantReason:     "has no verified V1 target",
			fixtureOptions: legacyV1FixtureOptions{omitMappingProofSourceTable: "gw_channels"},
		},
		{
			name:       "modified draft",
			wantReason: "target was modified",
			mutate: func(t *testing.T, fixture legacyDraftGuardFixture) {
				var channelID int64
				if err := fixture.db.QueryRow(`SELECT m.target_id FROM gw_migration_object_map m JOIN gw_migration_runs r ON r.id=m.run_id WHERE r.operation=? AND m.source_table='gw_channels' LIMIT 1`, legacyGatewayImportOperation).Scan(&channelID); err != nil {
					t.Fatal(err)
				}
				mustExecCatalogGuard(t, fixture.db, `UPDATE gateway_channels SET display_name='modified guard fixture' WHERE id=?`, channelID)
			},
		},
		{
			name:       "modified transport",
			wantReason: "transport was modified",
			mutate: func(t *testing.T, fixture legacyDraftGuardFixture) {
				mustExecCatalogGuard(t, fixture.db, `UPDATE gw_channel_transports SET request_path='/modified' WHERE release_id=?`, fixture.legacyRelease)
			},
		},
		{
			name:       "modified model graph",
			wantReason: "target was modified",
			mutate: func(t *testing.T, fixture legacyDraftGuardFixture) {
				mustExecCatalogGuard(t, fixture.db, `UPDATE gw_skus SET idempotency_mode='required' WHERE release_id=?`, fixture.legacyRelease)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gormDB, sqlDB := openCatalogGuardDatabase(t)
			fixture := seedLegacyDraftGuardFixture(t, gormDB, sqlDB, test.fixtureOptions)
			if test.mutate != nil {
				test.mutate(t, fixture)
			}

			report, err := ImportLegacyGateway(context.Background(), sqlDB, fixture.options)
			if !errors.Is(err, ErrCatalogImportIssues) || report.RunID == 0 || report.Issues == 0 {
				t.Fatalf("replacement guard did not reject import: report=%+v err=%v", report, err)
			}
			var issueCode, detail, status string
			if err := sqlDB.QueryRow(`SELECT issue_code,detail,status FROM gw_migration_issues WHERE run_id=? AND issue_code='target_catalog_not_replaceable'`, report.RunID).Scan(&issueCode, &detail, &status); err != nil {
				t.Fatal(err)
			}
			if issueCode != "target_catalog_not_replaceable" || status != "open" || !strings.Contains(detail, test.wantReason) {
				t.Fatalf("unexpected replacement issue: code=%q status=%q detail=%q", issueCode, status, detail)
			}
			assertLegacyDraftPreserved(t, sqlDB, fixture.legacyRelease)
		})
	}
}

func TestMySQLLegacyDraftMappingProofBackfill(t *testing.T) {
	gormDB, sqlDB := openCatalogGuardDatabase(t)
	fixture := seedLegacyDraftGuardFixture(t, gormDB, sqlDB, legacyV1FixtureOptions{omitMappingEvidence: true})

	var mappings, revisions, proofs int64
	if err := sqlDB.QueryRow(`SELECT
		(SELECT COUNT(*) FROM gw_migration_object_map mapping
		 JOIN gw_migration_runs run ON run.id=mapping.run_id WHERE run.operation=?),
		(SELECT COUNT(*) FROM gw_migration_source_revisions revision
		 JOIN gw_migration_object_map mapping ON mapping.id=revision.object_map_id
		 JOIN gw_migration_runs run ON run.id=mapping.run_id WHERE run.operation=?),
		(SELECT COUNT(*) FROM gw_migration_mapping_proofs proof
		 JOIN gw_migration_runs run ON run.id=proof.run_id WHERE run.operation=?)`,
		legacyGatewayImportOperation, legacyGatewayImportOperation, legacyGatewayImportOperation,
	).Scan(&mappings, &revisions, &proofs); err != nil {
		t.Fatal(err)
	}
	if mappings != 3 || revisions != 0 || proofs != 0 {
		t.Fatalf("unexpected pre-backfill evidence: mappings=%d revisions=%d proofs=%d", mappings, revisions, proofs)
	}

	migrations, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	const filename = "20260907_130000_backfill_legacy_catalog_mapping_proofs.sql"
	var migrationSQL string
	for _, migration := range migrations {
		if migration.Filename == filename {
			migrationSQL = migration.SQL
			break
		}
	}
	if migrationSQL == "" {
		t.Fatalf("migration %s not found", filename)
	}
	if _, err := sqlDB.Exec(migrationSQL); err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.QueryRow(`SELECT
		(SELECT COUNT(*) FROM gw_migration_source_revisions revision
		 JOIN gw_migration_object_map mapping ON mapping.id=revision.object_map_id
		 JOIN gw_migration_runs run ON run.id=mapping.run_id WHERE run.operation=?),
		(SELECT COUNT(*) FROM gw_migration_mapping_proofs proof
		 JOIN gw_migration_runs run ON run.id=proof.run_id WHERE run.operation=?)`,
		legacyGatewayImportOperation, legacyGatewayImportOperation,
	).Scan(&revisions, &proofs); err != nil {
		t.Fatal(err)
	}
	if revisions != mappings || proofs != mappings {
		t.Fatalf("incomplete backfill evidence: mappings=%d revisions=%d proofs=%d", mappings, revisions, proofs)
	}

	report, err := ImportLegacyGateway(context.Background(), sqlDB, fixture.options)
	if err != nil || report.ReleaseID == 0 || report.ReleaseID == fixture.legacyRelease {
		t.Fatalf("replace backfilled V1 draft: report=%+v err=%v", report, err)
	}
}

func openCatalogGuardDatabase(t *testing.T) (*gorm.DB, *sql.DB) {
	t.Helper()
	dsn := os.Getenv("PRISM_MIGRATION_TEST_DSN")
	if dsn == "" {
		t.Skip("PRISM_MIGRATION_TEST_DSN is not set")
	}
	config, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid MySQL test DSN")
	}
	host, _, err := net.SplitHostPort(config.Addr)
	if err != nil || config.Net != "tcp" || !net.ParseIP(host).IsLoopback() {
		t.Fatal("migration integration tests require an isolated loopback MySQL server")
	}
	config.DBName = ""
	server, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	databaseName := fmt.Sprintf("prism_test_catalog_guard_%d", time.Now().UnixNano())
	if _, err := server.Exec("CREATE DATABASE `" + databaseName + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := server.Exec("DROP DATABASE `" + databaseName + "`"); err != nil {
			t.Errorf("clean test database: %v", err)
		}
	})

	config.DBName, config.ParseTime, config.MultiStatements = databaseName, true, true
	gormDB, err := gorm.Open(mysql.Open(config.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := gormDB.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if _, err := Up(context.Background(), gormDB); err != nil {
		t.Fatal(err)
	}
	return gormDB, sqlDB
}

func seedLegacyDraftGuardFixture(t *testing.T, gormDB *gorm.DB, sqlDB *sql.DB, fixtureOptions legacyV1FixtureOptions) legacyDraftGuardFixture {
	t.Helper()
	channel := model.GwChannel{Name: "Replacement guard", Protocol: "openai", BaseURL: "https://guard.example.invalid", Status: 1}
	if err := gormDB.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	key := model.GwChannelKey{ChannelID: channel.ID, Name: "Replacement guard", APIKey: "replacement-guard-secret", Weight: 1, Status: 1}
	if err := gormDB.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	ability := model.GwAbility{ChannelID: channel.ID, KeyID: key.ID, ModelName: "replacement-guard-model", VendorModel: "replacement-guard-model", Status: 1}
	if err := gormDB.Create(&ability).Error; err != nil {
		t.Fatal(err)
	}
	options := ImportOptions{KEK: bytes.Repeat([]byte{81}, 32), HMACKey: bytes.Repeat([]byte{82}, 32)}
	report, err := importLegacyGatewayV1WithOptions(context.Background(), sqlDB, options, fixtureOptions)
	if err != nil || report.ReleaseID == 0 {
		t.Fatalf("seed V1 draft: report=%+v err=%v", report, err)
	}
	return legacyDraftGuardFixture{db: sqlDB, options: options, legacyRelease: report.ReleaseID}
}

func assertLegacyDraftPreserved(t *testing.T, db *sql.DB, releaseID int64) {
	t.Helper()
	var releases, channels, credentials int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM gw_catalog_releases WHERE id=?`, releaseID).Scan(&releases); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM gateway_channels WHERE channel_code LIKE 'legacy-channel-%'`).Scan(&channels); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM gw_credentials WHERE credential_code LIKE 'legacy-key-%'`).Scan(&credentials); err != nil {
		t.Fatal(err)
	}
	if releases != 1 || channels != 1 || credentials != 1 {
		t.Fatalf("guard changed V1 target: releases=%d channels=%d credentials=%d", releases, channels, credentials)
	}
}

func mustExecCatalogGuard(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

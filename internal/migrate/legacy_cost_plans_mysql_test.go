//go:build integration

package migrate

import (
	"database/sql"
	"testing"
)

func TestMySQLLegacyCostPlanRepairMigration(t *testing.T) {
	gormDB, sqlDB := openCatalogGuardDatabase(t)
	fixture := seedLegacyDraftGuardFixture(t, gormDB, sqlDB, legacyV1FixtureOptions{})
	migrationSQL := loadMigrationSQLForTest(t, "20260909_100000")

	var offerings, plans int64
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM gw_offerings WHERE release_id=?`, fixture.legacyRelease).Scan(&offerings); err != nil {
		t.Fatal(err)
	}
	if offerings == 0 {
		t.Fatal("legacy fixture has no offerings")
	}
	assertLegacyCostPlanCount(t, sqlDB, fixture.legacyRelease, 0)

	mustExecCatalogGuard(t, sqlDB, `UPDATE gw_catalog_releases SET status='published' WHERE id=?`, fixture.legacyRelease)
	mustExecCatalogGuard(t, sqlDB, migrationSQL)
	assertLegacyCostPlanCount(t, sqlDB, fixture.legacyRelease, 0)

	mustExecCatalogGuard(t, sqlDB, `UPDATE gw_catalog_releases SET status='draft',semantic_version='manual-draft' WHERE id=?`, fixture.legacyRelease)
	mustExecCatalogGuard(t, sqlDB, migrationSQL)
	assertLegacyCostPlanCount(t, sqlDB, fixture.legacyRelease, 0)

	mustExecCatalogGuard(t, sqlDB, `UPDATE gw_catalog_releases SET semantic_version='legacy-import-1' WHERE id=?`, fixture.legacyRelease)
	mustExecCatalogGuard(t, sqlDB, migrationSQL)
	assertLegacyCostPlanCount(t, sqlDB, fixture.legacyRelease, offerings)

	if err := sqlDB.QueryRow(`SELECT COUNT(*)
		FROM gw_cost_plans p
		JOIN gw_offerings o ON o.release_id=p.release_id AND o.id=p.offering_id
		WHERE p.release_id=? AND (p.plan_code<>o.cost_plan_code OR p.created_at<>o.created_at)`, fixture.legacyRelease).Scan(&plans); err != nil {
		t.Fatal(err)
	}
	if plans != 0 {
		t.Fatalf("legacy repair created %d mismatched cost plans", plans)
	}

	mustExecCatalogGuard(t, sqlDB, migrationSQL)
	assertLegacyCostPlanCount(t, sqlDB, fixture.legacyRelease, offerings)
}

func loadMigrationSQLForTest(t *testing.T, version string) string {
	t.Helper()
	migrations, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range migrations {
		if migration.Version == version {
			return migration.SQL
		}
	}
	t.Fatalf("migration %s not found", version)
	return ""
}

func assertLegacyCostPlanCount(t *testing.T, db interface {
	QueryRow(query string, args ...any) *sql.Row
}, releaseID, want int64) {
	t.Helper()
	var plans int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM gw_cost_plans WHERE release_id=?`, releaseID).Scan(&plans); err != nil {
		t.Fatal(err)
	}
	if plans != want {
		t.Fatalf("legacy cost plans=%d, want %d", plans, want)
	}
}

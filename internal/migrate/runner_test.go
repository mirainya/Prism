package migrate

import (
	"context"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestReconcileMigrationsFindsPending(t *testing.T) {
	available := []Migration{
		{Version: "20260718_150000", Name: "baseline", Filename: "baseline.sql", Checksum: "a"},
		{Version: "20260719_100000", Name: "next", Filename: "next.sql", Checksum: "b"},
	}
	applied := []AppliedMigration{{Version: "20260718_150000", Name: "baseline", Checksum: "a"}}
	pending, err := reconcileMigrations(available, applied, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Version != "20260719_100000" {
		t.Fatalf("pending=%#v", pending)
	}
}

func TestReconcileMigrationsRejectsChecksumMismatch(t *testing.T) {
	available := []Migration{{Version: "20260718_150000", Name: "baseline", Filename: "baseline.sql", Checksum: "a"}}
	applied := []AppliedMigration{{Version: "20260718_150000", Name: "baseline", Checksum: "changed"}}
	_, err := reconcileMigrations(available, applied, false)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("error=%v", err)
	}
}

func TestReconcileMigrationsRejectsUnknownDatabaseVersion(t *testing.T) {
	available := []Migration{{Version: "20260718_150000", Name: "baseline", Filename: "baseline.sql", Checksum: "a"}}
	applied := []AppliedMigration{{Version: "20260720_100000", Name: "future", Checksum: "future"}}
	_, err := reconcileMigrations(available, applied, false)
	if err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("error=%v", err)
	}
}

func TestReconcileMigrationsRetriesDirtyRecordOnlyForUp(t *testing.T) {
	available := []Migration{{Version: "20260718_150000", Name: "baseline", Filename: "baseline.sql", Checksum: "a"}}
	applied := []AppliedMigration{{Version: "20260718_150000", Name: "baseline", Checksum: "a", Dirty: true}}
	if _, err := reconcileMigrations(available, applied, false); err == nil || !strings.Contains(err.Error(), "dirty") {
		t.Fatalf("inspect error=%v", err)
	}
	pending, err := reconcileMigrations(available, applied, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Version != "20260718_150000" {
		t.Fatalf("pending=%#v", pending)
	}
}

func TestReconcileMigrationsRejectsNameMismatch(t *testing.T) {
	available := []Migration{{Version: "20260718_150000", Name: "baseline", Filename: "baseline.sql", Checksum: "a"}}
	applied := []AppliedMigration{{Version: "20260718_150000", Name: "renamed", Checksum: "a"}}
	_, err := reconcileMigrations(available, applied, false)
	if err == nil || !strings.Contains(err.Error(), "name mismatch") {
		t.Fatalf("error=%v", err)
	}
}

func TestReconcileMigrationsRejectsOutOfOrderHistory(t *testing.T) {
	available := []Migration{
		{Version: "20260718_150000", Name: "baseline", Filename: "baseline.sql", Checksum: "a"},
		{Version: "20260719_100000", Name: "next", Filename: "next.sql", Checksum: "b"},
	}
	applied := []AppliedMigration{{Version: "20260719_100000", Name: "next", Checksum: "b"}}
	_, err := reconcileMigrations(available, applied, false)
	if err == nil || !strings.Contains(err.Error(), "out of order") {
		t.Fatalf("error=%v", err)
	}
}

func TestInspectTreatsEmptyHistoryOnExistingSchemaAsLegacy(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:migrate_empty_history?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE users (id INTEGER PRIMARY KEY)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE prism_schema_migrations (
		version TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		checksum TEXT NOT NULL,
		dirty INTEGER NOT NULL,
		applied_at DATETIME NOT NULL
	)`).Error; err != nil {
		t.Fatal(err)
	}
	status, err := Inspect(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Legacy || len(status.Pending) == 0 {
		t.Fatalf("status=%#v", status)
	}
}

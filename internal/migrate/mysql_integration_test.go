//go:build integration

package migrate

import (
	"context"
	"os"
	"testing"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestMySQLMigrationLifecycle(t *testing.T) {
	dsn := os.Getenv("PRISM_MIGRATION_TEST_DSN")
	if dsn == "" {
		t.Skip("PRISM_MIGRATION_TEST_DSN is not set")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	ctx := context.Background()
	applied, err := Up(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != 1 || applied[0].Version != BaselineVersion() {
		t.Fatalf("applied=%#v", applied)
	}
	if err := EnsureCurrent(ctx, db); err != nil {
		t.Fatal(err)
	}
	status, err := Inspect(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if status.Legacy || len(status.Pending) != 0 || len(status.Applied) != 1 || status.Applied[0].Dirty {
		t.Fatalf("status=%#v", status)
	}
	for _, table := range requiredApplicationTables {
		if !db.Migrator().HasTable(table) {
			t.Errorf("missing table %s", table)
		}
	}
	if !db.Migrator().HasTable(migrationTableName) {
		t.Fatal("missing migration history table")
	}
	if !db.Migrator().HasIndex("conversations", "idx_conversations_canonical_match") {
		t.Fatal("missing canonical conversation match index")
	}
	if _, err := Adopt(ctx, db); err != nil {
		t.Fatalf("adopt already managed database: %v", err)
	}

	reapplied, err := Up(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(reapplied) != 0 {
		t.Fatalf("second migration run applied %#v", reapplied)
	}
}

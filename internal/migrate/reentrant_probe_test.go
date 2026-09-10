//go:build integration && probe

package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestManagedMigrationsAreReentrant is a diagnostic probe for dirty-run
// recovery. It executes every migration covered by the reentrant contract a
// second time after a clean install and reports the first failure per file.
func TestManagedMigrationsAreReentrant(t *testing.T) {
	dsn := os.Getenv("PRISM_MIGRATION_TEST_DSN")
	if dsn == "" {
		t.Skip("PRISM_MIGRATION_TEST_DSN is not set")
	}
	config, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	host, _, err := net.SplitHostPort(config.Addr)
	if err != nil || config.Net != "tcp" || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		t.Fatal("probe requires an isolated loopback MySQL server")
	}
	config.DBName = ""
	server, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	databaseName := fmt.Sprintf("prism_probe_%d", time.Now().UnixNano())
	if _, err := server.Exec("CREATE DATABASE `" + databaseName + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"); err != nil {
		t.Fatal(err)
	}
	defer server.Exec("DROP DATABASE `" + databaseName + "`")
	config.DBName, config.ParseTime, config.MultiStatements = databaseName, true, true
	db, err := gorm.Open(gormmysql.Open(config.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	sqlDB.SetMaxOpenConns(1)
	ctx := context.Background()
	if _, err := Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	migrations, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range migrations {
		if migration.Version < reentrantMigrationStart {
			continue
		}
		if _, err := sqlDB.Exec(migration.SQL); err != nil {
			t.Errorf("%s is not reentrant: %v", migration.Filename, err)
		}
	}
}

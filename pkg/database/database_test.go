package database

import (
	"testing"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/mirainya/Prism/pkg/config"
)

func TestBuildDSNRestrictsMultiStatementsToMigrationConnections(t *testing.T) {
	cfg := config.DatabaseConfig{
		Host:     "127.0.0.1",
		Port:     3306,
		User:     "prism",
		Password: "secret",
		DBName:   "prism",
	}
	regular, err := mysqldriver.ParseDSN(buildDSN(cfg, false))
	if err != nil {
		t.Fatal(err)
	}
	migration, err := mysqldriver.ParseDSN(buildDSN(cfg, true))
	if err != nil {
		t.Fatal(err)
	}
	if regular.MultiStatements {
		t.Fatal("regular database connection enables multi-statements")
	}
	if !migration.MultiStatements {
		t.Fatal("migration database connection does not enable multi-statements")
	}
	if migration.Params["charset"] != "utf8mb4" || !migration.ParseTime {
		t.Fatalf("migration DSN lost connection options: %#v", migration)
	}
}

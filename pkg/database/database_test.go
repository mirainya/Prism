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

func TestParseOracleMySQLVersion(t *testing.T) {
	tests := []struct {
		name    string
		version string
		comment string
		want    [3]int
		wantErr bool
	}{
		{name: "minimum", version: "8.0.16", comment: "MySQL Community Server - GPL", want: [3]int{8, 0, 16}},
		{name: "patch suffix", version: "8.0.45-log", comment: "MySQL Community Server - GPL", want: [3]int{8, 0, 45}},
		{name: "future major", version: "9.1.0", comment: "MySQL Community Server - GPL", want: [3]int{9, 1, 0}},
		{name: "mariadb masquerade", version: "5.5.5-10.11.6-MariaDB", comment: "MariaDB Server", wantErr: true},
		{name: "tidb masquerade", version: "5.7.25-TiDB-v7.5.0", comment: "TiDB Server", wantErr: true},
		{name: "percona variant", version: "8.0.36-28", comment: "Percona Server", wantErr: true},
		{name: "invalid", version: "unknown", comment: "unknown", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			major, minor, patch, err := parseOracleMySQLVersion(test.version, test.comment)
			if test.wantErr {
				if err == nil {
					t.Fatalf("parseOracleMySQLVersion(%q, %q) unexpectedly succeeded", test.version, test.comment)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := [3]int{major, minor, patch}; got != test.want {
				t.Fatalf("version = %v, want %v", got, test.want)
			}
		})
	}
}

func TestMinimumOracleMySQLVersion(t *testing.T) {
	for _, test := range []struct {
		version string
		ok      bool
	}{
		{version: "8.0.15", ok: false},
		{version: "8.0.16", ok: true},
		{version: "8.4.0", ok: true},
	} {
		major, minor, patch, err := parseOracleMySQLVersion(test.version, "MySQL Community Server - GPL")
		if err != nil {
			t.Fatal(err)
		}
		got := major > 8 || major == 8 && (minor > 0 || minor == 0 && patch >= 16)
		if got != test.ok {
			t.Fatalf("support for %s = %t, want %t", test.version, got, test.ok)
		}
	}
}

package migrate

import (
	"fmt"
	"strings"
	"testing"
	"testing/fstest"
)

func TestLoadIncludesImmutableBaseline(t *testing.T) {
	migrations, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != 2 {
		t.Fatalf("managed migrations=%d, want 2", len(migrations))
	}
	baseline := migrations[0]
	if baseline.Filename != "20260718_150000_schema_baseline.sql" {
		t.Fatalf("baseline filename=%q", baseline.Filename)
	}
	const expectedChecksum = "dedd18602a649315cb22b036f42501a016fe1f6ded0e7c132a6a8636fd2c3f8f"
	if baseline.Checksum != expectedChecksum {
		t.Fatalf("baseline checksum=%s, want %s", baseline.Checksum, expectedChecksum)
	}
	if migrations[1].Filename != "20260718_163622_drop_legacy_gateway_tables.sql" {
		t.Fatalf("cleanup migration filename=%q", migrations[1].Filename)
	}
	const expectedCleanupChecksum = "b15775816c3c74bb416dfc1aeb8d1d74014e7498c2dcd2525d876dcd86c96559"
	if migrations[1].Checksum != expectedCleanupChecksum {
		t.Fatalf("cleanup checksum=%s, want %s", migrations[1].Checksum, expectedCleanupChecksum)
	}
}

func TestBaselineContainsEveryRequiredApplicationTable(t *testing.T) {
	migrations, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	baseline := migrations[0].SQL
	for _, table := range requiredApplicationTables {
		statement := fmt.Sprintf("CREATE TABLE IF NOT EXISTS `%s`", table)
		if !strings.Contains(baseline, statement) {
			t.Errorf("baseline is missing %s", table)
		}
	}
	if count := strings.Count(baseline, "CREATE TABLE IF NOT EXISTS"); count != len(requiredApplicationTables) {
		t.Fatalf("baseline table count=%d, want %d", count, len(requiredApplicationTables))
	}
	if !strings.Contains(baseline, "idx_conversations_canonical_match") {
		t.Fatal("baseline is missing the canonical conversation match index")
	}
}

func TestLoadSortsManagedMigrationsAndIgnoresLegacyFiles(t *testing.T) {
	filesystem := fstest.MapFS{
		"20260718_120000_legacy.sql":          {Data: []byte("SELECT 1;")},
		"20260718_150000_schema_baseline.sql": {Data: []byte("CREATE TABLE example (id BIGINT);")},
		"20260719_100000_add_example.sql":     {Data: []byte("ALTER TABLE example ADD name TEXT;")},
	}
	migrations, err := loadFromFS(filesystem)
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != 2 || migrations[0].Version != "20260718_150000" || migrations[1].Version != "20260719_100000" {
		t.Fatalf("unexpected migrations: %#v", migrations)
	}
}

func TestLoadRejectsMissingBaseline(t *testing.T) {
	filesystem := fstest.MapFS{
		"20260719_100000_add_example.sql": {Data: []byte("SELECT 1;")},
	}
	if _, err := loadFromFS(filesystem); err == nil {
		t.Fatal("expected missing baseline to fail")
	}
}

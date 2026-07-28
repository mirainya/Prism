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
	if len(migrations) != 6 {
		t.Fatalf("managed migrations=%d, want 6", len(migrations))
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
	if migrations[2].Filename != "20260726_170000_endpoints_model_fk_on_update_cascade.sql" {
		t.Fatalf("fk migration filename=%q", migrations[2].Filename)
	}
	const expectedFKChecksum = "515d4206fef899add83d7f9fe70406cfa7b8e39b4ccf8e0eb16cd904e2c89c32"
	if migrations[2].Checksum != expectedFKChecksum {
		t.Fatalf("fk checksum=%s, want %s", migrations[2].Checksum, expectedFKChecksum)
	}
	if migrations[3].Filename != "20260727_180000_add_endpoint_accounts.sql" {
		t.Fatalf("endpoint account migration filename=%q", migrations[3].Filename)
	}
	const expectedEndpointAccountChecksum = "ef5ad77bd507db2bfcf56bfd09d60b6d71e9c28186cbcbcce19cea3192364cd3"
	if migrations[3].Checksum != expectedEndpointAccountChecksum {
		t.Fatalf("endpoint account checksum=%s, want %s", migrations[3].Checksum, expectedEndpointAccountChecksum)
	}
	if migrations[4].Filename != "20260727_210000_fix_duomi_gpt_image2_input_mapping.sql" {
		t.Fatalf("duomi image mapping migration filename=%q", migrations[4].Filename)
	}
	const expectedDuomiImageMappingChecksum = "a170e61d8334ce3ce634be572e0b6ea3240dfcb505beb3318f5bca821a8258ad"
	if migrations[4].Checksum != expectedDuomiImageMappingChecksum {
		t.Fatalf("duomi image mapping checksum=%s, want %s", migrations[4].Checksum, expectedDuomiImageMappingChecksum)
	}
	if migrations[5].Filename != "20260728_181500_unify_image_endpoint_supports_stream.sql" {
		t.Fatalf("image stream support migration filename=%q", migrations[5].Filename)
	}
	const expectedImageStreamSupportChecksum = "be762ef3b036c0115aa1c740cf580b9108eab49e122fa233b2df4c18fca8a189"
	if migrations[5].Checksum != expectedImageStreamSupportChecksum {
		t.Fatalf("image stream support checksum=%s, want %s", migrations[5].Checksum, expectedImageStreamSupportChecksum)
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

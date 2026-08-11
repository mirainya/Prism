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
	if len(migrations) != 33 {
		t.Fatalf("managed migrations=%d, want 33", len(migrations))
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
	if migrations[6].Filename != "20260801_120000_add_task_route_operation.sql" {
		t.Fatalf("task route operation migration filename=%q", migrations[6].Filename)
	}
	const expectedTaskRouteOperationChecksum = "d8052bc45ff8bc1ee1e173b1deafbecf3cf6660e7e52d29db541db3f96f34cc1"
	if migrations[6].Checksum != expectedTaskRouteOperationChecksum {
		t.Fatalf("task route operation checksum=%s, want %s", migrations[6].Checksum, expectedTaskRouteOperationChecksum)
	}
	if migrations[7].Filename != "20260801_130000_add_endpoint_adapters.sql" {
		t.Fatalf("endpoint adapters migration filename=%q", migrations[7].Filename)
	}
	const expectedEndpointAdaptersChecksum = "b090f08b73e7f02857122561bfe0b008ca02ecb1185c75144a8da13ef99115a9"
	if migrations[7].Checksum != expectedEndpointAdaptersChecksum {
		t.Fatalf("endpoint adapters checksum=%s, want %s", migrations[7].Checksum, expectedEndpointAdaptersChecksum)
	}
	if migrations[8].Filename != "20260801_140000_add_task_endpoint_snapshot.sql" {
		t.Fatalf("task endpoint snapshot migration filename=%q", migrations[8].Filename)
	}
	const expectedTaskEndpointSnapshotChecksum = "9fd25378c18aad689114c45144c901fa66cabd452123ce2040c469c94af2b1be"
	if migrations[8].Checksum != expectedTaskEndpointSnapshotChecksum {
		t.Fatalf("task endpoint snapshot checksum=%s, want %s", migrations[8].Checksum, expectedTaskEndpointSnapshotChecksum)
	}
	if migrations[9].Filename != "20260801_150000_repair_conversation_call_id.sql" {
		t.Fatalf("conversation call ID repair migration filename=%q", migrations[9].Filename)
	}
	const expectedConversationCallIDRepairChecksum = "1beee3abf6311b06e3e2776677681e1e741c1850da93dcb7dcc2d3daaa00abac"
	if migrations[9].Checksum != expectedConversationCallIDRepairChecksum {
		t.Fatalf("conversation call ID repair checksum=%s, want %s", migrations[9].Checksum, expectedConversationCallIDRepairChecksum)
	}
	if migrations[10].Filename != "20260801_160000_add_endpoint_route_operation.sql" {
		t.Fatalf("endpoint route operation migration filename=%q", migrations[10].Filename)
	}
	const expectedEndpointRouteOperationChecksum = "62ffd3837908d7ff779934d656aa3f5f7d47369deb6122f722fbeeb56db627b1"
	if migrations[10].Checksum != expectedEndpointRouteOperationChecksum {
		t.Fatalf("endpoint route operation checksum=%s, want %s", migrations[10].Checksum, expectedEndpointRouteOperationChecksum)
	}
	if migrations[11].Filename != "20260803_120000_add_endpoint_origin.sql" {
		t.Fatalf("endpoint origin migration filename=%q", migrations[11].Filename)
	}
	const expectedEndpointOriginChecksum = "dd1e4e6e8fc1859d1ebdf7e6422af7579f1b72aec42414baa8863551b2bad05e"
	if migrations[11].Checksum != expectedEndpointOriginChecksum {
		t.Fatalf("endpoint origin checksum=%s, want %s", migrations[11].Checksum, expectedEndpointOriginChecksum)
	}
	if migrations[12].Filename != "20260803_180000_add_endpoint_supported_operations.sql" {
		t.Fatalf("endpoint supported operations migration filename=%q", migrations[12].Filename)
	}
	const expectedEndpointSupportedOperationsChecksum = "ef67b78f84c3e5e9441785d9f81de3bc511e3f4b006bd60b22ba5320015077e6"
	if migrations[12].Checksum != expectedEndpointSupportedOperationsChecksum {
		t.Fatalf("endpoint supported operations checksum=%s, want %s", migrations[12].Checksum, expectedEndpointSupportedOperationsChecksum)
	}
	if migrations[13].Filename != "20260803_183000_normalize_image_endpoint_param_schema.sql" {
		t.Fatalf("image parameter schema migration filename=%q", migrations[13].Filename)
	}
	if migrations[14].Filename != "20260805_120000_add_video_engine_tables.sql" {
		t.Fatalf("video engine migration filename=%q", migrations[14].Filename)
	}
	if migrations[15].Filename != "20260805_180000_integrate_video_calls_and_assets.sql" {
		t.Fatalf("video integration migration filename=%q", migrations[15].Filename)
	}
	if migrations[16].Filename != "20260805_181000_unique_video_asset_hash.sql" {
		t.Fatalf("video asset uniqueness migration filename=%q", migrations[16].Filename)
	}
	if migrations[17].Filename != "20260807_120000_add_video_worker_reliability.sql" {
		t.Fatalf("video worker reliability migration filename=%q", migrations[17].Filename)
	}
	if migrations[18].Filename != "20260807_130000_configure_seedance_protocol.sql" {
		t.Fatalf("seedance protocol migration filename=%q", migrations[18].Filename)
	}
	if migrations[19].Filename != "20260807_150000_enable_sub2_r2_assets.sql" {
		t.Fatalf("sub2 R2 migration filename=%q", migrations[19].Filename)
	}
	if migrations[20].Filename != "20260807_160000_generalize_presigned_asset_resolver.sql" {
		t.Fatalf("presigned upload migration filename=%q", migrations[20].Filename)
	}
	if migrations[21].Filename != "20260807_170000_generic_video_adapter.sql" {
		t.Fatalf("generic video adapter migration filename=%q", migrations[21].Filename)
	}
	genericVideoSQL := migrations[21].SQL
	for _, fragment := range []string{
		"SET adapter_type = 'generic'",
		"'profile', 'json_task_v1'",
		"'success_code_optional', CAST('true' AS JSON)",
		"'disabled_models', JSON_ARRAY('seedance-2.5')",
		"'provider_object', 'storage_object_id'",
		"JSON_UNQUOTE(JSON_EXTRACT(extra_config, '$.protocol')) = 'sub2api'",
	} {
		if !strings.Contains(genericVideoSQL, fragment) {
			t.Errorf("generic video adapter migration is missing %q", fragment)
		}
	}
	if migrations[22].Filename != "20260808_120000_enable_generic_video_estimate.sql" {
		t.Fatalf("generic video estimate migration filename=%q", migrations[22].Filename)
	}
	estimateSQL := migrations[22].SQL
	for _, fragment := range []string{
		"'$.mode', 'upstream_estimate'",
		"'$.adapter.estimate'",
		"'/v1/video-generations/estimate'",
		"'$.adapter.response.estimated_cost_paths'",
		"asset_resolver = 'presigned_upload'",
	} {
		if !strings.Contains(estimateSQL, fragment) {
			t.Errorf("generic video estimate migration is missing %q", fragment)
		}
	}
	if migrations[23].Filename != "20260808_180000_fix_h_channel_seedance20_resolution.sql" {
		t.Fatalf("H channel resolution migration filename=%q", migrations[23].Filename)
	}
	hChannelResolutionSQL := migrations[23].SQL
	for _, fragment := range []string{
		`"seedance-2.0".resolutions`,
		"JSON_ARRAY('1080p')",
		"sub2api.0x0.fan/api%",
	} {
		if !strings.Contains(hChannelResolutionSQL, fragment) {
			t.Errorf("H channel resolution migration is missing %q", fragment)
		}
	}
	if migrations[24].Filename != "20260810_120000_fix_sub2_seedance_duration_bounds.sql" {
		t.Fatalf("Sub2 duration migration filename=%q", migrations[24].Filename)
	}
	sub2DurationSQL := migrations[24].SQL
	for _, fragment := range []string{
		`"seedance-2.0".duration_min`,
		`"seedance-2.0-fast".duration_min`,
		"adapter_type = 'generic'",
		"IS NOT NULL",
	} {
		if !strings.Contains(sub2DurationSQL, fragment) {
			t.Errorf("Sub2 duration migration is missing %q", fragment)
		}
	}
	if migrations[25].Filename != "20260810_190000_add_seedance_official_channel.sql" {
		t.Fatalf("official Seedance migration filename=%q", migrations[25].Filename)
	}
	officialSeedanceSQL := migrations[25].SQL
	for _, fragment := range []string{
		"'官满血-Seedance'",
		"JSON_ARRAY('seedance-2.0', 'seedance-2.0-fast')",
		"'first_frame', CAST('true' AS JSON)",
		"'web_search', CAST('true' AS JSON)",
		"'resolutions', JSON_ARRAY('480p', '720p', '1080p', '4k')",
		"API keys are provisioned separately",
	} {
		if !strings.Contains(officialSeedanceSQL, fragment) {
			t.Errorf("official Seedance migration is missing %q", fragment)
		}
	}
	if migrations[26].Filename != "20260811_150000_migrate_mirainya_grok_video.sql" {
		t.Fatalf("MiraiNya Grok migration filename=%q", migrations[26].Filename)
	}
	for _, fragment := range []string{
		"'MiraiNya Grok Video'",
		`"content_projections"`,
		`"models": ["grok-imagine-video-1.5"]`,
		"JOIN endpoint_accounts ea",
	} {
		if !strings.Contains(migrations[26].SQL, fragment) {
			t.Errorf("MiraiNya Grok migration is missing %q", fragment)
		}
	}
	if migrations[27].Filename != "20260811_151000_migrate_xingjing_grok_video.sql" {
		t.Fatalf("Xingjing Grok migration filename=%q", migrations[27].Filename)
	}
	for _, fragment := range []string{
		"'Xingjing Grok Video'",
		`"output": "array"`,
		`"duration": "seconds"`,
		"JOIN endpoint_accounts ea",
	} {
		if !strings.Contains(migrations[27].SQL, fragment) {
			t.Errorf("Xingjing Grok migration is missing %q", fragment)
		}
	}
	if migrations[28].Filename != "20260811_152000_disable_legacy_video_endpoints.sql" {
		t.Fatalf("legacy video disable migration filename=%q", migrations[28].Filename)
	}
	for _, modelCode := range []string{
		"Google_Veo", "sora2", "grok-imagine-video", "grok-imagine-video-1.5", "grok-video-1.5-preview",
	} {
		if !strings.Contains(migrations[28].SQL, modelCode) {
			t.Errorf("legacy video disable migration is missing %q", modelCode)
		}
	}
	if migrations[29].Filename != "20260811_160000_drop_unused_video_passthrough.sql" {
		t.Fatalf("video passthrough cleanup migration filename=%q", migrations[29].Filename)
	}
	if !strings.Contains(migrations[29].SQL, "DROP COLUMN passthrough") {
		t.Fatal("video passthrough cleanup migration does not drop the unused column")
	}
	if migrations[30].Filename != "20260811_170000_configure_seedance_playground_options.sql" {
		t.Fatalf("video playground options migration filename=%q", migrations[30].Filename)
	}
	for _, fragment := range []string{
		`"seedance-2.5".parameters`,
		"'name', 'priority'",
		"'type', 'select'",
		"require_visual_media_with_audio",
	} {
		if !strings.Contains(migrations[30].SQL, fragment) {
			t.Errorf("video playground options migration is missing %q", fragment)
		}
	}
	if migrations[31].Filename != "20260811_180000_add_model_aliases.sql" {
		t.Fatalf("model aliases migration filename=%q", migrations[31].Filename)
	}
	if !strings.Contains(migrations[31].SQL, "ADD COLUMN `aliases` JSON") {
		t.Fatal("model aliases migration does not add the aliases column")
	}
	if migrations[32].Filename != "20260811_190000_archive_legacy_video_models.sql" {
		t.Fatalf("legacy video archive migration filename=%q", migrations[32].Filename)
	}
	if !strings.Contains(migrations[32].SQL, "SET `status` = 0") {
		t.Fatal("legacy video archive migration does not disable archived models")
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

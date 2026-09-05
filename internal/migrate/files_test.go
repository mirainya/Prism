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
	if len(migrations) != 62 {
		t.Fatalf("managed migrations=%d, want 62", len(migrations))
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
	if migrations[33].Filename != "20260811_200000_update_sub2_seedance_v23.sql" {
		t.Fatalf("Sub2 v2.3 migration filename=%q", migrations[33].Filename)
	}
	for _, fragment := range []string{
		"/v1/video-generations/capabilities",
		"'video_extension'",
		"'forbidden_parameters'",
		"2026-08-14T00:00:00+08:00",
	} {
		if !strings.Contains(migrations[33].SQL, fragment) {
			t.Errorf("Sub2 v2.3 migration is missing %q", fragment)
		}
	}
	if migrations[34].Filename != "20260812_120000_add_video_route_plan.sql" {
		t.Fatalf("video route plan migration filename=%q", migrations[34].Filename)
	}
	for _, fragment := range []string{
		"ADD COLUMN vendor_model VARCHAR(120)",
		"SET vendor_model = model",
		"ADD COLUMN route_plan JSON",
	} {
		if !strings.Contains(migrations[34].SQL, fragment) {
			t.Errorf("video route plan migration is missing %q", fragment)
		}
	}
	if migrations[35].Filename != "20260812_180000_restore_configured_video_models.sql" {
		t.Fatalf("video model repair migration filename=%q", migrations[35].Filename)
	}
	for _, fragment := range []string{
		"JSON_ARRAY_APPEND",
		`validation.models."seedance-2.5"`,
		"NOT JSON_CONTAINS",
	} {
		if !strings.Contains(migrations[35].SQL, fragment) {
			t.Errorf("video model repair migration is missing %q", fragment)
		}
	}
	if migrations[36].Filename != "20260813_120000_repair_h_seedance_v23_channel.sql" {
		t.Fatalf("H Seedance repair migration filename=%q", migrations[36].Filename)
	}
	for _, fragment := range []string{
		"hig逆-Seedance",
		"'duration_max', 20",
		"'video_extension'",
		"'allow_generated_audio', CAST('true' AS JSON)",
		"'$.adapter.local_cancel.disabled_models', JSON_ARRAY()",
		"2026-08-14T00:00:00+08:00",
	} {
		if !strings.Contains(migrations[36].SQL, fragment) {
			t.Errorf("H Seedance repair migration is missing %q", fragment)
		}
	}
	if migrations[37].Filename != "20260820_170000_update_sub2_seedance_v27.sql" {
		t.Fatalf("Sub2 v2.7 migration filename=%q", migrations[37].Filename)
	}
	for _, fragment := range []string{
		"'$.adapter.request.fields.task_mode', 'provider_mode'",
		"'video_edit', 'video_edit'",
		"'source_video', 'extension_source'",
		"'$.asset_resolver.idempotency_body_field', 'idempotency_key'",
		"'duration_max_with_video_reference', 18",
		"'h_channel_points_vip'",
		`validation.models."seedance-2.5".require_media`,
	} {
		if !strings.Contains(migrations[37].SQL, fragment) {
			t.Errorf("Sub2 v2.7 migration is missing %q", fragment)
		}
	}
	if migrations[38].Filename != "20260820_183000_add_video_service_tiers.sql" {
		t.Fatalf("video service tier migration filename=%q", migrations[38].Filename)
	}
	for _, fragment := range []string{
		"service_tier VARCHAR(24)",
		"provider_metadata JSON",
		"h_channel_priority_queue",
		"h_channel_points_vip",
		"priority-queue",
	} {
		if !strings.Contains(migrations[38].SQL, fragment) {
			t.Errorf("video service tier migration is missing %q", fragment)
		}
	}
	if migrations[39].Filename != "20260824_110000_add_autodl_minimax_h3_workflow.sql" {
		t.Fatalf("AutoDL workflow migration filename=%q", migrations[39].Filename)
	}
	for _, fragment := range []string{
		"minimax_h3_image_audio_to_video_v2_15s",
		`"auth_prefix": ""`,
		`"video_url_paths": ["data.results.0.url"]`,
		`"target":"ref_image_8"`,
		`"target":"ref_audio_2"`,
		`"result_storage": {"enabled": true}`,
	} {
		if !strings.Contains(migrations[39].SQL, fragment) {
			t.Errorf("AutoDL workflow migration is missing %q", fragment)
		}
	}
	if migrations[40].Filename != "20260824_132000_allow_autodl_result_host.sql" {
		t.Fatalf("AutoDL result host migration filename=%q", migrations[40].Filename)
	}
	if migrations[41].Filename != "20260825_171500_use_direct_image_upstream.sql" {
		t.Fatalf("direct image upstream migration filename=%q", migrations[41].Filename)
	}
	if migrations[42].Filename != "20260825_172000_extend_gpt_image2_c_timeout.sql" {
		t.Fatalf("image timeout migration filename=%q", migrations[42].Filename)
	}
	if migrations[43].Filename != "20260825_174500_enable_duomi_image_edit.sql" {
		t.Fatalf("Duomi image edit migration filename=%q", migrations[43].Filename)
	}
	if migrations[44].Filename != "20260827_175629_disable_unverified_sub2_video_cancel.sql" {
		t.Fatalf("Sub2 cancel policy migration filename=%q", migrations[44].Filename)
	}
	for _, fragment := range []string{
		"'$.cancel', CAST('false' AS JSON)",
		"'$.adapter.cancel.enabled', CAST('false' AS JSON)",
		"'$.adapter.local_cancel.enabled', CAST('true' AS JSON)",
	} {
		if !strings.Contains(migrations[44].SQL, fragment) {
			t.Errorf("Sub2 cancel policy migration is missing %q", fragment)
		}
	}
	if migrations[45].Filename != "20260827_180000_formalize_video_channel_settings.sql" {
		t.Fatalf("video channel settings migration filename=%q", migrations[45].Filename)
	}
	for _, fragment := range []string{
		"ADD COLUMN adapter_profile",
		"ADD COLUMN cancel_mode",
		"ADD COLUMN pricing_mode",
		"ADD COLUMN result_storage_enabled",
		"JSON_EXTRACT(extra_config, '$.result_storage.enabled')",
	} {
		if !strings.Contains(migrations[45].SQL, fragment) {
			t.Errorf("video channel settings migration is missing %q", fragment)
		}
	}
	if migrations[46].Filename != "20260905_100000_unified_gateway_foundation.sql" {
		t.Fatalf("unified gateway foundation migration filename=%q", migrations[46].Filename)
	}
	const expectedUnifiedGatewayFoundationChecksum = "ef84439a80c4bcdb1c58791f877a2069f0053454e1981ba97f2928aff893b292"
	if migrations[46].Checksum != expectedUnifiedGatewayFoundationChecksum {
		t.Fatalf("unified gateway foundation checksum=%s, want %s", migrations[46].Checksum, expectedUnifiedGatewayFoundationChecksum)
	}
	if migrations[47].Filename != "20260905_110000_unified_gateway_crypto.sql" {
		t.Fatalf("unified gateway crypto migration filename=%q", migrations[47].Filename)
	}
	const expectedUnifiedGatewayCryptoChecksum = "f25f431d3d1b4471302a42b9ebedeb58271c1ddc482ec55d111aecd7547909ca"
	if migrations[47].Checksum != expectedUnifiedGatewayCryptoChecksum {
		t.Fatalf("unified gateway crypto checksum=%s, want %s", migrations[47].Checksum, expectedUnifiedGatewayCryptoChecksum)
	}
	if migrations[48].Filename != "20260905_120000_unified_gateway_credentials.sql" {
		t.Fatalf("unified gateway credentials migration filename=%q", migrations[48].Filename)
	}
	const expectedUnifiedGatewayCredentialsChecksum = "88ce1c6ad0540edf65d2bcc6e5dcb5c4cab62f5802e2779f5388206b89464385"
	if migrations[48].Checksum != expectedUnifiedGatewayCredentialsChecksum {
		t.Fatalf("unified gateway credentials checksum=%s, want %s", migrations[48].Checksum, expectedUnifiedGatewayCredentialsChecksum)
	}
	if migrations[49].Filename != "20260905_130000_unified_gateway_catalog_transport.sql" {
		t.Fatalf("unified gateway catalog transport migration filename=%q", migrations[49].Filename)
	}
	const expectedUnifiedGatewayCatalogTransportChecksum = "b89405c40cf040c9b6aeca769783623e8650bdc45145943208d309a541740125"
	if migrations[49].Checksum != expectedUnifiedGatewayCatalogTransportChecksum {
		t.Fatalf("unified gateway catalog transport checksum=%s, want %s", migrations[49].Checksum, expectedUnifiedGatewayCatalogTransportChecksum)
	}
	newMigrations := []struct {
		index    int
		filename string
		checksum string
	}{
		{50, "20260905_140000_unified_gateway_execution_core.sql", "9e5d6deaff4ed132dbccc91b7ae203e0ed6af1d09ee6b43d09ad6c192749a651"},
		{51, "20260905_150000_unified_gateway_request_logs.sql", "9eb7ac620fff8e55ee2618d8d0dfd949c0e71820f24b89555425162d8c0a8700"},
		{52, "20260905_160000_unified_gateway_async_identity.sql", "f4997cd79f2de34684c638e7812147720c7370243df46452e72bb161ca036220"},
		{53, "20260905_170000_unified_gateway_billing.sql", "e892f57963d0b05d9c02d3d29e8c6802ea6e4fe3f6dc565c70d084b15460e0c8"},
		{54, "20260905_180000_unified_gateway_control_plane.sql", "dab4ede65e1d0fb3c402fc73fc6ae465fddc503dd5f87a733a60fe1257a4b90a"},
		{55, "20260905_190000_unified_gateway_delivery.sql", "5de63df75f7fa80b428820d28f6a12a1d9cb33d94e1568a69d03a9362bab4dad"},
		{56, "20260905_200000_unified_gateway_resources.sql", "5e38c69d80b568aa55b711e413ba3b24c2b7640a5390cf68d132bb3a76a0c2a9"},
		{57, "20260905_210000_unified_gateway_pricing_validation.sql", "f789bffc578bf69c397b54522999c6bdfd35f3ea108f0a312d1f310137072fdb"},
		{58, "20260905_220000_unified_gateway_routing_policy_events.sql", "2534db692dcf2fb4cdfcb12d2f1d48deb84fd704fc0e81841164aa38117a537e"},
		{59, "20260905_230000_unified_gateway_operations_runtime_costs.sql", "27ded61b051a78758841c274f2a41a22c271207d043566255947c37ac767980a"},
		{60, "20260906_100000_unified_gateway_audit_events.sql", "18453fa359573bdcd6a345a865849526081d64cf87d4f9acf9e78a40b0fd4ac2"},
		{61, "20260906_110000_repair_legacy_import_transport.sql", "e29fdd7766f53a9aab09412a2d0405171b24cfe8fe81112688e912466308e1fa"},
	}
	for _, migration := range newMigrations {
		if migrations[migration.index].Filename != migration.filename || migrations[migration.index].Checksum != migration.checksum {
			t.Fatalf("migration[%d]=%s/%s, want %s/%s", migration.index, migrations[migration.index].Filename, migrations[migration.index].Checksum, migration.filename, migration.checksum)
		}
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

func TestUnifiedGatewayMigrationsContainOnlyTypedTargetFacts(t *testing.T) {
	migrations, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	var unified strings.Builder
	for _, migration := range migrations {
		if migration.Version >= "20260905_100000" {
			unified.WriteString(migration.SQL)
		}
	}
	sql := unified.String()
	for _, table := range []string{
		"gw_models", "gw_operation_contracts", "gateway_channels", "gw_catalog_releases",
		"gw_credential_pools", "gw_credentials", "gw_credential_versions", "encrypted_blobs",
		"gw_channel_transports", "gw_products", "gw_product_transports", "gw_offerings", "gw_routes",
		"gw_api_calls", "gw_api_call_attempts", "gw_async_executions", "gw_channel_request_logs",
		"gw_credential_slots", "gw_upstream_task_identities", "gw_async_outbox",
		"billing_accounts", "billing_reservations", "billing_events", "ledger_transactions", "ledger_entries",
		"gw_media_assets", "gw_result_deliveries", "gw_result_delivery_sources", "gw_callback_targets",
		"gw_callback_deliveries", "gw_capability_tasks", "gw_video_tasks", "gw_provider_state_refs",
		"gw_control_plane_runs", "gw_runtime_requirements", "gw_execution_health", "gw_upstream_cost_events",
		"gw_credential_purpose_grant_state_events", "gw_credential_version_state_events", "gw_catalog_release_state_events",
		"gw_routing_policy_version_events", "billing_account_state_events",
	} {
		if !strings.Contains(sql, "CREATE TABLE IF NOT EXISTS `"+table+"`") {
			t.Errorf("unified migrations are missing target table %s", table)
		}
	}
	for _, forbidden := range []string{"`api_key`", "`request_body`", "`response_body`", "`provider_response`", "`callback_url`"} {
		if strings.Contains(sql, forbidden) {
			t.Errorf("unified migration contains forbidden plaintext field %s", forbidden)
		}
	}
	for _, nonIdempotent := range []string{"ALTER TABLE ", "CREATE TRIGGER ", "DROP TABLE ", "DROP COLUMN "} {
		if strings.Contains(strings.ToUpper(sql), nonIdempotent) {
			t.Errorf("unified migration contains non-idempotent operation %q", nonIdempotent)
		}
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

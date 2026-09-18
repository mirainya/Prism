package service

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "github.com/glebarez/go-sqlite"
)

func TestListAvailableCapabilitiesUsesOnlyExecutableCatalogRoutes(t *testing.T) {
	db := setupPublicCatalogQueryDB(t)
	service := newQueryServiceWithDB(db)

	items, err := service.ListAvailableCapabilities(context.Background(), "openai-main", "chat")
	if err != nil {
		t.Fatalf("list available capabilities: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("capability count = %d, want 1: %#v", len(items), items)
	}
	item := items[0]
	if item.ID != "gpt-4.1" || item.ModelCode != "gpt_4_1" || item.Type != "chat" {
		t.Fatalf("unexpected model projection: %#v", item)
	}
	if item.Group != "" || item.MaxTokens != 0 || item.Thinking != nil {
		t.Fatalf("legacy-only metadata leaked into projection: %#v", item)
	}
	if !item.SupportsStream || !item.SupportsTools || item.SupportsMultimodal {
		t.Fatalf("unexpected feature projection: %#v", item.Features)
	}
	if item.DefaultStream {
		t.Fatal("stream support must not invent a catalog default")
	}
	if item.Availability == nil || item.Availability.SuccessRate != "98.7" || item.Availability.Source != "upstream" {
		t.Fatalf("unexpected availability: %#v", item.Availability)
	}
	if len(item.Operations) != 2 {
		t.Fatalf("operation count = %d, want 2: %#v", len(item.Operations), item.Operations)
	}
	if item.Operations[0].ID != "chat.completions" || item.Operations[0].Path != "/v1/chat/completions" {
		t.Fatalf("unexpected first operation: %#v", item.Operations[0])
	}
	if len(item.Operations[0].SKUs) != 1 || item.Operations[0].SKUs[0].Code != "chat-standard" {
		t.Fatalf("duplicate or missing SKU projection: %#v", item.Operations[0].SKUs)
	}
	if item.Operations[1].ID != "responses.create" || item.Operations[1].Path != "/v1/responses" {
		t.Fatalf("unexpected second operation: %#v", item.Operations[1])
	}
	if len(item.Channels) != 3 || len(item.Transports) != 1 || item.Transports[0] != "openai" {
		t.Fatalf("unexpected route projection: channels=%#v transports=%#v", item.Channels, item.Transports)
	}

	channels, err := service.ListAvailableChannels(context.Background())
	if err != nil {
		t.Fatalf("list available channels: %v", err)
	}
	if len(channels) != 1 || channels[0] != "openai-main" {
		t.Fatalf("unexpected channels: %#v", channels)
	}
	images, err := service.ListAvailableCapabilities(context.Background(), "", "image")
	if err != nil || len(images) != 0 {
		t.Fatalf("image filter returned %#v, error=%v", images, err)
	}
}

func TestListAvailableCapabilitiesPublishesOnlyPrimaryModelName(t *testing.T) {
	db := setupPublicCatalogQueryDB(t)
	for _, statement := range []string{
		`INSERT INTO gw_model_names VALUES (12,1,'legacy-gpt-4.1')`,
		`INSERT INTO gw_catalog_model_names VALUES (1,10,1,12,0)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}

	items, err := newQueryServiceWithDB(db).ListAvailableCapabilities(context.Background(), "", "chat")
	if err != nil {
		t.Fatalf("list available capabilities: %v", err)
	}
	if len(items) != 1 || items[0].ID != "gpt-4.1" {
		t.Fatalf("public capabilities exposed a non-primary model name: %#v", items)
	}
}

func TestListAvailableCapabilitiesRejectsInvalidServiceTiers(t *testing.T) {
	db := setupPublicCatalogQueryDB(t)
	if _, err := db.Exec(`UPDATE gw_skus SET service_tiers='[]' WHERE id=30`); err != nil {
		t.Fatal(err)
	}
	_, err := newQueryServiceWithDB(db).ListAvailableCapabilities(context.Background(), "", "chat")
	if err == nil || !strings.Contains(err.Error(), "invalid service tiers") {
		t.Fatalf("error = %v, want invalid service tiers", err)
	}
}

func TestListAvailableCapabilitiesUsesDeclaredDownstreamPaths(t *testing.T) {
	db := setupPublicCatalogQueryDB(t)
	for _, statement := range []string{
		`DELETE FROM gw_sku_downstream_paths WHERE path='/v1/responses'`,
		`INSERT INTO gw_operation_contracts VALUES (102,'messages.create','active')`,
		`INSERT INTO gw_operation_routes VALUES (102,'POST','/v1/messages')`,
		`INSERT INTO gw_sku_downstream_paths VALUES (1,30,'/v1/messages')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}

	items, err := newQueryServiceWithDB(db).ListAvailableCapabilities(context.Background(), "", "chat")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || len(items[0].Operations) != 2 {
		t.Fatalf("unexpected downstream operations: %#v", items)
	}
	if items[0].Operations[0].ID != "chat.completions" || items[0].Operations[1].ID != "messages.create" {
		t.Fatalf("operations did not follow downstream paths: %#v", items[0].Operations)
	}
	if items[0].Operations[1].Path != "/v1/messages" || len(items[0].Operations[1].SKUs) != 1 || items[0].Operations[1].SKUs[0].ID != 30 {
		t.Fatalf("downstream alias did not retain its SKU: %#v", items[0].Operations[1])
	}
}

func TestListAvailableCapabilitiesPrefersNativeSKUOverProtocolAliases(t *testing.T) {
	db := setupPublicCatalogQueryDB(t)
	for _, statement := range []string{
		`INSERT INTO gw_sku_downstream_paths VALUES (1,30,'/v1/responses')`,
		`INSERT INTO gw_sku_downstream_paths VALUES (1,31,'/v1/chat/completions')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}

	items, err := newQueryServiceWithDB(db).ListAvailableCapabilities(context.Background(), "", "chat")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || len(items[0].Operations) != 2 {
		t.Fatalf("unexpected operation projection: %#v", items)
	}
	for _, operation := range items[0].Operations {
		if len(operation.SKUs) != 1 {
			t.Fatalf("operation %s exposes alias SKUs: %#v", operation.ID, operation.SKUs)
		}
		if operation.ID == "chat.completions" && operation.SKUs[0].ID != 30 {
			t.Fatalf("chat operation SKU=%d, want 30", operation.SKUs[0].ID)
		}
		if operation.ID == "responses.create" && operation.SKUs[0].ID != 31 {
			t.Fatalf("responses operation SKU=%d, want 31", operation.SKUs[0].ID)
		}
	}
}

func TestListAvailableCapabilitiesExcludesUnexecutableCatalogRoutes(t *testing.T) {
	for _, test := range []struct {
		name   string
		change string
	}{
		{name: "unpublished release", change: `UPDATE gw_catalog_releases SET status='draft'`},
		{name: "inactive offering", change: `UPDATE gw_offering_runtime_state SET state='disabled'`},
		{name: "inactive channel", change: `UPDATE gateway_channels SET status='disabled'`},
		{name: "inactive credential", change: `UPDATE gw_credentials SET status='disabled'`},
		{name: "missing execution grant", change: `UPDATE gw_credential_purpose_grants SET status='revoked'`},
		{name: "inactive credential version", change: `UPDATE gw_credential_versions SET status='retired'`},
		{name: "noncurrent key", change: `UPDATE crypto_key_versions SET status='readable'`},
		{name: "missing key wrap", change: `DELETE FROM encrypted_blob_key_wraps`},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := setupPublicCatalogQueryDB(t)
			if _, err := db.Exec(test.change); err != nil {
				t.Fatal(err)
			}
			items, err := newQueryServiceWithDB(db).ListAvailableCapabilities(context.Background(), "", "")
			if err != nil || len(items) != 0 {
				t.Fatalf("items = %#v, error = %v", items, err)
			}
		})
	}
}

func TestListAvailableCapabilitiesDoesNotRequireLegacyValidations(t *testing.T) {
	db := setupPublicCatalogQueryDB(t)
	for _, statement := range []string{
		`DELETE FROM gw_credential_validation_events`,
		`DELETE FROM gw_credential_entitlement_state`,
		`DELETE FROM gw_commercial_validation_events`,
		`DELETE FROM gw_commercial_state`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	items, err := newQueryServiceWithDB(db).ListAvailableCapabilities(context.Background(), "", "")
	if err != nil || len(items) != 1 {
		t.Fatalf("items = %#v, error = %v", items, err)
	}
}

func TestListAvailableCapabilitiesUsesProviderDescriptionWhenCatalogDescriptionIsEmpty(t *testing.T) {
	db := setupPublicCatalogQueryDB(t)
	if _, err := db.Exec(`UPDATE gw_catalog_models SET description='' WHERE id=10`); err != nil {
		t.Fatal(err)
	}
	items, err := newQueryServiceWithDB(db).ListAvailableCapabilities(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Description != "AiCost model description" {
		t.Fatalf("provider description was not projected: %#v", items)
	}
}

func TestPricingUsesSKUComponentsWithoutRouteMultiplication(t *testing.T) {
	db := setupPublicCatalogQueryDB(t)
	pricing := &PricingService{query: newQueryServiceWithDB(db)}

	items, err := pricing.GetPricing(context.Background())
	if err != nil {
		t.Fatalf("get pricing: %v", err)
	}
	if len(items) != 1 || len(items[0].SKUs) != 2 {
		t.Fatalf("unexpected pricing projection: %#v", items)
	}
	if items[0].Code != "gpt-4.1" || items[0].ModelCode != "gpt-4.1" {
		t.Fatalf("pricing exposed internal model identity: %#v", items[0])
	}
	chatSKU := items[0].SKUs[0]
	if chatSKU.Code != "chat-standard" || len(chatSKU.Routes) != 1 || len(chatSKU.Components) != 2 {
		t.Fatalf("route or rate components multiplied: %#v", chatSKU)
	}
	if chatSKU.Currency.Code != "USD" || chatSKU.Currency.FractionDigits != 8 {
		t.Fatalf("unexpected currency: %#v", chatSKU.Currency)
	}
	if chatSKU.Components[0].UnitPrice != "0.000003" || chatSKU.Components[1].UnitPrice != "0.000012" {
		t.Fatalf("decimal price changed: %#v", chatSKU.Components)
	}
}

func TestPricingRejectsMissingSKUSchedule(t *testing.T) {
	db := setupPublicCatalogQueryDB(t)
	if _, err := db.Exec(`DELETE FROM gw_sell_rates WHERE sku_id=31`); err != nil {
		t.Fatal(err)
	}
	_, err := (&PricingService{query: newQueryServiceWithDB(db)}).GetPricing(context.Background())
	if err == nil || !strings.Contains(err.Error(), "has no valid sell schedule") {
		t.Fatalf("error = %v, want missing sell schedule", err)
	}
}

func TestPricingUsesProviderDescriptionWhenCatalogDescriptionIsEmpty(t *testing.T) {
	db := setupPublicCatalogQueryDB(t)
	if _, err := db.Exec(`UPDATE gw_catalog_models SET description='' WHERE id=10`); err != nil {
		t.Fatal(err)
	}
	items, err := (&PricingService{query: newQueryServiceWithDB(db)}).GetPricing(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Description != "AiCost model description" {
		t.Fatalf("provider description was not projected: %#v", items)
	}
}

func setupPublicCatalogQueryDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	statements := []string{
		`CREATE TABLE gw_catalog_runtime_state(id INTEGER PRIMARY KEY,active_release_id INTEGER)`,
		`CREATE TABLE gw_catalog_releases(id INTEGER PRIMARY KEY,status TEXT)`,
		`CREATE TABLE gw_catalog_models(id INTEGER PRIMARY KEY,release_id INTEGER,model_id INTEGER,sort_order INTEGER,display_name TEXT,description TEXT,visibility TEXT,capability_tags TEXT)`,
		`CREATE TABLE gw_models(id INTEGER PRIMARY KEY,model_code TEXT)`,
		`CREATE TABLE gw_catalog_model_names(release_id INTEGER,catalog_model_id INTEGER,model_id INTEGER,model_name_id INTEGER,is_primary INTEGER)`,
		`CREATE TABLE gw_model_names(id INTEGER PRIMARY KEY,model_id INTEGER,api_name TEXT)`,
		`CREATE TABLE gw_model_operations(id INTEGER PRIMARY KEY,release_id INTEGER,catalog_model_id INTEGER,operation_contract_id INTEGER)`,
		`CREATE TABLE gw_operation_contracts(id INTEGER PRIMARY KEY,operation_code TEXT,status TEXT)`,
		`CREATE TABLE gw_operation_routes(operation_contract_id INTEGER,http_method TEXT,route_template TEXT)`,
		`CREATE TABLE gw_skus(id INTEGER PRIMARY KEY,release_id INTEGER,model_operation_id INTEGER,sku_code TEXT,delivery_mode TEXT,max_results INTEGER,idempotency_mode TEXT,service_tiers TEXT)`,
		`CREATE TABLE gw_sku_downstream_paths(release_id INTEGER,sku_id INTEGER,path TEXT)`,
		`CREATE TABLE gw_routes(release_id INTEGER,sku_id INTEGER,offering_id INTEGER)`,
		`CREATE TABLE gw_offerings(id INTEGER PRIMARY KEY,release_id INTEGER,product_transport_id INTEGER,credential_pool_id INTEGER,entitlement_fingerprint TEXT,commercial_fingerprint TEXT)`,
		`CREATE TABLE gw_offering_runtime_state(release_id INTEGER,offering_id INTEGER,state TEXT)`,
		`CREATE TABLE gw_product_transports(id INTEGER PRIMARY KEY,release_id INTEGER,product_id INTEGER,channel_transport_id INTEGER,task_scope TEXT,cancel_mode TEXT)`,
		`CREATE TABLE gw_products(id INTEGER PRIMARY KEY,release_id INTEGER,channel_id INTEGER,vendor_model TEXT,capability_constraints TEXT)`,
		`CREATE TABLE gw_channel_transports(id INTEGER PRIMARY KEY,release_id INTEGER,channel_id INTEGER,protocol TEXT)`,
		`CREATE TABLE gateway_channels(id INTEGER PRIMARY KEY,channel_code TEXT,display_name TEXT,status TEXT)`,
		`CREATE TABLE gw_credential_pools(id INTEGER PRIMARY KEY,channel_id INTEGER,status TEXT)`,
		`CREATE TABLE gw_credentials(id INTEGER PRIMARY KEY,channel_id INTEGER,credential_pool_id INTEGER,status TEXT,secret_identity_id INTEGER,current_version_id INTEGER,secret TEXT)`,
		`CREATE TABLE gw_credential_secret_identities(id INTEGER PRIMARY KEY,channel_id INTEGER,status TEXT)`,
		`CREATE TABLE gw_credential_purpose_grants(credential_id INTEGER,purpose TEXT,status TEXT)`,
		`CREATE TABLE gw_credential_versions(id INTEGER PRIMARY KEY,credential_id INTEGER,secret_identity_id INTEGER,status TEXT,valid_until TEXT,encrypted_blob_id INTEGER)`,
		`CREATE TABLE gw_credential_validation_events(id INTEGER PRIMARY KEY,credential_id INTEGER,credential_version_id INTEGER,entitlement_fingerprint TEXT,state TEXT,valid_until TEXT)`,
		`CREATE TABLE gw_credential_entitlement_state(credential_id INTEGER,credential_version_id INTEGER,entitlement_fingerprint TEXT,state TEXT,latest_event_id INTEGER)`,
		`CREATE TABLE gw_commercial_validation_events(id INTEGER PRIMARY KEY,commercial_fingerprint TEXT,state TEXT,valid_until TEXT)`,
		`CREATE TABLE gw_commercial_state(commercial_fingerprint TEXT,state TEXT,latest_event_id INTEGER)`,
		`CREATE TABLE encrypted_blobs(id INTEGER PRIMARY KEY,keyring_id INTEGER,purged_at DATETIME)`,
		`CREATE TABLE crypto_keyring_state(id INTEGER PRIMARY KEY,current_version INTEGER)`,
		`CREATE TABLE crypto_key_versions(keyring_id INTEGER,key_version INTEGER,status TEXT)`,
		`CREATE TABLE encrypted_blob_key_wraps(encrypted_blob_id INTEGER,keyring_id INTEGER,kek_version INTEGER)`,
		`CREATE TABLE gw_sell_rates(id INTEGER PRIMARY KEY,release_id INTEGER,sku_id INTEGER,rate_evidence_review_event_id INTEGER,component_code TEXT,unit_code TEXT,quantity_source TEXT,charge_event TEXT,unit_price TEXT,unit_scale INTEGER,quantity_step TEXT,max_quantity TEXT,currency_code TEXT,currency_version INTEGER,pricing_mode TEXT DEFAULT 'flat',pricing_expr TEXT,max_price TEXT)`,
		`CREATE TABLE billing_system_state(id INTEGER PRIMARY KEY,currency_code TEXT,currency_version INTEGER)`,
		`CREATE TABLE billing_currency_definitions(id INTEGER PRIMARY KEY,currency_code TEXT,definition_version INTEGER,status TEXT,fraction_digits INTEGER,rounding_mode TEXT,max_amount TEXT)`,
		`CREATE TABLE gw_rate_evidence_review_events(id INTEGER PRIMARY KEY,rate_evidence_id INTEGER,decision TEXT)`,
		`CREATE TABLE gw_rate_evidence_review_state(id INTEGER PRIMARY KEY,rate_evidence_id INTEGER,latest_review_event_id INTEGER,state TEXT)`,
		`CREATE TABLE gw_api_calls(id INTEGER PRIMARY KEY,catalog_release_id INTEGER,sku_id INTEGER,status TEXT,created_at DATETIME)`,
		`CREATE TABLE gw_catalog_sources(id INTEGER PRIMARY KEY,channel_id INTEGER,status TEXT)`,
		`CREATE TABLE gw_upstream_availability(catalog_source_id INTEGER,model_code TEXT,success_rate TEXT,window_minutes INTEGER,observed_at DATETIME)`,
		`CREATE TABLE gw_catalog_source_profiles(catalog_source_id INTEGER,contract_code TEXT)`,
		`CREATE TABLE gw_catalog_release_sources(id INTEGER PRIMARY KEY,release_id INTEGER,catalog_source_id INTEGER)`,
		`CREATE TABLE gw_catalog_discovery_snapshots(id INTEGER PRIMARY KEY,release_source_id INTEGER,observed_at DATETIME)`,
		`CREATE TABLE gw_catalog_discovery_items(snapshot_id INTEGER,ordinal INTEGER,model_code TEXT,description TEXT,tags TEXT,selected_group_enabled INTEGER)`,
		`INSERT INTO gw_catalog_runtime_state VALUES (1,1)`,
		`INSERT INTO gw_catalog_releases VALUES (1,'published')`,
		`INSERT INTO gw_models VALUES (1,'gpt_4_1')`,
		`INSERT INTO gw_catalog_models VALUES (10,1,1,10,'GPT-4.1','General chat model','public','{"stream":true,"tools":true}')`,
		`INSERT INTO gw_model_names VALUES (11,1,'gpt-4.1')`,
		`INSERT INTO gw_catalog_model_names VALUES (1,10,1,11,1)`,
		`INSERT INTO gw_operation_contracts VALUES (100,'chat.completions','active'),(101,'responses.create','active')`,
		`INSERT INTO gw_operation_routes VALUES (100,'POST','/v1/chat/completions'),(101,'POST','/v1/responses')`,
		`INSERT INTO gw_model_operations VALUES (20,1,10,100),(21,1,10,101)`,
		`INSERT INTO gw_skus VALUES (30,1,20,'chat-standard','sync',1,'request','["standard"]'),(31,1,21,'responses-standard','sync',1,'request','["standard","priority"]')`,
		`INSERT INTO gw_sku_downstream_paths VALUES (1,30,'/v1/chat/completions'),(1,31,'/v1/responses')`,
		`INSERT INTO gw_routes VALUES (1,30,40),(1,30,41),(1,31,40)`,
		`INSERT INTO gw_offerings VALUES (40,1,60,90,'entitlement-main','commercial-main'),(41,1,61,90,'entitlement-main','commercial-main')`,
		`INSERT INTO gw_offering_runtime_state VALUES (1,40,'active'),(1,41,'active')`,
		`INSERT INTO gw_product_transports VALUES (60,1,70,80,'request','none'),(61,1,71,80,'request','none')`,
		`INSERT INTO gw_products VALUES (70,1,50,'gpt-4.1','{}'),(71,1,50,'gpt-4.1-2025','{}')`,
		`INSERT INTO gw_channel_transports VALUES (80,1,50,'openai')`,
		`INSERT INTO gateway_channels VALUES (50,'openai-main','OpenAI Main','active')`,
		`INSERT INTO gw_credential_pools VALUES (90,50,'active')`,
		`INSERT INTO gw_credentials VALUES (100,50,90,'active',110,120,NULL)`,
		`INSERT INTO gw_credential_secret_identities VALUES (110,50,'active')`,
		`INSERT INTO gw_credential_purpose_grants VALUES (100,'execution','active')`,
		`INSERT INTO gw_credential_versions VALUES (120,100,110,'active',NULL,130)`,
		`INSERT INTO gw_credential_validation_events VALUES (121,100,120,'entitlement-main','valid',datetime('now','+1 hour'))`,
		`INSERT INTO gw_credential_entitlement_state VALUES (100,120,'entitlement-main','valid',121)`,
		`INSERT INTO gw_commercial_validation_events VALUES (122,'commercial-main','valid',datetime('now','+1 hour'))`,
		`INSERT INTO gw_commercial_state VALUES ('commercial-main','valid',122)`,
		`INSERT INTO encrypted_blobs VALUES (130,140,NULL)`,
		`INSERT INTO crypto_keyring_state VALUES (140,1)`,
		`INSERT INTO crypto_key_versions VALUES (140,1,'current')`,
		`INSERT INTO encrypted_blob_key_wraps VALUES (130,140,1)`,
		`INSERT INTO billing_system_state VALUES (1,'USD',1)`,
		`INSERT INTO billing_currency_definitions VALUES (1,'USD',1,'active',8,'half_even','1000000.00000000')`,
		`INSERT INTO gw_rate_evidence_review_events VALUES (201,301,'accepted')`,
		`INSERT INTO gw_rate_evidence_review_state VALUES (401,301,201,'accepted')`,
		`INSERT INTO gw_catalog_sources VALUES (601,50,'active')`,
		`INSERT INTO gw_upstream_availability VALUES (601,'  GPT-4.1  ','98.7',120,datetime('now'))`,
		`INSERT INTO gw_catalog_source_profiles VALUES (601,'aicost_pricing_v1')`,
		`INSERT INTO gw_catalog_release_sources VALUES (602,1,601)`,
		`INSERT INTO gw_catalog_discovery_snapshots VALUES (603,602,datetime('now'))`,
		`INSERT INTO gw_catalog_discovery_items VALUES (603,0,'gpt-4.1','AiCost model description','chat',1)`,
		`INSERT INTO gw_sell_rates VALUES (501,1,30,201,'input','token','usage.input_tokens','call.succeeded','0.000003',0,'0','1000000','USD',1,'flat',NULL,NULL)`,
		`INSERT INTO gw_sell_rates VALUES (502,1,30,201,'output','token','usage.output_tokens','call.succeeded','0.000012',0,'0','1000000','USD',1,'flat',NULL,NULL)`,
		`INSERT INTO gw_sell_rates VALUES (503,1,31,201,'request','request','one','call.succeeded','0.02',0,'0','1','USD',1,'flat',NULL,NULL)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("catalog fixture statement failed: %v\n%s", err, statement)
		}
	}
	return db
}

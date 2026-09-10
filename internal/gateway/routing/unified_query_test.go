package routing

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	_ "github.com/glebarez/go-sqlite"
)

func TestUnifiedCandidateQueryUsesNormalizedSchema(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, table := range []string{
		`gw_catalog_releases(id INTEGER, status TEXT)`,
		`gw_catalog_models(id INTEGER, release_id INTEGER, model_id INTEGER, capability_tags TEXT)`,
		`gw_catalog_model_names(release_id INTEGER, catalog_model_id INTEGER, model_name_id INTEGER)`,
		`gw_model_names(id INTEGER, model_id INTEGER, api_name TEXT)`,
		`gw_model_operations(id INTEGER, release_id INTEGER, catalog_model_id INTEGER, operation_contract_id INTEGER)`,
		`gw_operation_contracts(id INTEGER, status TEXT)`,
		`gw_operation_routes(operation_contract_id INTEGER, http_method TEXT, route_template TEXT)`,
		`gw_skus(id INTEGER, release_id INTEGER, model_operation_id INTEGER, delivery_mode TEXT)`,
		`gw_routes(id INTEGER, release_id INTEGER, sku_id INTEGER, offering_id INTEGER, priority INTEGER, weight INTEGER)`,
		`gw_offerings(id INTEGER, release_id INTEGER, product_transport_id INTEGER, credential_pool_id INTEGER, cost_plan_code TEXT, entitlement_fingerprint TEXT, commercial_fingerprint TEXT)`,
		`gw_cost_plans(id INTEGER, release_id INTEGER, offering_id INTEGER, plan_code TEXT)`,
		`gw_product_transports(id INTEGER, release_id INTEGER, product_id INTEGER, channel_transport_id INTEGER)`,
		`gw_products(id INTEGER, release_id INTEGER, channel_id INTEGER, vendor_model TEXT)`,
		`gw_channel_transports(id INTEGER, release_id INTEGER, protocol TEXT, base_url TEXT, transport_code TEXT, request_method TEXT, request_path TEXT)`,
		`gateway_channels(id INTEGER, status TEXT)`,
		`gw_offering_runtime_state(release_id INTEGER, offering_id INTEGER, state TEXT)`,
		`gw_credential_pools(id INTEGER, channel_id INTEGER, status TEXT)`,
		`gw_credentials(id INTEGER, channel_id INTEGER, credential_pool_id INTEGER, status TEXT, secret_identity_id INTEGER, current_version_id INTEGER, weight INTEGER)`,
		`gw_credential_secret_identities(id INTEGER, channel_id INTEGER, status TEXT)`,
		`gw_credential_purpose_grants(id INTEGER, credential_id INTEGER, purpose TEXT, status TEXT)`,
		`gw_credential_versions(id INTEGER, credential_id INTEGER, status TEXT, valid_until TEXT, encrypted_blob_id INTEGER)`,
		`gw_credential_entitlement_state(credential_id INTEGER, credential_version_id INTEGER, entitlement_fingerprint TEXT, state TEXT, latest_event_id INTEGER)`,
		`gw_credential_validation_events(id INTEGER, credential_id INTEGER, credential_version_id INTEGER, entitlement_fingerprint TEXT, state TEXT, valid_until TEXT)`,
		`gw_commercial_state(commercial_fingerprint TEXT, state TEXT, latest_event_id INTEGER)`,
		`gw_commercial_validation_events(id INTEGER, commercial_fingerprint TEXT, state TEXT, valid_until TEXT)`,
		`encrypted_blobs(id INTEGER, keyring_id INTEGER, nonce BLOB, ciphertext BLOB)`,
		`crypto_keyring_state(id INTEGER, current_version INTEGER)`,
		`crypto_key_versions(keyring_id INTEGER, key_version INTEGER, status TEXT)`,
		`encrypted_blob_key_wraps(encrypted_blob_id INTEGER, keyring_id INTEGER, kek_version INTEGER, wrap_nonce BLOB, wrapped_dek BLOB)`,
		`gw_sell_rates(id INTEGER, release_id INTEGER, sku_id INTEGER, unit_code TEXT, unit_price NUMERIC, currency_code TEXT, currency_version INTEGER)`,
		`gw_route_states(id INTEGER, key_id INTEGER, model_name TEXT, transport TEXT, disabled_until TEXT)`,
	} {
		if _, err := db.Exec("CREATE TABLE " + table); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []string{
		`gw_catalog_releases VALUES (1,'published')`,
		`gw_catalog_models VALUES (1,1,1,'{}')`,
		`gw_catalog_model_names VALUES (1,1,1)`,
		`gw_model_names VALUES (1,1,'fixture-model')`,
		`gw_model_operations VALUES (1,1,1,1)`,
		`gw_operation_contracts VALUES (1,'active')`,
		`gw_operation_routes VALUES (1,'POST','/v1/chat/completions')`,
		`gw_skus VALUES (1,1,1,'reference')`,
		`gw_routes VALUES (1,1,1,1,1,1)`,
		`gw_offerings VALUES (1,1,1,1,'provider-default','entitlement','commercial')`,
		`gw_cost_plans VALUES (1,1,1,'provider-default')`,
		`gw_product_transports VALUES (1,1,1,1)`,
		`gw_products VALUES (1,1,1,'fixture-vendor')`,
		`gw_channel_transports VALUES (1,1,'openai','https://example.com','openai_chat','POST','/v1/chat/completions')`,
		`gateway_channels VALUES (1,'active')`,
		`gw_offering_runtime_state VALUES (1,1,'active')`,
		`gw_credential_pools VALUES (1,1,'active')`,
		`gw_credentials VALUES (1,1,1,'active',1,1,1)`,
		`gw_credential_secret_identities VALUES (1,1,'active')`,
		`gw_credential_purpose_grants VALUES (1,1,'execution','active')`,
		`gw_credential_versions VALUES (1,1,'active',NULL,1)`,
		`gw_credential_entitlement_state VALUES (1,1,'entitlement','valid',1)`,
		`gw_credential_validation_events VALUES (1,1,1,'entitlement','valid','2100-01-01 00:00:00')`,
		`gw_commercial_state VALUES ('commercial','valid',1)`,
		`gw_commercial_validation_events VALUES (1,'commercial','valid','2100-01-01 00:00:00')`,
		`encrypted_blobs VALUES (1,1,X'00',X'00')`,
		`crypto_keyring_state VALUES (1,1)`,
		`crypto_key_versions VALUES (1,1,'current')`,
		`encrypted_blob_key_wraps VALUES (1,1,1,X'00',X'00')`,
		`gw_sell_rates VALUES (1,1,1,'request',1,'CNY',1)`,
		`gw_sell_rates VALUES (2,1,1,'token',2,'CNY',1)`,
	} {
		if _, err := db.Exec("INSERT INTO " + row); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("public_operation_and_unique_sku", func(t *testing.T) {
		options := RouteOptions{OperationMethod: "POST", OperationPath: "/v1/chat/completions"}
		id, err := resolveUnifiedSKU(context.Background(), db, 1, "fixture-model", options)
		if err != nil || id != 1 {
			t.Fatalf("public SKU=%d error=%v", id, err)
		}
		options.OperationPath = "/v1/responses"
		if _, err := resolveUnifiedSKU(context.Background(), db, 1, "fixture-model", options); !errors.Is(err, ErrNoRoute) {
			t.Fatalf("another API operation must not reuse chat pricing: %v", err)
		}
		options.OperationPath = "/v1/chat/completions"
		if _, err := db.Exec(`INSERT INTO gw_skus VALUES (2,1,1,'reference')`); err != nil {
			t.Fatal(err)
		}
		if _, err := resolveUnifiedSKU(context.Background(), db, 1, "fixture-model", options); !errors.Is(err, ErrAmbiguousSKU) {
			t.Fatalf("routing must not randomly choose the user's price: %v", err)
		}
	})
	query := strings.ReplaceAll(unifiedCandidatesSQL, "UTC_TIMESTAMP(3)", "'2099-01-01 00:00:00'")
	for _, test := range []struct {
		name, change string
		found        bool
	}{
		{"active", "", true},
		{"entitlement_drift", `UPDATE gw_credential_entitlement_state SET state='drift'`, false},
		{"entitlement_valid", `UPDATE gw_credential_entitlement_state SET state='valid'`, true},
		{"entitlement_expired", `UPDATE gw_credential_validation_events SET valid_until='2098-01-01 00:00:00'`, false},
		{"entitlement_unexpired", `UPDATE gw_credential_validation_events SET valid_until='2100-01-01 00:00:00'`, true},
		{"commercial_drift", `UPDATE gw_commercial_state SET state='drift'`, false},
		{"commercial_valid", `UPDATE gw_commercial_state SET state='valid'`, true},
		{"commercial_expired", `UPDATE gw_commercial_validation_events SET valid_until='2098-01-01 00:00:00'`, false},
		{"commercial_unexpired", `UPDATE gw_commercial_validation_events SET valid_until='2100-01-01 00:00:00'`, true},
		{"draining_pool", `UPDATE gw_credential_pools SET status='draining'`, false},
		{"active_pool", `UPDATE gw_credential_pools SET status='active'`, true},
		{"expired_credential", `UPDATE gw_credential_versions SET valid_until='2098-01-01 00:00:00'`, false},
		{"unexpired_credential", `UPDATE gw_credential_versions SET valid_until='2100-01-01 00:00:00'`, true},
		{"revoked_key", `UPDATE crypto_key_versions SET status='security_revoked'`, false},
		// Cross-request circuit breaker (gw_route_states):
		// key_id = unifiedKeyStateMask | credential.id, so credential 1 becomes 2^31 | 1.
		{"revive_after_circuit_breaker_reset", `UPDATE crypto_key_versions SET status='current'`, true},
		{"circuit_breaker_active", `INSERT INTO gw_route_states VALUES (1,2147483649,'fixture-model','openai_chat','2199-01-01 00:00:00')`, false},
		{"circuit_breaker_expired", `UPDATE gw_route_states SET disabled_until='2000-01-01 00:00:00' WHERE id=1`, true},
		{"circuit_breaker_other_model", `UPDATE gw_route_states SET disabled_until='2199-01-01 00:00:00', model_name='other-model' WHERE id=1`, true},
		{"circuit_breaker_other_transport", `UPDATE gw_route_states SET model_name='fixture-model', transport='anthropic_messages' WHERE id=1`, true},
		{"circuit_breaker_other_credential", `UPDATE gw_route_states SET transport='openai_chat', key_id=2147483650 WHERE id=1`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.change != "" {
				if _, err := db.Exec(test.change); err != nil {
					t.Fatal(err)
				}
			}
			rows, err := db.Query(query, unifiedKeyStateMask, 1, "fixture-model", 1)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			found := rows.Next()
			if found != test.found {
				t.Fatalf("expected candidate=%v", test.found)
			}
			if found {
				candidate, err := scanUnifiedCandidate(rows)
				if err != nil {
					t.Fatal(err)
				}
				if candidate.CostPlanID != 1 {
					t.Fatalf("cost plan = %d", candidate.CostPlanID)
				}
				if candidate.RequestMethod != "POST" || candidate.RequestPath != "/v1/chat/completions" {
					t.Fatalf("transport operation = %s %s", candidate.RequestMethod, candidate.RequestPath)
				}
			}
			if rows.Next() {
				t.Fatal("multiple price components must not duplicate a route candidate")
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	sqlite "github.com/glebarez/go-sqlite"
	"github.com/mirainya/Prism/internal/gateway/billing"
)

// cloneFixtureTables mirrors the production DDL closely enough for the walk: the
// columns the clone reads and writes, plus the identity tables the digest joins.
// Every keyed table holds two rows so a broken id map cannot pass by accident.
var cloneFixtureTables = []string{
	`CREATE TABLE gw_catalog_releases(id INTEGER PRIMARY KEY AUTOINCREMENT, release_no INTEGER, status TEXT, config_version INTEGER, serialization_version INTEGER, content_hash_algorithm TEXT, content_hash TEXT, semantic_version TEXT, semantic_digest TEXT, reviewed_by INTEGER, published_at TEXT, created_at TEXT, updated_at TEXT)`,
	`CREATE TABLE gw_models(id INTEGER PRIMARY KEY, model_code TEXT)`,
	`CREATE TABLE gw_model_names(id INTEGER PRIMARY KEY, model_id INTEGER, api_name TEXT, is_primary INTEGER)`,
	`CREATE TABLE gw_operation_contracts(id INTEGER PRIMARY KEY, operation_code TEXT, contract_version INTEGER, status TEXT)`,
	`CREATE TABLE gw_operation_routes(id INTEGER PRIMARY KEY, operation_contract_id INTEGER, http_method TEXT, route_template TEXT)`,
	`CREATE TABLE gw_adapter_implementations(id INTEGER PRIMARY KEY, adapter_code TEXT, contract_version INTEGER, implementation_digest TEXT, minimum_semantic_version TEXT)`,
	`CREATE TABLE gateway_channels(id INTEGER PRIMARY KEY, channel_code TEXT, status TEXT)`,
	`CREATE TABLE gw_credential_pools(id INTEGER PRIMARY KEY, pool_code TEXT)`,
	`CREATE TABLE gw_rate_evidence_review_events(id INTEGER PRIMARY KEY, rate_evidence_id INTEGER, decision TEXT)`,
	`CREATE TABLE gw_catalog_models(id INTEGER PRIMARY KEY AUTOINCREMENT, release_id INTEGER, model_id INTEGER, display_name TEXT, description TEXT, capability_tags TEXT, sort_order INTEGER, visibility TEXT, created_at TEXT)`,
	`CREATE TABLE gw_catalog_model_names(id INTEGER PRIMARY KEY AUTOINCREMENT, release_id INTEGER, catalog_model_id INTEGER, model_id INTEGER, model_name_id INTEGER, is_primary INTEGER, created_at TEXT)`,
	`CREATE TABLE gw_model_operations(id INTEGER PRIMARY KEY AUTOINCREMENT, release_id INTEGER, catalog_model_id INTEGER, operation_contract_id INTEGER, normalization_version INTEGER, semantic_digest TEXT, created_at TEXT)`,
	`CREATE TABLE gw_skus(id INTEGER PRIMARY KEY AUTOINCREMENT, release_id INTEGER, model_operation_id INTEGER, sku_code TEXT, variant_code TEXT, delivery_mode TEXT, max_results INTEGER, idempotency_mode TEXT, service_tiers TEXT, created_at TEXT)`,
	`CREATE TABLE gw_sku_downstream_paths(id INTEGER PRIMARY KEY AUTOINCREMENT, release_id INTEGER, sku_id INTEGER, path TEXT, created_at TEXT)`,
	`CREATE TABLE gw_sell_rates(id INTEGER PRIMARY KEY AUTOINCREMENT, release_id INTEGER, sku_id INTEGER, rate_evidence_review_event_id INTEGER, unit_code TEXT, unit_price TEXT, currency_code TEXT, currency_version INTEGER, component_code TEXT, quantity_source TEXT, charge_event TEXT, unit_scale INTEGER, quantity_step TEXT, max_quantity TEXT, pricing_mode TEXT, pricing_expr TEXT, max_price TEXT, created_at TEXT)`,
	`CREATE TABLE gw_channel_transports(id INTEGER PRIMARY KEY AUTOINCREMENT, release_id INTEGER, channel_id INTEGER, adapter_implementation_id INTEGER, transport_code TEXT, base_url TEXT, protocol TEXT, request_method TEXT, request_path TEXT, auth_scheme TEXT, execution_fingerprint TEXT, state_compatibility_fingerprint TEXT, timeout_ms INTEGER, created_at TEXT)`,
	`CREATE TABLE gw_transport_allowed_hosts(id INTEGER PRIMARY KEY AUTOINCREMENT, release_id INTEGER, channel_transport_id INTEGER, protocol TEXT, host_pattern TEXT, port INTEGER, created_at TEXT)`,
	`CREATE TABLE gw_products(id INTEGER PRIMARY KEY AUTOINCREMENT, release_id INTEGER, channel_id INTEGER, product_code TEXT, vendor_model TEXT, capability_constraints TEXT, constraints_schema_version INTEGER, created_at TEXT)`,
	`CREATE TABLE gw_product_transports(id INTEGER PRIMARY KEY AUTOINCREMENT, release_id INTEGER, product_id INTEGER, channel_transport_id INTEGER, task_scope TEXT, cancel_mode TEXT, source_url_policy TEXT, upstream_scope_kind TEXT, upstream_scope_key TEXT, execution_fingerprint TEXT, state_compatibility_fingerprint TEXT, timeout_ms INTEGER, created_at TEXT)`,
	`CREATE TABLE gw_product_transport_actions(id INTEGER PRIMARY KEY AUTOINCREMENT, release_id INTEGER, product_transport_id INTEGER, action_code TEXT, allowed_source_state TEXT, idempotency_mode TEXT, request_schema_version INTEGER, response_schema_version INTEGER, created_at TEXT)`,
	`CREATE TABLE gw_offerings(id INTEGER PRIMARY KEY AUTOINCREMENT, release_id INTEGER, product_transport_id INTEGER, credential_pool_id INTEGER, entitlement_fingerprint TEXT, commercial_fingerprint TEXT, cost_plan_code TEXT, created_at TEXT)`,
	`CREATE TABLE gw_cost_plans(id INTEGER PRIMARY KEY AUTOINCREMENT, release_id INTEGER, offering_id INTEGER, plan_code TEXT, created_at TEXT)`,
	`CREATE TABLE gw_cost_rates(id INTEGER PRIMARY KEY AUTOINCREMENT, release_id INTEGER, cost_plan_id INTEGER, rate_evidence_review_event_id INTEGER, unit_code TEXT, unit_price TEXT, currency_code TEXT, currency_version INTEGER, component_code TEXT, quantity_source TEXT, charge_event TEXT, unit_scale INTEGER, quantity_step TEXT, max_quantity TEXT, pricing_mode TEXT, pricing_expr TEXT, max_price TEXT, created_at TEXT)`,
	`CREATE TABLE gw_routes(id INTEGER PRIMARY KEY AUTOINCREMENT, release_id INTEGER, sku_id INTEGER, offering_id INTEGER, priority INTEGER, weight INTEGER, created_at TEXT)`,
	`CREATE TABLE gw_offering_runtime_state(id INTEGER PRIMARY KEY AUTOINCREMENT, release_id INTEGER, offering_id INTEGER, state TEXT, state_version INTEGER, reason_code TEXT, updated_at TEXT)`,
	`CREATE TABLE gw_offering_state_events(id INTEGER PRIMARY KEY AUTOINCREMENT, release_id INTEGER, offering_id INTEGER, state_version INTEGER, old_state TEXT, new_state TEXT, reason_code TEXT, created_at TEXT)`,
}

// cloneFixtureRows is one complete two-model, two-channel release: enough graph
// that every layer has more than one row and every parent map has to be right.
var cloneFixtureRows = []string{
	`INSERT INTO gw_catalog_releases(id,release_no,status,config_version,serialization_version,content_hash_algorithm,content_hash,semantic_version,semantic_digest,created_at,updated_at) VALUES (1,1,'published',6,1,'sha256','源','1.0.0','d1','t','t')`,
	`INSERT INTO gw_models VALUES (7,'seedance-2.0'),(8,'seedance-2.0-fast')`,
	`INSERT INTO gw_model_names VALUES (17,7,'seedance-2.0',1),(18,8,'seedance-2.0-fast',1)`,
	`INSERT INTO gw_operation_contracts VALUES (3,'video.generate',1,'active')`,
	`INSERT INTO gw_operation_routes VALUES (4,3,'POST','/v1/videos/generations')`,
	`INSERT INTO gw_adapter_implementations VALUES (60,'seedance',1,'digest','1.0.0')`,
	`INSERT INTO gateway_channels VALUES (5,'ark','active'),(6,'h-channel','active')`,
	`INSERT INTO gw_credential_pools VALUES (9,'primary')`,
	`INSERT INTO gw_rate_evidence_review_events VALUES (11,101,'accepted'),(12,102,'accepted')`,
	`INSERT INTO gw_catalog_models(id,release_id,model_id,display_name,description,capability_tags,sort_order,visibility,created_at) VALUES (101,1,7,'Seedance 2.0','','["video"]',0,'visible','t'),(102,1,8,'Seedance Fast','','["video"]',1,'visible','t')`,
	`INSERT INTO gw_catalog_model_names(id,release_id,catalog_model_id,model_id,model_name_id,is_primary,created_at) VALUES (201,1,101,7,17,1,'t'),(202,1,102,8,18,1,'t')`,
	`INSERT INTO gw_model_operations(id,release_id,catalog_model_id,operation_contract_id,normalization_version,semantic_digest,created_at) VALUES (301,1,101,3,1,'s1','t'),(302,1,102,3,1,'s2','t')`,
	`INSERT INTO gw_skus(id,release_id,model_operation_id,sku_code,variant_code,delivery_mode,max_results,idempotency_mode,service_tiers,created_at) VALUES (401,1,301,'official','official','reference',1,'required','["standard"]','t'),(402,1,302,'fast','default','reference',1,'required','["standard"]','t')`,
	`INSERT INTO gw_sku_downstream_paths(id,release_id,sku_id,path,created_at) VALUES (501,1,401,'/v1/videos/generations','t')`,
	`INSERT INTO gw_sell_rates(id,release_id,sku_id,rate_evidence_review_event_id,unit_code,unit_price,currency_code,currency_version,component_code,quantity_source,charge_event,unit_scale,quantity_step,max_quantity,pricing_mode,pricing_expr,max_price,created_at) VALUES (601,1,401,11,'second','0.20','CNY',1,'generated_second','result.seconds','call.succeeded',0,'1','60','flat',NULL,NULL,'t'),(602,1,402,11,'second','0.10','CNY',1,'generated_second','result.seconds','call.succeeded',0,'1','60','flat',NULL,NULL,'t')`,
	`INSERT INTO gw_channel_transports(id,release_id,channel_id,adapter_implementation_id,transport_code,base_url,protocol,request_method,request_path,auth_scheme,execution_fingerprint,state_compatibility_fingerprint,timeout_ms,created_at) VALUES (701,1,5,60,'ark-v1','https://ark.example.com','volcengine_responses_v3','POST','/tasks','bearer','f1',NULL,30000,'t'),(702,1,6,60,'h-v1','https://h.example.com','volcengine_responses_v3','POST','/tasks','bearer','f2','c2',30000,'t')`,
	`INSERT INTO gw_transport_allowed_hosts(id,release_id,channel_transport_id,protocol,host_pattern,port,created_at) VALUES (801,1,701,'https','ark.example.com',443,'t'),(802,1,702,'https','h.example.com',443,'t')`,
	`INSERT INTO gw_products(id,release_id,channel_id,product_code,vendor_model,capability_constraints,constraints_schema_version,created_at) VALUES (901,1,5,'official','seedance-2-0','{"duration":{"max":12}}',1,'t'),(902,1,6,'h','seedance-2-0',NULL,1,'t')`,
	`INSERT INTO gw_product_transports(id,release_id,product_id,channel_transport_id,task_scope,cancel_mode,source_url_policy,upstream_scope_kind,upstream_scope_key,execution_fingerprint,state_compatibility_fingerprint,timeout_ms,created_at) VALUES (1001,1,901,701,'task','none','fixed','product_transport','ark','f1',NULL,120000,'t'),(1002,1,902,702,'task','upstream','fixed','product_transport','h','f2','c2',120000,'t')`,
	`INSERT INTO gw_product_transport_actions(id,release_id,product_transport_id,action_code,allowed_source_state,idempotency_mode,request_schema_version,response_schema_version,created_at) VALUES (1101,1,1001,'submit','allocated','user_keyed',1,1,'t'),(1102,1,1001,'query','accepted','none',1,1,'t'),(1103,1,1002,'submit','allocated','user_keyed',1,1,'t'),(1104,1,1002,'query','accepted','none',1,1,'t'),(1105,1,1002,'cancel','accepted','none',1,1,'t')`,
	`INSERT INTO gw_offerings(id,release_id,product_transport_id,credential_pool_id,entitlement_fingerprint,commercial_fingerprint,cost_plan_code,created_at) VALUES (1201,1,1001,9,'e1','m1','official-cny','t'),(1202,1,1002,9,'e2','m2','h-cny','t')`,
	`INSERT INTO gw_cost_plans(id,release_id,offering_id,plan_code,created_at) VALUES (1301,1,1201,'official-cny','t'),(1302,1,1202,'h-cny','t')`,
	`INSERT INTO gw_cost_rates(id,release_id,cost_plan_id,rate_evidence_review_event_id,unit_code,unit_price,currency_code,currency_version,component_code,quantity_source,charge_event,unit_scale,quantity_step,max_quantity,pricing_mode,pricing_expr,max_price,created_at) VALUES (1401,1,1301,12,'second','0.08','CNY',1,'generated_second','result.seconds','call.succeeded',0,'1','60','flat',NULL,NULL,'t'),(1402,1,1302,12,'second','0.05','CNY',1,'generated_second','result.seconds','call.succeeded',0,'1','60','flat',NULL,NULL,'t')`,
	`INSERT INTO gw_routes(id,release_id,sku_id,offering_id,priority,weight,created_at) VALUES (1501,1,401,1201,100,100,'t'),(1502,1,401,1202,50,100,'t'),(1503,1,402,1202,100,100,'t')`,
	`INSERT INTO gw_offering_runtime_state(id,release_id,offering_id,state,state_version,reason_code,updated_at) VALUES (1601,1,1201,'active',1,'admin_create','t'),(1602,1,1202,'draining',4,'operator_drain','t')`,
	`INSERT INTO gw_offering_state_events(id,release_id,offering_id,state_version,old_state,new_state,reason_code,created_at) VALUES (1701,1,1201,1,NULL,'active','admin_create','t'),(1702,1,1202,4,'active','draining','operator_drain','t')`,
}

// clonePublishableFixture adds what the two publication gates read but the clone
// itself does not touch: the settlement currency and the evidence review state
// behind every price. With these, a fork can be checked end to end.
var clonePublishableFixture = []string{
	`CREATE TABLE billing_system_state(id INTEGER, currency_code TEXT, currency_version INTEGER)`,
	`CREATE TABLE billing_currency_definitions(id INTEGER, currency_code TEXT, definition_version INTEGER, status TEXT, fraction_digits INTEGER, rounding_mode TEXT, max_amount TEXT)`,
	`CREATE TABLE gw_rate_evidence_review_state(id INTEGER, rate_evidence_id INTEGER, state TEXT, latest_review_event_id INTEGER)`,
	`CREATE TABLE gw_catalog_release_state_events(id INTEGER PRIMARY KEY AUTOINCREMENT, release_id INTEGER, old_state TEXT, new_state TEXT, reason_code TEXT, created_at TEXT)`,
	`CREATE TABLE audit_events(id INTEGER PRIMARY KEY AUTOINCREMENT, actor_type TEXT, actor_user_id INTEGER, action TEXT, resource_type TEXT, resource_id TEXT, outcome TEXT, http_status INTEGER, metadata TEXT, created_at TEXT)`,
	`INSERT INTO billing_system_state VALUES (1,'CNY',1)`,
	`INSERT INTO billing_currency_definitions VALUES (1,'CNY',1,'active',8,'half_even','1000000000.000000000000000000')`,
	`INSERT INTO gw_rate_evidence_review_state VALUES (1,101,'accepted',11),(2,102,'accepted',12)`,
}

// registerJSONLength teaches the test driver the one MySQL JSON function the
// publication gates use (CheckCatalogStructure rejects a SKU whose service_tiers
// array is empty). Registering it is what lets the fork be checked end to end on
// sqlite; the alternative — weakening the gate for the test — would stop the test
// from proving anything about production.
var registerJSONLength = sync.OnceValue(func() error {
	return sqlite.RegisterDeterministicScalarFunction("JSON_LENGTH", 1,
		func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			text, ok := args[0].(string)
			if !ok {
				return nil, nil
			}
			var array []json.RawMessage
			if err := json.Unmarshal([]byte(text), &array); err != nil {
				// A JSON scalar has length 1 in MySQL, and a malformed value is not
				// something this fixture should paper over.
				var scalar json.RawMessage
				if json.Unmarshal([]byte(text), &scalar) != nil {
					return nil, fmt.Errorf("JSON_LENGTH: %q is not JSON", text)
				}
				return int64(1), nil
			}
			return int64(len(array)), nil
		})
})

func cloneFixtureDB(t *testing.T) *sql.DB {
	t.Helper()
	if err := registerJSONLength(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	for _, query := range slices.Concat(cloneFixtureTables, cloneFixtureRows) {
		if _, err := db.Exec(query); err != nil {
			t.Fatalf("fixture %q: %v", query, err)
		}
	}
	return db
}

// cloneInTx runs the layer walk against a fresh empty draft and returns its id.
// It exercises cloneCatalogContent rather than ForkCatalogRelease because the
// latter's gates (CheckCatalogStructure, CheckCatalogPricing) need the billing
// currency tables that catalog_pricing_test.go covers separately.
func cloneInTx(t *testing.T, db *sql.DB) (*sql.Tx, uint64) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.Rollback() })
	result, err := tx.Exec(`INSERT INTO gw_catalog_releases(release_no,status,config_version,serialization_version,content_hash_algorithm,content_hash,semantic_version,semantic_digest,created_at,updated_at) VALUES (2,'draft',1,1,'sha256','草稿','1.0.1','d1','t','t')`)
	if err != nil {
		t.Fatal(err)
	}
	target, err := lastID(result)
	if err != nil {
		t.Fatal(err)
	}
	if err := cloneCatalogContent(context.Background(), tx, 1, target); err != nil {
		t.Fatalf("clone failed: %v", err)
	}
	return tx, target
}

func TestCloneCopiesEveryLayerRowForRow(t *testing.T) {
	tx, target := cloneInTx(t, cloneFixtureDB(t))
	for _, table := range append(cloneLayerTables(), "gw_offering_runtime_state", "gw_offering_state_events") {
		var source, cloned uint64
		if err := tx.QueryRow(`SELECT
(SELECT COUNT(*) FROM `+table+` WHERE release_id=1),
(SELECT COUNT(*) FROM `+table+` WHERE release_id=?)`, target).Scan(&source, &cloned); err != nil {
			t.Fatalf("%s: %v", table, err)
		}
		if source == 0 {
			t.Fatalf("%s has no source rows, so its clone proves nothing", table)
		}
		if cloned != source {
			t.Fatalf("%s: cloned %d rows, source has %d", table, cloned, source)
		}
	}
}

func cloneLayerTables() []string {
	tables := make([]string, 0, len(catalogCloneLayers))
	for _, layer := range catalogCloneLayers {
		tables = append(tables, layer.table)
	}
	return tables
}

// TestCloneCoversExactlyTheDigestedTables is the guard that keeps the two lists
// from drifting: the digest defines what the content of a release is, so a table
// it hashes but the clone skips would be configuration silently dropped from
// every fork, and one the clone copies but the digest ignores is not content.
func TestCloneCoversExactlyTheDigestedTables(t *testing.T) {
	digested := map[string]bool{}
	pattern := regexp.MustCompile(`(?:FROM|JOIN)\s+(gw_[a-z_]+)`)
	for _, query := range catalogDigestQueries {
		for _, match := range pattern.FindAllStringSubmatch(query, -1) {
			digested[match[1]] = true
		}
	}
	// The digest joins release-independent identity tables to hash stable codes
	// instead of surrogate keys, and the evidence review event to hash the decision
	// behind a price. None of them carry a release_id, so they are shared by every
	// release rather than cloned into each one.
	for _, shared := range []string{"gw_models", "gw_model_names", "gw_operation_contracts",
		"gw_operation_routes", "gw_adapter_implementations", "gw_credential_pools",
		"gw_rate_evidence_review_events"} {
		if !digested[shared] {
			t.Fatalf("%s is no longer joined by any digest query; this exemption is stale", shared)
		}
		delete(digested, shared)
	}
	cloned := cloneLayerTables()
	for table := range digested {
		if !slices.Contains(cloned, table) {
			t.Fatalf("the digest hashes %s but the clone does not copy it", table)
		}
	}
	for _, table := range cloned {
		if !digested[table] {
			t.Fatalf("the clone copies %s but no digest query covers it", table)
		}
	}
}

// TestCloneKeepsEveryPayloadColumnIdentical checks the half of faithfulness the
// row counts cannot see: an unedited fork must describe the behaviour running
// now, so prices, weights, fingerprints and NULLs all survive unchanged.
func TestCloneKeepsEveryPayloadColumnIdentical(t *testing.T) {
	tx, target := cloneInTx(t, cloneFixtureDB(t))
	for _, layer := range catalogCloneLayers {
		read := func(release uint64) []string {
			t.Helper()
			rows, err := tx.Query(`SELECT `+strings.Join(layer.columns, ",")+` FROM `+layer.table+` WHERE release_id=? ORDER BY id`, release)
			if err != nil {
				t.Fatalf("%s: %v", layer.table, err)
			}
			defer rows.Close()
			var collected []string
			for rows.Next() {
				values := make([]sql.NullString, len(layer.columns))
				targets := make([]any, len(values))
				for index := range values {
					targets[index] = &values[index]
				}
				if err := rows.Scan(targets...); err != nil {
					t.Fatalf("%s: %v", layer.table, err)
				}
				parts := make([]string, len(values))
				for index, value := range values {
					// NULL and the empty string are printed differently: a nullable
					// fingerprint flattened to '' would change the content digest.
					parts[index] = "NULL"
					if value.Valid {
						parts[index] = "=" + value.String
					}
				}
				collected = append(collected, strings.Join(parts, "\x1f"))
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			return collected
		}
		if source, cloned := read(1), read(target); !slices.Equal(source, cloned) {
			t.Fatalf("%s payload differs\nsource: %q\nclone:  %q", layer.table, source, cloned)
		}
	}
}

// TestCloneReusesRateEvidence locks §5.2.1's third requirement. CreateCatalogRate
// reads the price from accepted evidence and CatalogRateInput carries no price,
// so a clone that minted fresh evidence would be asserting a second review of a
// price nobody reviewed twice. Only an edited rate needs new evidence.
func TestCloneReusesRateEvidence(t *testing.T) {
	tx, target := cloneInTx(t, cloneFixtureDB(t))
	for _, table := range []string{"gw_sell_rates", "gw_cost_rates"} {
		var reused uint64
		if err := tx.QueryRow(`SELECT COUNT(DISTINCT rate_evidence_review_event_id) FROM `+table+` WHERE release_id=?`, target).Scan(&reused); err != nil {
			t.Fatal(err)
		}
		if reused == 0 {
			t.Fatalf("%s: the clone references no evidence at all", table)
		}
		var invented uint64
		if err := tx.QueryRow(`SELECT COUNT(*) FROM `+table+` c WHERE c.release_id=? AND NOT EXISTS (
SELECT 1 FROM `+table+` s WHERE s.release_id=1 AND s.rate_evidence_review_event_id=c.rate_evidence_review_event_id)`, target).Scan(&invented); err != nil {
			t.Fatal(err)
		}
		if invented != 0 {
			t.Fatalf("%s: %d cloned rates reference evidence the source never used", table, invented)
		}
	}
}

// TestCloneProducesADifferentContentHash locks §5.2.1's second requirement. The
// digest hashes primary keys, the clone necessarily has new ones, and
// uq_gw_catalog_releases_content_hash is UNIQUE — so an unedited fork hashing
// differently is required, not a defect. Equal hashes would mean the fork could
// never be published alongside its source.
func TestCloneProducesADifferentContentHash(t *testing.T) {
	tx, target := cloneInTx(t, cloneFixtureDB(t))
	ctx := context.Background()
	source, err := catalogContentDigest(ctx, tx, 1)
	if err != nil {
		t.Fatal(err)
	}
	cloned, err := catalogContentDigest(ctx, tx, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(source) != 64 || len(cloned) != 64 {
		t.Fatalf("digests are not sha256 hex: source=%q clone=%q", source, cloned)
	}
	if source == cloned {
		t.Fatal("the fork hashes identically to its source, so the UNIQUE content_hash would reject it")
	}
	// Same content, same digest: the difference above is the id space, not noise.
	again, err := catalogContentDigest(ctx, tx, target)
	if err != nil {
		t.Fatal(err)
	}
	if again != cloned {
		t.Fatalf("digest is unstable: %s then %s", cloned, again)
	}
}

func TestForkRejectsUnforkableReleases(t *testing.T) {
	for _, test := range []struct {
		name   string
		source uint64
		change string
		want   error
	}{
		{"missing", 99, "", ErrNotFound},
		{"draft", 1, `UPDATE gw_catalog_releases SET status='draft' WHERE id=1`, ErrConflict},
		{"retired", 1, `UPDATE gw_catalog_releases SET status='retired' WHERE id=1`, ErrConflict},
		{"no_source", 0, "", ErrInvalidInput},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := cloneFixtureDB(t)
			if test.change != "" {
				if _, err := db.Exec(test.change); err != nil {
					t.Fatal(err)
				}
			}
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			store := &Store{}
			_, err = store.ForkCatalogRelease(context.Background(), tx, test.source,
				CatalogDraftInput{SemanticVersion: "1.0.1", SemanticDigest: strings.Repeat("a", 64)}, 1)
			if !errors.Is(err, test.want) {
				t.Fatalf("err=%v, want %v", err, test.want)
			}
		})
	}
}

// TestForkWithZeroEditsIsPublishable is §5.2.1's first requirement and the whole
// point of M3a: a fork nobody has edited must be publishable, or the operator has
// no starting point to edit from. It runs the real ForkCatalogRelease, so the
// gates inside it are the gates PublishRelease will run.
func TestForkWithZeroEditsIsPublishable(t *testing.T) {
	db := cloneFixtureDB(t)
	for _, query := range clonePublishableFixture {
		if _, err := db.Exec(query); err != nil {
			t.Fatalf("fixture %q: %v", query, err)
		}
	}
	// The source SKU names an explicit variant, so the fork also re-runs the
	// manifest variant gate on cloned rows.
	withSpecSource(t, stubSpecSource{specs: map[string]billing.ExpressionSpec{
		"seedance@1/official": secondsSpec(t, "digest-a", "5", "10"),
	}})
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	store := &Store{}
	target, err := store.ForkCatalogRelease(context.Background(), tx, 1,
		CatalogDraftInput{SemanticVersion: "1.0.1", SemanticDigest: strings.Repeat("a", 64)}, 1)
	if err != nil {
		t.Fatalf("an unedited fork of a publishable release was rejected: %v", err)
	}
	if target == 1 {
		t.Fatal("the fork reused the source release id")
	}
	// The fork is recorded on the release timeline and in the audit log, so the
	// operator can see where a draft came from.
	var forkEvents, auditRows uint64
	if err := tx.QueryRow(`SELECT COUNT(*) FROM gw_catalog_release_state_events WHERE release_id=? AND reason_code='catalog_fork'`, target).Scan(&forkEvents); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='unified.catalog.fork' AND resource_id=?`, strconv.FormatUint(target, 10)).Scan(&auditRows); err != nil {
		t.Fatal(err)
	}
	if forkEvents != 1 || auditRows != 1 {
		t.Fatalf("fork provenance: state events=%d audit rows=%d", forkEvents, auditRows)
	}
	// The rest of what PublishRelease does to content. Its own status UPDATE takes
	// a literal FOR UPDATE lock this driver has no syntax for, so the real
	// fork -> publish -> activate lifecycle is proven by the MySQL integration
	// test; what is checked here is that the cloned content survives every step
	// that inspects or rewrites it.
	if err := proveCatalogExpressionBounds(context.Background(), tx, target); err != nil {
		t.Fatalf("bound proof rejected the fork: %v", err)
	}
	digest, err := finalizeCatalogDigest(context.Background(), tx, target)
	if err != nil {
		t.Fatalf("digest finalization rejected the fork: %v", err)
	}
	var stored, sourceHash string
	if err := tx.QueryRow(`SELECT content_hash FROM gw_catalog_releases WHERE id=?`, target).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(`SELECT content_hash FROM gw_catalog_releases WHERE id=1`).Scan(&sourceHash); err != nil {
		t.Fatal(err)
	}
	// The draft's placeholder hash must be gone, replaced by the real digest, and
	// it must not collide with the source: uq_gw_catalog_releases_content_hash
	// would reject the fork at publication if it did.
	if stored != digest || len(stored) != 64 || stored == sourceHash {
		t.Fatalf("content_hash was not finalized: stored=%q returned=%q source=%q", stored, digest, sourceHash)
	}
}

// TestCloneRemapsParentsWithinTheNewRelease is the ID-remapping proof: every
// foreign key in the clone must point at a cloned row, never at the source's.
func TestCloneRemapsParentsWithinTheNewRelease(t *testing.T) {
	tx, target := cloneInTx(t, cloneFixtureDB(t))
	for _, layer := range catalogCloneLayers {
		for _, parent := range layer.parents {
			var dangling uint64
			query := `SELECT COUNT(*) FROM ` + layer.table + ` child WHERE child.release_id=? AND NOT EXISTS (
SELECT 1 FROM ` + parent.table + ` p WHERE p.release_id=child.release_id AND p.id=child.` + parent.column + `)`
			if err := tx.QueryRow(query, target).Scan(&dangling); err != nil {
				t.Fatalf("%s.%s: %v", layer.table, parent.column, err)
			}
			if dangling != 0 {
				t.Fatalf("%s.%s has %d rows pointing outside the cloned release",
					layer.table, parent.column, dangling)
			}
		}
	}
}

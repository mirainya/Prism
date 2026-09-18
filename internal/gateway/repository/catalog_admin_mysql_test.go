//go:build integration

package repository_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/mirainya/Prism/internal/gateway/adapter"
	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/migrate"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func mysqlCatalogStore(t *testing.T) *repository.Store {
	t.Helper()
	dsn := os.Getenv("PRISM_MIGRATION_TEST_DSN")
	if dsn == "" {
		t.Skip("PRISM_MIGRATION_TEST_DSN is not set")
	}
	config, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid MySQL test DSN")
	}
	host, _, err := net.SplitHostPort(config.Addr)
	if err != nil || config.Net != "tcp" || !net.ParseIP(host).IsLoopback() {
		t.Fatal("catalog tests require an isolated loopback MySQL server")
	}
	config.DBName = ""
	server, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	databaseName := fmt.Sprintf("prism_test_catalog_%d", time.Now().UnixNano())
	if _, err := server.Exec("CREATE DATABASE `" + databaseName + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := server.Exec("DROP DATABASE `" + databaseName + "`"); err != nil {
			t.Errorf("clean catalog test database: %v", err)
		}
	})
	config.DBName, config.ParseTime, config.MultiStatements = databaseName, true, true
	database, err := gorm.Open(mysql.Open(config.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrate.Up(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	store, err := repository.New(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestMySQLCatalogAllowsMultipleSKUsForOneModelOperation(t *testing.T) {
	store := mysqlCatalogStore(t)
	ctx := context.Background()
	var releaseID uint64
	err := store.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		releaseID, err = store.CreateCatalogDraft(ctx, tx, repository.CatalogDraftInput{
			SemanticVersion: "1.0.0",
			SemanticDigest:  adapter.SemanticDigest(),
		}, 1)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	input := repository.CatalogSKUInput{
		ExpectedVersion: 1, ModelCode: "seedance-2.0", APIName: "seedance-2.0",
		DisplayName: "Seedance 2.0", Description: "video generation", Visibility: "visible",
		CapabilityTags: []string{"video"}, OperationCode: "video.generate", ContractVersion: 1,
		HTTPMethod: "POST", RouteTemplate: "/v1/videos/generations", NormalizationVersion: 1,
		SKUCode: "seedance-2.0-720p", DeliveryMode: "reference", MaxResults: 1,
		IdempotencyMode: "required", ServiceTiers: []string{"standard"},
	}
	create := func(value repository.CatalogSKUInput) error {
		return store.WithTx(ctx, func(tx *sql.Tx) error {
			_, err := store.CreateCatalogSKU(ctx, tx, releaseID, value, 1)
			return err
		})
	}
	if err := create(input); err != nil {
		t.Fatal(err)
	}
	input.ExpectedVersion = 2
	input.SKUCode = "seedance-2.0-1080p"
	if err := create(input); err != nil {
		t.Fatal(err)
	}

	for query, want := range map[string]int{
		`SELECT COUNT(*) FROM gw_catalog_models WHERE release_id=?`:   1,
		`SELECT COUNT(*) FROM gw_model_operations WHERE release_id=?`: 1,
		`SELECT COUNT(*) FROM gw_skus WHERE release_id=?`:             2,
	} {
		var got int
		if err := store.DB().QueryRowContext(ctx, query, releaseID).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s = %d, want %d", query, got, want)
		}
	}

	input.ExpectedVersion = 3
	input.SKUCode = "seedance-2.0-4k"
	input.DisplayName = "conflicting model metadata"
	err = create(input)
	if !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("metadata conflict error = %v", err)
	}
	var version uint64
	if err := store.DB().QueryRowContext(ctx, `SELECT config_version FROM gw_catalog_releases WHERE id=?`, releaseID).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 3 {
		t.Fatalf("config version after rollback = %d, want 3", version)
	}
}

// mysqlPublishableRelease builds one complete draft against real MySQL: channel,
// credential pool, settlement currency, SKU, product with transport and actions,
// cost plan, and reviewed sell and cost evidence. It stops short of publishing so
// callers can drive the lifecycle themselves.
func mysqlPublishableRelease(t *testing.T) (*repository.Store, uint64) {
	t.Helper()
	store := mysqlCatalogStore(t)
	ctx := context.Background()
	const actorID = 1
	var channelID, poolID, releaseID, skuID uint64
	err := store.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		channelID, err = store.CreateChannel(ctx, tx, repository.ChannelInput{Code: "seedance_official", Name: "Seedance Official"}, actorID)
		if err != nil {
			return err
		}
		poolID, err = store.CreateManagedCredentialPool(ctx, tx, repository.PoolInput{ChannelID: channelID, PoolCode: "primary", DisplayName: "Primary"}, actorID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	err = store.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := store.CreateCurrencyDefinition(ctx, tx, repository.CurrencyDefinitionInput{Code: "CNY", Version: 1, FractionDigits: 8, RoundingMode: "half_even", MaxAmount: "1000000000"}, actorID); err != nil {
			return err
		}
		return store.ActivateSettlementCurrency(ctx, tx, "CNY", 1, actorID)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WithTx(ctx, func(tx *sql.Tx) error {
		return store.ActivateSettlementCurrency(ctx, tx, "CNY", 1, actorID)
	}); err != nil {
		t.Fatal(err)
	}
	var postingRules, systemLedgers int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM billing_posting_rules WHERE currency_code='CNY' AND currency_version=1 AND status='active'`).Scan(&postingRules); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM ledger_accounts WHERE currency_code='CNY' AND currency_version=1 AND system_account_code IN ('gateway_revenue','funding_clearing')`).Scan(&systemLedgers); err != nil {
		t.Fatal(err)
	}
	if postingRules != 7 || systemLedgers != 2 {
		t.Fatalf("billing infrastructure: rules=%d ledgers=%d", postingRules, systemLedgers)
	}
	err = store.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		releaseID, err = store.CreateCatalogDraft(ctx, tx, repository.CatalogDraftInput{SemanticVersion: "1.0.0", SemanticDigest: adapter.SemanticDigest()}, actorID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	err = store.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		skuID, err = store.CreateCatalogSKU(ctx, tx, releaseID, repository.CatalogSKUInput{
			ExpectedVersion: 1, ModelCode: "seedance-2.0", APIName: "seedance-2.0", DisplayName: "Seedance 2.0",
			Description: "video generation", Visibility: "visible", CapabilityTags: []string{"video"},
			OperationCode: "video.generate", ContractVersion: 1, HTTPMethod: "POST", RouteTemplate: "/v1/videos/generations", NormalizationVersion: 1,
			SKUCode: "seedance-2.0-standard", DeliveryMode: "reference", MaxResults: 1, IdempotencyMode: "required", ServiceTiers: []string{"standard"},
		}, actorID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	descriptor, found := adapter.DescriptorFor("seedance", 1)
	if !found {
		t.Fatal("seedance adapter descriptor is missing")
	}
	err = store.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := store.CreateCatalogProduct(ctx, tx, releaseID, repository.CatalogProductInput{
			ExpectedVersion: 2, ChannelID: channelID, CredentialPoolID: poolID, ProductCode: "seedance-2.0-official", VendorModel: "seedance-2-0-250428",
			CapabilityConstraints: json.RawMessage(`{"duration":{"min":2,"max":12}}`), ConstraintsSchemaVersion: 1,
			AdapterCode: descriptor.Code, AdapterVersion: descriptor.Version, TransportCode: "seedance-v1", BaseURL: "https://ark.example.com",
			Protocol: descriptor.Protocol, RequestMethod: "POST", RequestPath: "/api/v3/contents/generations/tasks", AuthScheme: "bearer",
			TransportTimeoutMS: 30000, TaskTimeoutMS: 120000, TaskScope: "task", CancelMode: "none", SourceURLPolicy: "fixed",
			UpstreamScopeKind: "product_transport", UpstreamScopeKey: "seedance-v1",
			Actions: []repository.CatalogProductActionInput{
				{ActionCode: "submit", AllowedSourceState: "allocated", IdempotencyMode: "user_keyed", RequestSchemaVersion: 1, ResponseSchemaVersion: 1},
				{ActionCode: "query", AllowedSourceState: "accepted", IdempotencyMode: "none", RequestSchemaVersion: 1, ResponseSchemaVersion: 1},
			},
			CostPlanCode: "official-cny", Routes: []repository.CatalogRouteInput{{SKUID: skuID, Priority: 100, Weight: 100}},
			Adapter: repository.CatalogAdapterInput{Code: descriptor.Code, Version: descriptor.Version, Protocol: descriptor.Protocol, ImplementationDigest: descriptor.ImplementationDigest, MinimumSemanticVersion: descriptor.MinimumSemanticVersion},
		}, actorID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var costPlanID uint64
	if err := store.DB().QueryRowContext(ctx, `SELECT cp.id FROM gw_cost_plans cp JOIN gw_offerings o ON o.id=cp.offering_id WHERE cp.release_id=?`, releaseID).Scan(&costPlanID); err != nil {
		t.Fatal(err)
	}
	createEvidence := func(price string) uint64 {
		t.Helper()
		var id uint64
		factHMAC := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		if price == "0.08" {
			factHMAC = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		}
		err := store.WithTx(ctx, func(tx *sql.Tx) error {
			var err error
			id, err = store.CreateRateEvidence(ctx, tx, repository.RateEvidenceInput{
				SourceType: "provider_pricing", AuthorityLevel: "official", SourceReference: "https://pricing.example.com/seedance",
				ObservedAt: time.Now().UTC(), UnitCode: "second", UnitPrice: price, CurrencyCode: "CNY", CurrencyVersion: 1,
				FactHMAC: factHMAC,
			}, actorID)
			if err != nil {
				return err
			}
			return store.ReviewRateEvidence(ctx, tx, id, repository.RateEvidenceReviewInput{Decision: "accepted", ReasonCode: "verified", ExpectedVersion: 1}, actorID)
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	sellEvidenceID := createEvidence("0.20")
	costEvidenceID := createEvidence("0.08")
	createRate := func(kind string, parentID, evidenceID, expectedVersion uint64) {
		t.Helper()
		err := store.WithTx(ctx, func(tx *sql.Tx) error {
			_, err := store.CreateCatalogRate(ctx, tx, kind, releaseID, parentID, repository.CatalogRateInput{
				ExpectedVersion: expectedVersion, EvidenceID: evidenceID, ComponentCode: "generated_second",
				QuantitySource: billing.QuantityGeneratedSeconds, ChargeEvent: billing.ChargeSucceeded,
				UnitScale: 0, QuantityStep: "1", MaxQuantity: "60",
			}, actorID)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	createRate("sell", skuID, sellEvidenceID, 3)
	createRate("cost", costPlanID, costEvidenceID, 4)
	var downstreamPath string
	if err := store.DB().QueryRowContext(ctx, `SELECT path FROM gw_sku_downstream_paths WHERE release_id=? AND sku_id=?`, releaseID, skuID).Scan(&downstreamPath); err != nil {
		t.Fatal(err)
	}
	if downstreamPath != "/v1/videos/generations" {
		t.Fatalf("default downstream path=%q", downstreamPath)
	}
	return store, releaseID
}

func TestMySQLCatalogManagementLifecyclePublishesCompleteRelease(t *testing.T) {
	store, releaseID := mysqlPublishableRelease(t)
	ctx := context.Background()
	if err := store.WithTx(ctx, func(tx *sql.Tx) error { return store.PublishRelease(ctx, tx, releaseID, 1) }); err != nil {
		t.Fatal(err)
	}
	var status, contentHash string
	var version uint64
	if err := store.DB().QueryRowContext(ctx, `SELECT status,config_version,content_hash FROM gw_catalog_releases WHERE id=?`, releaseID).Scan(&status, &version, &contentHash); err != nil {
		t.Fatal(err)
	}
	if status != "published" || version != 6 || len(contentHash) != 64 {
		t.Fatalf("published release status=%s version=%d hash=%q", status, version, contentHash)
	}
}

// mysqlClonedTables is checked for row-count equality after a fork. The
// authoritative layer set — and its correspondence with the content digest — is
// locked by TestCloneCoversExactlyTheDigestedTables; what this list adds is that
// real composite foreign keys and CHECK constraints accept every cloned row.
var mysqlClonedTables = []string{
	"gw_catalog_models", "gw_catalog_model_names", "gw_model_operations", "gw_skus",
	"gw_sku_downstream_paths", "gw_sell_rates", "gw_channel_transports",
	"gw_transport_allowed_hosts", "gw_products", "gw_product_transports",
	"gw_product_transport_actions", "gw_offerings", "gw_cost_plans", "gw_cost_rates",
	"gw_routes", "gw_offering_runtime_state",
}

// TestMySQLForkedReleasePublishesAndActivates is the lifecycle the sqlite clone
// tests cannot reach: PublishRelease locks with a literal FOR UPDATE, the catalog
// tables carry composite (release_id, id) foreign keys, and MySQL 8 enforces the
// CHECK constraints. It walks the whole operator edit cycle with zero edits —
// publish, fork, publish the fork, activate it — because a fork that cannot
// complete that cycle is not a usable starting point for any edit.
func TestMySQLForkedReleasePublishesAndActivates(t *testing.T) {
	store, sourceID := mysqlPublishableRelease(t)
	ctx := context.Background()
	const actorID = 1
	if err := store.WithTx(ctx, func(tx *sql.Tx) error { return store.PublishRelease(ctx, tx, sourceID, actorID) }); err != nil {
		t.Fatal(err)
	}
	if err := store.WithTx(ctx, func(tx *sql.Tx) error { return store.ActivateRelease(ctx, tx, sourceID, 1) }); err != nil {
		t.Fatal(err)
	}
	var forkID uint64
	err := store.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		forkID, err = store.ForkCatalogRelease(ctx, tx, sourceID, repository.CatalogDraftInput{
			SemanticVersion: "1.0.1", SemanticDigest: adapter.SemanticDigest(),
		}, actorID)
		return err
	})
	if err != nil {
		t.Fatalf("forking a published release failed: %v", err)
	}
	if forkID == sourceID {
		t.Fatal("the fork reused the source release id")
	}
	mysqlAssertForkMirrorsSource(t, store, sourceID, forkID)
	mysqlAssertForkGoesLive(t, store, sourceID, forkID)
}

// mysqlAssertForkMirrorsSource checks the clone against the source row for row,
// and that nothing in the fork still points into the source release.
func mysqlAssertForkMirrorsSource(t *testing.T, store *repository.Store, sourceID, forkID uint64) {
	t.Helper()
	ctx := context.Background()
	for _, table := range mysqlClonedTables {
		var source, fork uint64
		query := `SELECT COUNT(*) FROM ` + table + ` WHERE release_id=?`
		if err := store.DB().QueryRowContext(ctx, query, sourceID).Scan(&source); err != nil {
			t.Fatalf("count %s of source: %v", table, err)
		}
		if err := store.DB().QueryRowContext(ctx, query, forkID).Scan(&fork); err != nil {
			t.Fatalf("count %s of fork: %v", table, err)
		}
		// An empty source table would make the comparison vacuous, so the fixture
		// is required to populate every layer the fork is supposed to copy.
		if source == 0 {
			t.Fatalf("the source release has no %s rows, so the clone assertion proves nothing", table)
		}
		if fork != source {
			t.Fatalf("%s: fork has %d rows, source has %d", table, fork, source)
		}
	}
	// Rates reuse the source's accepted evidence rather than inventing a second
	// review of a price nobody reviewed twice (§5.2.1 requirement 3).
	for _, table := range []string{"gw_sell_rates", "gw_cost_rates"} {
		var shared uint64
		if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` f WHERE f.release_id=? AND NOT EXISTS (SELECT 1 FROM `+table+` s WHERE s.release_id=? AND s.rate_evidence_review_event_id=f.rate_evidence_review_event_id)`, forkID, sourceID).Scan(&shared); err != nil {
			t.Fatal(err)
		}
		if shared != 0 {
			t.Fatalf("%s: %d cloned rates reference evidence the source never used", table, shared)
		}
	}
	// The composite foreign keys already make a cross-release parent impossible to
	// insert; this states the invariant the keys enforce, so a future migration
	// that relaxes one does not silently relax the clone.
	var leaked uint64
	if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_routes r JOIN gw_skus s ON s.id=r.sku_id JOIN gw_offerings o ON o.id=r.offering_id WHERE r.release_id=? AND (s.release_id<>? OR o.release_id<>?)`, forkID, forkID, forkID).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked != 0 {
		t.Fatalf("%d forked routes point outside the fork", leaked)
	}
}

// mysqlAssertForkGoesLive publishes the unedited fork and moves the active
// pointer onto it. This is the requirement M3a exists to satisfy: the operator
// forks, edits, publishes, activates, and the fork must survive that path even
// when the edit is empty.
func mysqlAssertForkGoesLive(t *testing.T, store *repository.Store, sourceID, forkID uint64) {
	t.Helper()
	ctx := context.Background()
	if err := store.WithTx(ctx, func(tx *sql.Tx) error { return store.PublishRelease(ctx, tx, forkID, 1) }); err != nil {
		t.Fatalf("an unedited fork was not publishable: %v", err)
	}
	var forkHash, sourceHash string
	if err := store.DB().QueryRowContext(ctx, `SELECT content_hash FROM gw_catalog_releases WHERE id=?`, forkID).Scan(&forkHash); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRowContext(ctx, `SELECT content_hash FROM gw_catalog_releases WHERE id=?`, sourceID).Scan(&sourceHash); err != nil {
		t.Fatal(err)
	}
	// Different by design: the digest covers primary keys and the clone has new
	// ones. Equal hashes would trip uq_gw_catalog_releases_content_hash and make
	// the fork impossible to publish alongside the release it came from
	// (§5.2.1 requirement 2).
	if len(forkHash) != 64 || forkHash == sourceHash {
		t.Fatalf("fork hash=%q source hash=%q", forkHash, sourceHash)
	}
	// The source activation bumped the singleton to version 2.
	if err := store.WithTx(ctx, func(tx *sql.Tx) error { return store.ActivateRelease(ctx, tx, forkID, 2) }); err != nil {
		t.Fatalf("the published fork could not be activated: %v", err)
	}
	var active uint64
	if err := store.DB().QueryRowContext(ctx, `SELECT active_release_id FROM gw_catalog_runtime_state WHERE id=1`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != forkID {
		t.Fatalf("active release is %d, want the fork %d", active, forkID)
	}
	// Routing serves only offerings whose runtime state is active, so a fork that
	// copied catalog content but not runtime state would activate and then serve
	// nothing. Every cloned offering must be routable.
	var offerings, routable uint64
	if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_offerings WHERE release_id=?`, forkID).Scan(&offerings); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_offering_runtime_state WHERE release_id=? AND state='active'`, forkID).Scan(&routable); err != nil {
		t.Fatal(err)
	}
	if offerings == 0 || routable != offerings {
		t.Fatalf("the activated fork has %d offerings but %d are active", offerings, routable)
	}
}

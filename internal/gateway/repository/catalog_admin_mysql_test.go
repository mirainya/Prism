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

func TestMySQLCatalogManagementLifecyclePublishesCompleteRelease(t *testing.T) {
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
	if err := store.WithTx(ctx, func(tx *sql.Tx) error { return store.PublishRelease(ctx, tx, releaseID, actorID) }); err != nil {
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

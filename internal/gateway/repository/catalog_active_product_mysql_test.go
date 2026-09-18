//go:build integration

package repository_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/mirainya/Prism/internal/gateway/adapter"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

type catalogGraphCount struct {
	name   string
	query  string
	growth uint64
	count  uint64
}

func activeCatalogGraphCounts(t *testing.T, store *repository.Store, releaseID uint64) []catalogGraphCount {
	t.Helper()
	counts := []catalogGraphCount{
		{name: "transports", query: `SELECT COUNT(*) FROM gw_channel_transports WHERE release_id=?`, growth: 1},
		{name: "hosts", query: `SELECT COUNT(*) FROM gw_transport_allowed_hosts WHERE release_id=?`, growth: 1},
		{name: "products", query: `SELECT COUNT(*) FROM gw_products WHERE release_id=?`, growth: 1},
		{name: "product transports", query: `SELECT COUNT(*) FROM gw_product_transports WHERE release_id=?`, growth: 1},
		{name: "actions", query: `SELECT COUNT(*) FROM gw_product_transport_actions WHERE release_id=?`, growth: 2},
		{name: "offerings", query: `SELECT COUNT(*) FROM gw_offerings WHERE release_id=?`, growth: 1},
		{name: "offering runtime states", query: `SELECT COUNT(*) FROM gw_offering_runtime_state WHERE release_id=?`, growth: 1},
		{name: "offering state events", query: `SELECT COUNT(*) FROM gw_offering_state_events WHERE release_id=?`, growth: 1},
		{name: "cost plans", query: `SELECT COUNT(*) FROM gw_cost_plans WHERE release_id=?`, growth: 1},
		{name: "routes", query: `SELECT COUNT(*) FROM gw_routes WHERE release_id=?`, growth: 1},
	}
	for index := range counts {
		if err := store.DB().QueryRow(counts[index].query, releaseID).Scan(&counts[index].count); err != nil {
			t.Fatal(err)
		}
	}
	return counts
}

func assertCatalogGraphGrowth(t *testing.T, before, after []catalogGraphCount) {
	t.Helper()
	for index := range before {
		if after[index].name != before[index].name || after[index].count != before[index].count+before[index].growth {
			t.Fatalf("%s count=%d, before=%d", before[index].name, after[index].count, before[index].count)
		}
	}
}

func TestMySQLCreateActiveCatalogProductEditsInPlaceAndProtectsConcurrency(t *testing.T) {
	store, releaseID := mysqlActiveRelease(t)
	ctx := context.Background()
	const actorID = 1

	var channelID, poolID, skuID, version uint64
	var oldHash string
	if err := store.DB().QueryRowContext(ctx, `SELECT channel_id,id FROM gw_credential_pools ORDER BY id LIMIT 1`).Scan(&channelID, &poolID); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRowContext(ctx, `SELECT id FROM gw_skus WHERE release_id=? ORDER BY id LIMIT 1`, releaseID).Scan(&skuID); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRowContext(ctx, `SELECT config_version,content_hash FROM gw_catalog_releases WHERE id=?`, releaseID).Scan(&version, &oldHash); err != nil {
		t.Fatal(err)
	}
	descriptor, found := adapter.DescriptorFor("seedance", 1)
	if !found {
		t.Fatal("seedance adapter descriptor is missing")
	}
	input := repository.CatalogProductInput{
		ChannelID: channelID, CredentialPoolID: poolID, ProductCode: "seedance-2.0-backup", VendorModel: "seedance-2-0-backup",
		CapabilityConstraints: json.RawMessage(`{"duration":{"min":2,"max":12}}`), ConstraintsSchemaVersion: 1,
		AdapterCode: descriptor.Code, AdapterVersion: descriptor.Version, TransportCode: "seedance-backup-v1", BaseURL: "https://backup.example.com",
		Protocol: descriptor.Protocol, RequestMethod: "POST", RequestPath: "/api/v3/contents/generations/tasks", AuthScheme: "bearer",
		TransportTimeoutMS: 30000, TaskTimeoutMS: 120000, TaskScope: "task", CancelMode: "none", SourceURLPolicy: "fixed",
		UpstreamScopeKind: "product_transport", UpstreamScopeKey: "seedance-backup-v1",
		Actions: []repository.CatalogProductActionInput{
			{ActionCode: "submit", AllowedSourceState: "allocated", IdempotencyMode: "user_keyed", RequestSchemaVersion: 1, ResponseSchemaVersion: 1},
			{ActionCode: "query", AllowedSourceState: "accepted", IdempotencyMode: "none", RequestSchemaVersion: 1, ResponseSchemaVersion: 1},
		},
		CostPlanCode: "backup-cny", Routes: []repository.CatalogRouteInput{{SKUID: skuID, Priority: 200, Weight: 100}},
		Adapter: repository.CatalogAdapterInput{Code: descriptor.Code, Version: descriptor.Version, Protocol: descriptor.Protocol, ImplementationDigest: descriptor.ImplementationDigest, MinimumSemanticVersion: descriptor.MinimumSemanticVersion},
	}

	before := activeCatalogGraphCounts(t, store, releaseID)
	var result repository.CatalogChangeResult
	err := store.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		result, err = store.CreateActiveCatalogProduct(ctx, tx, releaseID, version, input, actorID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ReleaseID != releaseID || result.SourceReleaseID != releaseID || result.ConfigVersion != version+1 || !result.Activated {
		t.Fatalf("result=%+v, release=%d version=%d", result, releaseID, version)
	}
	after := activeCatalogGraphCounts(t, store, releaseID)
	assertCatalogGraphGrowth(t, before, after)

	var activeReleaseID, releaseCount, auditCount uint64
	var newHash string
	if err := store.DB().QueryRowContext(ctx, `SELECT active_release_id FROM gw_catalog_runtime_state WHERE id=1`).Scan(&activeReleaseID); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_catalog_releases`).Scan(&releaseCount); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRowContext(ctx, `SELECT content_hash FROM gw_catalog_releases WHERE id=?`, releaseID).Scan(&newHash); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE action='catalog_change.product_create' AND resource_type='catalog_release' AND resource_id=?`, releaseID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if activeReleaseID != releaseID || releaseCount != 1 || newHash == oldHash || auditCount != 1 {
		t.Fatalf("active=%d releases=%d old_hash=%s new_hash=%s audits=%d", activeReleaseID, releaseCount, oldHash, newHash, auditCount)
	}

	for name, guards := range map[string][2]uint64{
		"stale release": {releaseID + 1, result.ConfigVersion},
		"stale version": {releaseID, version},
	} {
		t.Run(name, func(t *testing.T) {
			err := store.WithTx(ctx, func(tx *sql.Tx) error {
				_, err := store.CreateActiveCatalogProduct(ctx, tx, guards[0], guards[1], input, actorID)
				return err
			})
			if !errors.Is(err, repository.ErrConflict) {
				t.Fatalf("err=%v, want ErrConflict", err)
			}
		})
	}

	broken := input
	broken.TransportCode = "rollback-check-v1"
	broken.UpstreamScopeKey = broken.TransportCode
	err = store.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := store.CreateActiveCatalogProduct(ctx, tx, releaseID, result.ConfigVersion, broken, actorID)
		return err
	})
	if err == nil {
		t.Fatal("duplicate product unexpectedly succeeded")
	}
	rolledBack := activeCatalogGraphCounts(t, store, releaseID)
	for index := range after {
		if rolledBack[index].count != after[index].count {
			t.Fatalf("%s count after rollback=%d, want %d", after[index].name, rolledBack[index].count, after[index].count)
		}
	}
	var finalVersion, finalAuditCount uint64
	var finalHash string
	if err := store.DB().QueryRowContext(ctx, `SELECT config_version,content_hash FROM gw_catalog_releases WHERE id=?`, releaseID).Scan(&finalVersion, &finalHash); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE action='catalog_change.product_create' AND resource_id=?`, releaseID).Scan(&finalAuditCount); err != nil {
		t.Fatal(err)
	}
	if finalVersion != result.ConfigVersion || finalHash != newHash || finalAuditCount != auditCount {
		t.Fatalf("rollback left version=%d hash=%s audits=%d", finalVersion, finalHash, finalAuditCount)
	}
}

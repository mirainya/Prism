package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/mirainya/Prism/internal/gateway/billing"
)

// stubSpecSource stands in for the adapter manifest registry.
type stubSpecSource struct {
	specs map[string]billing.ExpressionSpec
}

func (s stubSpecSource) ExpressionSpec(code string, version uint32, variant string) (billing.ExpressionSpec, error) {
	key := fmt.Sprintf("%s@%d/%s", code, version, variant)
	spec, ok := s.specs[key]
	if !ok {
		return billing.ExpressionSpec{}, fmt.Errorf("no manifest for %s", key)
	}
	return spec, nil
}

func secondsSpec(t *testing.T, digest, min, max string) billing.ExpressionSpec {
	t.Helper()
	domain, err := billing.IntDomain(min, max)
	if err != nil {
		t.Fatal(err)
	}
	return billing.ExpressionSpec{
		Digest:   digest,
		Declared: map[string]bool{"seconds": true},
		Domains:  map[string]billing.VarDomain{"seconds": domain},
	}
}

func boundGateDB(t *testing.T, expr string, storedBound any) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	for _, query := range []string{
		`CREATE TABLE gw_skus(id INTEGER, release_id INTEGER, variant_code TEXT)`,
		`CREATE TABLE gw_routes(id INTEGER, release_id INTEGER, sku_id INTEGER, offering_id INTEGER)`,
		`CREATE TABLE gw_offerings(id INTEGER, release_id INTEGER, product_transport_id INTEGER)`,
		`CREATE TABLE gw_product_transports(id INTEGER, release_id INTEGER, channel_transport_id INTEGER)`,
		`CREATE TABLE gw_channel_transports(id INTEGER, release_id INTEGER, adapter_implementation_id INTEGER)`,
		`CREATE TABLE gw_adapter_implementations(id INTEGER, adapter_code TEXT, contract_version INTEGER)`,
		`CREATE TABLE gw_cost_plans(id INTEGER, release_id INTEGER, offering_id INTEGER)`,
		`CREATE TABLE gw_sell_rates(id INTEGER, release_id INTEGER, sku_id INTEGER, pricing_mode TEXT, pricing_expr TEXT, max_price TEXT)`,
		`CREATE TABLE gw_cost_rates(id INTEGER, release_id INTEGER, cost_plan_id INTEGER, pricing_mode TEXT, pricing_expr TEXT, max_price TEXT)`,
		`INSERT INTO gw_skus VALUES (10,1,'seedance25')`,
		`INSERT INTO gw_routes VALUES (20,1,10,30)`,
		`INSERT INTO gw_offerings VALUES (30,1,40)`,
		`INSERT INTO gw_product_transports VALUES (40,1,50)`,
		`INSERT INTO gw_channel_transports VALUES (50,1,60)`,
		`INSERT INTO gw_adapter_implementations VALUES (60,'seedance',1)`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO gw_sell_rates VALUES (70,1,10,'expression',?,?)`, expr, storedBound); err != nil {
		t.Fatal(err)
	}
	return db
}

func withSpecSource(t *testing.T, source ExpressionSpecSource) {
	t.Helper()
	SetExpressionSpecSource(source)
	t.Cleanup(func() { SetExpressionSpecSource(nil) })
}

func TestProveBoundsWritesMaxPriceFromManifestDomain(t *testing.T) {
	db := boundGateDB(t, "seconds * 0.20", nil)
	withSpecSource(t, stubSpecSource{specs: map[string]billing.ExpressionSpec{
		"seedance@1/seedance25": secondsSpec(t, "digest-a", "4", "30"),
	}})
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := proveCatalogExpressionBounds(context.Background(), tx, 1); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := tx.QueryRow(`SELECT max_price FROM gw_sell_rates WHERE id=70`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	// 30 * 0.20, stored at the money column's scale.
	if stored != "6.000000000000000000" {
		t.Fatalf("max_price=%q, want 6.000000000000000000", stored)
	}
}

func TestCheckBoundsRejectsStoredBoundBelowProof(t *testing.T) {
	// The stored bound was proven against a 4~10 domain; the manifest in this
	// binary widened it to 4~30, so the reservation would now under-charge.
	db := boundGateDB(t, "seconds * 0.20", "2.000000000000000000")
	withSpecSource(t, stubSpecSource{specs: map[string]billing.ExpressionSpec{
		"seedance@1/seedance25": secondsSpec(t, "digest-a", "4", "30"),
	}})
	if err := checkCatalogExpressionBounds(context.Background(), db, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("err=%v, want ErrConflict", err)
	}
	// A bound above the proof over-reserves, which is safe.
	if _, err := db.Exec(`UPDATE gw_sell_rates SET max_price='9.000000000000000000' WHERE id=70`); err != nil {
		t.Fatal(err)
	}
	if err := checkCatalogExpressionBounds(context.Background(), db, 1); err != nil {
		t.Fatalf("over-reserving bound was rejected: %v", err)
	}
}

func TestBoundGateFailsClosedWithoutManifestSource(t *testing.T) {
	db := boundGateDB(t, "seconds * 0.20", "6.000000000000000000")
	// No source registered: an expression rate has no provable bound, so the
	// release must not pass instead of defaulting to the stored value.
	SetExpressionSpecSource(nil)
	if err := checkCatalogExpressionBounds(context.Background(), db, 1); !errors.Is(err, ErrExpressionSpecUnavailable) {
		t.Fatalf("err=%v, want ErrExpressionSpecUnavailable", err)
	}
}

func TestBoundGateRejectsRateWithNoAdapterRoute(t *testing.T) {
	db := boundGateDB(t, "seconds * 0.20", "6.000000000000000000")
	if _, err := db.Exec(`DELETE FROM gw_routes`); err != nil {
		t.Fatal(err)
	}
	withSpecSource(t, stubSpecSource{specs: map[string]billing.ExpressionSpec{
		"seedance@1/seedance25": secondsSpec(t, "digest-a", "4", "30"),
	}})
	if err := checkCatalogExpressionBounds(context.Background(), db, 1); !errors.Is(err, ErrExpressionSpecUnavailable) {
		t.Fatalf("err=%v, want ErrExpressionSpecUnavailable", err)
	}
}

func TestBoundGateRequiresEveryServingManifestToAgree(t *testing.T) {
	db := boundGateDB(t, "seconds * 0.20", nil)
	// A second route reaches the same SKU through another adapter. The sell
	// price may not depend on the selected route, so the widest domain wins.
	for _, query := range []string{
		`INSERT INTO gw_routes VALUES (21,1,10,31)`,
		`INSERT INTO gw_offerings VALUES (31,1,41)`,
		`INSERT INTO gw_product_transports VALUES (41,1,51)`,
		`INSERT INTO gw_channel_transports VALUES (51,1,61)`,
		`INSERT INTO gw_adapter_implementations VALUES (61,'generic',1)`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	withSpecSource(t, stubSpecSource{specs: map[string]billing.ExpressionSpec{
		"seedance@1/seedance25": secondsSpec(t, "digest-a", "4", "30"),
		"generic@1/seedance25":  secondsSpec(t, "digest-b", "4", "60"),
	}})
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := proveCatalogExpressionBounds(context.Background(), tx, 1); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := tx.QueryRow(`SELECT max_price FROM gw_sell_rates WHERE id=70`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	// 60 * 0.20 from the wider manifest, not 6 from the narrower one.
	if stored != "12.000000000000000000" {
		t.Fatalf("max_price=%q, want 12.000000000000000000", stored)
	}
	// A manifest that does not declare the variable at all fails the whole rate.
	SetExpressionSpecSource(stubSpecSource{specs: map[string]billing.ExpressionSpec{
		"seedance@1/seedance25": secondsSpec(t, "digest-a", "4", "30"),
		"generic@1/seedance25":  {Digest: "digest-c", Declared: map[string]bool{}},
	}})
	if err := proveCatalogExpressionBounds(context.Background(), tx, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("err=%v, want ErrConflict", err)
	}
}

func TestBoundGateIgnoresFlatOnlyCatalog(t *testing.T) {
	db := boundGateDB(t, "seconds * 0.20", nil)
	if _, err := db.Exec(`UPDATE gw_sell_rates SET pricing_mode='flat',pricing_expr=NULL`); err != nil {
		t.Fatal(err)
	}
	// No source registered and none needed: today's catalogs are flat-only and
	// must keep passing untouched.
	SetExpressionSpecSource(nil)
	if err := checkCatalogExpressionBounds(context.Background(), db, 1); err != nil {
		t.Fatalf("flat-only catalog was gated: %v", err)
	}
}

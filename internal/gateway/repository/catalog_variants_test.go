package repository

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mirainya/Prism/internal/gateway/billing"
)

func TestVariantGateAcceptsDeclaredVariant(t *testing.T) {
	db := boundGateDB(t, "seconds * 0.20", "6.000000000000000000")
	withSpecSource(t, stubSpecSource{specs: map[string]billing.ExpressionSpec{
		"seedance@1/seedance25": secondsSpec(t, "digest-a", "4", "30"),
	}})
	if err := checkCatalogVariants(context.Background(), db, 1); err != nil {
		t.Fatalf("declared variant was rejected: %v", err)
	}
}

func TestVariantGateRejectsUndeclaredVariant(t *testing.T) {
	db := boundGateDB(t, "seconds * 0.20", "6.000000000000000000")
	// The binary's manifest describes seedance, but not this variant: no
	// parameter domains and no hard constraints, so the shape cannot be served.
	withSpecSource(t, stubSpecSource{specs: map[string]billing.ExpressionSpec{
		"seedance@1/official": secondsSpec(t, "digest-a", "5", "10"),
	}})
	err := checkCatalogVariants(context.Background(), db, 1)
	if !errors.Is(err, ErrUnknownVariant) {
		t.Fatalf("err=%v, want ErrUnknownVariant", err)
	}
	// The message must name the SKU and the coordinate so the operator can fix it.
	if got := err.Error(); !strings.Contains(got, "seedance@1/seedance25") || !strings.Contains(got, "SKU 10") {
		t.Fatalf("error does not identify the offender: %s", got)
	}
}

func TestVariantGateFailsClosedForExplicitVariantWithoutManifests(t *testing.T) {
	db := boundGateDB(t, "seconds * 0.20", "6.000000000000000000")
	SetExpressionSpecSource(nil)
	if err := checkCatalogVariants(context.Background(), db, 1); !errors.Is(err, ErrExpressionSpecUnavailable) {
		t.Fatalf("err=%v, want ErrExpressionSpecUnavailable", err)
	}
}

func TestVariantGateIgnoresDefaultVariant(t *testing.T) {
	db := boundGateDB(t, "seconds * 0.20", "6.000000000000000000")
	if _, err := db.Exec(`UPDATE gw_skus SET variant_code='default'`); err != nil {
		t.Fatal(err)
	}
	// Adapters that predate the manifest set declare no variants at all, so the
	// implicit default must stay publishable with no manifest registered.
	SetExpressionSpecSource(nil)
	if err := checkCatalogVariants(context.Background(), db, 1); err != nil {
		t.Fatalf("default variant was gated: %v", err)
	}
}

func TestVariantGateSkipsUnroutedSKU(t *testing.T) {
	db := boundGateDB(t, "seconds * 0.20", "6.000000000000000000")
	if _, err := db.Exec(`DELETE FROM gw_routes`); err != nil {
		t.Fatal(err)
	}
	// A SKU with no route reaches no adapter, so there is no manifest to check
	// it against. CheckCatalogStructure already refuses to publish it.
	SetExpressionSpecSource(nil)
	if err := checkCatalogVariants(context.Background(), db, 1); err != nil {
		t.Fatalf("unrouted SKU was gated: %v", err)
	}
}

// TestVariantGateChecksEveryServingAdapter mirrors the bound proof: a SKU
// reachable through two adapters must be declared by both, because routing may
// pick either one.
func TestVariantGateChecksEveryServingAdapter(t *testing.T) {
	db := boundGateDB(t, "seconds * 0.20", "6.000000000000000000")
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
	}})
	err := checkCatalogVariants(context.Background(), db, 1)
	if !errors.Is(err, ErrUnknownVariant) || !strings.Contains(err.Error(), "generic@1/seedance25") {
		t.Fatalf("err=%v, want ErrUnknownVariant naming generic@1/seedance25", err)
	}
	SetExpressionSpecSource(stubSpecSource{specs: map[string]billing.ExpressionSpec{
		"seedance@1/seedance25": secondsSpec(t, "digest-a", "4", "30"),
		"generic@1/seedance25":  secondsSpec(t, "digest-b", "4", "60"),
	}})
	if err := checkCatalogVariants(context.Background(), db, 1); err != nil {
		t.Fatalf("both adapters declare the variant, yet: %v", err)
	}
}

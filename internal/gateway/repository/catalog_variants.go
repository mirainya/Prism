package repository

import (
	"context"
	"errors"
	"fmt"
)

// DefaultVariantCode is the implicit variant every SKU imported before variants
// existed carries. It is deliberately exempt from the manifest check below: the
// adapters that predate the manifest set have no declared variants at all, and
// splitting their SKUs into real variants is an operator decision, not a
// condition for keeping today's catalog publishable.
const DefaultVariantCode = "default"

// ErrUnknownVariant means a SKU names a variant no manifest of its serving
// adapter declares. Publishing it would route real traffic to a commercial shape
// this binary cannot describe: no parameter domains, so no provable price bound,
// and no hard constraints, so no way to reject a request the upstream will.
var ErrUnknownVariant = errors.New("repository: SKU names a variant the adapter manifest does not declare")

// checkCatalogVariants verifies every explicitly chosen variant against the
// manifests of every adapter that can serve the SKU. Like the bound proof, this
// runs on both the publication path and the readiness path: a binary whose
// manifest dropped a variant must make the release un-ready rather than route to
// a shape it no longer understands.
func checkCatalogVariants(ctx context.Context, db CatalogPricingQuery, releaseID uint64) error {
	if db == nil || releaseID == 0 {
		return ErrInvalidInput
	}
	// One cheap count first: a catalog where nothing opted into variants — every
	// catalog that exists today — pays a single indexed count and never runs the
	// five-table join below.
	var explicit uint64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_skus WHERE release_id=? AND variant_code<>?`,
		releaseID, DefaultVariantCode).Scan(&explicit); err != nil {
		return err
	}
	if explicit == 0 {
		return nil
	}
	served, err := loadRateAdapterVariants(ctx, db, releaseID, 0, false)
	if err != nil {
		return err
	}
	pending := make(map[uint64][]adapterVariant)
	for skuID, refs := range served {
		for _, ref := range refs {
			if ref.Variant != DefaultVariantCode {
				pending[skuID] = append(pending[skuID], ref)
			}
		}
	}
	if len(pending) == 0 {
		// The SKUs naming a variant have no routes yet, so no adapter serves
		// them and there is nothing to check against.
		return nil
	}
	source := currentExpressionSpecSource()
	if source == nil {
		return fmt.Errorf("%w: %d SKUs name explicit variants", ErrExpressionSpecUnavailable, len(pending))
	}
	for skuID, refs := range pending {
		for _, ref := range refs {
			if _, err := source.ExpressionSpec(ref.Code, ref.Version, ref.Variant); err != nil {
				return fmt.Errorf("%w: SKU %d claims %s: %w", ErrUnknownVariant, skuID, ref, err)
			}
		}
	}
	return nil
}

// validateSKUVariant is the create-time half of the same rule, so a typo is
// rejected where the operator can still see which value they typed instead of
// surfacing as an un-publishable release later.
func validateSKUVariant(ctx context.Context, db CatalogPricingQuery, releaseID, skuID uint64, variant string) error {
	if variant == DefaultVariantCode {
		return nil
	}
	served, err := loadRateAdapterVariants(ctx, db, releaseID, skuID, false)
	if err != nil {
		return err
	}
	// A SKU has no routes yet when it is first created, so there is no adapter to
	// check against. The publication gate is the backstop that always runs.
	refs := served[skuID]
	if len(refs) == 0 {
		return nil
	}
	source := currentExpressionSpecSource()
	if source == nil {
		return fmt.Errorf("%w: SKU %d claims variant %q", ErrExpressionSpecUnavailable, skuID, variant)
	}
	for _, ref := range refs {
		if _, err := source.ExpressionSpec(ref.Code, ref.Version, ref.Variant); err != nil {
			return fmt.Errorf("%w: SKU %d claims %s: %w", ErrUnknownVariant, skuID, ref, err)
		}
	}
	return nil
}

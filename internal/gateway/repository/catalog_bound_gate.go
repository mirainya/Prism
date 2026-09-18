package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/mirainya/Prism/internal/gateway/billing"
)

// proveRateBound parses one rate's expression under every manifest coordinate
// that can serve it and returns the largest proven bound. Requiring all
// coordinates to agree keeps the sell price independent of the selected route.
func proveRateBound(rate expressionRate, refs []adapterVariant, source ExpressionSpecSource) (billing.Amount, error) {
	if len(refs) == 0 {
		return billing.Amount{}, fmt.Errorf("%w: rate %d has no adapter route", ErrExpressionSpecUnavailable, rate.id)
	}
	highest := billing.Zero()
	for _, ref := range refs {
		spec, err := source.ExpressionSpec(ref.Code, ref.Version, ref.Variant)
		if err != nil {
			return billing.Amount{}, fmt.Errorf("%w: rate %d under %s: %w", ErrExpressionSpecUnavailable, rate.id, ref, err)
		}
		expression, err := billing.ParseExpression(rate.source, spec.Declared)
		if err != nil {
			return billing.Amount{}, fmt.Errorf("%w: rate %d under %s: %w", ErrConflict, rate.id, ref, err)
		}
		proof, err := expression.ProveUpperBound(spec)
		if err != nil {
			return billing.Amount{}, fmt.Errorf("%w: rate %d under %s: %w", ErrConflict, rate.id, ref, err)
		}
		if proof.MaxPrice.Cmp(highest) > 0 {
			highest = proof.MaxPrice
		}
	}
	return highest, nil
}

// proveNewExpressionRate parses and bounds one expression before it is inserted,
// under exactly the manifest coordinates that already route to its parent. It
// exists so a bad expression is refused at the edit rather than at publication,
// when the operator has moved on and the failure reads as "the release will not
// publish" instead of "that formula is wrong".
//
// It deliberately reuses proveRateBound rather than approximating the bound: an
// expression whose bound this function could not prove is one publication would
// reject anyway, and two different bound calculations would eventually disagree.
func proveNewExpressionRate(ctx context.Context, tx *sql.Tx, kind string, releaseID, parentID uint64, source string) (*billing.Expression, billing.Amount, error) {
	if source == "" {
		return nil, billing.Amount{}, fmt.Errorf("%w: an expression rate needs an expression", ErrInvalidInput)
	}
	specs := currentExpressionSpecSource()
	if specs == nil {
		return nil, billing.Amount{}, fmt.Errorf("%w: no manifest source is registered", ErrExpressionSpecUnavailable)
	}
	cost := kind == "cost"
	variants, err := loadRateAdapterVariants(ctx, tx, releaseID, parentID, cost)
	if err != nil {
		return nil, billing.Amount{}, err
	}
	// The rate row does not exist yet, so proveRateBound is handed the identity of
	// the row about to be written; its id is only used in error messages.
	bound, err := proveRateBound(expressionRate{parent: parentID, source: source}, variants[parentID], specs)
	if err != nil {
		return nil, billing.Amount{}, err
	}
	// Parsed a second time against one coordinate's whitelist so the caller has a
	// compiled expression for RateComponent.Validate. proveRateBound already
	// checked it against every coordinate, so this parse cannot newly fail.
	spec, err := specs.ExpressionSpec(variants[parentID][0].Code, variants[parentID][0].Version, variants[parentID][0].Variant)
	if err != nil {
		return nil, billing.Amount{}, fmt.Errorf("%w: %w", ErrExpressionSpecUnavailable, err)
	}
	parsed, err := billing.ParseExpression(source, spec.Declared)
	if err != nil {
		return nil, billing.Amount{}, fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}
	return parsed, bound, nil
}

// proveCatalogExpressionBounds enumerates the manifest parameter domains of
// every expression rate in the draft and writes the proven maximum to
// max_price. It runs before CheckCatalogPricing during publication so the
// reservation amount is part of the published, digest-covered content.
func proveCatalogExpressionBounds(ctx context.Context, tx *sql.Tx, releaseID uint64) error {
	if tx == nil || releaseID == 0 {
		return ErrInvalidInput
	}
	for _, cost := range []bool{false, true} {
		rates, err := loadExpressionRates(ctx, tx, releaseID, cost)
		if err != nil {
			return err
		}
		if len(rates) == 0 {
			continue
		}
		source := currentExpressionSpecSource()
		if source == nil {
			return fmt.Errorf("%w: no manifest source is registered", ErrExpressionSpecUnavailable)
		}
		variants, err := loadRateAdapterVariants(ctx, tx, releaseID, 0, cost)
		if err != nil {
			return err
		}
		table, _ := rateTable(cost)
		for _, rate := range rates {
			bound, err := proveRateBound(rate, variants[rate.parent], source)
			if err != nil {
				return err
			}
			// The bound is stored at the money column's scale so the value the
			// digest covers is exactly the value Reserve() will read back.
			if _, err := tx.ExecContext(ctx, `UPDATE `+table+` SET max_price=? WHERE release_id=? AND id=?`,
				bound.Fixed(18), releaseID, rate.id); err != nil {
				return fmt.Errorf("store proven bound for rate %d: %w", rate.id, err)
			}
		}
	}
	return nil
}

// checkCatalogExpressionBounds re-proves the stored bounds without writing. It
// is the read-only half of the gate, so an already-published release is
// re-verified on every readiness check: if a manifest change in a new binary
// widened a parameter domain, the stored bound no longer covers the domain and
// the release stops being ready instead of silently under-reserving.
func checkCatalogExpressionBounds(ctx context.Context, db CatalogPricingQuery, releaseID uint64) error {
	for _, cost := range []bool{false, true} {
		rates, err := loadExpressionRates(ctx, db, releaseID, cost)
		if err != nil {
			return err
		}
		if len(rates) == 0 {
			continue
		}
		source := currentExpressionSpecSource()
		if source == nil {
			return fmt.Errorf("%w: no manifest source is registered", ErrExpressionSpecUnavailable)
		}
		variants, err := loadRateAdapterVariants(ctx, db, releaseID, 0, cost)
		if err != nil {
			return err
		}
		for _, rate := range rates {
			bound, err := proveRateBound(rate, variants[rate.parent], source)
			if err != nil {
				return err
			}
			stored, err := billing.ParseAmount(rate.stored, 18, true)
			if err != nil {
				return fmt.Errorf("%w: rate %d has no stored bound", ErrConflict, rate.id)
			}
			// A stored bound above the proof is safe (it over-reserves), below it
			// is not: the live charge could exceed the pre-authorized amount.
			if stored.Cmp(bound) < 0 {
				return fmt.Errorf("%w: rate %d stored bound %s is below the proven maximum %s",
					ErrConflict, rate.id, stored.String(), bound.String())
			}
		}
	}
	return nil
}

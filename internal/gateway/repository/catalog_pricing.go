package repository

import (
	"context"
	"fmt"
)

// CheckCatalogPricing checks the selected release, not prices that happen to
// exist in another draft. Accepted evidence is immutable once referenced.
func CheckCatalogPricing(ctx context.Context, db CatalogPricingQuery, releaseID uint64) error {
	if db == nil || releaseID == 0 {
		return ErrInvalidInput
	}
	var skus, invalid uint64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(CASE WHEN NOT EXISTS (
SELECT 1 FROM gw_sell_rates r
JOIN billing_system_state b ON b.id=1 AND b.currency_code=r.currency_code AND b.currency_version=r.currency_version
JOIN billing_currency_definitions d ON d.currency_code=r.currency_code AND d.definition_version=r.currency_version AND d.status='active'
JOIN gw_rate_evidence_review_events e ON e.id=r.rate_evidence_review_event_id AND e.decision='accepted'
JOIN gw_rate_evidence_review_state es ON es.rate_evidence_id=e.rate_evidence_id AND es.state='accepted' AND es.latest_review_event_id=e.id
WHERE r.release_id=s.release_id AND r.sku_id=s.id AND r.unit_price>=0 AND r.unit_code<>''
) THEN 1 ELSE 0 END),0) FROM gw_skus s WHERE s.release_id=?`, releaseID).Scan(&skus, &invalid); err != nil {
		return err
	}
	if skus == 0 || invalid > 0 {
		return fmt.Errorf("%w: catalog sell rates or settlement currency are incomplete", ErrConflict)
	}
	var offerings uint64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(CASE WHEN NOT EXISTS (
SELECT 1 FROM gw_cost_plans p WHERE p.release_id=o.release_id AND p.offering_id=o.id
) THEN 1 ELSE 0 END),0) FROM gw_offerings o WHERE o.release_id=?`, releaseID).Scan(&offerings, &invalid); err != nil {
		return err
	}
	if offerings == 0 || invalid > 0 {
		return fmt.Errorf("%w: catalog cost plans are incomplete", ErrConflict)
	}
	var plans uint64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(CASE WHEN EXISTS (
SELECT 1 FROM gw_cost_rates r
LEFT JOIN billing_currency_definitions d ON d.currency_code=r.currency_code AND d.definition_version=r.currency_version AND d.status='active'
LEFT JOIN gw_rate_evidence_review_events e ON e.id=r.rate_evidence_review_event_id AND e.decision='accepted'
LEFT JOIN gw_rate_evidence_review_state es ON es.rate_evidence_id=e.rate_evidence_id AND es.state='accepted' AND es.latest_review_event_id=e.id
WHERE r.release_id=p.release_id AND r.cost_plan_id=p.id AND (r.unit_price<0 OR r.unit_code='' OR d.id IS NULL OR e.id IS NULL OR es.id IS NULL)
) THEN 1 ELSE 0 END),0) FROM gw_cost_plans p WHERE p.release_id=?`, releaseID).Scan(&plans, &invalid); err != nil {
		return err
	}
	if plans == 0 || invalid > 0 {
		return fmt.Errorf("%w: configured catalog cost rates are invalid or unreviewed", ErrConflict)
	}
	if _, err := loadRateSchedules(ctx, db, releaseID, 0, false, true); err != nil {
		return err
	}
	if _, err := loadRateSchedules(ctx, db, releaseID, 0, true, true); err != nil {
		return err
	}
	// Variants are checked before the bound proof that runs under the same
	// adapter coordinates: an unknown variant has no declared parameter domains,
	// so "this shape is not declared" is the clearer failure. Both live on this
	// path rather than in CheckCatalogStructure because readiness re-runs only
	// this one, and a binary whose manifest dropped a variant must make the
	// release un-ready instead of serving a shape it no longer describes.
	if err := checkCatalogVariants(ctx, db, releaseID); err != nil {
		return err
	}
	// Expression rates carry a stored upper bound that Reserve() pre-authorizes.
	// Re-proving it here means a manifest whose parameter domains widened in a
	// newer binary makes the release un-ready instead of under-reserving.
	return checkCatalogExpressionBounds(ctx, db, releaseID)
}

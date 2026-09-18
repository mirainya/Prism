package repository

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"

	"github.com/mirainya/Prism/internal/gateway/billing"
)

type CatalogPricingQuery interface {
	ReadinessQuery
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// LoadSellSchedule reads all SKU components without joining them to routes.
// Prices cannot multiply route candidates or depend on the selected provider.
// Pre-flight callers use this to keep new Attempts on the currently accepted
// review event; settlement of in-flight Attempts must use
// LoadSettledSellSchedule instead so a later supersede cannot freeze funds.
func LoadSellSchedule(ctx context.Context, db CatalogPricingQuery, releaseID, skuID uint64) (billing.RateSchedule, error) {
	return loadSellSchedule(ctx, db, releaseID, skuID, true)
}

// LoadSettledSellSchedule reads sell rates for settling an existing Attempt.
// Published catalog rows are immutable, so an Attempt keeps its Release's
// originally accepted review event even after a later review supersedes it.
// Only pre-flight route selection requires the current review state.
func LoadSettledSellSchedule(ctx context.Context, db CatalogPricingQuery, releaseID, skuID uint64) (billing.RateSchedule, error) {
	return loadSellSchedule(ctx, db, releaseID, skuID, false)
}

func loadSellSchedule(ctx context.Context, db CatalogPricingQuery, releaseID, skuID uint64, requireCurrentReview bool) (billing.RateSchedule, error) {
	if db == nil || releaseID == 0 || skuID == 0 {
		return billing.RateSchedule{}, ErrInvalidInput
	}
	schedules, err := loadRateSchedules(ctx, db, releaseID, skuID, false, requireCurrentReview)
	if err != nil {
		return billing.RateSchedule{}, err
	}
	schedule, ok := schedules[skuID]
	if !ok {
		return billing.RateSchedule{}, fmt.Errorf("%w: SKU %d has no sell rates", ErrConflict, skuID)
	}
	return schedule, nil
}

// LoadSellSchedules returns every validated sell schedule in one bounded
// catalog query. Callers must select schedules by SKU; rates never participate
// in route expansion.
func LoadSellSchedules(ctx context.Context, db CatalogPricingQuery, releaseID uint64) (map[uint64]billing.RateSchedule, error) {
	if db == nil || releaseID == 0 {
		return nil, ErrInvalidInput
	}
	return loadRateSchedules(ctx, db, releaseID, 0, false, true)
}

// LoadCostSchedule resolves the cost plan selected by an Offering. The plan is
// selected by immutable catalog identity, never by provider response fields.
func LoadCostSchedule(ctx context.Context, db CatalogPricingQuery, releaseID, offeringID uint64) (billing.RateSchedule, error) {
	return loadCostSchedule(ctx, db, releaseID, offeringID, true)
}

func loadCostSchedule(ctx context.Context, db CatalogPricingQuery, releaseID, offeringID uint64, requireCurrentReview bool) (billing.RateSchedule, error) {
	if db == nil || releaseID == 0 || offeringID == 0 {
		return billing.RateSchedule{}, ErrInvalidInput
	}
	var planID uint64
	err := db.QueryRowContext(ctx, `SELECT p.id
FROM gw_offerings o
JOIN gw_cost_plans p ON p.release_id=o.release_id AND p.offering_id=o.id AND p.plan_code=o.cost_plan_code
WHERE o.release_id=? AND o.id=?`, releaseID, offeringID).Scan(&planID)
	if err == sql.ErrNoRows {
		return billing.RateSchedule{}, fmt.Errorf("%w: Offering %d has no selected cost plan", ErrConflict, offeringID)
	}
	if err != nil {
		return billing.RateSchedule{}, err
	}
	return loadCostScheduleByPlan(ctx, db, releaseID, planID, requireCurrentReview)
}

func loadCostScheduleByPlan(ctx context.Context, db CatalogPricingQuery, releaseID, planID uint64, requireCurrentReview bool) (billing.RateSchedule, error) {
	if db == nil || releaseID == 0 || planID == 0 {
		return billing.RateSchedule{}, ErrInvalidInput
	}
	schedules, err := loadRateSchedules(ctx, db, releaseID, planID, true, requireCurrentReview)
	if err != nil {
		return billing.RateSchedule{}, err
	}
	schedule, ok := schedules[planID]
	if !ok {
		return billing.RateSchedule{}, fmt.Errorf("%w: cost plan %d has no rates", ErrConflict, planID)
	}
	return schedule, nil
}

// pendingExpression locates a component that still needs its expression parsed.
type pendingExpression struct {
	parent uint64
	index  int
	rateID uint64
	source string
}

// rateExpressionCache is shared across loads: the same released expression text
// is parsed once per manifest coordinate for the lifetime of the process.
var rateExpressionCache = billing.NewExpressionCache()

// attachExpressions parses each expression component against the whitelist of
// every adapter manifest that can serve its parent. Requiring all coordinates to
// accept the text keeps a rate from becoming valid only on the route that
// happened to be selected.
func attachExpressions(ctx context.Context, db CatalogPricingQuery, releaseID uint64, cost bool,
	schedules map[uint64]billing.RateSchedule, pending []pendingExpression) error {
	if len(pending) == 0 {
		return nil
	}
	source := currentExpressionSpecSource()
	if source == nil {
		return fmt.Errorf("%w: no manifest source is registered", ErrExpressionSpecUnavailable)
	}
	variants, err := loadRateAdapterVariants(ctx, db, releaseID, 0, cost)
	if err != nil {
		return err
	}
	for _, item := range pending {
		refs := variants[item.parent]
		if len(refs) == 0 {
			return fmt.Errorf("%w: rate %d has no adapter route", ErrExpressionSpecUnavailable, item.rateID)
		}
		var parsed *billing.Expression
		var tiers map[string][]billing.ExprTier
		for _, ref := range refs {
			spec, err := source.ExpressionSpec(ref.Code, ref.Version, ref.Variant)
			if err != nil {
				return fmt.Errorf("%w: rate %d under %s: %w", ErrExpressionSpecUnavailable, item.rateID, ref, err)
			}
			expression, err := rateExpressionCache.Parse(item.source, spec.Declared, spec.Digest)
			if err != nil {
				return fmt.Errorf("%w: rate %d under %s: %w", ErrConflict, item.rateID, ref, err)
			}
			if parsed == nil {
				parsed = expression
				tiers = cloneExpressionTiers(spec.Tiers)
			} else if !reflect.DeepEqual(tiers, spec.Tiers) {
				return fmt.Errorf("%w: rate %d has incompatible tier tables under %s", ErrExpressionSpecUnavailable, item.rateID, ref)
			}
		}
		schedule := schedules[item.parent]
		schedule.Components[item.index].Expr = parsed
		schedule.Components[item.index].ExprTiers = tiers
		schedules[item.parent] = schedule
	}
	return nil
}

func cloneExpressionTiers(source map[string][]billing.ExprTier) map[string][]billing.ExprTier {
	if len(source) == 0 {
		return nil
	}
	clone := make(map[string][]billing.ExprTier, len(source))
	for name, tiers := range source {
		clone[name] = append([]billing.ExprTier(nil), tiers...)
	}
	return clone
}

func loadRateSchedules(ctx context.Context, db CatalogPricingQuery, releaseID, parentID uint64, cost, requireCurrentReview bool) (map[uint64]billing.RateSchedule, error) {
	table, parent := "gw_sell_rates", "sku_id"
	platform := ` AND EXISTS (SELECT 1 FROM billing_system_state b WHERE b.id=1 AND b.currency_code=r.currency_code AND b.currency_version=r.currency_version)`
	if cost {
		table, parent, platform = "gw_cost_rates", "cost_plan_id", ""
	}
	validity := `d.id IS NOT NULL AND d.status='active' AND e.decision='accepted' AND es.id IS NOT NULL`
	if !requireCurrentReview {
		// Published catalog rows are immutable. Existing Attempts keep using the
		// accepted review event fixed by their release even if a later review
		// supersedes it and prevents new Attempts.
		validity = `d.id IS NOT NULL AND e.decision='accepted'`
	}
	query := `SELECT r.` + parent + `,r.id,COALESCE(r.component_code,''),r.unit_code,
COALESCE(r.quantity_source,''),COALESCE(r.charge_event,''),r.unit_price,
COALESCE(r.pricing_mode,''),COALESCE(r.pricing_expr,''),COALESCE(r.max_price,''),r.unit_scale,
r.quantity_step,COALESCE(r.max_quantity,''),r.currency_code,r.currency_version,
COALESCE(d.fraction_digits,0),COALESCE(d.rounding_mode,''),COALESCE(d.max_amount,0),
CASE WHEN ` + validity + platform + ` THEN 1 ELSE 0 END
FROM ` + table + ` r
LEFT JOIN billing_currency_definitions d ON d.currency_code=r.currency_code AND d.definition_version=r.currency_version
LEFT JOIN gw_rate_evidence_review_events e ON e.id=r.rate_evidence_review_event_id
LEFT JOIN gw_rate_evidence_review_state es ON es.rate_evidence_id=e.rate_evidence_id AND es.latest_review_event_id=e.id AND es.state='accepted'
WHERE r.release_id=?`
	args := []any{releaseID}
	if parentID != 0 {
		query += ` AND r.` + parent + `=?`
		args = append(args, parentID)
	}
	query += ` ORDER BY r.` + parent + `,r.id`
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	schedules := make(map[uint64]billing.RateSchedule)
	// Expression rows need the adapter manifest whitelist before they can be
	// parsed. They are collected here so a flat-only schedule — every schedule
	// that exists today — costs no extra query on the hot path.
	var pending []pendingExpression
	for rows.Next() {
		var id uint64
		var component billing.RateComponent
		var currency billing.Currency
		var source string
		var valid bool
		if err := rows.Scan(&id, &component.ID, &component.Code, &component.Unit, &component.Source, &component.Event,
			&component.UnitPrice, &component.PricingMode, &source, &component.MaxPrice, &component.UnitScale,
			&component.QuantityStep, &component.MaxQuantity,
			&currency.Code, &currency.Version, &currency.FractionDigits, &currency.RoundingMode, &currency.MaxAmount, &valid); err != nil {
			return nil, err
		}
		if !valid {
			return nil, fmt.Errorf("%w: rate %d is unreviewed or has an invalid currency", ErrConflict, component.ID)
		}
		schedule, exists := schedules[id]
		if exists && schedule.Currency != currency {
			return nil, fmt.Errorf("%w: mixed currencies in rate schedule %d", ErrConflict, id)
		}
		schedule.Currency = currency
		schedule.Components = append(schedule.Components, component)
		if len(schedule.Components) > 32 {
			return nil, fmt.Errorf("%w: too many rate components", ErrConflict)
		}
		if component.PricingMode == billing.PricingModeExpression {
			pending = append(pending, pendingExpression{
				parent: id, index: len(schedule.Components) - 1, rateID: component.ID, source: source,
			})
		}
		schedules[id] = schedule
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := attachExpressions(ctx, db, releaseID, cost, schedules, pending); err != nil {
		return nil, err
	}
	for id, schedule := range schedules {
		if _, err := schedule.Reserve(); err != nil {
			return nil, fmt.Errorf("%w: invalid rate schedule %d: %w", ErrConflict, id, err)
		}
	}
	return schedules, nil
}

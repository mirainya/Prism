package repository

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"github.com/mirainya/Prism/internal/gateway/billing"
)

// ErrExpressionSpecUnavailable means no adapter manifest could be resolved for a
// rate that prices through an expression. The gate fails closed: without the
// declared parameter domains there is no provable upper bound, and SPEC §6.1
// refuses to publish a prepaid SKU whose maximum price cannot be stated.
var ErrExpressionSpecUnavailable = errors.New("repository: adapter manifest is unavailable for an expression rate")

// ExpressionSpecSource resolves the manifest facts an expression is checked
// against. It lives here rather than in the adapter package because the adapter
// package already imports this one; the process wiring registers the concrete
// source at startup.
type ExpressionSpecSource interface {
	ExpressionSpec(adapterCode string, adapterVersion uint32, variantCode string) (billing.ExpressionSpec, error)
}

var (
	expressionSpecMu     sync.RWMutex
	expressionSpecSource ExpressionSpecSource
)

// SetExpressionSpecSource installs the manifest source. Passing nil restores the
// fail-closed default, which is also what a process that never calls this gets.
func SetExpressionSpecSource(source ExpressionSpecSource) {
	expressionSpecMu.Lock()
	defer expressionSpecMu.Unlock()
	expressionSpecSource = source
}

func currentExpressionSpecSource() ExpressionSpecSource {
	expressionSpecMu.RLock()
	defer expressionSpecMu.RUnlock()
	return expressionSpecSource
}

// adapterVariant is one manifest coordinate a rate can be served under.
type adapterVariant struct {
	Code    string
	Version uint32
	Variant string
}

func (a adapterVariant) String() string {
	return a.Code + "@" + strconv.FormatUint(uint64(a.Version), 10) + "/" + a.Variant
}

// expressionRate is one rate row whose amount comes from an expression.
type expressionRate struct {
	id     uint64
	parent uint64
	source string
	stored string
}

func rateTable(cost bool) (table, parent string) {
	if cost {
		return "gw_cost_rates", "cost_plan_id"
	}
	return "gw_sell_rates", "sku_id"
}

func loadExpressionRates(ctx context.Context, db CatalogPricingQuery, releaseID uint64, cost bool) ([]expressionRate, error) {
	table, parent := rateTable(cost)
	rows, err := db.QueryContext(ctx, `SELECT id,`+parent+`,COALESCE(pricing_expr,''),COALESCE(max_price,'')
FROM `+table+` WHERE release_id=? AND pricing_mode='expression' ORDER BY id`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []expressionRate
	for rows.Next() {
		var rate expressionRate
		if err := rows.Scan(&rate.id, &rate.parent, &rate.source, &rate.stored); err != nil {
			return nil, err
		}
		if rate.source == "" {
			return nil, fmt.Errorf("%w: rate %d prices by expression but has no expression", ErrConflict, rate.id)
		}
		out = append(out, rate)
	}
	return out, rows.Err()
}

// loadRateAdapterVariants maps each SKU (or cost plan) to every manifest
// coordinate that can serve it. A SKU reachable through several adapters must
// satisfy all of them: the sell price may not depend on the selected route, so
// the expression has to be valid and bounded under each one.
func loadRateAdapterVariants(ctx context.Context, db CatalogPricingQuery, releaseID, parentID uint64, cost bool) (map[uint64][]adapterVariant, error) {
	query := `SELECT DISTINCT s.id,s.variant_code,a.adapter_code,a.contract_version
FROM gw_skus s
JOIN gw_routes rt ON rt.release_id=s.release_id AND rt.sku_id=s.id
JOIN gw_offerings o ON o.release_id=rt.release_id AND o.id=rt.offering_id`
	if cost {
		// A cost plan belongs to one Offering; its variant comes from the SKUs
		// that Offering actually serves.
		query = `SELECT DISTINCT p.id,s.variant_code,a.adapter_code,a.contract_version
FROM gw_cost_plans p
JOIN gw_offerings o ON o.release_id=p.release_id AND o.id=p.offering_id
JOIN gw_routes rt ON rt.release_id=o.release_id AND rt.offering_id=o.id
JOIN gw_skus s ON s.release_id=rt.release_id AND s.id=rt.sku_id`
	}
	query += `
JOIN gw_product_transports pt ON pt.release_id=o.release_id AND pt.id=o.product_transport_id
JOIN gw_channel_transports ct ON ct.release_id=pt.release_id AND ct.id=pt.channel_transport_id
JOIN gw_adapter_implementations a ON a.id=ct.adapter_implementation_id
WHERE `
	owner := "s"
	if cost {
		owner = "p"
	}
	query += owner + `.release_id=?`
	args := []any{releaseID}
	if parentID != 0 {
		query += ` AND ` + owner + `.id=?`
		args = append(args, parentID)
	}
	query += ` ORDER BY ` + owner + `.id,a.adapter_code,a.contract_version,s.variant_code`
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[uint64][]adapterVariant)
	for rows.Next() {
		var parent uint64
		var ref adapterVariant
		if err := rows.Scan(&parent, &ref.Variant, &ref.Code, &ref.Version); err != nil {
			return nil, err
		}
		out[parent] = append(out[parent], ref)
	}
	return out, rows.Err()
}

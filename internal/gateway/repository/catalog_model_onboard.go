package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/gateway/billing"
)

// CatalogModelOnboardInput describes the complete graph needed to expose one
// new downstream model and route it to one upstream offering. Nested draft
// versions and route ids are intentionally omitted: this operation owns those
// relationships and advances the active catalog exactly once.
type CatalogModelOnboardInput struct {
	ExpectedActiveReleaseID uint64                   `json:"expected_active_release_id"`
	ExpectedConfigVersion   uint64                   `json:"expected_config_version"`
	SKU                     CatalogSKUInput          `json:"sku"`
	Product                 CatalogProductInput      `json:"product"`
	Route                   CatalogModelOnboardRoute `json:"route"`
	SellRate                CatalogModelOnboardRate  `json:"sell_rate"`
	CostRate                CatalogModelOnboardRate  `json:"cost_rate"`
}

type CatalogModelOnboardRoute struct {
	Priority uint32 `json:"priority"`
	Weight   uint64 `json:"weight"`
}

// CatalogModelOnboardRate is the operator-facing price shape. Onboarding owns
// the evidence lifecycle, so an operator enters the price once instead of
// creating and approving a separate evidence row before the model exists.
type CatalogModelOnboardRate struct {
	UnitCode       string                 `json:"unit_code"`
	UnitPrice      string                 `json:"unit_price"`
	ComponentCode  string                 `json:"component_code"`
	QuantitySource billing.QuantitySource `json:"quantity_source"`
	ChargeEvent    billing.ChargeEvent    `json:"charge_event"`
	UnitScale      int32                  `json:"unit_scale"`
	QuantityStep   string                 `json:"quantity_step"`
	MaxQuantity    string                 `json:"max_quantity"`
	PricingMode    string                 `json:"pricing_mode"`
	PricingExpr    string                 `json:"pricing_expr"`
	ReasonCode     string                 `json:"reason_code"`
}

func (in *CatalogModelOnboardRate) Normalize() {
	in.UnitCode = strings.ToLower(strings.TrimSpace(in.UnitCode))
	in.UnitPrice = strings.TrimSpace(in.UnitPrice)
	in.ComponentCode = strings.ToLower(strings.TrimSpace(in.ComponentCode))
	in.QuantitySource = billing.QuantitySource(strings.ToLower(strings.TrimSpace(string(in.QuantitySource))))
	in.ChargeEvent = billing.ChargeEvent(strings.ToLower(strings.TrimSpace(string(in.ChargeEvent))))
	in.QuantityStep = strings.TrimSpace(in.QuantityStep)
	in.MaxQuantity = strings.TrimSpace(in.MaxQuantity)
	in.PricingMode = strings.ToLower(strings.TrimSpace(in.PricingMode))
	if in.PricingMode == "" {
		in.PricingMode = billing.PricingModeFlat
	}
	in.PricingExpr = strings.TrimSpace(in.PricingExpr)
	in.ReasonCode = strings.ToLower(strings.TrimSpace(in.ReasonCode))
	if in.ReasonCode == "" {
		in.ReasonCode = "model_onboard"
	}
}

func (in CatalogModelOnboardRate) Validate() error {
	pricing := ratePricingChange{
		UnitPrice: in.UnitPrice, PricingMode: in.PricingMode,
		PricingExpr: in.PricingExpr, ReasonCode: in.ReasonCode,
	}
	if err := pricing.validate(); err != nil {
		return err
	}
	// Expression parsing needs the newly-created SKU/Product domains and runs
	// inside createCatalogRateRow. Validate the meter itself here as a flat
	// component so malformed unit/source/event pairs fail before the lock.
	component := billing.RateComponent{
		Code: in.ComponentCode, Unit: in.UnitCode, Source: in.QuantitySource,
		Event: in.ChargeEvent, UnitPrice: in.UnitPrice, PricingMode: billing.PricingModeFlat,
		UnitScale: in.UnitScale, QuantityStep: in.QuantityStep, MaxQuantity: in.MaxQuantity,
	}
	if err := component.Validate(); err != nil {
		return ErrInvalidInput
	}
	return nil
}

func (in CatalogModelOnboardRate) catalogRate(evidenceID uint64) CatalogRateInput {
	return CatalogRateInput{
		EvidenceID: evidenceID, ComponentCode: in.ComponentCode,
		QuantitySource: in.QuantitySource, ChargeEvent: in.ChargeEvent,
		UnitScale: in.UnitScale, QuantityStep: in.QuantityStep, MaxQuantity: in.MaxQuantity,
		PricingMode: in.PricingMode, PricingExpr: in.PricingExpr,
	}
}

type CatalogModelOnboardResult struct {
	CatalogChangeResult
	SKUID      uint64 `json:"sku_id"`
	ProductID  uint64 `json:"product_id"`
	OfferingID uint64 `json:"offering_id"`
	CostPlanID uint64 `json:"cost_plan_id"`
	SellRateID uint64 `json:"sell_rate_id"`
	CostRateID uint64 `json:"cost_rate_id"`
}

func (in *CatalogModelOnboardInput) Normalize() error {
	in.SKU.Normalize()
	if err := in.Product.Normalize(); err != nil {
		return err
	}
	in.SellRate.Normalize()
	in.CostRate.Normalize()
	return nil
}

func (in CatalogModelOnboardInput) Validate() error {
	if in.ExpectedActiveReleaseID == 0 || in.ExpectedConfigVersion == 0 ||
		in.SKU.ExpectedVersion != 0 || in.Product.ExpectedVersion != 0 ||
		len(in.Product.Routes) != 0 || in.Route.Weight == 0 || in.Route.Weight > 1000000 {
		return ErrInvalidInput
	}
	sku := in.SKU
	sku.ExpectedVersion = in.ExpectedConfigVersion
	if err := sku.Validate(); err != nil {
		return err
	}
	product := in.Product
	product.ExpectedVersion = in.ExpectedConfigVersion
	product.Routes = []CatalogRouteInput{{SKUID: 1, Priority: in.Route.Priority, Weight: in.Route.Weight}}
	if err := product.Validate(); err != nil {
		return err
	}
	if err := in.SellRate.Validate(); err != nil {
		return err
	}
	if err := in.CostRate.Validate(); err != nil {
		return err
	}
	return nil
}

// OnboardCatalogModel creates the complete model, commercial, and upstream
// graph in the caller's transaction. No intermediate version is visible: an
// error leaves rollback to Store.WithTx and the active version advances only
// after every content row exists.
func (s *Store) OnboardCatalogModel(ctx context.Context, tx *sql.Tx, in CatalogModelOnboardInput, sign RateEvidenceSigner, actorID uint64) (CatalogModelOnboardResult, error) {
	if tx == nil || sign == nil || actorID == 0 {
		return CatalogModelOnboardResult{}, ErrInvalidInput
	}
	if err := in.Normalize(); err != nil {
		return CatalogModelOnboardResult{}, err
	}
	if err := in.Validate(); err != nil {
		return CatalogModelOnboardResult{}, err
	}
	active, err := lockActiveCatalogRelease(ctx, tx, in.ExpectedActiveReleaseID, in.ExpectedConfigVersion)
	if err != nil {
		return CatalogModelOnboardResult{}, fmt.Errorf("lock active catalog release: %w", err)
	}

	sku := in.SKU
	sku.ExpectedVersion = in.ExpectedConfigVersion
	skuID, err := createCatalogSKURows(ctx, tx, active.ID, active.SemanticDigest, sku)
	if err != nil {
		return CatalogModelOnboardResult{}, fmt.Errorf("create catalog SKU: %w", err)
	}

	product := in.Product
	product.ExpectedVersion = in.ExpectedConfigVersion
	product.Routes = []CatalogRouteInput{{SKUID: skuID, Priority: in.Route.Priority, Weight: in.Route.Weight}}
	productID, offeringID, err := createCatalogProductRows(ctx, tx, active.ID, product)
	if err != nil {
		return CatalogModelOnboardResult{}, fmt.Errorf("create catalog product: %w", err)
	}
	if err := validateSKUVariant(ctx, tx, active.ID, skuID, sku.VariantCode); err != nil {
		return CatalogModelOnboardResult{}, fmt.Errorf("validate SKU variant: %w", err)
	}
	downstreamPaths, err := modelOnboardDownstreamPaths(product.AdapterCode, product.AdapterVersion, sku.RouteTemplate)
	if err != nil {
		return CatalogModelOnboardResult{}, fmt.Errorf("load downstream paths: %w", err)
	}
	if err := validateRequestedDownstreamPaths(ctx, tx, active.ID, skuID, downstreamPaths); err != nil {
		return CatalogModelOnboardResult{}, fmt.Errorf("validate downstream path: %w", err)
	}
	for _, path := range downstreamPaths {
		if _, err := tx.ExecContext(ctx, `INSERT IGNORE INTO gw_sku_downstream_paths(release_id,sku_id,path,created_at) VALUES (?,?,?,?)`, active.ID, skuID, path, nowUTC()); err != nil {
			return CatalogModelOnboardResult{}, fmt.Errorf("declare downstream path: %w", err)
		}
	}

	var costPlanID uint64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM gw_cost_plans WHERE release_id=? AND offering_id=? AND plan_code=?`, active.ID, offeringID, product.CostPlanCode).Scan(&costPlanID); err == sql.ErrNoRows {
		return CatalogModelOnboardResult{}, fmt.Errorf("load cost plan: %w", ErrNotFound)
	} else if err != nil {
		return CatalogModelOnboardResult{}, fmt.Errorf("load cost plan: %w", err)
	}
	currencyCode, currencyVersion, err := activeSettlementCurrency(ctx, tx)
	if err != nil {
		return CatalogModelOnboardResult{}, fmt.Errorf("load settlement currency: %w", err)
	}
	evidenceStamp := nowUTC().Format(time.RFC3339Nano)
	sellEvidenceID, err := s.declareOperatorPrice(ctx, tx, operatorPriceDeclaration{
		Reference: fmt.Sprintf("prism-console:model-onboard:sell:%s:%s", sku.SKUCode, evidenceStamp),
		UnitCode:  in.SellRate.UnitCode, UnitPrice: in.SellRate.UnitPrice,
		CurrencyCode: currencyCode, CurrencyVersion: currencyVersion, ReasonCode: in.SellRate.ReasonCode,
	}, sign, actorID)
	if err != nil {
		return CatalogModelOnboardResult{}, fmt.Errorf("declare sell price: %w", err)
	}
	sellRate := in.SellRate.catalogRate(sellEvidenceID)
	sellRateID, err := createCatalogRateRow(ctx, tx, "sell", active.ID, skuID, sellRate)
	if err != nil {
		return CatalogModelOnboardResult{}, fmt.Errorf("create sell rate: %w", err)
	}
	costEvidenceID, err := s.declareOperatorPrice(ctx, tx, operatorPriceDeclaration{
		Reference: fmt.Sprintf("prism-console:model-onboard:cost:%s:%s", product.ProductCode, evidenceStamp),
		UnitCode:  in.CostRate.UnitCode, UnitPrice: in.CostRate.UnitPrice,
		CurrencyCode: currencyCode, CurrencyVersion: currencyVersion, ReasonCode: in.CostRate.ReasonCode,
	}, sign, actorID)
	if err != nil {
		return CatalogModelOnboardResult{}, fmt.Errorf("declare cost price: %w", err)
	}
	costRate := in.CostRate.catalogRate(costEvidenceID)
	costRateID, err := createCatalogRateRow(ctx, tx, "cost", active.ID, costPlanID, costRate)
	if err != nil {
		return CatalogModelOnboardResult{}, fmt.Errorf("create cost rate: %w", err)
	}
	if err := CheckCatalogStructure(ctx, tx, active.ID); err != nil {
		return CatalogModelOnboardResult{}, fmt.Errorf("check catalog structure: %w", err)
	}
	if err := CheckCatalogPricing(ctx, tx, active.ID); err != nil {
		return CatalogModelOnboardResult{}, fmt.Errorf("check catalog pricing: %w", err)
	}

	version, err := advanceActiveCatalog(ctx, tx, active, "catalog_change.model_onboard", actorID, ginSafeMetadata{
		"sku_id": skuID, "sku_code": sku.SKUCode,
		"product_id": productID, "product_code": product.ProductCode,
		"offering_id": offeringID, "cost_plan_id": costPlanID,
		"sell_rate_id": sellRateID, "cost_rate_id": costRateID,
	})
	if err != nil {
		return CatalogModelOnboardResult{}, fmt.Errorf("advance active catalog: %w", err)
	}
	return CatalogModelOnboardResult{
		CatalogChangeResult: CatalogChangeResult{ReleaseID: active.ID, ConfigVersion: version, Activated: true, SourceReleaseID: active.ID},
		SKUID:               skuID, ProductID: productID, OfferingID: offeringID, CostPlanID: costPlanID,
		SellRateID: sellRateID, CostRateID: costRateID,
	}, nil
}

func modelOnboardDownstreamPaths(adapterCode string, adapterVersion uint32, operationPath string) ([]string, error) {
	if downstreamPathSource == nil {
		return []string{operationPath}, nil
	}
	paths, err := downstreamPathSource.DownstreamPaths(adapterCode, adapterVersion)
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		if path == operationPath {
			return paths, nil
		}
	}
	return nil, ErrInvalidInput
}

func activeSettlementCurrency(ctx context.Context, tx *sql.Tx) (string, uint32, error) {
	var code string
	var version uint32
	err := tx.QueryRowContext(ctx, `SELECT s.currency_code,s.currency_version
FROM billing_system_state s
JOIN billing_currency_definitions d ON d.currency_code=s.currency_code AND d.definition_version=s.currency_version AND d.status='active'
WHERE s.id=1 FOR SHARE`).Scan(&code, &version)
	if err == sql.ErrNoRows {
		return "", 0, ErrConflict
	}
	if err != nil {
		return "", 0, err
	}
	return code, version, nil
}

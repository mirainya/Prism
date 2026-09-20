package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/gateway/billing"
)

// Catalog changes are applied to the active release in one transaction. The
// runtime pointer and release id stay stable; config_version/content_hash and
// an audit row advance together with the edited content.
type CatalogChangeResult struct {
	// ReleaseID is the active release that was edited in place.
	ReleaseID uint64 `json:"release_id"`
	// ConfigVersion is the active release's version after the edit.
	ConfigVersion uint64 `json:"config_version"`
	Activated     bool   `json:"activated"`
	// SourceReleaseID is retained for API compatibility and equals ReleaseID for
	// an in-place active-release edit.
	SourceReleaseID uint64 `json:"source_release_id"`
}

// catalogChangeGuard is the part every catalog change shares: which release the
// caller believes is live and, for compatibility with older clients, the
// semantic metadata that used to label a fork.
type catalogChangeGuard struct {
	// ExpectedActiveReleaseID prevents a stale operator from editing a release
	// that is no longer active. It is required, not optional.
	ExpectedActiveReleaseID uint64 `json:"expected_active_release_id"`
	// ExpectedConfigVersion is optional for callers that want an additional
	// optimistic check. Zero keeps compatibility with older clients and means
	// "use the version currently held by the locked active release".
	ExpectedConfigVersion uint64 `json:"expected_config_version,omitempty"`
	// Semantic metadata remains accepted for wire compatibility with v5.4 clients.
	SemanticVersion string `json:"semantic_version"`
	SemanticDigest  string `json:"-"`
}

func (g catalogChangeGuard) draft() CatalogDraftInput {
	return CatalogDraftInput{SemanticVersion: g.SemanticVersion, SemanticDigest: g.SemanticDigest}
}

func (g catalogChangeGuard) validate() error {
	if g.ExpectedActiveReleaseID == 0 {
		return fmt.Errorf("%w: expected_active_release_id is required for a release-scoped change", ErrInvalidInput)
	}
	// Direct edits use the active row's config_version as their optimistic
	// concurrency check.  Semantic metadata belonged to the removed
	// fork/publish workflow and is accepted only for older clients.
	if g.ExpectedConfigVersion == 0 && g.SemanticVersion == "" {
		return fmt.Errorf("%w: expected_config_version is required", ErrInvalidInput)
	}
	// Semantic metadata belonged to the removed fork/publish workflow. Keep it
	// optional for older clients that still send it, but do not require operators
	// to invent a version for an in-place edit.
	if g.SemanticVersion == "" && g.SemanticDigest == "" {
		return nil
	}
	if g.SemanticVersion != "" && !semanticVersionPattern.MatchString(g.SemanticVersion) {
		return ErrInvalidInput
	}
	if g.SemanticDigest != "" && !validHexDigest(g.SemanticDigest, 32) {
		return ErrInvalidInput
	}
	return nil
}

// activeCatalogLock is the complete lock state for an in-place edit. The
// runtime singleton is locked first, then the active release row is locked for
// the remainder of the transaction. This serializes all console edits and
// prevents a stale operator from writing over a newer active configuration.
type activeCatalogLock struct {
	ID             uint64
	Version        uint64
	ContentHash    string
	SemanticDigest string
}

func lockActiveCatalogRelease(ctx context.Context, tx *sql.Tx, expectedReleaseID, expectedConfigVersion uint64) (activeCatalogLock, error) {
	if tx == nil || expectedReleaseID == 0 {
		return activeCatalogLock{}, ErrInvalidInput
	}
	var active sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT active_release_id FROM gw_catalog_runtime_state WHERE id=1 FOR UPDATE`).Scan(&active); err == sql.ErrNoRows {
		return activeCatalogLock{}, ErrNotFound
	} else if err != nil {
		return activeCatalogLock{}, err
	}
	if !active.Valid || active.Int64 <= 0 {
		return activeCatalogLock{}, fmt.Errorf("%w: no catalog release is active", ErrConflict)
	}
	if uint64(active.Int64) != expectedReleaseID {
		return activeCatalogLock{}, fmt.Errorf("%w: release %d is active, not %d", ErrConflict, active.Int64, expectedReleaseID)
	}
	var status string
	var lock activeCatalogLock
	if err := tx.QueryRowContext(ctx, `SELECT status,config_version,content_hash,semantic_digest FROM gw_catalog_releases WHERE id=? FOR UPDATE`, expectedReleaseID).
		Scan(&status, &lock.Version, &lock.ContentHash, &lock.SemanticDigest); err == sql.ErrNoRows {
		return activeCatalogLock{}, ErrNotFound
	} else if err != nil {
		return activeCatalogLock{}, err
	}
	if status != "published" {
		return activeCatalogLock{}, fmt.Errorf("%w: active release %d is %s", ErrConflict, expectedReleaseID, status)
	}
	if expectedConfigVersion != 0 && lock.Version != expectedConfigVersion {
		return activeCatalogLock{}, fmt.Errorf("%w: config version is %d, not %d", ErrConflict, lock.Version, expectedConfigVersion)
	}
	lock.ID = expectedReleaseID
	return lock, nil
}

// advanceActiveCatalog recalculates the content digest after the content rows
// have been changed, increments config_version, and records the operator
// action. The row remains published and the runtime pointer remains unchanged.
func advanceActiveCatalog(ctx context.Context, tx *sql.Tx, lock activeCatalogLock, action string, actorID uint64, metadata ginSafeMetadata) (uint64, error) {
	if tx == nil || lock.ID == 0 || lock.Version == 0 || actorID == 0 {
		return 0, ErrInvalidInput
	}
	digest, err := catalogContentDigest(ctx, tx, lock.ID)
	if err != nil {
		return 0, err
	}
	// Keep the content hash unique across releases. Reusing a historical hash
	// would violate the catalog's uniqueness invariant and is reported as a
	// conflict rather than allowing a driver-specific duplicate-key error.
	var duplicate uint64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_catalog_releases WHERE content_hash=? AND id<>?`, digest, lock.ID).Scan(&duplicate); err != nil {
		return 0, err
	}
	if duplicate != 0 {
		return 0, fmt.Errorf("%w: content hash already belongs to another release", ErrConflict)
	}
	// A catalog edit changes data, not the serving binary. Existing readiness
	// records therefore remain valid; refresh their content hash atomically so
	// the next request does not enter the retired prove/activate workflow.
	if _, err := tx.ExecContext(ctx, `UPDATE gw_catalog_readiness SET content_hash=?,heartbeat_at=? WHERE release_id=?`, digest, nowUTC(), lock.ID); err != nil {
		return 0, err
	}
	next := lock.Version + 1
	result, err := tx.ExecContext(ctx, `UPDATE gw_catalog_releases SET config_version=?,content_hash=?,updated_at=? WHERE id=? AND status='published' AND config_version=?`, next, digest, nowUTC(), lock.ID, lock.Version)
	if err != nil {
		return 0, err
	}
	ok, err := affected(result)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, ErrConflict
	}
	if metadata == nil {
		metadata = ginSafeMetadata{}
	}
	metadata["release_id"] = lock.ID
	metadata["config_version"] = next
	if err := recordCatalogAdminChange(ctx, tx, actorID, action, "catalog_release", lock.ID, metadata); err != nil {
		return 0, err
	}
	return next, nil
}

// ratePricingChange is the shared shape of every B-class rate change: a new
// price, an optional pricing mode override, and a reason for the audit trail.
// Sell and cost carry different addressing on top of this, but the pricing
// fields and their validation are identical.
type ratePricingChange struct {
	// UnitPrice is the new flat price. For an expression rate it is still
	// recorded as the evidence fact — that is what the operator declared — but
	// the charge comes from PricingExpr.
	UnitPrice string `json:"unit_price"`
	// PricingMode empty keeps whatever the existing rate used, so an operator
	// editing a flat price never has to know the field exists.
	PricingMode string `json:"pricing_mode"`
	PricingExpr string `json:"pricing_expr"`
	// ReasonCode explains the change in the audit trail. Empty defaults to
	// console_price_change, which is what §5.2.2 names for this path.
	ReasonCode string `json:"reason_code"`
}

func (in *ratePricingChange) normalize() {
	in.UnitPrice = strings.TrimSpace(in.UnitPrice)
	in.PricingMode = strings.ToLower(strings.TrimSpace(in.PricingMode))
	in.PricingExpr = strings.TrimSpace(in.PricingExpr)
	in.ReasonCode = strings.ToLower(strings.TrimSpace(in.ReasonCode))
	if in.ReasonCode == "" {
		in.ReasonCode = "console_price_change"
	}
}

func (in ratePricingChange) validate() error {
	if !catalogIdentityPattern.MatchString(in.ReasonCode) || len(in.ReasonCode) > 128 {
		return ErrInvalidInput
	}
	// 18 decimal places is the money column's scale. Non-negative is enforced
	// even on cost rates because a negative cost is an accounting error, not a
	// rebate, and the CHECK constraint on unit_price >= 0 would reject it later
	// anyway.
	if _, err := billing.ParseAmount(in.UnitPrice, 18, true); err != nil {
		return fmt.Errorf("%w: unit_price %q is not a usable price", ErrInvalidInput, in.UnitPrice)
	}
	switch in.PricingMode {
	case "", billing.PricingModeFlat:
		if in.PricingExpr != "" {
			return fmt.Errorf("%w: a flat rate carries no expression", ErrInvalidInput)
		}
	case billing.PricingModeExpression:
		if in.PricingExpr == "" {
			return fmt.Errorf("%w: an expression rate needs an expression", ErrInvalidInput)
		}
	default:
		return ErrInvalidInput
	}
	return nil
}

// SellRateChange replaces one SKU's sell rate. The SKU is addressed by its
// release-independent sku_code and component_code rather than by the row id,
// because the fork gives every row a new id: an id read from the console before
// the fork names nothing in the release the edit actually lands in.
type SellRateChange struct {
	catalogChangeGuard
	ratePricingChange
	SKUCode       string `json:"sku_code"`
	ComponentCode string `json:"component_code"`
}

func (in *SellRateChange) Normalize() {
	in.SKUCode = strings.ToLower(strings.TrimSpace(in.SKUCode))
	in.ComponentCode = strings.ToLower(strings.TrimSpace(in.ComponentCode))
	in.SemanticVersion = strings.TrimSpace(in.SemanticVersion)
	in.ratePricingChange.normalize()
}

func (in SellRateChange) Validate() error {
	if err := in.catalogChangeGuard.validate(); err != nil {
		return err
	}
	if !catalogIdentityPattern.MatchString(in.SKUCode) || !catalogIdentityPattern.MatchString(in.ComponentCode) {
		return ErrInvalidInput
	}
	return in.ratePricingChange.validate()
}

// CostRateChange replaces one cost rate on the active release. Cost rates live
// on cost plans which live on offerings, so the natural business identity is
// three codes: which product the offering delivers, which plan on that offering,
// and which meter component the rate charges. A product with multiple transports
// or credential pools can carry the same plan_code twice, so pool_code exists as
// an optional disambiguator; omitting it when only one row matches is normal.
//
// §5.2.2: the operator can declare a cost rate, because Prism cannot always know
// the real upstream price, but the evidence has to say so honestly. The chain
// records source_type=operator_manual + authority_level=operator_declared here,
// exactly as sell-rate does — a cost rate that pretended to be provider_confirmed
// would mask a guess as a verified fact.
type CostRateChange struct {
	catalogChangeGuard
	ratePricingChange
	ProductCode   string `json:"product_code"`
	PlanCode      string `json:"plan_code"`
	ComponentCode string `json:"component_code"`
	// PoolCode is optional and only used when the (product_code, plan_code)
	// pair matches more than one row. In that case it names the credential pool
	// the target cost plan belongs to.
	PoolCode string `json:"pool_code"`
}

func (in *CostRateChange) Normalize() {
	in.ProductCode = strings.ToLower(strings.TrimSpace(in.ProductCode))
	in.PlanCode = strings.ToLower(strings.TrimSpace(in.PlanCode))
	in.ComponentCode = strings.ToLower(strings.TrimSpace(in.ComponentCode))
	in.PoolCode = strings.ToLower(strings.TrimSpace(in.PoolCode))
	in.SemanticVersion = strings.TrimSpace(in.SemanticVersion)
	in.ratePricingChange.normalize()
}

func (in CostRateChange) Validate() error {
	if err := in.catalogChangeGuard.validate(); err != nil {
		return err
	}
	if !catalogIdentityPattern.MatchString(in.ProductCode) ||
		!catalogIdentityPattern.MatchString(in.PlanCode) ||
		!catalogIdentityPattern.MatchString(in.ComponentCode) {
		return ErrInvalidInput
	}
	// PoolCode is optional, so it validates only when present rather than being
	// forced through the identity pattern.
	if in.PoolCode != "" && !catalogIdentityPattern.MatchString(in.PoolCode) {
		return ErrInvalidInput
	}
	return in.ratePricingChange.validate()
}

// RateEvidenceSigner produces the fact HMAC for a piece of evidence. It is a
// callback so the MAC key stays with the caller that already owns it and never
// reaches this package.
type RateEvidenceSigner func(RateEvidenceInput) (string, error)

// ChangeSellRate updates the addressed sell rate on the active release in the
// same transaction that records the operator audit. No draft or deployment
// generation is created; the runtime already points at this release.
func (s *Store) ChangeSellRate(ctx context.Context, tx *sql.Tx, in SellRateChange, sign RateEvidenceSigner, actorID uint64) (CatalogChangeResult, error) {
	if tx == nil || actorID == 0 || sign == nil {
		return CatalogChangeResult{}, ErrInvalidInput
	}
	in.Normalize()
	if err := in.Validate(); err != nil {
		return CatalogChangeResult{}, err
	}
	active, err := lockActiveCatalogRelease(ctx, tx, in.ExpectedActiveReleaseID, in.ExpectedConfigVersion)
	if err != nil {
		return CatalogChangeResult{}, err
	}
	old, err := loadSellRateByCode(ctx, tx, active.ID, in.SKUCode, in.ComponentCode)
	if err != nil {
		return CatalogChangeResult{}, err
	}
	mode := in.PricingMode
	expr := in.PricingExpr
	if mode == "" {
		mode = old.PricingMode
		if expr == "" {
			expr = old.PricingExpr
		}
	}
	reference := fmt.Sprintf("prism-console:sell-rate:%s:%s:%s", in.SKUCode, in.ComponentCode, nowUTC().Format(time.RFC3339Nano))
	evidenceID, err := s.declareOperatorPrice(ctx, tx, operatorPriceDeclaration{
		Reference: reference, UnitCode: old.UnitCode, UnitPrice: in.UnitPrice,
		CurrencyCode: old.CurrencyCode, CurrencyVersion: old.CurrencyVersion, ReasonCode: in.ReasonCode,
	}, sign, actorID)
	if err != nil {
		return CatalogChangeResult{}, err
	}
	accepted, eventID, err := acceptedRateEvidence(ctx, tx, evidenceID)
	if err != nil {
		return CatalogChangeResult{}, err
	}
	var expression any
	var maxPrice any
	if mode == billing.PricingModeExpression {
		_, bound, err := proveNewExpressionRate(ctx, tx, "sell", active.ID, old.SKUID, expr)
		if err != nil {
			return CatalogChangeResult{}, err
		}
		expression, maxPrice = expr, bound.Fixed(18)
	}
	if mode == billing.PricingModeFlat {
		expression, maxPrice = nil, nil
	}
	result, err := tx.ExecContext(ctx, `UPDATE gw_sell_rates SET rate_evidence_review_event_id=?,unit_code=?,unit_price=?,currency_code=?,currency_version=?,pricing_mode=?,pricing_expr=?,max_price=? WHERE release_id=? AND id=?`,
		eventID, accepted.UnitCode, accepted.UnitPrice, accepted.CurrencyCode, accepted.CurrencyVersion, mode, expression, maxPrice, active.ID, old.ID)
	if err != nil {
		return CatalogChangeResult{}, err
	}
	ok, err := affected(result)
	if err != nil {
		return CatalogChangeResult{}, err
	}
	if !ok {
		return CatalogChangeResult{}, ErrNotFound
	}
	version, err := advanceActiveCatalog(ctx, tx, active, "catalog_change.sell_rate", actorID, ginSafeMetadata{
		"source_release_id": active.ID, "sku_code": in.SKUCode, "component_code": in.ComponentCode,
		"reason_code": in.ReasonCode,
		"evidence_id": evidenceID,
		"before":      ginSafeMetadata{"unit_price": old.UnitPrice, "pricing_mode": old.PricingMode, "pricing_expr": old.PricingExpr},
		"after":       ginSafeMetadata{"unit_price": accepted.UnitPrice, "pricing_mode": mode, "pricing_expr": expr},
	})
	if err != nil {
		return CatalogChangeResult{}, err
	}
	return CatalogChangeResult{ReleaseID: active.ID, ConfigVersion: version, Activated: true, SourceReleaseID: active.ID}, nil
}

// ChangeCostRate is the cost-side twin of ChangeSellRate and updates the active
// cost rate in place while preserving the operator-declared evidence trail.
func (s *Store) ChangeCostRate(ctx context.Context, tx *sql.Tx, in CostRateChange, sign RateEvidenceSigner, actorID uint64) (CatalogChangeResult, error) {
	if tx == nil || actorID == 0 || sign == nil {
		return CatalogChangeResult{}, ErrInvalidInput
	}
	in.Normalize()
	if err := in.Validate(); err != nil {
		return CatalogChangeResult{}, err
	}
	active, err := lockActiveCatalogRelease(ctx, tx, in.ExpectedActiveReleaseID, in.ExpectedConfigVersion)
	if err != nil {
		return CatalogChangeResult{}, err
	}
	old, err := loadCostRateByCode(ctx, tx, active.ID, in.ProductCode, in.PlanCode, in.ComponentCode, in.PoolCode)
	if err != nil {
		return CatalogChangeResult{}, err
	}
	mode := in.PricingMode
	expr := in.PricingExpr
	if mode == "" {
		mode = old.PricingMode
		if expr == "" {
			expr = old.PricingExpr
		}
	}
	// The reference names the cost plan, not just the product: a product with two
	// pools carries two plans of the same code, and the reference has to point at
	// exactly one row to be honest evidence.
	reference := fmt.Sprintf("prism-console:cost-rate:%s:%s:%s:%s:%s", in.ProductCode, in.PlanCode, old.PoolCode, in.ComponentCode, nowUTC().Format(time.RFC3339Nano))
	evidenceID, err := s.declareOperatorPrice(ctx, tx, operatorPriceDeclaration{
		Reference: reference, UnitCode: old.UnitCode, UnitPrice: in.UnitPrice,
		CurrencyCode: old.CurrencyCode, CurrencyVersion: old.CurrencyVersion, ReasonCode: in.ReasonCode,
	}, sign, actorID)
	if err != nil {
		return CatalogChangeResult{}, err
	}
	accepted, eventID, err := acceptedRateEvidence(ctx, tx, evidenceID)
	if err != nil {
		return CatalogChangeResult{}, err
	}
	var expression any
	var maxPrice any
	if mode == billing.PricingModeExpression {
		_, bound, err := proveNewExpressionRate(ctx, tx, "cost", active.ID, old.CostPlanID, expr)
		if err != nil {
			return CatalogChangeResult{}, err
		}
		expression, maxPrice = expr, bound.Fixed(18)
	}
	if mode == billing.PricingModeFlat {
		expression, maxPrice = nil, nil
	}
	result, err := tx.ExecContext(ctx, `UPDATE gw_cost_rates SET rate_evidence_review_event_id=?,unit_code=?,unit_price=?,currency_code=?,currency_version=?,pricing_mode=?,pricing_expr=?,max_price=? WHERE release_id=? AND id=?`,
		eventID, accepted.UnitCode, accepted.UnitPrice, accepted.CurrencyCode, accepted.CurrencyVersion, mode, expression, maxPrice, active.ID, old.ID)
	if err != nil {
		return CatalogChangeResult{}, err
	}
	ok, err := affected(result)
	if err != nil {
		return CatalogChangeResult{}, err
	}
	if !ok {
		return CatalogChangeResult{}, ErrNotFound
	}
	version, err := advanceActiveCatalog(ctx, tx, active, "catalog_change.cost_rate", actorID, ginSafeMetadata{
		"source_release_id": active.ID, "product_code": in.ProductCode, "plan_code": in.PlanCode,
		"component_code": in.ComponentCode, "pool_code": old.PoolCode,
		"reason_code": in.ReasonCode,
		"evidence_id": evidenceID,
		"before":      ginSafeMetadata{"unit_price": old.UnitPrice, "pricing_mode": old.PricingMode, "pricing_expr": old.PricingExpr},
		"after":       ginSafeMetadata{"unit_price": accepted.UnitPrice, "pricing_mode": mode, "pricing_expr": expr},
	})
	if err != nil {
		return CatalogChangeResult{}, err
	}
	return CatalogChangeResult{ReleaseID: active.ID, ConfigVersion: version, Activated: true, SourceReleaseID: active.ID}, nil
}

// sellRateRow is the rate being replaced. Everything except the price is carried
// forward: the operator is changing what a unit costs, not what is metered.
type sellRateRow struct {
	ID              uint64
	SKUID           uint64
	ComponentCode   string
	QuantitySource  string
	ChargeEvent     string
	UnitCode        string
	UnitPrice       string
	CurrencyCode    string
	CurrencyVersion uint32
	UnitScale       int32
	QuantityStep    string
	MaxQuantity     string
	PricingMode     string
	PricingExpr     string
}

func loadSellRateByCode(ctx context.Context, tx *sql.Tx, releaseID uint64, skuCode, componentCode string) (sellRateRow, error) {
	var out sellRateRow
	err := tx.QueryRowContext(ctx, `SELECT r.id,r.sku_id,r.component_code,r.quantity_source,r.charge_event,r.unit_code,r.unit_price,r.currency_code,r.currency_version,r.unit_scale,r.quantity_step,r.max_quantity,COALESCE(r.pricing_mode,'flat'),COALESCE(r.pricing_expr,'')
FROM gw_sell_rates r JOIN gw_skus s ON s.release_id=r.release_id AND s.id=r.sku_id
WHERE r.release_id=? AND s.sku_code=? AND r.component_code=?`, releaseID, skuCode, componentCode).
		Scan(&out.ID, &out.SKUID, &out.ComponentCode, &out.QuantitySource, &out.ChargeEvent, &out.UnitCode,
			&out.UnitPrice, &out.CurrencyCode, &out.CurrencyVersion, &out.UnitScale, &out.QuantityStep,
			&out.MaxQuantity, &out.PricingMode, &out.PricingExpr)
	if err == sql.ErrNoRows {
		return sellRateRow{}, fmt.Errorf("%w: no sell rate for sku %q component %q", ErrNotFound, skuCode, componentCode)
	}
	if err != nil {
		return sellRateRow{}, err
	}
	return out, nil
}

// operatorPriceDeclaration is the fact side of an operator's price change,
// stripped of everything about which row is being edited. Both sell and cost
// rates feed this to declareOperatorPrice so the labelling is identical: the
// reason a cost rate is not allowed to say provider_confirmed (§5.2.2) is the
// same reason a sell rate isn't — the operator is the source, and the evidence
// has to say so.
type operatorPriceDeclaration struct {
	// Reference is what uq_gw_rate_evidence_fact keys on together with
	// (source_type, fact_hmac). Two edits of the same rate to the same price
	// would collide without a per-edit component, so callers include a
	// nanosecond timestamp in the reference they build.
	Reference       string
	UnitCode        string
	UnitPrice       string
	CurrencyCode    string
	CurrencyVersion uint32
	ReasonCode      string
}

// declareOperatorPrice creates and accepts the evidence for an operator's own
// price, in one transaction, honestly labelled (SPEC §5.2.2). The operator is
// the highest authority on Prism's own sell price and the only authority Prism
// has for a cost declared through the console, so self-review is legitimate on
// both — but it is recorded as operator_declared, never as an external
// observation, because nothing outside this console attested to it.
func (s *Store) declareOperatorPrice(ctx context.Context, tx *sql.Tx, in operatorPriceDeclaration, sign RateEvidenceSigner, actorID uint64) (uint64, error) {
	evidence := RateEvidenceInput{
		SourceType:      "operator_manual",
		AuthorityLevel:  "operator_declared",
		SourceReference: in.Reference,
		ObservedAt:      nowUTC(),
		UnitCode:        in.UnitCode,
		UnitPrice:       in.UnitPrice,
		CurrencyCode:    in.CurrencyCode,
		CurrencyVersion: in.CurrencyVersion,
	}
	evidence.Normalize()
	digest, err := sign(evidence)
	if err != nil {
		return 0, err
	}
	evidence.FactHMAC = digest
	evidenceID, err := s.CreateRateEvidence(ctx, tx, evidence, actorID)
	if err != nil {
		return 0, err
	}
	// Version 1 is what CreateRateEvidence just wrote; accepting in the same
	// transaction is the whole point of the operator path.
	if err := s.ReviewRateEvidence(ctx, tx, evidenceID, RateEvidenceReviewInput{
		Decision: "accepted", ReasonCode: in.ReasonCode, ExpectedVersion: 1,
	}, actorID); err != nil {
		return 0, err
	}
	return evidenceID, nil
}

// costRateRow is the rate being replaced. Its shape matches sellRateRow row for
// row on the metering columns, because the shared CreateCatalogRate carries them
// forward the same way for both kinds; the identity fields differ.
type costRateRow struct {
	ID              uint64
	CostPlanID      uint64
	PoolCode        string
	ComponentCode   string
	QuantitySource  string
	ChargeEvent     string
	UnitCode        string
	UnitPrice       string
	CurrencyCode    string
	CurrencyVersion uint32
	UnitScale       int32
	QuantityStep    string
	MaxQuantity     string
	PricingMode     string
	PricingExpr     string
}

// loadCostRateByCode resolves the three-or-four codes back to exactly one cost
// rate. Two rows matching means a product has more than one offering under the
// same plan_code — legitimate, but the operator has to name the credential pool
// so the change lands on the row they meant.
func loadCostRateByCode(ctx context.Context, tx *sql.Tx, releaseID uint64, productCode, planCode, componentCode, poolCode string) (costRateRow, error) {
	// LIMIT 2 rather than LIMIT 1: seeing the second row is what tells the loader
	// the address is ambiguous. Without the extra row a silent pick would edit the
	// wrong plan and the operator would find out only in the audit log.
	query := `SELECT r.id,r.cost_plan_id,pool.pool_code,r.component_code,r.quantity_source,r.charge_event,r.unit_code,r.unit_price,r.currency_code,r.currency_version,r.unit_scale,r.quantity_step,r.max_quantity,COALESCE(r.pricing_mode,'flat'),COALESCE(r.pricing_expr,'')
FROM gw_cost_rates r
JOIN gw_cost_plans plan ON plan.release_id=r.release_id AND plan.id=r.cost_plan_id
JOIN gw_offerings o ON o.release_id=plan.release_id AND o.id=plan.offering_id
JOIN gw_product_transports pt ON pt.release_id=o.release_id AND pt.id=o.product_transport_id
JOIN gw_products prod ON prod.release_id=pt.release_id AND prod.id=pt.product_id
JOIN gw_credential_pools pool ON pool.id=o.credential_pool_id
WHERE r.release_id=? AND prod.product_code=? AND plan.plan_code=? AND r.component_code=?`
	args := []any{releaseID, productCode, planCode, componentCode}
	if poolCode != "" {
		query += ` AND pool.pool_code=?`
		args = append(args, poolCode)
	}
	query += ` LIMIT 2`
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return costRateRow{}, err
	}
	defer rows.Close()
	var matches []costRateRow
	for rows.Next() {
		var out costRateRow
		if err := rows.Scan(&out.ID, &out.CostPlanID, &out.PoolCode, &out.ComponentCode, &out.QuantitySource,
			&out.ChargeEvent, &out.UnitCode, &out.UnitPrice, &out.CurrencyCode, &out.CurrencyVersion,
			&out.UnitScale, &out.QuantityStep, &out.MaxQuantity, &out.PricingMode, &out.PricingExpr); err != nil {
			return costRateRow{}, err
		}
		matches = append(matches, out)
	}
	if err := rows.Err(); err != nil {
		return costRateRow{}, err
	}
	switch len(matches) {
	case 0:
		return costRateRow{}, fmt.Errorf("%w: no cost rate for product %q plan %q component %q", ErrNotFound, productCode, planCode, componentCode)
	case 1:
		return matches[0], nil
	default:
		// Refused rather than picked-arbitrarily: silent selection would land the
		// price on whichever pool the join happened to yield first, which is not
		// something the operator could observe from the request they sent.
		return costRateRow{}, fmt.Errorf("%w: cost rate for product %q plan %q component %q matches more than one pool — pass pool_code to disambiguate", ErrConflict, productCode, planCode, componentCode)
	}
}

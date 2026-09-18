//go:build integration

package repository_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/mirainya/Prism/internal/gateway/adapter"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

// A B-class change cannot be exercised on the sqlite harness at all: every step
// it composes — lockActiveRelease, lockCatalogDraft, acceptedRateEvidence,
// ReviewRateEvidence, PublishRelease — locks with a literal FOR UPDATE or FOR
// SHARE, which sqlite rejects as a syntax error rather than ignoring. So the
// whole chain is pinned here, against the same MySQL 8 the production catalog
// runs on, where the composite foreign keys and the expression pairing CHECK are
// also enforced.

// changeRateSigner stands in for the console's HMAC signer. The real one needs
// PRISM_GATEWAY_HMAC_B64; what these tests are about is the chain, not the MAC,
// and a fixed digest is still unique per row because the source reference
// carries a nanosecond timestamp. The prefix is passed in so both the sell-rate
// and the cost-rate tests can assert their own reference shape without letting
// through a swap.
func changeRateSigner(t *testing.T, referencePrefix string) repository.RateEvidenceSigner {
	t.Helper()
	return func(in repository.RateEvidenceInput) (string, error) {
		// Asserted inside the signer because this is the only place the evidence is
		// visible before it is written, and §5.2.2 is about how it is labelled. The
		// cost side needs the same labels as the sell side: operator-declared, not
		// provider-confirmed, because a cost rate declared through the console is
		// Prism's estimate rather than the upstream's word.
		if in.SourceType != "operator_manual" || in.AuthorityLevel != "operator_declared" {
			t.Errorf("evidence labelled source_type=%q authority_level=%q", in.SourceType, in.AuthorityLevel)
		}
		if !strings.HasPrefix(in.SourceReference, referencePrefix) {
			t.Errorf("evidence reference = %q, want prefix %q", in.SourceReference, referencePrefix)
		}
		return strings.Repeat("c", 64), nil
	}
}

func changeSellRateSigner(t *testing.T) repository.RateEvidenceSigner {
	return changeRateSigner(t, "prism-console:sell-rate:")
}

func changeCostRateSigner(t *testing.T) repository.RateEvidenceSigner {
	return changeRateSigner(t, "prism-console:cost-rate:")
}

// mysqlActiveRelease publishes and activates the fixture release, which is the
// state every B-class change starts from: the operator edits what is live.
func mysqlActiveRelease(t *testing.T) (*repository.Store, uint64) {
	t.Helper()
	store, releaseID := mysqlPublishableRelease(t)
	ctx := context.Background()
	if err := store.WithTx(ctx, func(tx *sql.Tx) error { return store.PublishRelease(ctx, tx, releaseID, 1) }); err != nil {
		t.Fatal(err)
	}
	if err := store.WithTx(ctx, func(tx *sql.Tx) error { return store.ActivateRelease(ctx, tx, releaseID, 1) }); err != nil {
		t.Fatal(err)
	}
	return store, releaseID
}

func mysqlSellRateChange(sourceID uint64, price string) repository.SellRateChange {
	change := repository.SellRateChange{
		SKUCode:       "seedance-2.0-standard",
		ComponentCode: "generated_second",
	}
	change.UnitPrice = price
	change.ExpectedActiveReleaseID = sourceID
	change.SemanticVersion = "1.0.1"
	change.SemanticDigest = adapter.SemanticDigest()
	return change
}

func TestMySQLChangeSellRateForksEditsAndPublishes(t *testing.T) {
	store, sourceID := mysqlActiveRelease(t)
	ctx := context.Background()
	const actorID = 1
	var result repository.CatalogChangeResult
	err := store.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		result, err = store.ChangeSellRate(ctx, tx, mysqlSellRateChange(sourceID, "0.30"), changeSellRateSigner(t), actorID)
		return err
	})
	if err != nil {
		t.Fatalf("changing a sell rate on the active release failed: %v", err)
	}
	if result.ReleaseID == sourceID || result.SourceReleaseID != sourceID || result.Activated {
		t.Fatalf("result = %+v, source = %d", result, sourceID)
	}
	// The version arithmetic the repository does by hand: the fork starts at 1,
	// then the delete, the insert and the publication each advance it once. If
	// cloneCatalogContent ever starts writing through the admin helpers, this is
	// the assertion that catches the drift.
	if result.ConfigVersion != 4 {
		t.Fatalf("config_version after the change = %d, want 4", result.ConfigVersion)
	}
	var status string
	var version uint64
	var forkHash, sourceHash string
	if err := store.DB().QueryRowContext(ctx, `SELECT status,config_version,content_hash FROM gw_catalog_releases WHERE id=?`, result.ReleaseID).Scan(&status, &version, &forkHash); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRowContext(ctx, `SELECT content_hash FROM gw_catalog_releases WHERE id=?`, sourceID).Scan(&sourceHash); err != nil {
		t.Fatal(err)
	}
	if status != "published" || version != result.ConfigVersion || forkHash == sourceHash || len(forkHash) != 64 {
		t.Fatalf("fork status=%s version=%d hash=%q source hash=%q", status, version, forkHash, sourceHash)
	}
	// The old release keeps its old price. This is SPEC §6 billing rule #5: a
	// price change is a new release, never a rewrite of what was already charged.
	var oldPrice string
	if err := store.DB().QueryRowContext(ctx, `SELECT unit_price FROM gw_sell_rates WHERE release_id=?`, sourceID).Scan(&oldPrice); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(oldPrice, "0.2") {
		t.Fatalf("the source release's price changed to %s", oldPrice)
	}
	mysqlAssertSellRateCarriedForward(t, store, result.ReleaseID, "0.3")
	mysqlAssertOperatorPriceEvidence(t, store, result.ReleaseID)
	mysqlAssertSellRateAudit(t, store, result, sourceID)
	// The point of the whole chain: the edited release can go live. Activation is
	// the caller's step, which is why ChangeSellRate reported Activated=false.
	if err := store.WithTx(ctx, func(tx *sql.Tx) error { return store.ActivateRelease(ctx, tx, result.ReleaseID, 2) }); err != nil {
		t.Fatalf("the edited release could not be activated: %v", err)
	}
}

// mysqlAssertSellRateCarriedForward checks that the replacement row differs from
// the one it replaced in the price and nothing else. A delete-and-recreate that
// dropped the metering columns would still publish, and would silently change
// what the SKU counts.
func mysqlAssertSellRateCarriedForward(t *testing.T, store *repository.Store, forkID uint64, wantPricePrefix string) {
	t.Helper()
	ctx := context.Background()
	var price, unit, component, source, event, mode string
	var scale int32
	var step, max string
	err := store.DB().QueryRowContext(ctx, `SELECT unit_price,unit_code,component_code,quantity_source,charge_event,COALESCE(pricing_mode,'flat'),unit_scale,quantity_step,max_quantity FROM gw_sell_rates WHERE release_id=?`, forkID).
		Scan(&price, &unit, &component, &source, &event, &mode, &scale, &step, &max)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(price, wantPricePrefix) {
		t.Fatalf("new price = %s, want %s…", price, wantPricePrefix)
	}
	if unit != "second" || component != "generated_second" || source != "generated_seconds" || event != "succeeded" {
		t.Fatalf("metering changed: unit=%s component=%s source=%s event=%s", unit, component, source, event)
	}
	if mode != "flat" || scale != 0 || !strings.HasPrefix(step, "1") || !strings.HasPrefix(max, "60") {
		t.Fatalf("shape changed: mode=%s scale=%d step=%s max=%s", mode, scale, step, max)
	}
	// The fork must not still be pointing at the source's evidence for the row it
	// replaced, or the published price would not be the one that was declared.
	var linked string
	if err := store.DB().QueryRowContext(ctx, `SELECT e.unit_price FROM gw_sell_rates r JOIN gw_rate_evidence_review_events v ON v.id=r.rate_evidence_review_event_id JOIN gw_rate_evidence e ON e.id=v.rate_evidence_id WHERE r.release_id=?`, forkID).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	if linked != price {
		t.Fatalf("the rate charges %s but its evidence attests to %s", price, linked)
	}
}

// mysqlAssertOperatorPriceEvidence pins §5.2.2: the operator's own price is
// recorded as a declaration, accepted by the operator, and never dressed up as an
// external observation.
func mysqlAssertOperatorPriceEvidence(t *testing.T, store *repository.Store, forkID uint64) {
	t.Helper()
	ctx := context.Background()
	var sourceType, authority, reference, state, decision string
	var reviewer uint64
	err := store.DB().QueryRowContext(ctx, `SELECT e.source_type,e.authority_level,e.source_reference,s.state,v.decision,v.reviewer_user_id
FROM gw_sell_rates r
JOIN gw_rate_evidence_review_events v ON v.id=r.rate_evidence_review_event_id
JOIN gw_rate_evidence e ON e.id=v.rate_evidence_id
JOIN gw_rate_evidence_review_state s ON s.rate_evidence_id=e.id
WHERE r.release_id=?`, forkID).Scan(&sourceType, &authority, &reference, &state, &decision, &reviewer)
	if err != nil {
		t.Fatal(err)
	}
	if sourceType != "operator_manual" || authority != "operator_declared" {
		t.Fatalf("evidence source_type=%s authority_level=%s", sourceType, authority)
	}
	if state != "accepted" || decision != "accepted" || reviewer != 1 {
		t.Fatalf("review state=%s decision=%s reviewer=%d", state, decision, reviewer)
	}
	if !strings.Contains(reference, "seedance-2.0-standard") {
		t.Fatalf("evidence reference = %q", reference)
	}
}

// mysqlAssertSellRateAudit checks the one row the chain adds that no step below
// it would write: what the operator meant, with the price on both sides of the
// change.
func mysqlAssertSellRateAudit(t *testing.T, store *repository.Store, result repository.CatalogChangeResult, sourceID uint64) {
	t.Helper()
	ctx := context.Background()
	var metadata string
	err := store.DB().QueryRowContext(ctx, `SELECT metadata FROM audit_events WHERE action='catalog_change.sell_rate' AND resource_type='catalog_release' AND resource_id=?`, result.ReleaseID).Scan(&metadata)
	if err != nil {
		t.Fatalf("no audit row for the change: %v", err)
	}
	for _, want := range []string{
		`"reason_code":"console_price_change"`, `"sku_code":"seedance-2.0-standard"`,
		`"component_code":"generated_second"`, `"before"`, `"after"`,
	} {
		if !strings.Contains(metadata, want) {
			t.Fatalf("audit metadata %s is missing %s", metadata, want)
		}
	}
	// The before/after prices are what make the row useful when someone asks why a
	// call was charged differently on either side of a release boundary.
	if !strings.Contains(metadata, "0.30") || !strings.Contains(metadata, "0.2") {
		t.Fatalf("audit metadata %s does not carry both prices", metadata)
	}
}

// TestMySQLChangeSellRateRefusesAStaleBase is the §5.3 anti-fork guard. Two
// admins editing the same page cannot both fork from the release they each saw:
// the second would branch off a base that is no longer live and, on activation,
// throw away the first one's change.
func TestMySQLChangeSellRateRefusesAStaleBase(t *testing.T) {
	store, sourceID := mysqlActiveRelease(t)
	ctx := context.Background()
	err := store.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := store.ChangeSellRate(ctx, tx, mysqlSellRateChange(sourceID+1000, "0.30"), changeSellRateSigner(t), 1)
		return err
	})
	if !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("stale expected_active_release_id error = %v, want a conflict", err)
	}
	// Refused before anything was written: no fork, no evidence, no audit row.
	// A guard that rejected after forking would leave abandoned drafts behind
	// every time two admins collided.
	var releases, evidence uint64
	if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_catalog_releases`).Scan(&releases); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_rate_evidence WHERE source_type='operator_manual'`).Scan(&evidence); err != nil {
		t.Fatal(err)
	}
	if releases != 1 || evidence != 0 {
		t.Fatalf("the refused change left %d releases and %d operator evidence rows", releases, evidence)
	}
}

func TestMySQLChangeSellRateRejectsUnknownAndInvalidTargets(t *testing.T) {
	store, sourceID := mysqlActiveRelease(t)
	ctx := context.Background()
	for name, testCase := range map[string]struct {
		mutate func(*repository.SellRateChange)
		want   error
	}{
		"unknown sku":       {func(in *repository.SellRateChange) { in.SKUCode = "no-such-sku" }, repository.ErrNotFound},
		"unknown component": {func(in *repository.SellRateChange) { in.ComponentCode = "input_token" }, repository.ErrNotFound},
		"negative price":    {func(in *repository.SellRateChange) { in.UnitPrice = "-1" }, repository.ErrInvalidInput},
		"unparsable price":  {func(in *repository.SellRateChange) { in.UnitPrice = "free" }, repository.ErrInvalidInput},
		// A flat rate with an expression, and an expression rate without one, are
		// both refused here rather than by the pairing CHECK, so the operator gets an
		// error about the formula instead of a constraint name.
		"flat with expression":       {func(in *repository.SellRateChange) { in.PricingExpr = "1+1" }, repository.ErrInvalidInput},
		"expression without formula": {func(in *repository.SellRateChange) { in.PricingMode = "expression" }, repository.ErrInvalidInput},
		"no base guard":              {func(in *repository.SellRateChange) { in.ExpectedActiveReleaseID = 0 }, repository.ErrInvalidInput},
	} {
		change := mysqlSellRateChange(sourceID, "0.30")
		testCase.mutate(&change)
		err := store.WithTx(ctx, func(tx *sql.Tx) error {
			_, err := store.ChangeSellRate(ctx, tx, change, changeSellRateSigner(t), 1)
			return err
		})
		if !errors.Is(err, testCase.want) {
			t.Fatalf("%s: error = %v, want %v", name, err, testCase.want)
		}
	}
}

// mysqlCostRateChange is the cost-side counterpart to mysqlSellRateChange. The
// fixture builds exactly one product with one plan under one pool, so the natural
// address is three codes; pool_code is verified by TestMySQLChangeCostRate…Pool.
func mysqlCostRateChange(sourceID uint64, price string) repository.CostRateChange {
	change := repository.CostRateChange{
		ProductCode:   "seedance-2.0-official",
		PlanCode:      "official-cny",
		ComponentCode: "generated_second",
	}
	change.UnitPrice = price
	change.ExpectedActiveReleaseID = sourceID
	change.SemanticVersion = "1.0.1"
	change.SemanticDigest = adapter.SemanticDigest()
	return change
}

// TestMySQLChangeCostRateForksEditsAndPublishes is the cost side of the same
// chain the sell-rate integration test pins. The cost side matters for §5.2.2:
// upstream prices are not directly observable, so what looks like a "cost update"
// is actually an operator declaration, and the evidence must not pretend
// otherwise.
func TestMySQLChangeCostRateForksEditsAndPublishes(t *testing.T) {
	store, sourceID := mysqlActiveRelease(t)
	ctx := context.Background()
	const actorID = 1
	var result repository.CatalogChangeResult
	err := store.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		result, err = store.ChangeCostRate(ctx, tx, mysqlCostRateChange(sourceID, "0.12"), changeCostRateSigner(t), actorID)
		return err
	})
	if err != nil {
		t.Fatalf("changing a cost rate on the active release failed: %v", err)
	}
	if result.ReleaseID == sourceID || result.SourceReleaseID != sourceID || result.Activated {
		t.Fatalf("result = %+v, source = %d", result, sourceID)
	}
	if result.ConfigVersion != 4 {
		t.Fatalf("config_version after the change = %d, want 4", result.ConfigVersion)
	}
	var status, forkHash, sourceHash string
	var version uint64
	if err := store.DB().QueryRowContext(ctx, `SELECT status,config_version,content_hash FROM gw_catalog_releases WHERE id=?`, result.ReleaseID).Scan(&status, &version, &forkHash); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRowContext(ctx, `SELECT content_hash FROM gw_catalog_releases WHERE id=?`, sourceID).Scan(&sourceHash); err != nil {
		t.Fatal(err)
	}
	if status != "published" || version != result.ConfigVersion || forkHash == sourceHash || len(forkHash) != 64 {
		t.Fatalf("fork status=%s version=%d hash=%q source hash=%q", status, version, forkHash, sourceHash)
	}
	// The old release's cost is preserved. Same reason as the sell test: §6 rule 5
	// forbids rewriting what was already charged.
	var oldPrice string
	if err := store.DB().QueryRowContext(ctx, `SELECT unit_price FROM gw_cost_rates WHERE release_id=?`, sourceID).Scan(&oldPrice); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(oldPrice, "0.08") {
		t.Fatalf("the source release's cost changed to %s", oldPrice)
	}
	mysqlAssertCostRateCarriedForward(t, store, result.ReleaseID, "0.12")
	mysqlAssertOperatorCostEvidence(t, store, result.ReleaseID)
	mysqlAssertCostRateAudit(t, store, result, sourceID)
	if err := store.WithTx(ctx, func(tx *sql.Tx) error { return store.ActivateRelease(ctx, tx, result.ReleaseID, 2) }); err != nil {
		t.Fatalf("the edited cost release could not be activated: %v", err)
	}
}

func mysqlAssertCostRateCarriedForward(t *testing.T, store *repository.Store, forkID uint64, wantPricePrefix string) {
	t.Helper()
	ctx := context.Background()
	var price, unit, component, source, event, mode string
	var scale int32
	var step, max string
	err := store.DB().QueryRowContext(ctx, `SELECT unit_price,unit_code,component_code,quantity_source,charge_event,COALESCE(pricing_mode,'flat'),unit_scale,quantity_step,max_quantity FROM gw_cost_rates WHERE release_id=?`, forkID).
		Scan(&price, &unit, &component, &source, &event, &mode, &scale, &step, &max)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(price, wantPricePrefix) {
		t.Fatalf("new cost = %s, want %s…", price, wantPricePrefix)
	}
	if unit != "second" || component != "generated_second" || source != "generated_seconds" || event != "succeeded" {
		t.Fatalf("metering changed: unit=%s component=%s source=%s event=%s", unit, component, source, event)
	}
	if mode != "flat" || scale != 0 || !strings.HasPrefix(step, "1") || !strings.HasPrefix(max, "60") {
		t.Fatalf("shape changed: mode=%s scale=%d step=%s max=%s", mode, scale, step, max)
	}
	var linked string
	if err := store.DB().QueryRowContext(ctx, `SELECT e.unit_price FROM gw_cost_rates r JOIN gw_rate_evidence_review_events v ON v.id=r.rate_evidence_review_event_id JOIN gw_rate_evidence e ON e.id=v.rate_evidence_id WHERE r.release_id=?`, forkID).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	if linked != price {
		t.Fatalf("the cost rate charges %s but its evidence attests to %s", price, linked)
	}
}

// mysqlAssertOperatorCostEvidence is the §5.2.2 assertion the cost side exists
// to defend: even though the rate lives on the cost table, the evidence for it
// still says operator_declared, because the operator is the one who declared it.
func mysqlAssertOperatorCostEvidence(t *testing.T, store *repository.Store, forkID uint64) {
	t.Helper()
	ctx := context.Background()
	var sourceType, authority, reference, state, decision string
	var reviewer uint64
	err := store.DB().QueryRowContext(ctx, `SELECT e.source_type,e.authority_level,e.source_reference,s.state,v.decision,v.reviewer_user_id
FROM gw_cost_rates r
JOIN gw_rate_evidence_review_events v ON v.id=r.rate_evidence_review_event_id
JOIN gw_rate_evidence e ON e.id=v.rate_evidence_id
JOIN gw_rate_evidence_review_state s ON s.rate_evidence_id=e.id
WHERE r.release_id=?`, forkID).Scan(&sourceType, &authority, &reference, &state, &decision, &reviewer)
	if err != nil {
		t.Fatal(err)
	}
	// This is the single most load-bearing assertion in the whole cost path: a
	// cost declared through the console must never be labelled provider_confirmed,
	// because it is a Prism estimate, not an upstream fact.
	if sourceType != "operator_manual" || authority != "operator_declared" {
		t.Fatalf("cost evidence source_type=%s authority_level=%s (must be operator_manual/operator_declared per §5.2.2)", sourceType, authority)
	}
	if state != "accepted" || decision != "accepted" || reviewer != 1 {
		t.Fatalf("review state=%s decision=%s reviewer=%d", state, decision, reviewer)
	}
	// The reference identifies the rate uniquely enough that a re-declaration is
	// a new fact and not a duplicate under uq_gw_rate_evidence_fact.
	if !strings.Contains(reference, "seedance-2.0-official") || !strings.Contains(reference, "official-cny") {
		t.Fatalf("cost evidence reference = %q, missing product/plan address", reference)
	}
}

func mysqlAssertCostRateAudit(t *testing.T, store *repository.Store, result repository.CatalogChangeResult, sourceID uint64) {
	t.Helper()
	ctx := context.Background()
	var metadata string
	err := store.DB().QueryRowContext(ctx, `SELECT metadata FROM audit_events WHERE action='catalog_change.cost_rate' AND resource_type='catalog_release' AND resource_id=?`, result.ReleaseID).Scan(&metadata)
	if err != nil {
		t.Fatalf("no audit row for the cost change: %v", err)
	}
	for _, want := range []string{
		`"reason_code":"console_price_change"`, `"product_code":"seedance-2.0-official"`,
		`"plan_code":"official-cny"`, `"component_code":"generated_second"`, `"before"`, `"after"`,
	} {
		if !strings.Contains(metadata, want) {
			t.Fatalf("audit metadata %s is missing %s", metadata, want)
		}
	}
	if !strings.Contains(metadata, "0.12") || !strings.Contains(metadata, "0.08") {
		t.Fatalf("audit metadata %s does not carry both cost prices", metadata)
	}
	// The pool_code should be present even though the caller did not send one:
	// the loader resolves it from the row and echoes it back so the audit trail
	// records which pool was actually touched.
	if !strings.Contains(metadata, `"pool_code":"primary"`) {
		t.Fatalf("audit metadata %s is missing resolved pool_code", metadata)
	}
}

// TestMySQLChangeCostRateRefusesAStaleBase — same §5.3 anti-fork guard the sell
// side has, kept explicit here because the codes it addresses differ.
func TestMySQLChangeCostRateRefusesAStaleBase(t *testing.T) {
	store, sourceID := mysqlActiveRelease(t)
	ctx := context.Background()
	err := store.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := store.ChangeCostRate(ctx, tx, mysqlCostRateChange(sourceID+1000, "0.12"), changeCostRateSigner(t), 1)
		return err
	})
	if !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("stale expected_active_release_id error = %v, want a conflict", err)
	}
	var releases, evidence uint64
	if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_catalog_releases`).Scan(&releases); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_rate_evidence WHERE source_type='operator_manual'`).Scan(&evidence); err != nil {
		t.Fatal(err)
	}
	if releases != 1 || evidence != 0 {
		t.Fatalf("the refused cost change left %d releases and %d operator evidence rows", releases, evidence)
	}
}

func TestMySQLChangeCostRateRejectsUnknownAndInvalidTargets(t *testing.T) {
	store, sourceID := mysqlActiveRelease(t)
	ctx := context.Background()
	for name, testCase := range map[string]struct {
		mutate func(*repository.CostRateChange)
		want   error
	}{
		"unknown product":            {func(in *repository.CostRateChange) { in.ProductCode = "no-such-product" }, repository.ErrNotFound},
		"unknown plan":               {func(in *repository.CostRateChange) { in.PlanCode = "no-such-plan" }, repository.ErrNotFound},
		"unknown component":          {func(in *repository.CostRateChange) { in.ComponentCode = "input_token" }, repository.ErrNotFound},
		"unknown pool":               {func(in *repository.CostRateChange) { in.PoolCode = "no-such-pool" }, repository.ErrNotFound},
		"negative price":             {func(in *repository.CostRateChange) { in.UnitPrice = "-0.01" }, repository.ErrInvalidInput},
		"flat with expression":       {func(in *repository.CostRateChange) { in.PricingExpr = "1+1" }, repository.ErrInvalidInput},
		"expression without formula": {func(in *repository.CostRateChange) { in.PricingMode = "expression" }, repository.ErrInvalidInput},
		"no base guard":              {func(in *repository.CostRateChange) { in.ExpectedActiveReleaseID = 0 }, repository.ErrInvalidInput},
	} {
		change := mysqlCostRateChange(sourceID, "0.12")
		testCase.mutate(&change)
		err := store.WithTx(ctx, func(tx *sql.Tx) error {
			_, err := store.ChangeCostRate(ctx, tx, change, changeCostRateSigner(t), 1)
			return err
		})
		if !errors.Is(err, testCase.want) {
			t.Fatalf("%s: error = %v, want %v", name, err, testCase.want)
		}
	}
}

// TestMySQLChangeCostRateRefusesAnAmbiguousAddress covers the case a product has
// more than one offering under the same plan_code — legitimate, but the operator
// has to say which pool. loadCostRateByCode refuses silently picking one so a
// cost change never lands on a plan the operator did not name.
//
// The ambiguity is seeded onto the *source* draft before publication rather than
// onto a fork, because the fork inherits its content by clone: to make the
// clone-then-change path see two matching rows, the two rows have to already
// exist in what is being cloned.
func TestMySQLChangeCostRateRefusesAnAmbiguousAddress(t *testing.T) {
	store, draftID := mysqlPublishableRelease(t)
	ctx := context.Background()
	const actorID = 1
	// A second credential pool on the same channel, and a second offering under
	// the same product+transport but tied to that pool, gives the join a legit
	// second row without breaking any composite foreign key. The cost plan then
	// reuses plan_code, which is what makes the (product, plan) address ambiguous.
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO gw_credential_pools(channel_id,pool_code,display_name,status,config_version,created_at,updated_at) SELECT channel_id,'secondary','Secondary','active',1,CURRENT_TIMESTAMP(3),CURRENT_TIMESTAMP(3) FROM gw_credential_pools WHERE pool_code='primary'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO gw_offerings(release_id,product_transport_id,credential_pool_id,cost_plan_code,created_at) SELECT release_id,product_transport_id,(SELECT id FROM gw_credential_pools WHERE pool_code='secondary'),cost_plan_code,CURRENT_TIMESTAMP(3) FROM gw_offerings WHERE release_id=?`, draftID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO gw_cost_plans(release_id,offering_id,plan_code,created_at) SELECT o.release_id,o.id,'official-cny',CURRENT_TIMESTAMP(3) FROM gw_offerings o WHERE o.release_id=? AND o.credential_pool_id=(SELECT id FROM gw_credential_pools WHERE pool_code='secondary')`, draftID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO gw_cost_rates(release_id,cost_plan_id,rate_evidence_review_event_id,unit_code,unit_price,currency_code,currency_version,component_code,quantity_source,charge_event,unit_scale,quantity_step,max_quantity,pricing_mode,created_at) SELECT r.release_id,(SELECT id FROM gw_cost_plans WHERE release_id=r.release_id AND offering_id=(SELECT id FROM gw_offerings WHERE release_id=r.release_id AND credential_pool_id=(SELECT id FROM gw_credential_pools WHERE pool_code='secondary'))),r.rate_evidence_review_event_id,r.unit_code,r.unit_price,r.currency_code,r.currency_version,r.component_code,r.quantity_source,r.charge_event,r.unit_scale,r.quantity_step,r.max_quantity,r.pricing_mode,CURRENT_TIMESTAMP(3) FROM gw_cost_rates r WHERE r.release_id=?`, draftID); err != nil {
		t.Fatal(err)
	}
	// Runtime state for the new offering is required for CheckCatalogReadiness, and
	// activation is the state the change starts from. Reusing the primary offering's
	// state row shape keeps the second one routable.
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO gw_offering_runtime_state(release_id,offering_id,state,reason_code,updated_at) SELECT release_id,id,'active','seed',CURRENT_TIMESTAMP(3) FROM gw_offerings WHERE release_id=? AND credential_pool_id=(SELECT id FROM gw_credential_pools WHERE pool_code='secondary')`, draftID); err != nil {
		t.Fatal(err)
	}
	// Add a route for the second offering so publish's structural check passes.
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO gw_routes(release_id,sku_id,offering_id,priority,weight,created_at) SELECT r.release_id,r.sku_id,o.id,r.priority,r.weight,CURRENT_TIMESTAMP(3) FROM gw_routes r JOIN gw_offerings o ON o.release_id=r.release_id AND o.credential_pool_id=(SELECT id FROM gw_credential_pools WHERE pool_code='secondary') WHERE r.release_id=?`, draftID); err != nil {
		t.Fatal(err)
	}
	if err := store.WithTx(ctx, func(tx *sql.Tx) error { return store.PublishRelease(ctx, tx, draftID, actorID) }); err != nil {
		t.Fatalf("publishing the seeded ambiguous release failed: %v", err)
	}
	if err := store.WithTx(ctx, func(tx *sql.Tx) error { return store.ActivateRelease(ctx, tx, draftID, 1) }); err != nil {
		t.Fatal(err)
	}
	// The (product, plan, component) triple now matches two rows on the fork.
	// Without pool_code the loader refuses; with pool_code it resolves.
	err := store.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := store.ChangeCostRate(ctx, tx, mysqlCostRateChange(draftID, "0.12"), changeCostRateSigner(t), actorID)
		return err
	})
	if !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("ambiguous cost address error = %v, want ErrConflict", err)
	}
	// Second attempt names the pool, and the same change lands. The whole point of
	// pool_code being optional is that this only has to be supplied when the two
	// rows are in fact there.
	disambiguated := mysqlCostRateChange(draftID, "0.13")
	disambiguated.PoolCode = "primary"
	err = store.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := store.ChangeCostRate(ctx, tx, disambiguated, changeCostRateSigner(t), actorID)
		return err
	})
	if err != nil {
		t.Fatalf("disambiguated cost change failed: %v", err)
	}
}

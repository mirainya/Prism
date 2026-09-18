package admin

import (
	"database/sql"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/model"
)

func ListUnifiedCurrencies(c *gin.Context) {
	page, size, ok := unifiedGatewayPagination(c)
	if !ok {
		return
	}
	db, err := model.DB().DB()
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	ctx := c.Request.Context()
	var total int64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM billing_currency_definitions`).Scan(&total); err != nil {
		unifiedChannelError(c, err)
		return
	}
	rows, err := db.QueryContext(ctx, `SELECT d.id,d.currency_code,d.definition_version,d.fraction_digits,d.rounding_mode,d.max_amount,d.status,d.created_at,CASE WHEN s.id=1 THEN TRUE ELSE FALSE END FROM billing_currency_definitions d LEFT JOIN billing_system_state s ON s.currency_code=d.currency_code AND s.currency_version=d.definition_version ORDER BY d.id DESC LIMIT ? OFFSET ?`, size, (page-1)*size)
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0, size)
	for rows.Next() {
		var id, version uint64
		var digits int32
		var code, rounding, maxAmount, status string
		var created sql.NullTime
		var active bool
		if err := rows.Scan(&id, &code, &version, &digits, &rounding, &maxAmount, &status, &created, &active); err != nil {
			unifiedChannelError(c, err)
			return
		}
		items = append(items, gin.H{"id": id, "currency_code": code, "definition_version": version, "fraction_digits": digits, "rounding_mode": rounding, "max_amount": maxAmount, "status": status, "is_settlement": active, "created_at": nullableTime(created)})
	}
	if err := rows.Err(); err != nil {
		unifiedChannelError(c, err)
		return
	}
	resp.Success(c, unifiedGatewayPage{Items: items, Page: page, PageSize: size, Total: total})
}

func CreateUnifiedCurrency(c *gin.Context) {
	var in repository.CurrencyDefinitionInput
	if !unifiedChannelBody(c, &in) {
		return
	}
	unifiedChannelWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) (uint64, error) {
		return store.CreateCurrencyDefinition(c.Request.Context(), tx, in, actor)
	})
}

func ActivateUnifiedCurrency(c *gin.Context) {
	code := strings.TrimSpace(c.Param("code"))
	var in struct {
		Version uint32 `json:"definition_version"`
	}
	if !unifiedChannelBody(c, &in) {
		return
	}
	unifiedChannelWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) (uint64, error) {
		return uint64(in.Version), store.ActivateSettlementCurrency(c.Request.Context(), tx, code, in.Version, actor)
	})
}

func ListUnifiedRateEvidence(c *gin.Context) {
	page, size, ok := unifiedGatewayPagination(c)
	if !ok {
		return
	}
	state := strings.ToLower(strings.TrimSpace(c.Query("state")))
	if state != "" && state != "submitted" && state != "accepted" && state != "rejected" && state != "superseded" {
		unifiedChannelError(c, repository.ErrInvalidInput)
		return
	}
	db, err := model.DB().DB()
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	where := ` WHERE (?='' OR s.state=?)`
	args := []any{state, state}
	ctx := c.Request.Context()
	var total int64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_rate_evidence e JOIN gw_rate_evidence_review_state s ON s.rate_evidence_id=e.id`+where, args...).Scan(&total); err != nil {
		unifiedChannelError(c, err)
		return
	}
	rows, err := db.QueryContext(ctx, `SELECT e.id,e.source_type,e.authority_level,e.source_reference,e.observed_at,e.unit_code,e.unit_price,e.currency_code,e.currency_version,e.created_at,s.state,s.state_version,v.reason_code,v.reviewer_user_id,v.created_at FROM gw_rate_evidence e JOIN gw_rate_evidence_review_state s ON s.rate_evidence_id=e.id JOIN gw_rate_evidence_review_events v ON v.id=s.latest_review_event_id`+where+` ORDER BY e.id DESC LIMIT ? OFFSET ?`, append(args, size, (page-1)*size)...)
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0, size)
	for rows.Next() {
		var id, version, currencyVersion, reviewer uint64
		var sourceType, authority, reference, unit, price, currency, stateValue, reason string
		var observed, created, reviewed sql.NullTime
		if err := rows.Scan(&id, &sourceType, &authority, &reference, &observed, &unit, &price, &currency, &currencyVersion, &created, &stateValue, &version, &reason, &reviewer, &reviewed); err != nil {
			unifiedChannelError(c, err)
			return
		}
		items = append(items, gin.H{"id": id, "source_type": sourceType, "authority_level": authority, "source_reference": reference, "observed_at": nullableTime(observed), "unit_code": unit, "unit_price": price, "currency_code": currency, "currency_version": currencyVersion, "state": stateValue, "state_version": version, "reason_code": reason, "reviewer_user_id": reviewer, "reviewed_at": nullableTime(reviewed), "created_at": nullableTime(created)})
	}
	if err := rows.Err(); err != nil {
		unifiedChannelError(c, err)
		return
	}
	resp.Success(c, unifiedGatewayPage{Items: items, Page: page, PageSize: size, Total: total})
}

func CreateUnifiedRateEvidence(c *gin.Context) {
	var in repository.RateEvidenceInput
	if !unifiedChannelBody(c, &in) {
		return
	}
	in.Normalize()
	digest, err := rateEvidenceHMAC(in)
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	in.FactHMAC = digest
	unifiedChannelWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) (uint64, error) {
		return store.CreateRateEvidence(c.Request.Context(), tx, in, actor)
	})
}

func ReviewUnifiedRateEvidence(c *gin.Context) {
	evidenceID, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	var in repository.RateEvidenceReviewInput
	if !unifiedChannelBody(c, &in) {
		return
	}
	unifiedChannelWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) (uint64, error) {
		return uint64(evidenceID), store.ReviewRateEvidence(c.Request.Context(), tx, uint64(evidenceID), in, actor)
	})
}

func ListUnifiedCatalogRates(c *gin.Context) {
	releaseID, ok := catalogReleaseID(c)
	if !ok {
		return
	}
	page, size, ok := unifiedGatewayPagination(c)
	if !ok {
		return
	}
	kind := strings.ToLower(strings.TrimSpace(c.Query("kind")))
	if kind == "" {
		kind = "sell"
	}
	if kind != "sell" && kind != "cost" {
		unifiedChannelError(c, repository.ErrInvalidInput)
		return
	}
	db, err := model.DB().DB()
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	table, parent, parentTable, label := "gw_sell_rates", "sku_id", "gw_skus", "sku_code"
	parentKey := "p.sku_code"
	parentJoins := ""
	if kind == "cost" {
		table, parent, parentTable, label = "gw_cost_rates", "cost_plan_id", "gw_cost_plans", "plan_code"
		// A plan code is only unique within an offering. Include the stable
		// product and credential-pool identities so a cloned release can be
		// addressed without relying on its new surrogate IDs.
		parentKey = "CONCAT(prod.product_code,'|',pool.pool_code,'|',p.plan_code)"
		parentJoins = ` JOIN gw_offerings o ON o.release_id=p.release_id AND o.id=p.offering_id
JOIN gw_product_transports pt ON pt.release_id=o.release_id AND pt.id=o.product_transport_id
JOIN gw_products prod ON prod.release_id=pt.release_id AND prod.id=pt.product_id
JOIN gw_credential_pools pool ON pool.id=o.credential_pool_id`
	}
	ctx := c.Request.Context()
	var total int64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE release_id=?`, releaseID).Scan(&total); err != nil {
		unifiedChannelError(c, err)
		return
	}
	query := `SELECT r.id,r.` + parent + `,p.` + label + `,` + parentKey + `,r.component_code,r.unit_code,r.quantity_source,r.charge_event,r.unit_price,r.unit_scale,r.quantity_step,r.max_quantity,r.currency_code,r.currency_version,COALESCE(r.pricing_mode,'flat'),COALESCE(r.pricing_expr,''),COALESCE(r.max_price,''),e.id,s.rate_evidence_id,s.state FROM ` + table + ` r JOIN ` + parentTable + ` p ON p.id=r.` + parent + ` AND p.release_id=r.release_id` + parentJoins + ` JOIN gw_rate_evidence_review_events e ON e.id=r.rate_evidence_review_event_id JOIN gw_rate_evidence_review_state s ON s.rate_evidence_id=e.rate_evidence_id WHERE r.release_id=? ORDER BY r.id DESC LIMIT ? OFFSET ?`
	rows, err := db.QueryContext(ctx, query, releaseID, size, (page-1)*size)
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0, size)
	for rows.Next() {
		var id, parentID, scale, currencyVersion, reviewEventID, evidenceID uint64
		var parentLabel, stableParentKey, component, unit, source, event, price, step, max, currency, pricingMode, pricingExpr, maxPrice, state string
		if err := rows.Scan(&id, &parentID, &parentLabel, &stableParentKey, &component, &unit, &source, &event, &price, &scale, &step, &max, &currency, &currencyVersion, &pricingMode, &pricingExpr, &maxPrice, &reviewEventID, &evidenceID, &state); err != nil {
			unifiedChannelError(c, err)
			return
		}
		items = append(items, gin.H{"id": id, "kind": kind, "parent_id": parentID, "parent_label": parentLabel, "parent_key": stableParentKey, "component_code": component, "unit_code": unit, "quantity_source": source, "charge_event": event, "unit_price": price, "unit_scale": scale, "quantity_step": step, "max_quantity": max, "currency_code": currency, "currency_version": currencyVersion, "pricing_mode": pricingMode, "pricing_expr": pricingExpr, "max_price": maxPrice, "review_event_id": reviewEventID, "evidence_id": evidenceID, "evidence_state": state})
	}
	if err := rows.Err(); err != nil {
		unifiedChannelError(c, err)
		return
	}
	resp.Success(c, unifiedGatewayPage{Items: items, Page: page, PageSize: size, Total: total})
}

func CreateUnifiedCatalogRate(c *gin.Context) {
	releaseID, ok := catalogReleaseID(c)
	if !ok {
		return
	}
	kind := strings.ToLower(strings.TrimSpace(c.Param("kind")))
	if kind != "sell" && kind != "cost" {
		unifiedChannelError(c, repository.ErrInvalidInput)
		return
	}
	var in struct {
		ParentID uint64 `json:"parent_id"`
		repository.CatalogRateInput
	}
	if !unifiedChannelBody(c, &in) {
		return
	}
	unifiedChannelWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) (uint64, error) {
		return store.CreateCatalogRate(c.Request.Context(), tx, kind, releaseID, in.ParentID, in.CatalogRateInput, actor)
	})
}

func DeleteUnifiedCatalogRate(c *gin.Context) {
	releaseID, rateID, ok := catalogResourceIDs(c, "rate_id")
	if !ok {
		return
	}
	kind := strings.ToLower(strings.TrimSpace(c.Param("kind")))
	var in struct {
		ExpectedVersion uint64 `json:"expected_version"`
	}
	if !unifiedChannelBody(c, &in) {
		return
	}
	unifiedChannelWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) (uint64, error) {
		return rateID, store.DeleteCatalogRate(c.Request.Context(), tx, kind, releaseID, rateID, in.ExpectedVersion, actor)
	})
}

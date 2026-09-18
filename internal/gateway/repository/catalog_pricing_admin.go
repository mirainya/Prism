package repository

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/security"
)

type CurrencyDefinitionInput struct {
	Code           string `json:"currency_code"`
	Version        uint32 `json:"definition_version"`
	FractionDigits int32  `json:"fraction_digits"`
	RoundingMode   string `json:"rounding_mode"`
	MaxAmount      string `json:"max_amount"`
}

func (in *CurrencyDefinitionInput) Normalize() {
	in.Code = strings.ToUpper(strings.TrimSpace(in.Code))
	in.RoundingMode = strings.ToLower(strings.TrimSpace(in.RoundingMode))
}

func (in CurrencyDefinitionInput) Validate() error {
	currency := billing.Currency{Code: in.Code, Version: in.Version, FractionDigits: in.FractionDigits, RoundingMode: in.RoundingMode, MaxAmount: in.MaxAmount}
	if len(in.Code) < 3 || len(in.Code) > 16 || !catalogIdentityPattern.MatchString(in.Code) || currency.Validate() != nil {
		return ErrInvalidInput
	}
	return nil
}

func (s *Store) CreateCurrencyDefinition(ctx context.Context, tx *sql.Tx, in CurrencyDefinitionInput, actorID uint64) (uint64, error) {
	if tx == nil || actorID == 0 {
		return 0, ErrInvalidInput
	}
	in.Normalize()
	if err := in.Validate(); err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO billing_currency_definitions(currency_code,definition_version,fraction_digits,rounding_mode,max_amount,status,created_at) VALUES (?,?,?,?,?,'active',?)`, in.Code, in.Version, in.FractionDigits, in.RoundingMode, in.MaxAmount, nowUTC())
	if err != nil {
		return 0, err
	}
	id, err := lastID(result)
	if err != nil {
		return 0, err
	}
	return id, recordCatalogAdminChange(ctx, tx, actorID, "unified.currency.create", "billing_currency", id, in)
}

func (s *Store) ActivateSettlementCurrency(ctx context.Context, tx *sql.Tx, code string, version uint32, actorID uint64) error {
	code = strings.ToUpper(strings.TrimSpace(code))
	if tx == nil || actorID == 0 || version == 0 || !catalogIdentityPattern.MatchString(code) {
		return ErrInvalidInput
	}
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM billing_currency_definitions WHERE currency_code=? AND definition_version=? FOR SHARE`, code, version).Scan(&status); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if status != "active" {
		return ErrConflict
	}
	var existingCode sql.NullString
	var existingVersion sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT currency_code,currency_version FROM billing_system_state WHERE id=1 FOR UPDATE`).Scan(&existingCode, &existingVersion)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil && existingCode.String == code && uint32(existingVersion.Int64) == version {
		return s.ensureBillingPostingInfrastructure(ctx, tx, code, version)
	}
	var accounts, events uint64
	if err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM billing_accounts)+(SELECT COUNT(*) FROM billing_events)`).Scan(&accounts); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM ledger_transactions`).Scan(&events); err != nil {
		return err
	}
	if accounts != 0 || events != 0 {
		return ErrConflict
	}
	if err == sql.ErrNoRows {
		_, err = tx.ExecContext(ctx, `INSERT INTO billing_system_state(id,currency_code,currency_version,initialization_version,updated_at) VALUES (1,?,?,1,?)`, code, version, nowUTC())
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE billing_system_state SET currency_code=?,currency_version=?,initialization_version=initialization_version+1,updated_at=? WHERE id=1`, code, version, nowUTC())
	}
	if err != nil {
		return err
	}
	if err := s.ensureBillingPostingInfrastructure(ctx, tx, code, version); err != nil {
		return err
	}
	return recordCatalogAdminChange(ctx, tx, actorID, "unified.currency.activate", "billing_currency", uint64(version), ginSafeMetadata{"currency_code": code, "definition_version": version})
}

type RateEvidenceInput struct {
	SourceType      string    `json:"source_type"`
	AuthorityLevel  string    `json:"authority_level"`
	SourceReference string    `json:"source_reference"`
	ObservedAt      time.Time `json:"observed_at"`
	UnitCode        string    `json:"unit_code"`
	UnitPrice       string    `json:"unit_price"`
	CurrencyCode    string    `json:"currency_code"`
	CurrencyVersion uint32    `json:"currency_version"`
	FactHMAC        string    `json:"-"`
}

func RateEvidenceFactHMAC(in RateEvidenceInput, key []byte) (string, error) {
	if len(key) != security.KeySize {
		return "", ErrCredentialEncryptionUnavailable
	}
	body, err := json.Marshal(struct {
		SourceType, AuthorityLevel, SourceReference, ObservedAt, UnitCode, UnitPrice, CurrencyCode string
		CurrencyVersion                                                                            uint32
	}{in.SourceType, in.AuthorityLevel, in.SourceReference, in.ObservedAt.UTC().Format("2006-01-02T15:04:05.000000000Z07:00"), in.UnitCode, in.UnitPrice, in.CurrencyCode, in.CurrencyVersion})
	if err != nil {
		return "", err
	}
	digest := security.DomainDigest(key, "rate-evidence-v1", body)
	return hex.EncodeToString(digest[:]), nil
}

func (in *RateEvidenceInput) Normalize() {
	in.SourceType = strings.ToLower(strings.TrimSpace(in.SourceType))
	in.AuthorityLevel = strings.ToLower(strings.TrimSpace(in.AuthorityLevel))
	in.SourceReference = strings.TrimSpace(in.SourceReference)
	in.UnitCode = strings.ToLower(strings.TrimSpace(in.UnitCode))
	in.CurrencyCode = strings.ToUpper(strings.TrimSpace(in.CurrencyCode))
	in.ObservedAt = in.ObservedAt.UTC()
}

func (in RateEvidenceInput) Validate(now time.Time) error {
	if !catalogIdentityPattern.MatchString(in.SourceType) || !catalogIdentityPattern.MatchString(in.AuthorityLevel) ||
		in.SourceReference == "" || len(in.SourceReference) > 512 || !utf8.ValidString(in.SourceReference) || strings.ContainsAny(in.SourceReference, "\x00\r\n") ||
		!catalogIdentityPattern.MatchString(in.UnitCode) || !catalogIdentityPattern.MatchString(in.CurrencyCode) || in.CurrencyVersion == 0 ||
		in.ObservedAt.IsZero() || in.ObservedAt.After(now.Add(5*time.Minute)) || !validHexDigest(in.FactHMAC, 32) {
		return ErrInvalidInput
	}
	if strings.Contains(in.SourceReference, "://") {
		reference, err := url.Parse(in.SourceReference)
		if err != nil || reference.Scheme != "http" && reference.Scheme != "https" || reference.Hostname() == "" || reference.User != nil || reference.RawQuery != "" || reference.Fragment != "" || !validRemoteHost(reference.Hostname()) {
			return ErrInvalidInput
		}
	}
	if _, err := billing.ParseAmount(in.UnitPrice, 18, true); err != nil {
		return ErrInvalidInput
	}
	return nil
}

func (s *Store) CreateRateEvidence(ctx context.Context, tx *sql.Tx, in RateEvidenceInput, actorID uint64) (uint64, error) {
	if tx == nil || actorID == 0 {
		return 0, ErrInvalidInput
	}
	in.Normalize()
	if err := in.Validate(nowUTC()); err != nil {
		return 0, err
	}
	var currencyStatus string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM billing_currency_definitions WHERE currency_code=? AND definition_version=? FOR SHARE`, in.CurrencyCode, in.CurrencyVersion).Scan(&currencyStatus); err == sql.ErrNoRows {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	}
	if currencyStatus != "active" {
		return 0, ErrConflict
	}
	now := nowUTC()
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_rate_evidence(source_type,authority_level,source_reference,observed_at,fact_hmac,unit_code,unit_price,currency_code,currency_version,created_at) VALUES (?,?,?,?,?,?,?,?,?,?)`, in.SourceType, in.AuthorityLevel, in.SourceReference, in.ObservedAt, in.FactHMAC, in.UnitCode, in.UnitPrice, in.CurrencyCode, in.CurrencyVersion, now)
	if err != nil {
		return 0, err
	}
	evidenceID, err := lastID(result)
	if err != nil {
		return 0, err
	}
	result, err = tx.ExecContext(ctx, `INSERT INTO gw_rate_evidence_review_events(rate_evidence_id,review_seq,decision,reviewer_user_id,reason_code,created_at) VALUES (?,1,'submitted',?,'submitted',?)`, evidenceID, actorID, now)
	if err != nil {
		return 0, err
	}
	eventID, err := lastID(result)
	if err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO gw_rate_evidence_review_state(rate_evidence_id,state,state_version,latest_review_event_id) VALUES (?,'submitted',1,?)`, evidenceID, eventID); err != nil {
		return 0, err
	}
	return evidenceID, recordCatalogAdminChange(ctx, tx, actorID, "unified.rate_evidence.create", "rate_evidence", evidenceID, ginSafeMetadata{"source_type": in.SourceType, "authority_level": in.AuthorityLevel, "unit_code": in.UnitCode, "currency_code": in.CurrencyCode, "currency_version": in.CurrencyVersion})
}

type RateEvidenceReviewInput struct {
	Decision        string `json:"decision"`
	ReasonCode      string `json:"reason_code"`
	ExpectedVersion uint64 `json:"expected_version"`
}

func (in *RateEvidenceReviewInput) Normalize() {
	in.Decision = strings.ToLower(strings.TrimSpace(in.Decision))
	in.ReasonCode = strings.ToLower(strings.TrimSpace(in.ReasonCode))
}

func (s *Store) ReviewRateEvidence(ctx context.Context, tx *sql.Tx, evidenceID uint64, in RateEvidenceReviewInput, actorID uint64) error {
	if tx == nil || evidenceID == 0 || actorID == 0 {
		return ErrInvalidInput
	}
	in.Normalize()
	if in.ExpectedVersion == 0 || !catalogIdentityPattern.MatchString(in.ReasonCode) || (in.Decision != "accepted" && in.Decision != "rejected" && in.Decision != "superseded") {
		return ErrInvalidInput
	}
	var state string
	var version uint64
	if err := tx.QueryRowContext(ctx, `SELECT state,state_version FROM gw_rate_evidence_review_state WHERE rate_evidence_id=? FOR UPDATE`, evidenceID).Scan(&state, &version); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if version != in.ExpectedVersion || !validEvidenceTransition(state, in.Decision) {
		return ErrConflict
	}
	next := version + 1
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_rate_evidence_review_events(rate_evidence_id,review_seq,decision,reviewer_user_id,reason_code,created_at) VALUES (?,?,?,?,?,?)`, evidenceID, next, in.Decision, actorID, in.ReasonCode, nowUTC())
	if err != nil {
		return err
	}
	eventID, err := lastID(result)
	if err != nil {
		return err
	}
	updated, err := tx.ExecContext(ctx, `UPDATE gw_rate_evidence_review_state SET state=?,state_version=?,latest_review_event_id=? WHERE rate_evidence_id=? AND state_version=?`, in.Decision, next, eventID, evidenceID, version)
	if err != nil {
		return err
	}
	ok, err := affected(updated)
	if err != nil {
		return err
	}
	if !ok {
		return ErrConflict
	}
	return recordCatalogAdminChange(ctx, tx, actorID, "unified.rate_evidence.review", "rate_evidence", evidenceID, in)
}

func validEvidenceTransition(from, to string) bool {
	return from == "submitted" && (to == "accepted" || to == "rejected") || from == "accepted" && to == "superseded"
}

type CatalogRateInput struct {
	ExpectedVersion uint64                 `json:"expected_version"`
	EvidenceID      uint64                 `json:"evidence_id"`
	ComponentCode   string                 `json:"component_code"`
	QuantitySource  billing.QuantitySource `json:"quantity_source"`
	ChargeEvent     billing.ChargeEvent    `json:"charge_event"`
	UnitScale       int32                  `json:"unit_scale"`
	QuantityStep    string                 `json:"quantity_step"`
	MaxQuantity     string                 `json:"max_quantity"`
	// PricingMode empty means flat, so every caller written before expression
	// pricing existed keeps working unchanged. An expression rate prices through
	// PricingExpr instead of the evidence unit price; the unit price is still
	// stored, because it is what the evidence attests to, but it is not read
	// while charging.
	PricingMode string `json:"pricing_mode"`
	PricingExpr string `json:"pricing_expr"`
}

func (in *CatalogRateInput) Normalize() {
	in.ComponentCode = strings.ToLower(strings.TrimSpace(in.ComponentCode))
	in.QuantitySource = billing.QuantitySource(strings.ToLower(strings.TrimSpace(string(in.QuantitySource))))
	in.ChargeEvent = billing.ChargeEvent(strings.ToLower(strings.TrimSpace(string(in.ChargeEvent))))
	in.PricingMode = strings.ToLower(strings.TrimSpace(in.PricingMode))
	if in.PricingMode == "" {
		in.PricingMode = billing.PricingModeFlat
	}
	in.PricingExpr = strings.TrimSpace(in.PricingExpr)
}

func (s *Store) CreateCatalogRate(ctx context.Context, tx *sql.Tx, kind string, releaseID, parentID uint64, in CatalogRateInput, actorID uint64) (uint64, error) {
	if tx == nil || releaseID == 0 || parentID == 0 || actorID == 0 || (kind != "sell" && kind != "cost") {
		return 0, ErrInvalidInput
	}
	in.Normalize()
	if in.ExpectedVersion == 0 || in.EvidenceID == 0 {
		return 0, ErrInvalidInput
	}
	lock, err := lockCatalogDraft(ctx, tx, releaseID, in.ExpectedVersion)
	if err != nil {
		return 0, err
	}
	rateID, err := createCatalogRateRow(ctx, tx, kind, releaseID, parentID, in)
	if err != nil {
		return 0, err
	}
	return rateID, advanceCatalogDraft(ctx, tx, releaseID, lock, "unified.catalog."+kind+"_rate.create", rateID, actorID, ginSafeMetadata{"parent_id": parentID, "evidence_id": in.EvidenceID, "component_code": in.ComponentCode})
}

func createCatalogRateRow(ctx context.Context, tx *sql.Tx, kind string, releaseID, parentID uint64, in CatalogRateInput) (uint64, error) {
	if tx == nil || releaseID == 0 || parentID == 0 || in.EvidenceID == 0 || (kind != "sell" && kind != "cost") {
		return 0, ErrInvalidInput
	}
	evidence, eventID, err := acceptedRateEvidence(ctx, tx, in.EvidenceID)
	if err != nil {
		return 0, err
	}
	if err := validateRateParent(ctx, tx, kind, releaseID, parentID); err != nil {
		return 0, err
	}
	component := billing.RateComponent{Code: in.ComponentCode, Unit: evidence.UnitCode, Source: in.QuantitySource, Event: in.ChargeEvent, UnitPrice: evidence.UnitPrice, UnitScale: in.UnitScale, QuantityStep: in.QuantityStep, MaxQuantity: in.MaxQuantity, PricingMode: in.PricingMode}
	var expression, bound any
	if in.PricingMode == billing.PricingModeExpression {
		parsed, proven, err := proveNewExpressionRate(ctx, tx, kind, releaseID, parentID, in.PricingExpr)
		if err != nil {
			return 0, err
		}
		// The bound is stored now rather than left NULL: the pairing CHECK refuses a
		// NULL, and storing the proven value keeps the row reservable the moment it
		// is published. Publication re-proves it, so a manifest that widens a
		// parameter domain later still cannot leave an under-reserving row behind.
		component.Expr, component.MaxPrice = parsed, proven.Fixed(18)
		expression, bound = in.PricingExpr, component.MaxPrice
	}
	if err := component.Validate(); err != nil {
		return 0, ErrInvalidInput
	}
	table, parent := "gw_sell_rates", "sku_id"
	if kind == "cost" {
		table, parent = "gw_cost_rates", "cost_plan_id"
	}
	query := `INSERT INTO ` + table + `(release_id,` + parent + `,rate_evidence_review_event_id,unit_code,unit_price,currency_code,currency_version,component_code,quantity_source,charge_event,unit_scale,quantity_step,max_quantity,pricing_mode,pricing_expr,max_price,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	result, err := tx.ExecContext(ctx, query, releaseID, parentID, eventID, evidence.UnitCode, evidence.UnitPrice, evidence.CurrencyCode, evidence.CurrencyVersion, component.Code, component.Source, component.Event, component.UnitScale, component.QuantityStep, component.MaxQuantity, in.PricingMode, expression, bound, nowUTC())
	if err != nil {
		return 0, err
	}
	rateID, err := lastID(result)
	if err != nil {
		return 0, err
	}
	return rateID, nil
}

type acceptedEvidence struct {
	UnitCode, UnitPrice, CurrencyCode string
	CurrencyVersion                   uint32
}

func acceptedRateEvidence(ctx context.Context, tx *sql.Tx, evidenceID uint64) (acceptedEvidence, uint64, error) {
	var out acceptedEvidence
	var eventID uint64
	var state, decision string
	err := tx.QueryRowContext(ctx, `SELECT e.unit_code,e.unit_price,e.currency_code,e.currency_version,s.state,s.latest_review_event_id,v.decision FROM gw_rate_evidence e JOIN gw_rate_evidence_review_state s ON s.rate_evidence_id=e.id JOIN gw_rate_evidence_review_events v ON v.id=s.latest_review_event_id WHERE e.id=? FOR SHARE`, evidenceID).Scan(&out.UnitCode, &out.UnitPrice, &out.CurrencyCode, &out.CurrencyVersion, &state, &eventID, &decision)
	if err == sql.ErrNoRows {
		return acceptedEvidence{}, 0, ErrNotFound
	}
	if err != nil {
		return acceptedEvidence{}, 0, err
	}
	if state != "accepted" || decision != "accepted" {
		return acceptedEvidence{}, 0, ErrConflict
	}
	return out, eventID, nil
}

func validateRateParent(ctx context.Context, tx *sql.Tx, kind string, releaseID, parentID uint64) error {
	table := "gw_skus"
	if kind == "cost" {
		table = "gw_cost_plans"
	}
	var count uint64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE id=? AND release_id=?`, parentID, releaseID).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteCatalogRate(ctx context.Context, tx *sql.Tx, kind string, releaseID, rateID, expectedVersion, actorID uint64) error {
	if tx == nil || releaseID == 0 || rateID == 0 || expectedVersion == 0 || actorID == 0 || (kind != "sell" && kind != "cost") {
		return ErrInvalidInput
	}
	lock, err := lockCatalogDraft(ctx, tx, releaseID, expectedVersion)
	if err != nil {
		return err
	}
	table := "gw_sell_rates"
	if kind == "cost" {
		table = "gw_cost_rates"
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE id=? AND release_id=?`, rateID, releaseID)
	if err != nil {
		return err
	}
	ok, err := affected(result)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	return advanceCatalogDraft(ctx, tx, releaseID, lock, "unified.catalog."+kind+"_rate.delete", rateID, actorID, ginSafeMetadata{"rate_id": rateID})
}

package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/go-sql-driver/mysql"
	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/execution"
)

var (
	costEvidenceSource = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,23}$`)
	costComponent      = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
)

type UpstreamCostEvidenceInput struct {
	ChannelID                         uint64
	SourceType, ExternalKey, FactHMAC string
}

func (s *Store) UpsertUpstreamCostEvidence(ctx context.Context, tx *sql.Tx, in UpstreamCostEvidenceInput) (uint64, error) {
	if s == nil || tx == nil || in.ChannelID == 0 || !costEvidenceSource.MatchString(in.SourceType) ||
		in.ExternalKey == "" || strings.TrimSpace(in.ExternalKey) != in.ExternalKey || len(in.ExternalKey) > 255 ||
		!validHexDigest(in.FactHMAC, 32) {
		return 0, ErrInvalidInput
	}
	var channelID uint64
	if err := tx.QueryRowContext(ctx, s.forShare(`SELECT id FROM gateway_channels WHERE id=?`), in.ChannelID).Scan(&channelID); err == sql.ErrNoRows {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	}
	id, factHMAC, err := s.readUpstreamCostEvidence(ctx, tx, in)
	if err == nil {
		if !strings.EqualFold(factHMAC, in.FactHMAC) {
			return 0, ErrConflict
		}
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_upstream_cost_evidence(channel_id,source_type,external_key,fact_hmac,created_at) VALUES (?,?,?,?,?)`, in.ChannelID, in.SourceType, in.ExternalKey, strings.ToLower(in.FactHMAC), nowUTC())
	if err != nil {
		if isMySQLDuplicateKey(err) {
			id, factHMAC, readErr := s.readUpstreamCostEvidence(ctx, tx, in)
			if readErr != nil {
				return 0, fmt.Errorf("read concurrently inserted upstream cost evidence: %w", readErr)
			}
			if !strings.EqualFold(factHMAC, in.FactHMAC) {
				return 0, ErrConflict
			}
			return id, nil
		}
		return 0, fmt.Errorf("insert upstream cost evidence: %w", err)
	}
	return lastID(result)
}

func (s *Store) readUpstreamCostEvidence(ctx context.Context, tx *sql.Tx, in UpstreamCostEvidenceInput) (uint64, string, error) {
	var id uint64
	var factHMAC string
	err := tx.QueryRowContext(ctx, s.forUpdate(`SELECT id,fact_hmac FROM gw_upstream_cost_evidence WHERE channel_id=? AND source_type=? AND external_key=?`), in.ChannelID, in.SourceType, in.ExternalKey).Scan(&id, &factHMAC)
	return id, factHMAC, err
}

type UpstreamCostEventInput struct {
	AttemptID                                  uint64
	ComponentCode                              string
	EventSeq                                   uint64
	Direction                                  string
	Amount, Quantity                           string
	CurrencyCode                               string
	CurrencyVersion                            uint32
	SourceType, ReconciliationState            string
	RequestLogID, ResultDeliveryID, EvidenceID *uint64
}

type upstreamCostBinding struct {
	channelID       uint64
	attemptState    execution.AttemptState
	currencyCode    string
	currencyVersion uint32
}

func (s *Store) InsertUpstreamCostEvent(ctx context.Context, tx *sql.Tx, in UpstreamCostEventInput) (uint64, bool, error) {
	amount, quantity, err := validateUpstreamCostEventInput(in)
	if s == nil || tx == nil || err != nil {
		return 0, false, ErrInvalidInput
	}
	binding, err := s.lockUpstreamCostBinding(ctx, tx, in.AttemptID, in.ComponentCode)
	if err != nil {
		return 0, false, err
	}
	if binding.currencyCode != in.CurrencyCode || binding.currencyVersion != in.CurrencyVersion {
		return 0, false, ErrConflict
	}
	if err := s.validateUpstreamCostParent(ctx, tx, binding, in); err != nil {
		return 0, false, err
	}

	id, existing, err := s.readUpstreamCostEvent(ctx, tx, in)
	if err == nil {
		if !existing.matches(in, amount, quantity) {
			return 0, false, ErrConflict
		}
		return id, true, nil
	}
	if err != sql.ErrNoRows {
		return 0, false, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_upstream_cost_events(attempt_id,component_code,event_seq,direction,amount,quantity,currency_code,currency_version,source_type,reconciliation_state,request_log_id,result_delivery_id,evidence_id,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, in.AttemptID, in.ComponentCode, in.EventSeq, in.Direction, amount.String(), quantity.String(), in.CurrencyCode, in.CurrencyVersion, in.SourceType, in.ReconciliationState, nullableID(in.RequestLogID), nullableID(in.ResultDeliveryID), nullableID(in.EvidenceID), nowUTC())
	if err != nil {
		if isMySQLDuplicateKey(err) {
			id, existing, readErr := s.readUpstreamCostEvent(ctx, tx, in)
			if readErr != nil {
				return 0, false, fmt.Errorf("read concurrently inserted upstream cost event: %w", readErr)
			}
			if !existing.matches(in, amount, quantity) {
				return 0, false, ErrConflict
			}
			return id, true, nil
		}
		return 0, false, fmt.Errorf("insert upstream cost event: %w", err)
	}
	id, err = lastID(result)
	return id, false, err
}

func (s *Store) readUpstreamCostEvent(ctx context.Context, tx *sql.Tx, in UpstreamCostEventInput) (uint64, upstreamCostEventRecord, error) {
	var id uint64
	var existing upstreamCostEventRecord
	err := tx.QueryRowContext(ctx, s.forUpdate(`SELECT id,direction,amount,quantity,currency_code,currency_version,source_type,reconciliation_state,request_log_id,result_delivery_id,evidence_id
FROM gw_upstream_cost_events WHERE attempt_id=? AND component_code=? AND event_seq=?`), in.AttemptID, in.ComponentCode, in.EventSeq).
		Scan(&id, &existing.direction, &existing.amount, &existing.quantity, &existing.currencyCode, &existing.currencyVersion,
			&existing.sourceType, &existing.reconciliationState, &existing.requestLogID, &existing.resultDeliveryID, &existing.evidenceID)
	return id, existing, err
}

type upstreamCostEventRecord struct {
	direction, amount, quantity, currencyCode, sourceType, reconciliationState string
	currencyVersion                                                            uint32
	requestLogID, resultDeliveryID, evidenceID                                 sql.NullInt64
}

func (r upstreamCostEventRecord) matches(in UpstreamCostEventInput, amount, quantity billing.Amount) bool {
	storedAmount, amountErr := billing.ParseAmount(r.amount, 18, true)
	storedQuantity, quantityErr := billing.ParseAmount(r.quantity, 18, true)
	return amountErr == nil && quantityErr == nil && storedAmount.Cmp(amount) == 0 && storedQuantity.Cmp(quantity) == 0 &&
		r.direction == in.Direction && r.currencyCode == in.CurrencyCode && r.currencyVersion == in.CurrencyVersion &&
		r.sourceType == in.SourceType && r.reconciliationState == in.ReconciliationState &&
		nullableCostIDMatches(r.requestLogID, in.RequestLogID) && nullableCostIDMatches(r.resultDeliveryID, in.ResultDeliveryID) &&
		nullableCostIDMatches(r.evidenceID, in.EvidenceID)
}

func validateUpstreamCostEventInput(in UpstreamCostEventInput) (billing.Amount, billing.Amount, error) {
	if in.AttemptID == 0 || !costComponent.MatchString(in.ComponentCode) || in.EventSeq == 0 ||
		in.CurrencyCode == "" || strings.TrimSpace(in.CurrencyCode) != in.CurrencyCode || len(in.CurrencyCode) > 16 || in.CurrencyVersion == 0 ||
		in.Amount == "" || in.Quantity == "" {
		return billing.Amount{}, billing.Amount{}, ErrInvalidInput
	}
	if in.Direction != "increase" && in.Direction != "decrease" ||
		in.SourceType != "estimated" && in.SourceType != "reported" && in.SourceType != "manual" ||
		in.ReconciliationState != "pending" && in.ReconciliationState != "confirmed" && in.ReconciliationState != "disputed" && in.ReconciliationState != "voided" {
		return billing.Amount{}, billing.Amount{}, ErrInvalidInput
	}
	if !validOptionalCostID(in.RequestLogID) || !validOptionalCostID(in.ResultDeliveryID) || !validOptionalCostID(in.EvidenceID) {
		return billing.Amount{}, billing.Amount{}, ErrInvalidInput
	}
	parents := boolCount(in.RequestLogID != nil, in.ResultDeliveryID != nil, in.EvidenceID != nil)
	if parents != 1 || in.SourceType == "estimated" && in.EvidenceID != nil || in.SourceType != "estimated" && in.EvidenceID == nil {
		return billing.Amount{}, billing.Amount{}, ErrInvalidInput
	}
	amount, err := billing.ParseAmount(in.Amount, 18, true)
	if err != nil || !fitsCostDecimal(amount.String()) {
		return billing.Amount{}, billing.Amount{}, ErrInvalidInput
	}
	quantity, err := billing.ParseAmount(in.Quantity, 18, true)
	if err != nil || !fitsCostDecimal(quantity.String()) {
		return billing.Amount{}, billing.Amount{}, ErrInvalidInput
	}
	return amount, quantity, nil
}

func (s *Store) lockUpstreamCostBinding(ctx context.Context, tx *sql.Tx, attemptID uint64, componentCode string) (upstreamCostBinding, error) {
	var out upstreamCostBinding
	err := tx.QueryRowContext(ctx, s.forUpdate(`SELECT a.state,product.channel_id,r.currency_code,r.currency_version
FROM gw_api_call_attempts a
JOIN gw_cost_plans p ON p.release_id=a.catalog_release_id AND p.offering_id=a.offering_id AND p.id=a.cost_plan_id
JOIN gw_cost_rates r ON r.release_id=p.release_id AND r.cost_plan_id=p.id AND r.component_code=?
JOIN gw_product_transports pt ON pt.release_id=a.catalog_release_id AND pt.id=a.product_transport_id
JOIN gw_products product ON product.release_id=pt.release_id AND product.id=pt.product_id
WHERE a.id=?`), componentCode, attemptID).Scan(&out.attemptState, &out.channelID, &out.currencyCode, &out.currencyVersion)
	if err == sql.ErrNoRows {
		return upstreamCostBinding{}, ErrConflict
	}
	if err != nil {
		return upstreamCostBinding{}, err
	}
	switch out.attemptState {
	case execution.AttemptCompleted, execution.AttemptFailed, execution.AttemptCancelled, execution.AttemptNotCreated, execution.AttemptTerminatedUnknown:
		return out, nil
	default:
		return upstreamCostBinding{}, ErrConflict
	}
}

func (s *Store) validateUpstreamCostParent(ctx context.Context, tx *sql.Tx, binding upstreamCostBinding, in UpstreamCostEventInput) error {
	if in.RequestLogID != nil {
		var attemptID uint64
		if err := tx.QueryRowContext(ctx, s.forShare(`SELECT attempt_id FROM gw_channel_request_logs WHERE id=?`), *in.RequestLogID).Scan(&attemptID); err == sql.ErrNoRows {
			return ErrNotFound
		} else if err != nil {
			return err
		} else if attemptID != in.AttemptID {
			return ErrConflict
		}
	}
	if in.ResultDeliveryID != nil {
		var attemptID uint64
		if err := tx.QueryRowContext(ctx, s.forShare(`SELECT attempt_id FROM gw_result_deliveries WHERE id=?`), *in.ResultDeliveryID).Scan(&attemptID); err == sql.ErrNoRows {
			return ErrNotFound
		} else if err != nil {
			return err
		} else if attemptID != in.AttemptID {
			return ErrConflict
		}
	}
	if in.EvidenceID != nil {
		var channelID uint64
		if err := tx.QueryRowContext(ctx, s.forShare(`SELECT channel_id FROM gw_upstream_cost_evidence WHERE id=?`), *in.EvidenceID).Scan(&channelID); err == sql.ErrNoRows {
			return ErrNotFound
		} else if err != nil {
			return err
		} else if channelID != binding.channelID {
			return ErrConflict
		}
	}
	return nil
}

// RecordEstimatedUpstreamCostEvents prices every component whose event and
// quantity are known. Unknown facts remain absent instead of becoming zero.
func (s *Store) RecordEstimatedUpstreamCostEvents(ctx context.Context, tx *sql.Tx, attemptID uint64, facts billing.Facts) error {
	if s == nil || tx == nil || attemptID == 0 {
		return ErrInvalidInput
	}
	var releaseID, costPlanID uint64
	var state execution.AttemptState
	if err := tx.QueryRowContext(ctx, s.forUpdate(`SELECT catalog_release_id,cost_plan_id,state FROM gw_api_call_attempts WHERE id=?`), attemptID).Scan(&releaseID, &costPlanID, &state); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	events := make(map[billing.ChargeEvent]bool, len(facts.Events)+1)
	for event, occurred := range facts.Events {
		events[event] = occurred
	}
	delete(events, billing.ChargeSucceeded)
	delete(events, billing.ChargeFailed)
	delete(events, billing.ChargeCancelled)
	switch state {
	case execution.AttemptCompleted:
		events[billing.ChargeSucceeded] = true
	case execution.AttemptFailed, execution.AttemptNotCreated:
		events[billing.ChargeFailed] = true
	case execution.AttemptCancelled:
		events[billing.ChargeCancelled] = true
	case execution.AttemptTerminatedUnknown:
	default:
		return ErrConflict
	}
	if !anyKnownCostEvent(events) {
		return nil
	}
	facts.Events = events
	schedule, err := loadCostScheduleByPlan(ctx, tx, releaseID, costPlanID, false)
	if err != nil {
		return err
	}
	for _, component := range schedule.Components {
		occurred, known := events[component.Event]
		if !known || !occurred {
			continue
		}
		parent, err := s.trustedEstimatedCostParent(ctx, tx, attemptID, component.Event)
		if err != nil {
			return err
		}
		if parent == nil {
			continue
		}
		componentSchedule := billing.RateSchedule{Currency: schedule.Currency, Components: []billing.RateComponent{component}}
		charge, err := componentSchedule.Evaluate(facts)
		if errors.Is(err, billing.ErrMissingFact) {
			continue
		}
		if err != nil {
			return err
		}
		if len(charge.Lines) != 1 || charge.Amount.Sign() == 0 {
			continue
		}
		input := UpstreamCostEventInput{
			AttemptID: attemptID, ComponentCode: component.Code, EventSeq: 1, Direction: "increase",
			Amount: charge.Amount.String(), Quantity: charge.Lines[0].Quantity,
			CurrencyCode: schedule.Currency.Code, CurrencyVersion: schedule.Currency.Version,
			SourceType: "estimated", ReconciliationState: "pending",
		}
		if component.Event == billing.ChargeDelivered || component.Event == billing.ChargeDeliveryFailed {
			input.ResultDeliveryID = parent
		} else {
			input.RequestLogID = parent
		}
		if _, _, err := s.InsertUpstreamCostEvent(ctx, tx, input); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) trustedEstimatedCostParent(ctx context.Context, tx *sql.Tx, attemptID uint64, event billing.ChargeEvent) (*uint64, error) {
	if event == billing.ChargeDelivered || event == billing.ChargeDeliveryFailed {
		state := "ready"
		if event == billing.ChargeDeliveryFailed {
			state = "delivery_failed"
		}
		var id uint64
		err := tx.QueryRowContext(ctx, s.forShare(`SELECT id FROM gw_result_deliveries WHERE attempt_id=? AND state=? ORDER BY result_ordinal LIMIT 1`), attemptID, state).Scan(&id)
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return costID(id), err
	}
	statuses := []string{"response_recorded"}
	if event == billing.ChargeCancelled {
		statuses = []string{"response_recorded", "sent", "unknown"}
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(statuses)), ",")
	args := make([]any, 0, len(statuses)+1)
	args = append(args, attemptID)
	for _, status := range statuses {
		args = append(args, status)
	}
	var id uint64
	err := tx.QueryRowContext(ctx, s.forShare(`SELECT id FROM gw_channel_request_logs WHERE attempt_id=? AND status IN (`+placeholders+`) ORDER BY request_seq DESC LIMIT 1`), args...).Scan(&id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return costID(id), err
}

func fitsCostDecimal(value string) bool {
	parts := strings.SplitN(value, ".", 2)
	return len(parts[0]) <= 20 && (len(parts) == 1 || len(parts[1]) <= 18)
}

func validOptionalCostID(value *uint64) bool { return value == nil || *value != 0 }

func boolCount(values ...bool) int {
	count := 0
	for _, value := range values {
		if value {
			count++
		}
	}
	return count
}

func nullableCostIDMatches(actual sql.NullInt64, expected *uint64) bool {
	if expected == nil {
		return !actual.Valid
	}
	return actual.Valid && actual.Int64 > 0 && uint64(actual.Int64) == *expected
}

func anyKnownCostEvent(events map[billing.ChargeEvent]bool) bool {
	for _, occurred := range events {
		if occurred {
			return true
		}
	}
	return false
}

func costID(id uint64) *uint64 {
	if id == 0 {
		return nil
	}
	value := id
	return &value
}

func isMySQLDuplicateKey(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}

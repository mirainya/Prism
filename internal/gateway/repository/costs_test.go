package repository

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-sql-driver/mysql"
	"github.com/mirainya/Prism/internal/gateway/billing"
)

func TestUpsertUpstreamCostEvidenceHandlesConcurrentDuplicate(t *testing.T) {
	for _, test := range []struct {
		name, storedHMAC string
		wantConflict     bool
	}{
		{name: "same fact", storedHMAC: strings.Repeat("A", 64)},
		{name: "different fact", storedHMAC: strings.Repeat("b", 64), wantConflict: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			store, _ := New(db)
			mock.ExpectBegin()
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			input := UpstreamCostEvidenceInput{ChannelID: 7, SourceType: "invoice", ExternalKey: "line-12", FactHMAC: strings.Repeat("a", 64)}
			mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM gateway_channels WHERE id=? FOR SHARE")).
				WithArgs(input.ChannelID).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(input.ChannelID))
			mock.ExpectQuery(regexp.QuoteMeta("SELECT id,fact_hmac FROM gw_upstream_cost_evidence WHERE channel_id=? AND source_type=? AND external_key=? FOR UPDATE")).
				WithArgs(input.ChannelID, input.SourceType, input.ExternalKey).WillReturnError(sql.ErrNoRows)
			mock.ExpectExec(regexp.QuoteMeta("INSERT INTO gw_upstream_cost_evidence(channel_id,source_type,external_key,fact_hmac,created_at) VALUES (?,?,?,?,?)")).
				WithArgs(input.ChannelID, input.SourceType, input.ExternalKey, input.FactHMAC, sqlmock.AnyArg()).
				WillReturnError(&mysql.MySQLError{Number: 1062, Message: "duplicate"})
			mock.ExpectQuery(regexp.QuoteMeta("SELECT id,fact_hmac FROM gw_upstream_cost_evidence WHERE channel_id=? AND source_type=? AND external_key=? FOR UPDATE")).
				WithArgs(input.ChannelID, input.SourceType, input.ExternalKey).
				WillReturnRows(sqlmock.NewRows([]string{"id", "fact_hmac"}).AddRow(22, test.storedHMAC))

			id, err := store.UpsertUpstreamCostEvidence(context.Background(), tx, input)
			if test.wantConflict {
				if !errors.Is(err, ErrConflict) || id != 0 {
					t.Fatalf("id=%d error=%v", id, err)
				}
			} else if err != nil || id != 22 {
				t.Fatalf("id=%d error=%v", id, err)
			}
			mock.ExpectRollback()
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInsertUpstreamCostEventHandlesConcurrentDuplicate(t *testing.T) {
	for _, test := range []struct {
		name, storedAmount string
		wantConflict       bool
	}{
		{name: "same event", storedAmount: "1.250000000000000000"},
		{name: "different event", storedAmount: "1.260000000000000000", wantConflict: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			store, _ := New(db)
			mock.ExpectBegin()
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			requestLogID := uint64(41)
			input := UpstreamCostEventInput{
				AttemptID: 12, ComponentCode: "base", EventSeq: 1, Direction: "increase",
				Amount: "1.25", Quantity: "2", CurrencyCode: "USD", CurrencyVersion: 1,
				SourceType: "estimated", ReconciliationState: "pending", RequestLogID: &requestLogID,
			}
			mock.ExpectQuery(regexp.QuoteMeta("SELECT a.state,product.channel_id,r.currency_code,r.currency_version")).
				WithArgs(input.ComponentCode, input.AttemptID).
				WillReturnRows(sqlmock.NewRows([]string{"state", "channel_id", "currency_code", "currency_version"}).
					AddRow("completed", 7, input.CurrencyCode, input.CurrencyVersion))
			mock.ExpectQuery(regexp.QuoteMeta("SELECT attempt_id FROM gw_channel_request_logs WHERE id=? FOR SHARE")).
				WithArgs(requestLogID).WillReturnRows(sqlmock.NewRows([]string{"attempt_id"}).AddRow(input.AttemptID))
			expectCostEventRead(mock, input, sqlmock.NewRows(costEventColumns()))
			mock.ExpectExec(regexp.QuoteMeta("INSERT INTO gw_upstream_cost_events(attempt_id,component_code,event_seq,direction,amount,quantity,currency_code,currency_version,source_type,reconciliation_state,request_log_id,result_delivery_id,evidence_id,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)")).
				WithArgs(input.AttemptID, input.ComponentCode, input.EventSeq, input.Direction, input.Amount, input.Quantity,
					input.CurrencyCode, input.CurrencyVersion, input.SourceType, input.ReconciliationState, requestLogID, nil, nil, sqlmock.AnyArg()).
				WillReturnError(&mysql.MySQLError{Number: 1062, Message: "duplicate"})
			expectCostEventRead(mock, input, sqlmock.NewRows(costEventColumns()).AddRow(
				91, input.Direction, test.storedAmount, "2.000000000000000000", input.CurrencyCode, input.CurrencyVersion,
				input.SourceType, input.ReconciliationState, requestLogID, nil, nil,
			))

			id, duplicate, err := store.InsertUpstreamCostEvent(context.Background(), tx, input)
			if test.wantConflict {
				if !errors.Is(err, ErrConflict) || id != 0 || duplicate {
					t.Fatalf("id=%d duplicate=%v error=%v", id, duplicate, err)
				}
			} else if err != nil || id != 91 || !duplicate {
				t.Fatalf("id=%d duplicate=%v error=%v", id, duplicate, err)
			}
			mock.ExpectRollback()
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestValidateUpstreamCostEventInputRejectsAmbiguousOrInvalidFacts(t *testing.T) {
	requestLogID, deliveryID, evidenceID := uint64(1), uint64(2), uint64(3)
	valid := UpstreamCostEventInput{
		AttemptID: 1, ComponentCode: "generated_second", EventSeq: 1, Direction: "increase",
		Amount: "1.25", Quantity: "2", CurrencyCode: "USD", CurrencyVersion: 1,
		SourceType: "estimated", ReconciliationState: "pending", RequestLogID: &requestLogID,
	}
	tests := []struct {
		name   string
		change func(*UpstreamCostEventInput)
	}{
		{name: "missing parent", change: func(in *UpstreamCostEventInput) { in.RequestLogID = nil }},
		{name: "multiple parents", change: func(in *UpstreamCostEventInput) { in.ResultDeliveryID = &deliveryID }},
		{name: "estimated evidence", change: func(in *UpstreamCostEventInput) { in.RequestLogID, in.EvidenceID = nil, &evidenceID }},
		{name: "reported without evidence", change: func(in *UpstreamCostEventInput) { in.SourceType = "reported" }},
		{name: "negative amount", change: func(in *UpstreamCostEventInput) { in.Amount = "-0.1" }},
		{name: "exponent quantity", change: func(in *UpstreamCostEventInput) { in.Quantity = "1e3" }},
		{name: "oversized amount", change: func(in *UpstreamCostEventInput) { in.Amount = "100000000000000000000" }},
		{name: "invalid component", change: func(in *UpstreamCostEventInput) { in.ComponentCode = "Generated.Second" }},
		{name: "trimmed currency", change: func(in *UpstreamCostEventInput) { in.CurrencyCode = " USD" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := valid
			test.change(&input)
			if _, _, err := validateUpstreamCostEventInput(input); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	manual := valid
	manual.SourceType, manual.RequestLogID, manual.EvidenceID = "manual", nil, &evidenceID
	if _, _, err := validateUpstreamCostEventInput(manual); err != nil {
		t.Fatalf("valid manual event: %v", err)
	}
}

func TestRecordEstimatedUpstreamCostEventsUsesAttemptCostPlanSnapshot(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	mock.ExpectBegin()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	const attemptID, releaseID, costPlanID, requestLogID = uint64(12), uint64(3), uint64(44), uint64(41)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT a.catalog_release_id,a.cost_plan_id,a.state,EXISTS(")).
		WithArgs(attemptID).WillReturnRows(sqlmock.NewRows([]string{"catalog_release_id", "cost_plan_id", "state", "has_rates"}).
		AddRow(releaseID, costPlanID, "completed", true))
	mock.ExpectQuery("FROM gw_cost_rates r").WithArgs(releaseID, costPlanID).
		WillReturnRows(sqlmock.NewRows([]string{
			"cost_plan_id", "id", "component_code", "unit_code", "quantity_source", "charge_event", "unit_price",
			"pricing_mode", "pricing_expr", "max_price", "unit_scale",
			"quantity_step", "max_quantity", "currency_code", "currency_version", "fraction_digits", "rounding_mode", "max_amount", "valid",
		}).AddRow(costPlanID, 71, "base", "request", "one", "call.succeeded", "0.25", "flat", "", "", 0, "0", "1", "USD", 1, 2, "half_even", "1000000", true))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM gw_channel_request_logs WHERE attempt_id=? AND status IN (?) ORDER BY request_seq DESC LIMIT 1 FOR SHARE")).
		WithArgs(attemptID, "response_recorded").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(requestLogID))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT a.state,product.channel_id,r.currency_code,r.currency_version")).
		WithArgs("base", attemptID).WillReturnRows(sqlmock.NewRows([]string{"state", "channel_id", "currency_code", "currency_version"}).
		AddRow("completed", 7, "USD", 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT attempt_id FROM gw_channel_request_logs WHERE id=? FOR SHARE")).
		WithArgs(requestLogID).WillReturnRows(sqlmock.NewRows([]string{"attempt_id"}).AddRow(attemptID))
	input := UpstreamCostEventInput{AttemptID: attemptID, ComponentCode: "base", EventSeq: 1}
	expectCostEventRead(mock, input, sqlmock.NewRows(costEventColumns()))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO gw_upstream_cost_events(attempt_id,component_code,event_seq,direction,amount,quantity,currency_code,currency_version,source_type,reconciliation_state,request_log_id,result_delivery_id,evidence_id,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)")).
		WithArgs(attemptID, "base", uint64(1), "increase", "0.25", "1", "USD", uint32(1), "estimated", "pending", requestLogID, nil, nil, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(91, 1))

	err = store.RecordEstimatedUpstreamCostEvents(context.Background(), tx, attemptID, billing.Facts{})
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectRollback()
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRecordEstimatedUpstreamCostEventsSkipsUnpricedPlan(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	mock.ExpectBegin()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	const attemptID, releaseID, costPlanID = uint64(12), uint64(3), uint64(44)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT a.catalog_release_id,a.cost_plan_id,a.state,EXISTS(")).
		WithArgs(attemptID).WillReturnRows(sqlmock.NewRows([]string{"catalog_release_id", "cost_plan_id", "state", "has_rates"}).
		AddRow(releaseID, costPlanID, "completed", false))

	if err := store.RecordEstimatedUpstreamCostEvents(context.Background(), tx, attemptID, billing.Facts{}); err != nil {
		t.Fatal(err)
	}
	mock.ExpectRollback()
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func expectCostEventRead(mock sqlmock.Sqlmock, in UpstreamCostEventInput, rows *sqlmock.Rows) {
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id,direction,amount,quantity,currency_code,currency_version,source_type,reconciliation_state,request_log_id,result_delivery_id,evidence_id")).
		WithArgs(in.AttemptID, in.ComponentCode, in.EventSeq).WillReturnRows(rows)
}

func costEventColumns() []string {
	return []string{"id", "direction", "amount", "quantity", "currency_code", "currency_version", "source_type", "reconciliation_state", "request_log_id", "result_delivery_id", "evidence_id"}
}

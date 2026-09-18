package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mirainya/Prism/internal/gateway/billing"
)

func expectReservation(mock sqlmock.Sqlmock, state string) {
	mock.ExpectQuery("SELECT call_id,billing_account_id,budget_window_id FROM billing_reservations").WithArgs(uint64(9)).WillReturnRows(sqlmock.NewRows([]string{"call_id", "billing_account_id", "budget_window_id"}).AddRow(1, 2, 3))
	mock.ExpectQuery("SELECT user_id,currency_code,currency_version,posted_balance,held_amount,credit_limit,status,state_version FROM billing_accounts").WithArgs(uint64(2)).WillReturnRows(sqlmock.NewRows([]string{"user_id", "currency_code", "currency_version", "posted_balance", "held_amount", "credit_limit", "status", "state_version"}).AddRow(8, "CNY", 1, "100", "10", "0", "open", 5))
	mock.ExpectQuery("SELECT token_id,limit_amount,used_amount,held_amount,window_start,window_end FROM token_budget_windows").WithArgs(uint64(3)).WillReturnRows(sqlmock.NewRows([]string{"token_id", "limit_amount", "used_amount", "held_amount", "window_start", "window_end"}).AddRow(7, nil, "0", "10", time.Now().Add(-time.Hour), nil))
	mock.ExpectQuery("SELECT user_id,token_id FROM gw_api_calls").WithArgs(uint64(1)).WillReturnRows(sqlmock.NewRows([]string{"user_id", "token_id"}).AddRow(8, 7))
	mock.ExpectQuery("SELECT user_id FROM tokens").WithArgs(uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow(8))
	mock.ExpectQuery("SELECT call_id,billing_account_id,budget_window_id,amount,state,state_version FROM billing_reservations").WithArgs(uint64(9)).WillReturnRows(sqlmock.NewRows([]string{"call_id", "billing_account_id", "budget_window_id", "amount", "state", "state_version"}).AddRow(1, 2, 3, "10", state, 4))
}

func TestBillingAccountCreditAvailability(t *testing.T) {
	posted, _ := billing.ParseAmount("0", 18, false)
	held, _ := billing.ParseAmount("3", 18, true)
	credit, _ := billing.ParseAmount("10", 18, true)
	account := billingAccount{posted: posted, held: held, credit: credit}

	available, _ := billing.ParseAmount("7", 18, true)
	if account.available().Cmp(available) != 0 {
		t.Fatalf("available = %s, want %s", account.available().String(), available.String())
	}
	withinCredit, _ := billing.ParseAmount("-10", 18, false)
	beyondCredit, _ := billing.ParseAmount("-10.00000001", 18, false)
	if account.exceedsCredit(withinCredit) {
		t.Fatal("balance at the credit limit was treated as an overrun")
	}
	if !account.exceedsCredit(beyondCredit) {
		t.Fatal("balance beyond the credit limit was accepted")
	}
}

func TestOpenBillingAccountRepairsMissingLedger(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id FROM users").WithArgs(uint64(8)).WillReturnRows(
		sqlmock.NewRows([]string{"id"}).AddRow(8),
	)
	mock.ExpectQuery("SELECT id,currency_code,currency_version FROM billing_accounts").WithArgs(uint64(8)).WillReturnRows(
		sqlmock.NewRows([]string{"id", "currency_code", "currency_version"}).AddRow(2, "CNY", 1),
	)
	mock.ExpectExec("INSERT INTO ledger_accounts").WithArgs(uint64(2), "CNY", uint32(1), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	var accountID uint64
	err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		accountID, err = store.OpenBillingAccount(context.Background(), tx, 8)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if accountID != 2 {
		t.Fatalf("account id = %d, want 2", accountID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func expectPostingRule(mock sqlmock.Sqlmock, event string) {
	rule, _ := expectedPostingRule(event)
	var direction, system any
	if rule.direction != "" {
		direction, system = rule.direction, rule.system
	}
	mock.ExpectQuery("SELECT id,amount_source,user_direction,system_account_code,budget_effect FROM billing_posting_rules").WithArgs(event, "CNY", uint32(1)).WillReturnRows(sqlmock.NewRows([]string{"id", "amount_source", "user_direction", "system_account_code", "budget_effect"}).AddRow(12, "event_amount", direction, system, rule.budget))
}

func expectSettlementJournal(mock sqlmock.Sqlmock) {
	mock.ExpectQuery("SELECT id FROM ledger_accounts WHERE billing_account_id").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(20))
	mock.ExpectQuery("SELECT id FROM ledger_accounts WHERE system_account_code").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(10))
	for i := 0; i < 2; i++ {
		mock.ExpectQuery("SELECT fraction_digits,max_amount,status").WillReturnRows(sqlmock.NewRows([]string{"fraction_digits", "max_amount", "status"}).AddRow(8, "1000000", "active"))
	}
	mock.ExpectQuery("SELECT id,state,currency_code,currency_version FROM ledger_transactions").WillReturnError(sql.ErrNoRows)
	for _, id := range []uint64{10, 20} {
		mock.ExpectQuery("SELECT id FROM ledger_accounts WHERE id").WithArgs(id, "CNY", uint32(1)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(id))
	}
	mock.ExpectExec("INSERT INTO ledger_transactions").WillReturnResult(sqlmock.NewResult(30, 1))
	for i := 0; i < 2; i++ {
		mock.ExpectExec("INSERT INTO ledger_entries").WillReturnResult(sqlmock.NewResult(int64(31+i), 1))
		mock.ExpectExec("UPDATE ledger_accounts").WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectExec("UPDATE ledger_transactions").WillReturnResult(sqlmock.NewResult(0, 1))
}

func TestSettlementRequiresAllAccountBudgetAndStateUpdates(t *testing.T) {
	for _, failure := range []string{"", "account", "budget", "reservation", "event"} {
		t.Run("failure_"+failure, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			store, _ := New(db)
			mock.ExpectBegin()
			expectReservation(mock, "active")
			mock.ExpectQuery("SELECT fraction_digits,max_amount,status").WithArgs("CNY", uint32(1)).WillReturnRows(sqlmock.NewRows([]string{"fraction_digits", "max_amount", "status"}).AddRow(8, "1000000", "active"))
			for _, op := range []struct{ name, query string }{
				{"account", "UPDATE billing_accounts"}, {"budget", "UPDATE token_budget_windows"}, {"reservation", "UPDATE billing_reservations"},
			} {
				rows := int64(1)
				if failure == op.name {
					rows = 0
				}
				mock.ExpectExec(op.query).WillReturnResult(sqlmock.NewResult(0, rows))
				if failure == op.name {
					break
				}
			}
			if failure == "" || failure == "event" {
				expectPostingRule(mock, "reservation_settled")
				expect := mock.ExpectExec("INSERT INTO billing_events").WithArgs(uint64(2), uint64(9), uint64(1), "reservation_settled:9", "reservation_settled", "6", "CNY", uint32(1), uint64(5), uint64(12), sqlmock.AnyArg())
				if failure == "event" {
					expect.WillReturnError(errors.New("event insert failed"))
				} else {
					expect.WillReturnResult(sqlmock.NewResult(5, 1))
					expectSettlementJournal(mock)
					mock.ExpectExec("INSERT INTO billing_settlements").WithArgs(uint64(9), int64(5), "10", "6", "0", "0", "0", sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
					expectPostingRule(mock, "reservation_released")
					mock.ExpectExec("INSERT INTO billing_events").WithArgs(uint64(2), uint64(9), uint64(1), "reservation_unused_hold:9", "reservation_released", "4", "CNY", uint32(1), uint64(5), uint64(12), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(6, 1))
				}
			}
			if failure == "" {
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			err = store.WithTx(context.Background(), func(tx *sql.Tx) error { return store.SettleReservation(context.Background(), tx, 9, "6") })
			if (err == nil) != (failure == "") {
				t.Fatalf("failure=%s err=%v", failure, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSettledReplayMustMatchRecordedCharge(t *testing.T) {
	for _, actual := range []string{"6", "5"} {
		t.Run(actual, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			store, _ := New(db)
			mock.ExpectBegin()
			expectReservation(mock, "settled")
			mock.ExpectQuery("SELECT actual_amount FROM billing_settlements").WithArgs(uint64(9)).WillReturnRows(sqlmock.NewRows([]string{"actual_amount"}).AddRow("6.000000000000000000"))
			if actual == "6" {
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			err = store.WithTx(context.Background(), func(tx *sql.Tx) error { return store.SettleReservation(context.Background(), tx, 9, actual) })
			if (actual == "6" && err != nil) || (actual != "6" && !errors.Is(err, ErrConflict)) {
				t.Fatalf("actual=%s err=%v", actual, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUnknownHoldCanSettleAfterUsageRecovery(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	mock.ExpectBegin()
	expectReservation(mock, "unknown_hold")
	mock.ExpectQuery("SELECT fraction_digits,max_amount,status").WillReturnRows(sqlmock.NewRows([]string{"fraction_digits", "max_amount", "status"}).AddRow(8, "1000000", "active"))
	mock.ExpectExec("UPDATE billing_accounts").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE token_budget_windows").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE billing_reservations").WithArgs(sqlmock.AnyArg(), uint64(9), "unknown_hold", uint64(4)).WillReturnResult(sqlmock.NewResult(0, 1))
	expectPostingRule(mock, "reservation_settled")
	mock.ExpectExec("INSERT INTO billing_events").WillReturnResult(sqlmock.NewResult(5, 1))
	expectSettlementJournal(mock)
	mock.ExpectExec("INSERT INTO billing_settlements").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := store.WithTx(context.Background(), func(tx *sql.Tx) error { return store.SettleReservation(context.Background(), tx, 9, "10") }); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSettlementRejectsOverPrecisionBeforeBalanceChange(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	mock.ExpectBegin()
	expectReservation(mock, "active")
	mock.ExpectQuery("SELECT fraction_digits,max_amount,status").WillReturnRows(sqlmock.NewRows([]string{"fraction_digits", "max_amount", "status"}).AddRow(2, "1000000", "active"))
	mock.ExpectRollback()
	err = store.WithTx(context.Background(), func(tx *sql.Tx) error { return store.SettleReservation(context.Background(), tx, 9, "0.001") })
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

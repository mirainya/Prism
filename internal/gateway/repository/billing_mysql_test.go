//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	migrationfiles "github.com/mirainya/Prism/database/migrations"
	"github.com/mirainya/Prism/internal/gateway/billing"
)

// All billing tables and their constraints come from the shipped migrations.
// Only unrelated identity tables use small fixtures in this repository test.
func mysqlBillingStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("PRISM_MIGRATION_TEST_DSN")
	if dsn == "" {
		t.Skip("PRISM_MIGRATION_TEST_DSN is not set")
	}
	config, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid test DSN")
	}
	host, _, err := net.SplitHostPort(config.Addr)
	if err != nil || config.Net != "tcp" || !net.ParseIP(host).IsLoopback() {
		t.Fatal("billing tests require an isolated loopback MySQL server")
	}
	config.DBName = ""
	server, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	name := fmt.Sprintf("prism_test_billing_%d", time.Now().UnixNano())
	if _, err := server.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := server.Exec("DROP DATABASE `" + name + "`"); err != nil {
			t.Error(err)
		}
	})
	config.DBName, config.ParseTime, config.MultiStatements = name, true, true
	config.Params = map[string]string{"sql_mode": "'STRICT_ALL_TABLES'"}
	db, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, query := range []string{
		`CREATE TABLE users(id bigint unsigned PRIMARY KEY)`,
		`CREATE TABLE tokens(id bigint unsigned PRIMARY KEY,user_id bigint unsigned NOT NULL)`,
		`CREATE TABLE gw_api_calls(id bigint unsigned PRIMARY KEY,user_id bigint unsigned NOT NULL,token_id bigint unsigned NOT NULL)`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"20260905_170000_unified_gateway_billing.sql", "20260906_150000_gateway_settlement_posting.sql"} {
		if file == "20260906_150000_gateway_settlement_posting.sql" {
			if _, err := db.Exec(`CREATE TABLE billing_posting_rules (id bigint unsigned NOT NULL AUTO_INCREMENT,event_type varchar(32) NOT NULL,rule_version int unsigned NOT NULL,currency_code varchar(16) NULL,currency_version int unsigned NULL,status varchar(16) NOT NULL,created_at datetime(3) NOT NULL,PRIMARY KEY(id),UNIQUE KEY uq_rule_type_version(event_type,rule_version))`); err != nil {
				t.Fatal(err)
			}
		}
		body, err := migrationfiles.Files.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
	}
	body, err := migrationfiles.Files.ReadFile("20260906_100000_unified_gateway_audit_events.sql")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(body), "CREATE TABLE IF NOT EXISTS `billing_account_state_events`")
	if start < 0 {
		t.Fatal("billing state events migration missing")
	}
	if _, err := db.Exec(string(body)[start:]); err != nil {
		t.Fatal(err)
	}
	store, _ := New(db)
	err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
		return store.InitializeBilling(context.Background(), tx, billing.Currency{Code: "CNY", Version: 1, FractionDigits: 8, RoundingMode: "half_even", MaxAmount: "1000000"})
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func billingFixture(t *testing.T, s *Store, userID uint64, limit *string) (accountID, windowID uint64) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.db.Exec(`INSERT INTO users(id) VALUES (?)`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO tokens(id,user_id) VALUES (?,?)`, userID, userID); err != nil {
		t.Fatal(err)
	}
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		accountID, err = s.OpenBillingAccount(ctx, tx, userID)
		if err != nil {
			return err
		}
		if err := s.CreditBillingAccount(ctx, tx, accountID, "10", "fixture_funding"); err != nil {
			return err
		}
		kind := "unlimited"
		if limit != nil {
			kind = "lifetime"
		}
		policyID, err := s.CreateBudgetPolicy(ctx, tx, BudgetPolicyInput{TokenID: userID, PolicyCode: "fixture", WindowKind: kind, LimitAmount: limit, AlgorithmVersion: 1})
		if err != nil {
			return err
		}
		start := time.Now().UTC().Add(-time.Hour)
		activationID, err := s.ActivateBudgetPolicy(ctx, tx, userID, policyID, start)
		if err != nil {
			return err
		}
		windowID, err = s.CreateBudgetWindow(ctx, tx, BudgetWindowInput{TokenID: userID, PolicyID: policyID, ActivationID: activationID, StartAt: start})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return
}

func authorizeFixture(t *testing.T, s *Store, callID, userID, accountID, windowID uint64, amount string) uint64 {
	t.Helper()
	if _, err := s.db.Exec(`INSERT INTO gw_api_calls(id,user_id,token_id) VALUES (?,?,?)`, callID, userID, userID); err != nil {
		t.Fatal(err)
	}
	var id uint64
	err := s.WithTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		id, err = s.ReserveBilling(context.Background(), tx, ReservationInput{CallID: callID, TokenID: userID, BillingAccountID: accountID, BudgetWindowID: windowID, Amount: amount, Currency: "CNY", CurrencyVersion: 1})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func expectMoney(t *testing.T, s *Store, query, want string, args ...any) {
	t.Helper()
	var got string
	if err := s.db.QueryRow(query, args...).Scan(&got); err != nil {
		t.Fatal(err)
	}
	actual, e1 := billing.ParseAmount(got, 18, false)
	expected, e2 := billing.ParseAmount(want, 18, false)
	if e1 != nil || e2 != nil || actual.Cmp(expected) != 0 {
		t.Fatalf("%s: got %s want %s", query, got, want)
	}
}

func TestMySQLSettlementReceivablesAndLedger(t *testing.T) {
	s := mysqlBillingStore(t)
	ctx := context.Background()
	limit := "6"
	account, window := billingFixture(t, s, 1, &limit)
	id := authorizeFixture(t, s, 1, 1, account, window, "4")
	if err := s.WithTx(ctx, func(tx *sql.Tx) error { return s.SettleReservation(ctx, tx, id, "12") }); err != nil {
		t.Fatal(err)
	}
	expectMoney(t, s, `SELECT posted_balance FROM billing_accounts WHERE id=?`, "-2", account)
	expectMoney(t, s, `SELECT held_amount FROM billing_accounts WHERE id=?`, "0", account)
	expectMoney(t, s, `SELECT used_amount FROM token_budget_windows WHERE id=?`, "12", window)
	expectMoney(t, s, `SELECT actual_amount FROM billing_settlements WHERE reservation_id=?`, "12", id)
	expectMoney(t, s, `SELECT authorization_excess FROM billing_settlements WHERE reservation_id=?`, "8", id)
	expectMoney(t, s, `SELECT debt_after FROM billing_settlements WHERE reservation_id=?`, "2", id)
	expectMoney(t, s, `SELECT budget_excess FROM billing_settlements WHERE reservation_id=?`, "6", id)
	expectMoney(t, s, `SELECT balance FROM ledger_accounts WHERE billing_account_id=?`, "2", account)
	expectMoney(t, s, `SELECT SUM(CASE WHEN direction='debit' THEN amount ELSE -amount END) FROM ledger_entries`, "0")
	var status string
	if err := s.db.QueryRow(`SELECT status FROM billing_accounts WHERE id=?`, account).Scan(&status); err != nil || status != "frozen" {
		t.Fatalf("status=%s err=%v", status, err)
	}
	if err := s.WithTx(ctx, func(tx *sql.Tx) error { return s.SettleReservation(ctx, tx, id, "12") }); err != nil {
		t.Fatal(err)
	}
	if err := s.WithTx(ctx, func(tx *sql.Tx) error { return s.SettleReservation(ctx, tx, id, "11") }); err == nil {
		t.Fatal("different charge accepted on replay")
	}
	if _, err := s.db.Exec(`INSERT INTO gw_api_calls VALUES (2,1,1)`); err != nil {
		t.Fatal(err)
	}
	if err := s.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := s.ReserveBilling(ctx, tx, ReservationInput{CallID: 2, TokenID: 1, BillingAccountID: account, BudgetWindowID: window, Amount: "0", Currency: "CNY", CurrencyVersion: 1})
		return err
	}); err == nil {
		t.Fatal("frozen account accepted a new call")
	}
	expectMoney(t, s, `SELECT COUNT(*) FROM ledger_transactions`, "2")
}

func TestMySQLConcurrentSettlementIsExactlyOnce(t *testing.T) {
	s := mysqlBillingStore(t)
	account, window := billingFixture(t, s, 1, nil)
	id := authorizeFixture(t, s, 1, 1, account, window, "4")
	var wg sync.WaitGroup
	errors := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errors <- s.WithTx(context.Background(), func(tx *sql.Tx) error { return s.SettleReservation(context.Background(), tx, id, "3") })
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	expectMoney(t, s, `SELECT posted_balance FROM billing_accounts WHERE id=?`, "7", account)
	expectMoney(t, s, `SELECT used_amount FROM token_budget_windows WHERE id=?`, "3", window)
	expectMoney(t, s, `SELECT COUNT(*) FROM billing_settlements`, "1")
	expectMoney(t, s, `SELECT COUNT(*) FROM ledger_transactions`, "2")
	expectMoney(t, s, `SELECT COUNT(*) FROM billing_events WHERE event_type='refund'`, "0")
}

func TestMySQLPostingFailureRollsBackSettlement(t *testing.T) {
	s := mysqlBillingStore(t)
	account, window := billingFixture(t, s, 1, nil)
	id := authorizeFixture(t, s, 1, 1, account, window, "4")
	if _, err := s.db.Exec(`CREATE TRIGGER fail_test_posting BEFORE INSERT ON ledger_entries FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='injected posting failure'`); err != nil {
		t.Fatal(err)
	}
	if err := s.WithTx(context.Background(), func(tx *sql.Tx) error { return s.SettleReservation(context.Background(), tx, id, "3") }); err == nil {
		t.Fatal("posting failure was ignored")
	}
	expectMoney(t, s, `SELECT posted_balance FROM billing_accounts WHERE id=?`, "10", account)
	expectMoney(t, s, `SELECT held_amount FROM billing_accounts WHERE id=?`, "4", account)
	expectMoney(t, s, `SELECT used_amount FROM token_budget_windows WHERE id=?`, "0", window)
	expectMoney(t, s, `SELECT COUNT(*) FROM billing_settlements`, "0")
	expectMoney(t, s, `SELECT COUNT(*) FROM billing_events WHERE event_type='reservation_settled'`, "0")
}

func TestMySQLBillingFactsCannotBeMutated(t *testing.T) {
	s := mysqlBillingStore(t)
	account, window := billingFixture(t, s, 1, nil)
	id := authorizeFixture(t, s, 1, 1, account, window, "4")
	if err := s.WithTx(context.Background(), func(tx *sql.Tx) error { return s.SettleReservation(context.Background(), tx, id, "4") }); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`UPDATE billing_posting_rules SET budget_effect='none'`, `DELETE FROM billing_posting_rules`, `UPDATE billing_settlements SET actual_amount=1`, `DELETE FROM billing_settlements`} {
		if _, err := s.db.Exec(query); err == nil {
			t.Fatalf("immutable fact changed: %s", query)
		}
	}
}

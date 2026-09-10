package repository

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBillingReadinessRequiresOneCurrentWindowPerEnabledToken(t *testing.T) {
	for _, test := range []struct {
		name   string
		change string
		ready  bool
	}{
		{name: "complete", ready: true},
		{name: "missing_account", change: `DELETE FROM billing_accounts`, ready: false},
		{name: "closed_account", change: `UPDATE billing_accounts SET status='closed'`, ready: false},
		{name: "disabled_owner", change: `UPDATE users SET status=0`, ready: false},
		{name: "missing_window", change: `DELETE FROM token_budget_windows`, ready: false},
		{name: "overlapping_windows", change: `INSERT INTO token_budget_windows VALUES (2,7,3,11,'2026-09-06 00:00:00',NULL)`, ready: false},
		{name: "future_policy_does_not_replace_current", change: `INSERT INTO token_budget_policy_activations VALUES (12,7,3,2,'2099-01-01 00:00:00')`, ready: true},
		{name: "new_effective_policy_requires_window", change: `INSERT INTO token_budget_policy_activations VALUES (12,7,3,2,'2026-09-05 00:00:00')`, ready: false},
		{name: "disabled_token_needs_no_budget", change: `UPDATE tokens SET status=0; DELETE FROM billing_accounts; DELETE FROM token_budget_windows`, ready: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := cryptoFixture(t)
			statements := []string{
				`CREATE TABLE billing_currency_definitions(currency_code TEXT, definition_version INTEGER, status TEXT)`,
				`CREATE TABLE billing_system_state(id INTEGER, currency_code TEXT, currency_version INTEGER)`,
				`CREATE TABLE users(id INTEGER, status INTEGER, deleted_at TEXT)`,
				`CREATE TABLE tokens(id INTEGER, user_id INTEGER, status INTEGER, deleted_at TEXT)`,
				`CREATE TABLE billing_accounts(id INTEGER, user_id INTEGER, currency_code TEXT, currency_version INTEGER, status TEXT)`,
				`CREATE TABLE token_budget_policy_activations(id INTEGER, token_id INTEGER, policy_id INTEGER, activation_seq INTEGER, effective_at TEXT)`,
				`CREATE TABLE token_budget_windows(id INTEGER, token_id INTEGER, policy_id INTEGER, activation_id INTEGER, window_start TEXT, window_end TEXT)`,
				`INSERT INTO billing_currency_definitions VALUES ('CREDIT',1,'active')`,
				`INSERT INTO billing_system_state VALUES (1,'CREDIT',1)`,
				`INSERT INTO users VALUES (5,1,NULL)`,
				`INSERT INTO tokens VALUES (7,5,1,NULL)`,
				`INSERT INTO billing_accounts VALUES (9,5,'CREDIT',1,'open')`,
				`INSERT INTO token_budget_policy_activations VALUES (11,7,3,1,'2026-09-05 00:00:00')`,
				`INSERT INTO token_budget_windows VALUES (1,7,3,11,'2026-09-05 00:00:00',NULL)`,
			}
			for _, statement := range statements {
				if _, err := db.Exec(statement); err != nil {
					t.Fatal(err)
				}
			}
			if test.change != "" {
				if _, err := db.Exec(test.change); err != nil {
					t.Fatal(err)
				}
			}
			err := checkBillingReadinessAt(context.Background(), db.DB, time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC))
			if test.ready && err != nil || !test.ready && !errors.Is(err, ErrBillingNotReady) {
				t.Fatalf("ready=%t error=%v", test.ready, err)
			}
		})
	}
}

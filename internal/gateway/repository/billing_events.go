package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/mirainya/Prism/internal/gateway/billing"
)

type postingRule struct{ event, direction, system, budget string }

var initialPostingRules = []postingRule{
	{"reservation_created", "", "", "hold"},
	{"reservation_released", "", "", "release"},
	{"reservation_held_unknown", "", "", "none"},
	{"reservation_settled", "debit", "gateway_revenue", "consume"},
	{"budget_limit_exceeded", "", "", "none"},
	{"funding_credit", "credit", "funding_clearing", "none"},
	{"funding_debit", "debit", "funding_clearing", "none"},
}

func expectedPostingRule(event string) (postingRule, error) {
	for _, rule := range initialPostingRules {
		if rule.event == event {
			return rule, nil
		}
	}
	return postingRule{}, ErrInvalidInput
}

// The caller owns the billing ancestors. Event and journal commit together;
// holds/releases have formal rules but no posted journal.
func (s *Store) appendBillingEvent(ctx context.Context, tx *sql.Tx, r lockedReservation, key, event string, amount billing.Amount, version uint64) (uint64, error) {
	expected, err := expectedPostingRule(event)
	if err != nil {
		return 0, err
	}
	var ruleID uint64
	var source, budget string
	var direction, system sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT id,amount_source,user_direction,system_account_code,budget_effect FROM billing_posting_rules
WHERE event_type=? AND currency_code=? AND currency_version=? ORDER BY rule_version DESC LIMIT 1 FOR SHARE`, event, r.account.currency, r.account.currencyVersion).
		Scan(&ruleID, &source, &direction, &system, &budget); err != nil {
		return 0, fmt.Errorf("load %s posting rule: %w", event, err)
	}
	if source != "event_amount" || direction.String != expected.direction || system.String != expected.system || budget != expected.budget {
		return 0, ErrConflict
	}
	var reservationID, callID any
	if r.id != 0 {
		reservationID = r.id
	}
	if r.callID != 0 {
		callID = r.callID
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO billing_events(billing_account_id,reservation_id,call_id,event_key,event_type,amount,currency_code,currency_version,state_version,posting_rule_id,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		r.account.id, reservationID, callID, key, event, amount.String(), r.account.currency, r.account.currencyVersion, version, ruleID, nowUTC())
	if err != nil {
		return 0, err
	}
	id, err := lastID(res)
	if err != nil {
		return 0, err
	}
	if expected.direction == "" || amount.Sign() == 0 {
		return id, nil
	}
	var userLedger, systemLedger uint64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM ledger_accounts WHERE billing_account_id=? AND currency_code=? AND currency_version=?`, r.account.id, r.account.currency, r.account.currencyVersion).Scan(&userLedger); err != nil {
		return 0, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT id FROM ledger_accounts WHERE system_account_code=? AND currency_code=? AND currency_version=?`, expected.system, r.account.currency, r.account.currencyVersion).Scan(&systemLedger); err != nil {
		return 0, err
	}
	userDirection, systemDirection := billing.Debit, billing.Credit
	if expected.direction == "credit" {
		userDirection, systemDirection = billing.Credit, billing.Debit
	}
	_, err = s.postLedger(ctx, tx, id, r.account.currency, r.account.currencyVersion, []LedgerPostingEntry{
		{AccountID: userLedger, Code: "user", Direction: userDirection, Amount: amount.String()},
		{AccountID: systemLedger, Code: "system", Direction: systemDirection, Amount: amount.String()},
	})
	return id, err
}

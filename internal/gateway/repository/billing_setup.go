package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/mirainya/Prism/internal/gateway/billing"
)

// InitializeBilling installs one immutable platform currency and its first
// posting rules. Existing balances are never inferred or rewritten here.
func (s *Store) InitializeBilling(ctx context.Context, tx *sql.Tx, c billing.Currency) error {
	if tx == nil {
		return ErrInvalidInput
	}
	if err := c.Validate(); err != nil {
		return err
	}
	var existing billing.Currency
	err := tx.QueryRowContext(ctx, `SELECT d.currency_code,d.definition_version,d.fraction_digits,d.rounding_mode,d.max_amount
FROM billing_system_state b JOIN billing_currency_definitions d ON d.currency_code=b.currency_code AND d.definition_version=b.currency_version WHERE b.id=1 FOR UPDATE`).
		Scan(&existing.Code, &existing.Version, &existing.FractionDigits, &existing.RoundingMode, &existing.MaxAmount)
	if err == nil {
		max, _ := billing.ParseAmount(c.MaxAmount, 18, true)
		oldMax, parseErr := billing.ParseAmount(existing.MaxAmount, 18, true)
		if parseErr != nil || existing.Code != c.Code || existing.Version != c.Version || existing.FractionDigits != c.FractionDigits || existing.RoundingMode != c.RoundingMode || max.Cmp(oldMax) != 0 {
			return ErrConflict
		}
		return s.ensureBillingPostingInfrastructure(ctx, tx, c.Code, c.Version)
	}
	if err != sql.ErrNoRows {
		return err
	}
	now := nowUTC()
	if _, err := tx.ExecContext(ctx, `INSERT INTO billing_currency_definitions(currency_code,definition_version,fraction_digits,rounding_mode,max_amount,status,created_at) VALUES (?,?,?,?,?,'active',?)`, c.Code, c.Version, c.FractionDigits, c.RoundingMode, c.MaxAmount, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO billing_system_state(id,currency_code,currency_version,initialization_version,updated_at) VALUES (1,?,?,1,?)`, c.Code, c.Version, now); err != nil {
		return err
	}
	return s.ensureBillingPostingInfrastructure(ctx, tx, c.Code, c.Version)
}

func (s *Store) ensureBillingPostingInfrastructure(ctx context.Context, tx *sql.Tx, currency string, currencyVersion uint32) error {
	for _, rule := range initialPostingRules {
		var existingID uint64
		var amountSource, budget string
		var existingDirection, existingSystem sql.NullString
		err := tx.QueryRowContext(ctx, s.forShare(`SELECT id,amount_source,user_direction,system_account_code,budget_effect
FROM billing_posting_rules
WHERE event_type=? AND currency_code=? AND currency_version=? AND status='active'
ORDER BY rule_version DESC LIMIT 1`), rule.event, currency, currencyVersion).
			Scan(&existingID, &amountSource, &existingDirection, &existingSystem, &budget)
		if err == nil {
			if amountSource != "event_amount" || existingDirection.String != rule.direction || existingSystem.String != rule.system || budget != rule.budget {
				return ErrConflict
			}
			continue
		}
		if err != sql.ErrNoRows {
			return err
		}
		var nextVersion uint32
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(rule_version),0)+1 FROM billing_posting_rules WHERE event_type=?`, rule.event).Scan(&nextVersion); err != nil {
			return err
		}
		var direction, system any
		if rule.direction != "" {
			direction, system = rule.direction, rule.system
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO billing_posting_rules(event_type,rule_version,currency_code,currency_version,amount_source,user_direction,system_account_code,budget_effect,status,created_at) VALUES (?,?,?,?,'event_amount',?,?,?,'active',?)`, rule.event, nextVersion, currency, currencyVersion, direction, system, rule.budget, nowUTC()); err != nil {
			return err
		}
	}
	for _, code := range []string{"gateway_revenue", "funding_clearing"} {
		var existingID uint64
		err := tx.QueryRowContext(ctx, s.forShare(`SELECT id FROM ledger_accounts WHERE system_account_code=? AND currency_code=? AND currency_version=?`), code, currency, currencyVersion).Scan(&existingID)
		if err == nil {
			continue
		}
		if err != sql.ErrNoRows {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO ledger_accounts(system_account_code,currency_code,currency_version,balance,created_at) VALUES (?,?,?,0,?)`, code, currency, currencyVersion, nowUTC()); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) OpenBillingAccount(ctx context.Context, tx *sql.Tx, userID uint64) (uint64, error) {
	if tx == nil || userID == 0 {
		return 0, ErrInvalidInput
	}
	var owner uint64
	if err := tx.QueryRowContext(ctx, s.forUpdate(`SELECT id FROM users WHERE id=?`), userID).Scan(&owner); err != nil {
		return 0, err
	}
	var id uint64
	var currency string
	var version uint32
	if err := tx.QueryRowContext(ctx, `SELECT id,currency_code,currency_version FROM billing_accounts WHERE user_id=?`, userID).Scan(&id, &currency, &version); err == nil {
		if err := ensureBillingAccountLedger(ctx, tx, id, currency, version); err != nil {
			return 0, err
		}
		return id, nil
	} else if err != sql.ErrNoRows {
		return 0, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT currency_code,currency_version FROM billing_system_state WHERE id=1`).Scan(&currency, &version); err != nil {
		return 0, err
	}
	now := nowUTC()
	res, err := tx.ExecContext(ctx, `INSERT INTO billing_accounts(user_id,currency_code,currency_version,status,state_version,created_at,updated_at) VALUES (?,?,?,'open',1,?,?)`, userID, currency, version, now, now)
	if err != nil {
		return 0, err
	}
	id, err = lastID(res)
	if err != nil {
		return 0, err
	}
	if err := ensureBillingAccountLedger(ctx, tx, id, currency, version); err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO billing_account_state_events(billing_account_id,state_version,old_state,new_state,reason_code,created_at) VALUES (?,1,NULL,'open','account_opened',?)`, id, now)
	return id, err
}

func ensureBillingAccountLedger(ctx context.Context, tx *sql.Tx, accountID uint64, currency string, currencyVersion uint32) error {
	if tx == nil || accountID == 0 || currency == "" || currencyVersion == 0 {
		return ErrInvalidInput
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO ledger_accounts(billing_account_id,currency_code,currency_version,balance,created_at)
VALUES (?,?,?,0,?)`, accountID, currency, currencyVersion, nowUTC())
	if err == nil {
		return nil
	}
	// The account row is locked by OpenBillingAccount, so a duplicate here
	// means another call already repaired the same ledger row. Verify that
	// case without relying on a database-specific upsert syntax.
	var existingID uint64
	if lookupErr := tx.QueryRowContext(ctx, `SELECT id FROM ledger_accounts WHERE billing_account_id=? AND currency_code=? AND currency_version=?`, accountID, currency, currencyVersion).Scan(&existingID); lookupErr == nil {
		return nil
	}
	return err
}

// CreditBillingAccount is the verified funding write boundary. The source key
// is mandatory and unique per account; it is not a client-supplied free credit.
func (s *Store) CreditBillingAccount(ctx context.Context, tx *sql.Tx, accountID uint64, amount, sourceKey string) error {
	if tx == nil || accountID == 0 || sourceKey == "" || len(sourceKey) > 140 {
		return ErrInvalidInput
	}
	value, err := billing.ParseAmount(amount, 18, true)
	if err != nil || value.Sign() <= 0 {
		return ErrInvalidInput
	}
	a, err := s.lockBillingAccount(ctx, tx, accountID)
	if err != nil {
		return err
	}
	key := "funding:" + sourceKey
	var recorded string
	var ruleID sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT amount,posting_rule_id FROM billing_events WHERE billing_account_id=? AND event_key=? AND event_type='funding_credit'`, accountID, key).Scan(&recorded, &ruleID)
	if err == nil {
		previous, e := billing.ParseAmount(recorded, 18, true)
		if e != nil || !ruleID.Valid || previous.Cmp(value) != 0 {
			return ErrConflict
		}
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}
	if a.status == "closed" {
		return ErrConflict
	}
	if err := validateCurrencyAmount(ctx, tx, a.currency, a.currencyVersion, value); err != nil {
		return err
	}
	if err := requireOneRow(tx.ExecContext(ctx, `UPDATE billing_accounts SET posted_balance=posted_balance+?,state_version=state_version+1,updated_at=? WHERE id=? AND state_version=?`, value.String(), nowUTC(), accountID, a.version)); err != nil {
		return err
	}
	_, err = s.appendBillingEvent(ctx, tx, lockedReservation{account: a}, key, "funding_credit", value, a.version+1)
	return err
}

func (s *Store) SettleCallRates(ctx context.Context, tx *sql.Tx, callID, reservationID uint64, facts billing.Facts) error {
	var releaseID, skuID uint64
	if err := tx.QueryRowContext(ctx, `SELECT catalog_release_id,sku_id FROM gw_api_calls WHERE id=?`, callID).Scan(&releaseID, &skuID); err != nil {
		return err
	}
	schedule, err := LoadSettledSellSchedule(ctx, tx, releaseID, skuID)
	if err != nil {
		return err
	}
	charge, err := schedule.Evaluate(facts)
	if err != nil {
		return fmt.Errorf("calculate call charge: %w", err)
	}
	return s.SettleReservation(ctx, tx, reservationID, charge.Amount.String())
}

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/mirainya/Prism/internal/gateway/billing"
)

type ReservationInput struct {
	CallID, TokenID, BillingAccountID, BudgetWindowID uint64
	Amount, Currency                                  string
	CurrencyVersion                                   uint32
}

// ReserveBilling locks the account and budget window in a fixed order. It is
// safe to retry because the call unique key makes a second reservation fail
// without changing balances.
func (s *Store) ReserveBilling(ctx context.Context, tx *sql.Tx, in ReservationInput) (uint64, error) {
	if tx == nil || in.CallID == 0 || in.TokenID == 0 || in.BillingAccountID == 0 || in.BudgetWindowID == 0 || in.Currency == "" || in.CurrencyVersion == 0 {
		return 0, ErrInvalidInput
	}
	amount, err := billing.ParseAmount(in.Amount, 18, true)
	if err != nil || amount.Sign() < 0 {
		return 0, ErrInvalidInput
	}
	var existingID, existingAccountID, existingWindowID uint64
	var existingAmount, existingState string
	if err := tx.QueryRowContext(ctx, `SELECT id,billing_account_id,budget_window_id,amount,state FROM billing_reservations WHERE call_id=? FOR UPDATE`, in.CallID).Scan(&existingID, &existingAccountID, &existingWindowID, &existingAmount, &existingState); err == nil {
		existingValue, parseErr := billing.ParseAmount(existingAmount, 18, true)
		if parseErr != nil || existingState != "active" || existingAccountID != in.BillingAccountID || existingWindowID != in.BudgetWindowID || existingValue.Cmp(amount) != 0 {
			return 0, ErrConflict
		}
		return existingID, nil
	} else if err != sql.ErrNoRows {
		return 0, err
	}
	if err := validateCurrencyAmount(ctx, tx, in.Currency, in.CurrencyVersion, amount); err != nil {
		return 0, err
	}
	var currency string
	var currencyVersion uint32
	var posted, held, credit string
	var status string
	if err = tx.QueryRowContext(ctx, `SELECT currency_code,currency_version,posted_balance,held_amount,credit_limit,status FROM billing_accounts WHERE id=? FOR UPDATE`, in.BillingAccountID).Scan(&currency, &currencyVersion, &posted, &held, &credit, &status); err == sql.ErrNoRows {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, fmt.Errorf("lock billing account: %w", err)
	}
	if currency != in.Currency || currencyVersion != in.CurrencyVersion || status != "open" {
		return 0, ErrConflict
	}
	postedAmount, e1 := billing.ParseAmount(posted, 18, true)
	heldAmount, e2 := billing.ParseAmount(held, 18, true)
	_, e3 := billing.ParseAmount(credit, 18, true)
	if e1 != nil || e2 != nil || e3 != nil {
		return 0, fmt.Errorf("invalid account amount")
	}
	// credit_limit is a policy ceiling, not a second balance. Until a
	// corresponding liability ledger is posted, reservations may only consume
	// settled funds; this keeps the non-negative account invariant intact.
	if amount.Cmp(postedAmount.Sub(heldAmount)) > 0 {
		return 0, ErrInsufficient
	}
	var windowToken uint64
	var limit, used, windowHeld sql.NullString
	var windowStart time.Time
	var windowEnd sql.NullTime
	if err = tx.QueryRowContext(ctx, `SELECT token_id,limit_amount,used_amount,held_amount,window_start,window_end FROM token_budget_windows WHERE id=? FOR UPDATE`, in.BudgetWindowID).Scan(&windowToken, &limit, &used, &windowHeld, &windowStart, &windowEnd); err == sql.ErrNoRows {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, fmt.Errorf("lock budget window: %w", err)
	}
	if windowToken != in.TokenID {
		return 0, ErrConflict
	}
	now := nowUTC()
	if now.Before(windowStart.UTC()) || windowEnd.Valid && !now.Before(windowEnd.Time.UTC()) {
		return 0, ErrConflict
	}
	if limit.Valid {
		limitAmount, e1 := billing.ParseAmount(limit.String, 18, true)
		usedAmount, e2 := billing.ParseAmount(used.String, 18, true)
		heldBudget, e3 := billing.ParseAmount(windowHeld.String, 18, true)
		if e1 != nil || e2 != nil || e3 != nil {
			return 0, fmt.Errorf("invalid budget amount")
		}
		if amount.Cmp(limitAmount.Sub(usedAmount).Sub(heldBudget)) > 0 {
			return 0, ErrInsufficient
		}
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO billing_reservations(call_id,billing_account_id,budget_window_id,amount,state,state_version,created_at) VALUES (?,?,?,?,'active',1,?)`, in.CallID, in.BillingAccountID, in.BudgetWindowID, amount.String(), now)
	if err != nil {
		return 0, fmt.Errorf("insert billing reservation: %w", err)
	}
	id, err := lastID(result)
	if err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE billing_accounts SET held_amount=held_amount+?,state_version=state_version+1,updated_at=? WHERE id=?`, amount.String(), now, in.BillingAccountID); err != nil {
		return 0, fmt.Errorf("hold account amount: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE token_budget_windows SET held_amount=held_amount+? WHERE id=?`, amount.String(), in.BudgetWindowID); err != nil {
		return 0, fmt.Errorf("hold budget amount: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO billing_events(billing_account_id,reservation_id,call_id,event_key,event_type,amount,currency_code,currency_version,state_version,created_at) VALUES (?,?,?,?,?,?,?,?,?,?)`, in.BillingAccountID, id, in.CallID, fmt.Sprintf("reservation:%d", id), "reservation_created", amount.String(), in.Currency, in.CurrencyVersion, 1, now); err != nil {
		return 0, fmt.Errorf("billing event: %w", err)
	}
	return id, nil
}

func validateCurrencyAmount(ctx context.Context, tx rowQuerier, currency string, version uint32, amount billing.Amount) error {
	var fraction uint8
	var maxAmount string
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT fraction_digits,max_amount,status FROM billing_currency_definitions WHERE currency_code=? AND definition_version=? FOR SHARE`, currency, version).Scan(&fraction, &maxAmount, &status); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if status != "active" || amount.Scale() > int32(fraction) {
		return ErrConflict
	}
	max, err := billing.ParseAmount(maxAmount, 18, true)
	if err != nil || amount.Cmp(max) > 0 {
		return ErrInsufficient
	}
	return nil
}

func (s *Store) ResolveReservation(ctx context.Context, tx *sql.Tx, reservationID uint64, target string, eventType string) error {
	if tx == nil || reservationID == 0 || (target != "settled" && target != "released" && target != "unknown_hold") {
		return ErrInvalidInput
	}
	if eventType == "" {
		eventType = "reservation_" + target
	}
	if eventType != "reservation_settled" && eventType != "reservation_released" && eventType != "reservation_held_unknown" {
		return ErrInvalidInput
	}
	var callID, accountID, windowID uint64
	var amount, state, currency string
	var currencyVersion uint32
	var version uint64
	err := tx.QueryRowContext(ctx, `SELECT r.call_id,r.billing_account_id,r.budget_window_id,r.amount,r.state,r.state_version,a.currency_code,a.currency_version FROM billing_reservations r JOIN billing_accounts a ON a.id=r.billing_account_id WHERE r.id=? FOR UPDATE`, reservationID).Scan(&callID, &accountID, &windowID, &amount, &state, &version, &currency, &currencyVersion)
	if err == sql.ErrNoRows {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock reservation: %w", err)
	}
	if state == target {
		return nil
	}
	if state != "active" && !(state == "unknown_hold" && target == "settled") {
		return ErrConflict
	}
	value, err := billing.ParseAmount(amount, 18, true)
	if err != nil {
		return err
	}
	now := nowUTC()
	if target == "settled" {
		result, execErr := tx.ExecContext(ctx, `UPDATE billing_accounts SET held_amount=held_amount-?,posted_balance=posted_balance-?,state_version=state_version+1,updated_at=? WHERE id=? AND held_amount>=?`, value.String(), value.String(), now, accountID, value.String())
		if execErr != nil {
			return execErr
		}
		ok, execErr := affected(result)
		if execErr != nil {
			return execErr
		}
		if !ok {
			return ErrConflict
		}
		result, execErr = tx.ExecContext(ctx, `UPDATE token_budget_windows SET held_amount=held_amount-?,used_amount=used_amount+? WHERE id=? AND held_amount>=?`, value.String(), value.String(), windowID, value.String())
		if execErr != nil {
			return execErr
		}
		ok, execErr = affected(result)
		if execErr != nil {
			return execErr
		}
		if !ok {
			return ErrConflict
		}
	} else if target == "released" {
		result, execErr := tx.ExecContext(ctx, `UPDATE billing_accounts SET held_amount=held_amount-?,state_version=state_version+1,updated_at=? WHERE id=? AND held_amount>=?`, value.String(), now, accountID, value.String())
		if execErr != nil {
			return execErr
		}
		ok, execErr := affected(result)
		if execErr != nil {
			return execErr
		}
		if !ok {
			return ErrConflict
		}
		result, execErr = tx.ExecContext(ctx, `UPDATE token_budget_windows SET held_amount=held_amount-? WHERE id=? AND held_amount>=?`, value.String(), windowID, value.String())
		if execErr != nil {
			return execErr
		}
		ok, execErr = affected(result)
		if execErr != nil {
			return execErr
		}
		if !ok {
			return ErrConflict
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE billing_reservations SET state=?,state_version=state_version+1,resolved_at=? WHERE id=? AND state=? AND state_version=?`, target, now, reservationID, state, version)
	if err != nil {
		return err
	}
	ok, err := affected(result)
	if err != nil {
		return err
	}
	if !ok {
		return ErrConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO billing_events(billing_account_id,reservation_id,call_id,event_key,event_type,amount,currency_code,currency_version,state_version,created_at) VALUES (?,?,?,?,?,?,?,?,?,?)`, accountID, reservationID, callID, fmt.Sprintf("%s:%d", eventType, reservationID), eventType, value.String(), currency, currencyVersion, version+1, now)
	return err
}

// SettleReservation settles only the measured amount and releases the
// unused hold in the same transaction. Actual usage may not exceed the
// original authorization; callers must create a new authorization first.
func (s *Store) SettleReservation(ctx context.Context, tx *sql.Tx, reservationID uint64, actual string) error {
	if tx == nil || reservationID == 0 {
		return ErrInvalidInput
	}
	actualAmount, err := billing.ParseAmount(actual, 18, true)
	if err != nil || actualAmount.Sign() < 0 {
		return ErrInvalidInput
	}
	var callID, accountID, windowID uint64
	var reserved, state, currency string
	var version uint64
	var currencyVersion uint32
	if err := tx.QueryRowContext(ctx, `SELECT r.call_id,r.billing_account_id,r.budget_window_id,r.amount,r.state,r.state_version,a.currency_code,a.currency_version FROM billing_reservations r JOIN billing_accounts a ON a.id=r.billing_account_id WHERE r.id=? FOR UPDATE`, reservationID).Scan(&callID, &accountID, &windowID, &reserved, &state, &version, &currency, &currencyVersion); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if state != "active" {
		if state == "settled" {
			return nil
		}
		return ErrConflict
	}
	reservedAmount, err := billing.ParseAmount(reserved, 18, true)
	if err != nil || actualAmount.Cmp(reservedAmount) > 0 {
		return ErrConflict
	}
	now := nowUTC()
	if _, err = tx.ExecContext(ctx, `UPDATE billing_accounts SET held_amount=held_amount-?,posted_balance=posted_balance-?,state_version=state_version+1,updated_at=? WHERE id=? AND held_amount>=? AND posted_balance>=?`, reservedAmount.String(), actualAmount.String(), now, accountID, reservedAmount.String(), actualAmount.String()); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE token_budget_windows SET held_amount=held_amount-?,used_amount=used_amount+? WHERE id=? AND held_amount>=?`, reservedAmount.String(), actualAmount.String(), windowID, reservedAmount.String()); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE billing_reservations SET state='settled',state_version=state_version+1,resolved_at=? WHERE id=? AND state='active' AND state_version=?`, now, reservationID, version); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO billing_events(billing_account_id,reservation_id,call_id,event_key,event_type,amount,currency_code,currency_version,state_version,created_at) VALUES (?,?,?,?,?,?,?,?,?,?)`, accountID, reservationID, callID, fmt.Sprintf("reservation_settled:%d", reservationID), "reservation_settled", actualAmount.String(), currency, currencyVersion, version+1, now); err != nil {
		return err
	}
	if refund := reservedAmount.Sub(actualAmount); refund.Sign() > 0 {
		if _, err = tx.ExecContext(ctx, `INSERT INTO billing_events(billing_account_id,reservation_id,call_id,event_key,event_type,amount,currency_code,currency_version,state_version,created_at) VALUES (?,?,?,?,?,?,?,?,?,?)`, accountID, reservationID, callID, fmt.Sprintf("reservation_refund:%d", reservationID), "refund", refund.String(), currency, currencyVersion, version+2, now); err != nil {
			return err
		}
	}
	return nil
}

package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/mirainya/Prism/internal/gateway/billing"
)

type ReservationInput struct {
	CallID, TokenID, BillingAccountID, BudgetWindowID uint64
	Amount, Currency                                  string
	CurrencyVersion                                   uint32
}

func (s *Store) ReserveBilling(ctx context.Context, tx *sql.Tx, in ReservationInput) (uint64, error) {
	if tx == nil || in.CallID == 0 || in.TokenID == 0 || in.BillingAccountID == 0 || in.BudgetWindowID == 0 || in.Currency == "" || in.CurrencyVersion == 0 {
		return 0, ErrInvalidInput
	}
	amount, err := billing.ParseAmount(in.Amount, 18, true)
	if err != nil {
		return 0, err
	}
	if err := validateCurrencyAmount(ctx, tx, in.Currency, in.CurrencyVersion, amount); err != nil {
		return 0, err
	}
	account, err := s.lockBillingAccount(ctx, tx, in.BillingAccountID)
	if err != nil {
		return 0, err
	}
	window, err := s.lockBillingWindow(ctx, tx, in.BudgetWindowID)
	if err != nil {
		return 0, err
	}
	if account.currency != in.Currency || account.currencyVersion != in.CurrencyVersion || window.tokenID != in.TokenID {
		return 0, ErrConflict
	}
	if err := lockBillingCall(ctx, tx, in.CallID, account.userID, in.TokenID); err != nil {
		return 0, err
	}
	var id, existingAccount, existingWindow uint64
	var existingAmount, state string
	err = tx.QueryRowContext(ctx, `SELECT id,billing_account_id,budget_window_id,amount,state FROM billing_reservations WHERE call_id=? FOR UPDATE`, in.CallID).
		Scan(&id, &existingAccount, &existingWindow, &existingAmount, &state)
	if err == nil {
		previous, err := billing.ParseAmount(existingAmount, 18, true)
		if err != nil || state != "active" || existingAccount != account.id || existingWindow != window.id || previous.Cmp(amount) != 0 {
			return 0, ErrConflict
		}
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	if account.status != "open" {
		return 0, ErrConflict
	}
	if amount.Cmp(account.posted.Sub(account.held)) > 0 {
		return 0, ErrInsufficient
	}
	now := nowUTC()
	if now.Before(window.start.UTC()) || window.end.Valid && !now.Before(window.end.Time.UTC()) {
		return 0, ErrConflict
	}
	if window.limit != nil && amount.Cmp(window.limit.Sub(window.used).Sub(window.held)) > 0 {
		return 0, ErrInsufficient
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO billing_reservations(call_id,billing_account_id,budget_window_id,amount,state,state_version,created_at) VALUES (?,?,?,?,'active',1,?)`, in.CallID, account.id, window.id, amount.String(), now)
	if err != nil {
		return 0, err
	}
	id, err = lastID(res)
	if err != nil {
		return 0, err
	}
	if err := requireOneRow(tx.ExecContext(ctx, `UPDATE billing_accounts SET held_amount=held_amount+?,state_version=state_version+1,updated_at=? WHERE id=?`, amount.String(), now, account.id)); err != nil {
		return 0, err
	}
	if err := requireOneRow(tx.ExecContext(ctx, `UPDATE token_budget_windows SET held_amount=held_amount+? WHERE id=?`, amount.String(), window.id)); err != nil {
		return 0, err
	}
	_, err = s.appendBillingEvent(ctx, tx, lockedReservation{id: id, callID: in.CallID, account: account, window: window}, fmt.Sprintf("reservation:%d", id), "reservation_created", amount, 1)
	return id, err
}

func validateCurrencyAmount(ctx context.Context, tx rowQuerier, currency string, version uint32, amount billing.Amount) error {
	var fraction uint8
	var maximum, status string
	if err := tx.QueryRowContext(ctx, `SELECT fraction_digits,max_amount,status FROM billing_currency_definitions WHERE currency_code=? AND definition_version=? FOR SHARE`, currency, version).Scan(&fraction, &maximum, &status); err != nil {
		return err
	}
	// SQL DECIMAL pads trailing zeroes; validate the value, not its formatting.
	value, err := billing.ParseAmount(amount.String(), 18, true)
	if err != nil || status != "active" || value.Scale() > int32(fraction) {
		return ErrConflict
	}
	max, err := billing.ParseAmount(maximum, 18, true)
	if err != nil {
		return err
	}
	return value.EnsureRange(max)
}

func (s *Store) ResolveReservation(ctx context.Context, tx *sql.Tx, id uint64, target, event string) error {
	if tx == nil || id == 0 {
		return ErrInvalidInput
	}
	expected := ""
	switch target {
	case "released":
		expected = "reservation_released"
	case "unknown_hold":
		expected = "reservation_held_unknown"
	default:
		return ErrInvalidInput
	}
	if event != "" && event != expected {
		return ErrInvalidInput
	}
	r, err := s.lockReservation(ctx, tx, id)
	if err != nil {
		return err
	}
	if r.state == target {
		return nil
	}
	if r.state != "active" && !(r.state == "unknown_hold" && target == "released") {
		return ErrConflict
	}
	now := nowUTC()
	if target == "released" {
		if err := requireOneRow(tx.ExecContext(ctx, `UPDATE billing_accounts SET held_amount=held_amount-?,state_version=state_version+1,updated_at=? WHERE id=? AND held_amount>=?`, r.amount.String(), now, r.account.id, r.amount.String())); err != nil {
			return err
		}
		if err := requireOneRow(tx.ExecContext(ctx, `UPDATE token_budget_windows SET held_amount=held_amount-? WHERE id=? AND held_amount>=?`, r.amount.String(), r.window.id, r.amount.String())); err != nil {
			return err
		}
	}
	var resolved any
	if target == "released" {
		resolved = now
	}
	if err := requireOneRow(tx.ExecContext(ctx, `UPDATE billing_reservations SET state=?,state_version=state_version+1,resolved_at=? WHERE id=? AND state=? AND state_version=?`, target, resolved, id, r.state, r.version)); err != nil {
		return err
	}
	_, err = s.appendBillingEvent(ctx, tx, r, fmt.Sprintf("%s:%d", expected, id), expected, r.amount, r.version+1)
	return err
}

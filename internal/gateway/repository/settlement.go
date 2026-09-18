package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/mirainya/Prism/internal/gateway/billing"
)

// SettleReservation records measured receivables in full. Authorization is a
// hold, not a cap on earned charges. Overruns freeze new calls and are audited.
func (s *Store) SettleReservation(ctx context.Context, tx *sql.Tx, id uint64, actual string) error {
	if tx == nil || id == 0 {
		return ErrInvalidInput
	}
	amount, err := billing.ParseAmount(actual, 18, true)
	if err != nil {
		return err
	}
	r, err := s.lockReservation(ctx, tx, id)
	if err != nil {
		return err
	}
	if r.state == "settled" {
		var recorded string
		if err := tx.QueryRowContext(ctx, `SELECT actual_amount FROM billing_settlements WHERE reservation_id=?`, id).Scan(&recorded); err != nil {
			return err
		}
		previous, err := billing.ParseAmount(recorded, 18, true)
		if err != nil || previous.Cmp(amount) != 0 {
			return ErrConflict
		}
		return nil
	}
	if r.state != "active" && r.state != "unknown_hold" {
		return ErrConflict
	}
	if err := validateCurrencyAmount(ctx, tx, r.account.currency, r.account.currencyVersion, amount); err != nil {
		return err
	}
	if r.account.held.Cmp(r.amount) < 0 || r.window.held.Cmp(r.amount) < 0 {
		return ErrConflict
	}
	posted := r.account.posted.Sub(amount)
	excess := positivePart(amount.Sub(r.amount))
	debt := positivePart(billing.Zero().Sub(posted))
	budgetExcess := billing.Zero()
	if r.window.limit != nil {
		budgetExcess = positivePart(r.window.used.Add(amount).Add(r.window.held).Sub(r.amount).Sub(*r.window.limit))
	}
	status := r.account.status
	if r.account.exceedsCredit(posted) || excess.Sign() > 0 || budgetExcess.Sign() > 0 {
		status = "frozen"
	}
	now := nowUTC()
	if err := requireOneRow(tx.ExecContext(ctx, `UPDATE billing_accounts SET held_amount=held_amount-?,posted_balance=?,status=?,state_version=state_version+1,updated_at=? WHERE id=? AND state_version=?`, r.amount.String(), posted.String(), status, now, r.account.id, r.account.version)); err != nil {
		return err
	}
	if status != r.account.status {
		if _, err := tx.ExecContext(ctx, `INSERT INTO billing_account_state_events(billing_account_id,state_version,old_state,new_state,reason_code,created_at) VALUES (?,?,?,?,?,?)`, r.account.id, r.account.version+1, r.account.status, status, "settlement_overrun", now); err != nil {
			return err
		}
	}
	if err := requireOneRow(tx.ExecContext(ctx, `UPDATE token_budget_windows SET held_amount=held_amount-?,used_amount=used_amount+? WHERE id=? AND held_amount>=?`, r.amount.String(), amount.String(), r.window.id, r.amount.String())); err != nil {
		return err
	}
	if err := requireOneRow(tx.ExecContext(ctx, `UPDATE billing_reservations SET state='settled',state_version=state_version+1,resolved_at=? WHERE id=? AND state=? AND state_version=?`, now, id, r.state, r.version)); err != nil {
		return err
	}
	eventID, err := s.appendBillingEvent(ctx, tx, r, fmt.Sprintf("reservation_settled:%d", id), "reservation_settled", amount, r.version+1)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO billing_settlements(reservation_id,billing_event_id,authorized_amount,actual_amount,authorization_excess,debt_after,budget_excess,created_at) VALUES (?,?,?,?,?,?,?,?)`, id, eventID, r.amount.String(), amount.String(), excess.String(), debt.String(), budgetExcess.String(), now); err != nil {
		return err
	}
	if unused := r.amount.Sub(amount); unused.Sign() > 0 {
		if _, err := s.appendBillingEvent(ctx, tx, r, fmt.Sprintf("reservation_unused_hold:%d", id), "reservation_released", unused, r.version+1); err != nil {
			return err
		}
	}
	if budgetExcess.Sign() > 0 {
		if _, err := s.appendBillingEvent(ctx, tx, r, fmt.Sprintf("budget_limit_exceeded:%d", id), "budget_limit_exceeded", budgetExcess, r.version+1); err != nil {
			return err
		}
	}
	return nil
}

func positivePart(amount billing.Amount) billing.Amount {
	if amount.Sign() < 0 {
		return billing.Zero()
	}
	return amount
}

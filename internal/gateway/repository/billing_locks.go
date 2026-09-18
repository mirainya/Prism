package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/mirainya/Prism/internal/gateway/billing"
)

type billingAccount struct {
	id, userID, version  uint64
	currency             string
	currencyVersion      uint32
	posted, held, credit billing.Amount
	status               string
}

func (a billingAccount) available() billing.Amount {
	return a.posted.Add(a.credit).Sub(a.held)
}

func (a billingAccount) exceedsCredit(posted billing.Amount) bool {
	return posted.Add(a.credit).Sign() < 0
}

type billingWindow struct {
	id, tokenID uint64
	limit       *billing.Amount
	used, held  billing.Amount
	start       time.Time
	end         sql.NullTime
}

type lockedReservation struct {
	id, callID, version uint64
	amount              billing.Amount
	state               string
	account             billingAccount
	window              billingWindow
}

func (s *Store) lockBillingAccount(ctx context.Context, tx *sql.Tx, id uint64) (billingAccount, error) {
	a := billingAccount{id: id}
	var posted, held, credit string
	if err := tx.QueryRowContext(ctx, s.forUpdate(`SELECT user_id,currency_code,currency_version,posted_balance,held_amount,credit_limit,status,state_version FROM billing_accounts WHERE id=?`), id).
		Scan(&a.userID, &a.currency, &a.currencyVersion, &posted, &held, &credit, &a.status, &a.version); err != nil {
		return a, err
	}
	var err error
	a.posted, err = billing.ParseAmount(posted, 18, false)
	if err != nil {
		return a, err
	}
	a.held, err = billing.ParseAmount(held, 18, true)
	if err != nil {
		return a, err
	}
	a.credit, err = billing.ParseAmount(credit, 18, true)
	return a, err
}

func (s *Store) lockBillingWindow(ctx context.Context, tx *sql.Tx, id uint64) (billingWindow, error) {
	w := billingWindow{id: id}
	var limit sql.NullString
	var used, held string
	if err := tx.QueryRowContext(ctx, s.forUpdate(`SELECT token_id,limit_amount,used_amount,held_amount,window_start,window_end FROM token_budget_windows WHERE id=?`), id).
		Scan(&w.tokenID, &limit, &used, &held, &w.start, &w.end); err != nil {
		return w, err
	}
	var err error
	w.used, err = billing.ParseAmount(used, 18, true)
	if err != nil {
		return w, err
	}
	w.held, err = billing.ParseAmount(held, 18, true)
	if err != nil {
		return w, err
	}
	if limit.Valid {
		value, err := billing.ParseAmount(limit.String, 18, true)
		if err != nil {
			return w, err
		}
		w.limit = &value
	}
	return w, nil
}

// Ancestor identities are immutable. Read them without locking, then lock
// account -> budget -> call -> reservation and recheck all associations.
func (s *Store) lockReservation(ctx context.Context, tx *sql.Tx, id uint64) (lockedReservation, error) {
	r := lockedReservation{id: id}
	var accountID, windowID uint64
	if err := tx.QueryRowContext(ctx, `SELECT call_id,billing_account_id,budget_window_id FROM billing_reservations WHERE id=?`, id).
		Scan(&r.callID, &accountID, &windowID); err != nil {
		return r, err
	}
	var err error
	r.account, err = s.lockBillingAccount(ctx, tx, accountID)
	if err != nil {
		return r, err
	}
	r.window, err = s.lockBillingWindow(ctx, tx, windowID)
	if err != nil {
		return r, err
	}
	if err := lockBillingCall(ctx, tx, r.callID, r.account.userID, r.window.tokenID); err != nil {
		return r, err
	}
	var callID, lockedAccount, lockedWindow uint64
	var amount string
	if err := tx.QueryRowContext(ctx, `SELECT call_id,billing_account_id,budget_window_id,amount,state,state_version FROM billing_reservations WHERE id=? FOR UPDATE`, id).
		Scan(&callID, &lockedAccount, &lockedWindow, &amount, &r.state, &r.version); err != nil {
		return r, err
	}
	if callID != r.callID || lockedAccount != accountID || lockedWindow != windowID {
		return r, ErrConflict
	}
	r.amount, err = billing.ParseAmount(amount, 18, true)
	return r, err
}

func lockBillingCall(ctx context.Context, tx *sql.Tx, id, userID, tokenID uint64) error {
	var owner, token uint64
	if err := tx.QueryRowContext(ctx, `SELECT user_id,token_id FROM gw_api_calls WHERE id=? FOR UPDATE`, id).Scan(&owner, &token); err != nil {
		return err
	}
	if owner != userID || token != tokenID {
		return ErrConflict
	}
	var tokenOwner uint64
	if err := tx.QueryRowContext(ctx, `SELECT user_id FROM tokens WHERE id=?`, tokenID).Scan(&tokenOwner); err != nil {
		return err
	}
	if tokenOwner != owner {
		return ErrConflict
	}
	return nil
}

// LockCallBillingAncestors is used before a runtime transaction locks Call,
// Attempt or AsyncExecution. Zero means the call has no authorization yet.
func (s *Store) LockCallBillingAncestors(ctx context.Context, tx *sql.Tx, callID uint64) (uint64, error) {
	if tx == nil || callID == 0 {
		return 0, ErrInvalidInput
	}
	var id uint64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM billing_reservations WHERE call_id=?`, callID).Scan(&id); err == sql.ErrNoRows {
		return 0, nil
	} else if err != nil {
		return 0, err
	}
	_, err := s.lockReservation(ctx, tx, id)
	return id, err
}

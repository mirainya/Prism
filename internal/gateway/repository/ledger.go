package repository

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"github.com/mirainya/Prism/internal/gateway/billing"
)

type LedgerPostingEntry struct {
	AccountID uint64
	Code      string
	Direction billing.Direction
	Amount    string
}

// PostLedger writes one balanced double-entry transaction. Account rows are
// locked in ascending ID order, which is the same order every caller must use.
func (s *Store) PostLedger(ctx context.Context, tx *sql.Tx, billingEventID uint64, currency string, currencyVersion uint32, entries []LedgerPostingEntry) (uint64, error) {
	if tx == nil || billingEventID == 0 || currency == "" || currencyVersion == 0 || len(entries) < 2 {
		return 0, ErrInvalidInput
	}
	journal := make([]billing.LedgerEntry, len(entries))
	amounts := make([]billing.Amount, len(entries))
	ids := make([]uint64, len(entries))
	for i, entry := range entries {
		if entry.AccountID == 0 {
			return 0, ErrInvalidInput
		}
		if entry.Direction != billing.Debit && entry.Direction != billing.Credit {
			return 0, ErrInvalidInput
		}
		value, err := billing.ParseAmount(entry.Amount, 18, true)
		if err != nil {
			return 0, ErrInvalidInput
		}
		amounts[i] = value
		if err := validateCurrencyAmount(ctx, tx, currency, currencyVersion, value); err != nil {
			return 0, err
		}
		ids[i] = entry.AccountID
		journal[i] = billing.LedgerEntry{Code: entry.Code, Direction: entry.Direction, Amount: value}
	}
	if err := billing.ValidateBalanced(journal); err != nil {
		return 0, err
	}
	var existingID uint64
	var existingState, existingCurrency string
	var existingVersion uint32
	if err := tx.QueryRowContext(ctx, `SELECT id,state,currency_code,currency_version FROM ledger_transactions WHERE billing_event_id=? FOR UPDATE`, billingEventID).Scan(&existingID, &existingState, &existingCurrency, &existingVersion); err == nil {
		if existingCurrency != currency || existingVersion != currencyVersion {
			return 0, ErrConflict
		}
		if existingState == "posted" {
			return existingID, nil
		}
		return 0, ErrConflict
	} else if err != sql.ErrNoRows {
		return 0, err
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		var locked uint64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM ledger_accounts WHERE id=? AND currency_code=? AND currency_version=? FOR UPDATE`, id, currency, currencyVersion).Scan(&locked); err == sql.ErrNoRows {
			return 0, ErrNotFound
		} else if err != nil {
			return 0, err
		}
	}
	now := nowUTC()
	result, err := tx.ExecContext(ctx, `INSERT INTO ledger_transactions(billing_event_id,currency_code,currency_version,state,created_at) VALUES (?,?,?,'assembling',?)`, billingEventID, currency, currencyVersion, now)
	if err != nil {
		return 0, fmt.Errorf("insert ledger transaction: %w", err)
	}
	transactionID, err := lastID(result)
	if err != nil {
		return 0, err
	}
	for i, entry := range entries {
		if _, err = tx.ExecContext(ctx, `INSERT INTO ledger_entries(ledger_transaction_id,ledger_account_id,currency_code,currency_version,entry_code,direction,amount,created_at) VALUES (?,?,?,?,?,?,?,?)`, transactionID, entry.AccountID, currency, currencyVersion, entry.Code, string(entry.Direction), amounts[i].String(), now); err != nil {
			return 0, fmt.Errorf("insert ledger entry: %w", err)
		}
		delta := amounts[i].String()
		operator := "+"
		if entry.Direction == billing.Credit {
			operator = "-"
		}
		query := `UPDATE ledger_accounts SET balance=balance+? WHERE id=? AND currency_code=? AND currency_version=?`
		if operator == "-" {
			query = `UPDATE ledger_accounts SET balance=balance-? WHERE id=? AND currency_code=? AND currency_version=?`
		}
		if _, err = tx.ExecContext(ctx, query, delta, entry.AccountID, currency, currencyVersion); err != nil {
			return 0, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE ledger_transactions SET state='posted',posted_at=? WHERE id=? AND state='assembling'`, now, transactionID); err != nil {
		return 0, err
	}
	return transactionID, nil
}

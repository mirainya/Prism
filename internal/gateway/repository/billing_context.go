package repository

import (
	"context"
	"database/sql"
)

// ResolveCurrentBillingContext locks the one open account and the one budget
// window attached to the latest effective policy activation for a token.
func (s *Store) ResolveCurrentBillingContext(ctx context.Context, tx *sql.Tx, userID, tokenID uint64, currency string, currencyVersion uint32) (uint64, uint64, error) {
	if tx == nil || userID == 0 || tokenID == 0 || currency == "" || currencyVersion == 0 {
		return 0, 0, ErrInvalidInput
	}
	var accountID uint64
	if err := tx.QueryRowContext(ctx, s.forUpdate(`SELECT id FROM billing_accounts
WHERE user_id=? AND currency_code=? AND currency_version=? AND status='open'
`), userID, currency, currencyVersion).Scan(&accountID); err == sql.ErrNoRows {
		return 0, 0, ErrNotFound
	} else if err != nil {
		return 0, 0, err
	}

	var activationID uint64
	if err := tx.QueryRowContext(ctx, s.forShare(`SELECT a.id
FROM token_budget_policy_activations a
JOIN tokens t ON t.id=a.token_id
WHERE a.token_id=? AND t.user_id=? AND t.status=1 AND t.deleted_at IS NULL
  AND a.effective_at<=CURRENT_TIMESTAMP(3)
ORDER BY a.activation_seq DESC
LIMIT 1`), tokenID, userID).Scan(&activationID); err == sql.ErrNoRows {
		return 0, 0, ErrNotFound
	} else if err != nil {
		return 0, 0, err
	}

	rows, err := tx.QueryContext(ctx, s.forUpdate(`SELECT id FROM token_budget_windows
WHERE token_id=? AND activation_id=?
  AND window_start<=CURRENT_TIMESTAMP(3)
  AND (window_end IS NULL OR window_end>CURRENT_TIMESTAMP(3))
ORDER BY window_start DESC
LIMIT 2`), tokenID, activationID)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()
	var windows []uint64
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			return 0, 0, err
		}
		windows = append(windows, id)
	}
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	if len(windows) == 0 {
		return 0, 0, ErrNotFound
	}
	if len(windows) != 1 {
		return 0, 0, ErrConflict
	}
	return accountID, windows[0], nil
}

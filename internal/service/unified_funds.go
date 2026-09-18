package service

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/model"
	"github.com/shopspring/decimal"
)

type fundsSnapshot struct {
	Available decimal.Decimal
	Used      decimal.Decimal
}

func unifiedFundsStore() (*repository.Store, error) {
	db, err := model.DB().DB()
	if err != nil {
		return nil, fmt.Errorf("open unified funds database: %w", err)
	}
	return repository.New(db)
}

func loadUserFunds(ctx context.Context, userIDs []uint) (map[uint]fundsSnapshot, error) {
	result := make(map[uint]fundsSnapshot, len(userIDs))
	if len(userIDs) == 0 {
		return result, nil
	}
	db, err := model.DB().DB()
	if err != nil {
		return nil, err
	}
	args := make([]any, len(userIDs))
	for i, id := range userIDs {
		args[i] = id
	}
	query := `SELECT a.user_id,a.posted_balance,a.credit_limit,a.held_amount,
COALESCE(SUM(CASE WHEN e.event_type='reservation_settled' THEN e.amount ELSE 0 END),0)
FROM billing_accounts a
LEFT JOIN billing_events e ON e.billing_account_id=a.id
WHERE a.user_id IN (` + placeholders(len(userIDs)) + `)
GROUP BY a.user_id,a.posted_balance,a.credit_limit,a.held_amount`
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var userID uint
		var posted, credit, held, used string
		if err := rows.Scan(&userID, &posted, &credit, &held, &used); err != nil {
			return nil, err
		}
		postedAmount, err := parseStoredAmount(posted)
		if err != nil {
			return nil, err
		}
		creditAmount, err := parseStoredAmount(credit)
		if err != nil {
			return nil, err
		}
		heldAmount, err := parseStoredAmount(held)
		if err != nil {
			return nil, err
		}
		usedAmount, err := parseStoredAmount(used)
		if err != nil {
			return nil, err
		}
		result[userID] = fundsSnapshot{
			Available: postedAmount.Add(creditAmount).Sub(heldAmount),
			Used:      usedAmount,
		}
	}
	return result, rows.Err()
}

func loadTokenFunds(ctx context.Context, tokenIDs []uint) (map[uint]fundsSnapshot, error) {
	result := make(map[uint]fundsSnapshot, len(tokenIDs))
	if len(tokenIDs) == 0 {
		return result, nil
	}
	db, err := model.DB().DB()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	args := make([]any, 0, len(tokenIDs)+4)
	for _, id := range tokenIDs {
		args = append(args, id)
	}
	args = append(args, now, now, now, now)
	query := `SELECT w.token_id,w.limit_amount,w.used_amount,w.held_amount
FROM token_budget_windows w
JOIN token_budget_policy_activations a ON a.id=w.activation_id AND a.token_id=w.token_id
WHERE w.token_id IN (` + placeholders(len(tokenIDs)) + `)
  AND a.effective_at<=?
  AND w.window_start<=?
  AND (w.window_end IS NULL OR w.window_end>?)
  AND NOT EXISTS (
    SELECT 1 FROM token_budget_policy_activations newer
    WHERE newer.token_id=a.token_id AND newer.activation_seq>a.activation_seq AND newer.effective_at<=?
  )`
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var tokenID uint
		var limit sql.NullString
		var used, held string
		if err := rows.Scan(&tokenID, &limit, &used, &held); err != nil {
			return nil, err
		}
		if _, exists := result[tokenID]; exists {
			return nil, repository.ErrConflict
		}
		usedAmount, err := parseStoredAmount(used)
		if err != nil {
			return nil, err
		}
		heldAmount, err := parseStoredAmount(held)
		if err != nil {
			return nil, err
		}
		available := decimal.Zero
		if limit.Valid {
			limitAmount, err := parseStoredAmount(limit.String)
			if err != nil {
				return nil, err
			}
			available = limitAmount.Sub(usedAmount).Sub(heldAmount)
		}
		result[tokenID] = fundsSnapshot{Available: available, Used: usedAmount}
	}
	return result, rows.Err()
}

func currentTokenBudgetWindow(ctx context.Context, tx *sql.Tx, userID, tokenID uint64) (uint64, error) {
	now := time.Now().UTC()
	rows, err := tx.QueryContext(ctx, `SELECT w.id
FROM token_budget_windows w
JOIN token_budget_policy_activations a ON a.id=w.activation_id AND a.token_id=w.token_id
JOIN tokens t ON t.id=w.token_id
WHERE t.id=? AND t.user_id=? AND t.deleted_at IS NULL AND t.status=1 AND t.revoked_at IS NULL
  AND a.effective_at<=? AND w.window_start<=? AND (w.window_end IS NULL OR w.window_end>?)
  AND NOT EXISTS (
    SELECT 1 FROM token_budget_policy_activations newer
    WHERE newer.token_id=a.token_id AND newer.activation_seq>a.activation_seq AND newer.effective_at<=?
  )
ORDER BY w.window_start DESC
LIMIT 2`, tokenID, userID, now, now, now, now)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var ids []uint64
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, repository.ErrNotFound
	}
	if len(ids) != 1 {
		return 0, repository.ErrConflict
	}
	return ids[0], nil
}

func readBudgetWindowFunds(ctx context.Context, tx *sql.Tx, windowID uint64) (fundsSnapshot, error) {
	var limit sql.NullString
	var used, held string
	if err := tx.QueryRowContext(ctx, `SELECT limit_amount,used_amount,held_amount FROM token_budget_windows WHERE id=?`, windowID).
		Scan(&limit, &used, &held); err != nil {
		return fundsSnapshot{}, err
	}
	usedAmount, err := parseStoredAmount(used)
	if err != nil {
		return fundsSnapshot{}, err
	}
	heldAmount, err := parseStoredAmount(held)
	if err != nil {
		return fundsSnapshot{}, err
	}
	available := decimal.Zero
	if limit.Valid {
		limitAmount, err := parseStoredAmount(limit.String)
		if err != nil {
			return fundsSnapshot{}, err
		}
		available = limitAmount.Sub(usedAmount).Sub(heldAmount)
	}
	return fundsSnapshot{Available: available, Used: usedAmount}, nil
}

func parseStoredAmount(value string) (decimal.Decimal, error) {
	amount, err := decimal.NewFromString(value)
	if err != nil {
		return decimal.Zero, fmt.Errorf("parse stored funds amount %q: %w", value, err)
	}
	return amount, nil
}

func placeholders(count int) string {
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
}

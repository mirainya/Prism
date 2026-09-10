package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrBillingNotReady = errors.New("gateway repository: billing is not ready")

// CheckBillingReadiness verifies that every enabled token can resolve exactly
// one current account and budget window before an instance accepts traffic.
func CheckBillingReadiness(ctx context.Context, db *sql.DB) error {
	return checkBillingReadinessAt(ctx, db, time.Now().UTC().Truncate(time.Millisecond))
}

func checkBillingReadinessAt(ctx context.Context, db *sql.DB, now time.Time) error {
	if db == nil {
		return ErrBillingNotReady
	}
	var initialized uint64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*)
FROM billing_system_state s
JOIN billing_currency_definitions c
  ON c.currency_code=s.currency_code AND c.definition_version=s.currency_version
WHERE s.id=1 AND c.status='active'`).Scan(&initialized); err != nil || initialized != 1 {
		return ErrBillingNotReady
	}

	var invalidAccounts uint64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*)
FROM tokens t
LEFT JOIN users u ON u.id=t.user_id
LEFT JOIN billing_accounts a ON a.user_id=t.user_id
LEFT JOIN billing_system_state s ON s.id=1
WHERE t.status=1 AND t.deleted_at IS NULL
  AND (u.id IS NULL OR u.deleted_at IS NOT NULL OR u.status<>1
    OR a.id IS NULL OR a.status='closed'
    OR a.currency_code<>s.currency_code OR a.currency_version<>s.currency_version)`).Scan(&invalidAccounts); err != nil || invalidAccounts != 0 {
		return ErrBillingNotReady
	}

	var invalidBudgets uint64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*)
FROM tokens t
JOIN users u ON u.id=t.user_id AND u.deleted_at IS NULL AND u.status=1
WHERE t.status=1 AND t.deleted_at IS NULL
  AND (SELECT COUNT(*)
       FROM token_budget_policy_activations a
       JOIN token_budget_windows w
         ON w.token_id=a.token_id AND w.policy_id=a.policy_id AND w.activation_id=a.id
       WHERE a.token_id=t.id AND a.effective_at<=?
         AND NOT EXISTS (
           SELECT 1 FROM token_budget_policy_activations newer
           WHERE newer.token_id=a.token_id AND newer.effective_at<=?
             AND newer.activation_seq>a.activation_seq)
         AND w.window_start<=?
         AND (w.window_end IS NULL OR w.window_end>?))<>1`, now, now, now, now).Scan(&invalidBudgets); err != nil || invalidBudgets != 0 {
		return ErrBillingNotReady
	}
	return nil
}

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/mirainya/Prism/internal/gateway/billing"
)

type BudgetPolicyInput struct {
	TokenID                              uint64
	PolicyCode, WindowKind, TimezoneName string
	LimitAmount                          *string
	PeriodSeconds                        *uint64
	AlgorithmVersion                     uint32
}

func (s *Store) CreateBudgetPolicy(ctx context.Context, tx *sql.Tx, in BudgetPolicyInput) (uint64, error) {
	if tx == nil || in.TokenID == 0 || in.PolicyCode == "" || len(in.PolicyCode) > 128 || in.AlgorithmVersion == 0 {
		return 0, ErrInvalidInput
	}
	if in.WindowKind != "periodic" && in.WindowKind != "lifetime" && in.WindowKind != "unlimited" {
		return 0, ErrInvalidInput
	}
	if in.WindowKind == "unlimited" && (in.LimitAmount != nil || in.PeriodSeconds != nil) {
		return 0, ErrInvalidInput
	}
	if in.WindowKind != "unlimited" && in.LimitAmount == nil {
		return 0, ErrInvalidInput
	}
	if in.WindowKind == "periodic" && (in.PeriodSeconds == nil || *in.PeriodSeconds == 0) {
		return 0, ErrInvalidInput
	}
	if in.WindowKind != "periodic" && in.PeriodSeconds != nil {
		return 0, ErrInvalidInput
	}
	var limit any
	if in.LimitAmount != nil {
		amount, err := billing.ParseAmount(*in.LimitAmount, 18, true)
		if err != nil {
			return 0, ErrInvalidInput
		}
		limit = amount.String()
	}
	if in.TimezoneName == "" {
		in.TimezoneName = "UTC"
	}
	if len(in.TimezoneName) > 64 {
		return 0, ErrInvalidInput
	}
	if _, err := time.LoadLocation(in.TimezoneName); err != nil {
		return 0, ErrInvalidInput
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO token_budget_policies(token_id,policy_code,window_kind,limit_amount,period_seconds,timezone_name,algorithm_version,created_at) VALUES (?,?,?,?,?,?,?,?)`, in.TokenID, in.PolicyCode, in.WindowKind, limit, nullableUint64(in.PeriodSeconds), in.TimezoneName, in.AlgorithmVersion, nowUTC())
	if err != nil {
		return 0, fmt.Errorf("create budget policy: %w", err)
	}
	return lastID(result)
}

func (s *Store) ActivateBudgetPolicy(ctx context.Context, tx *sql.Tx, tokenID, policyID uint64, effectiveAt time.Time) (uint64, error) {
	if tx == nil || tokenID == 0 || policyID == 0 || effectiveAt.IsZero() {
		return 0, ErrInvalidInput
	}
	var lockedPolicy uint64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM token_budget_policies WHERE id=? AND token_id=? FOR SHARE`, policyID, tokenID).Scan(&lockedPolicy); err == sql.ErrNoRows {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	}
	var predecessor sql.NullInt64
	var seq uint64
	var previousAt time.Time
	err := tx.QueryRowContext(ctx, `SELECT id,activation_seq,effective_at FROM token_budget_policy_activations WHERE token_id=? ORDER BY activation_seq DESC LIMIT 1 FOR UPDATE`, tokenID).Scan(&predecessor, &seq, &previousAt)
	if err != nil && err != sql.ErrNoRows {
		return 0, err
	}
	if err == sql.ErrNoRows {
		seq = 0
	}
	if !previousAt.IsZero() && !effectiveAt.UTC().After(previousAt.UTC()) {
		return 0, ErrConflict
	}
	var pred any
	if predecessor.Valid {
		pred = predecessor.Int64
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO token_budget_policy_activations(token_id,policy_id,activation_seq,predecessor_id,effective_at,created_at) VALUES (?,?,?,?,?,?)`, tokenID, policyID, seq+1, pred, effectiveAt.UTC(), nowUTC())
	if err != nil {
		return 0, fmt.Errorf("activate budget policy: %w", err)
	}
	return lastID(result)
}

type BudgetWindowInput struct {
	TokenID, PolicyID, ActivationID uint64
	StartAt                         time.Time
	EndAt                           *time.Time
}

func (s *Store) CreateBudgetWindow(ctx context.Context, tx *sql.Tx, in BudgetWindowInput) (uint64, error) {
	if tx == nil || in.TokenID == 0 || in.PolicyID == 0 || in.ActivationID == 0 || in.StartAt.IsZero() {
		return 0, ErrInvalidInput
	}
	var policyLimit sql.NullString
	var kind string
	var activationPolicyID uint64
	var effectiveAt time.Time
	if err := tx.QueryRowContext(ctx, `SELECT p.window_kind,p.limit_amount,a.policy_id,a.effective_at FROM token_budget_policies p JOIN token_budget_policy_activations a ON a.token_id=p.token_id AND a.policy_id=p.id WHERE p.id=? AND p.token_id=? AND a.id=? FOR SHARE`, in.PolicyID, in.TokenID, in.ActivationID).Scan(&kind, &policyLimit, &activationPolicyID, &effectiveAt); err == sql.ErrNoRows {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	}
	if activationPolicyID != in.PolicyID || in.StartAt.UTC().Before(effectiveAt.UTC()) {
		return 0, ErrConflict
	}
	if in.EndAt != nil && !in.EndAt.After(in.StartAt) {
		return 0, ErrInvalidInput
	}
	var limit any
	if policyLimit.Valid {
		if _, err := billing.ParseAmount(policyLimit.String, 18, true); err != nil {
			return 0, ErrConflict
		}
		limit = policyLimit.String
	} else if kind != "unlimited" {
		return 0, ErrConflict
	}
	var end any
	if in.EndAt != nil {
		end = in.EndAt.UTC()
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO token_budget_windows(token_id,policy_id,activation_id,window_start,window_end,limit_amount,created_at) VALUES (?,?,?,?,?,?,?)`, in.TokenID, in.PolicyID, in.ActivationID, in.StartAt.UTC(), end, limit, nowUTC())
	if err != nil {
		return 0, fmt.Errorf("create budget window: %w", err)
	}
	return lastID(result)
}

type BudgetAdjustmentInput struct {
	WindowID                           uint64
	AmountDelta, SourceType, SourceKey string
}

func (s *Store) ApplyBudgetAdjustment(ctx context.Context, tx *sql.Tx, in BudgetAdjustmentInput) (uint64, bool, error) {
	if tx == nil || in.WindowID == 0 || in.AmountDelta == "" || in.SourceType == "" || len(in.SourceType) > 32 || in.SourceKey == "" || len(in.SourceKey) > 160 {
		return 0, false, ErrInvalidInput
	}
	delta, err := billing.ParseAmount(in.AmountDelta, 18, false)
	if err != nil {
		return 0, false, ErrInvalidInput
	}
	var existing, existingWindow uint64
	if err := tx.QueryRowContext(ctx, `SELECT id,budget_window_id FROM token_budget_adjustment_events WHERE source_type=? AND source_key=? FOR UPDATE`, in.SourceType, in.SourceKey).Scan(&existing, &existingWindow); err == nil {
		if existingWindow != in.WindowID {
			return 0, false, ErrConflict
		}
		return existing, true, nil
	} else if err != sql.ErrNoRows {
		return 0, false, err
	}
	var limit, used, held sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT limit_amount,used_amount,held_amount FROM token_budget_windows WHERE id=? FOR UPDATE`, in.WindowID).Scan(&limit, &used, &held); err == sql.ErrNoRows {
		return 0, false, ErrNotFound
	} else if err != nil {
		return 0, false, err
	}
	if !limit.Valid {
		return 0, false, ErrConflict
	}
	current, e1 := billing.ParseAmount(limit.String, 18, true)
	usedAmount, e2 := billing.ParseAmount(used.String, 18, true)
	heldAmount, e3 := billing.ParseAmount(held.String, 18, true)
	if e1 != nil || e2 != nil || e3 != nil {
		return 0, false, ErrConflict
	}
	next := current.Add(delta)
	if next.Sign() < 0 || next.Cmp(usedAmount.Add(heldAmount)) < 0 {
		return 0, false, ErrInsufficient
	}
	var seq uint64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(adjustment_seq),0)+1 FROM token_budget_adjustment_events WHERE budget_window_id=?`, in.WindowID).Scan(&seq); err != nil {
		return 0, false, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO token_budget_adjustment_events(budget_window_id,adjustment_seq,amount_delta,source_type,source_key,created_at) VALUES (?,?,?,?,?,?)`, in.WindowID, seq, delta.String(), in.SourceType, in.SourceKey, nowUTC())
	if err != nil {
		return 0, false, fmt.Errorf("insert budget adjustment: %w", err)
	}
	id, err := lastID(result)
	if err != nil {
		return 0, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE token_budget_windows SET limit_amount=? WHERE id=?`, next.String(), in.WindowID); err != nil {
		return 0, false, err
	}
	return id, false, nil
}

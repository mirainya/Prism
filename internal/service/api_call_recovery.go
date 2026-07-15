package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"github.com/shopspring/decimal"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	staleCallPendingCode      = "stale_reconciliation_pending"
	staleCallFinalCode        = "execution_abandoned"
	callCompletionPendingCode = "completion_reconciliation_pending"
)

type callCompletionIntent struct {
	FinalAttemptID        uint            `json:"final_attempt_id,omitempty"`
	InputTokens           int             `json:"input_tokens,omitempty"`
	OutputTokens          int             `json:"output_tokens,omitempty"`
	TotalTokens           int             `json:"total_tokens,omitempty"`
	CachedInputTokens     int             `json:"cached_input_tokens,omitempty"`
	ReasoningOutputTokens int             `json:"reasoning_output_tokens,omitempty"`
	UsageJSON             json.RawMessage `json:"usage_json,omitempty"`
	ProviderResponseID    string          `json:"provider_response_id,omitempty"`
	HTTPStatus            int             `json:"http_status,omitempty"`
}

func persistCallCompletionIntent(callID string, req *CompleteCallRequest, _ error) error {
	if strings.TrimSpace(callID) == "" || req == nil {
		return nil
	}
	intent := callCompletionIntent{
		FinalAttemptID: req.FinalAttemptID,
		InputTokens:    req.InputTokens, OutputTokens: req.OutputTokens, TotalTokens: req.TotalTokens,
		CachedInputTokens: req.CachedInputTokens, ReasoningOutputTokens: req.ReasoningOutputTokens,
		UsageJSON:          append(json.RawMessage(nil), req.UsageJSON...),
		ProviderResponseID: req.ProviderResponseID, HTTPStatus: req.HTTPStatus,
	}
	encoded, err := json.Marshal(&intent)
	if err != nil {
		return err
	}
	result := model.DB().Model(&model.APICall{}).
		Where("id = ? AND status IN ?", callID, []model.APICallStatus{
			model.APICallStatusReceived,
			model.APICallStatusInProgress,
		}).
		Updates(map[string]any{
			"error_type":      "internal_error",
			"error_code":      callCompletionPendingCode,
			"error_message":   "Successful execution is pending ledger reconciliation",
			"error_param":     datatypes.JSON(encoded),
			"error_retryable": true,
		})
	return result.Error
}

func (s *APICallService) AcquireCallLease(callID, owner string, expiresAt time.Time) error {
	callID, owner = strings.TrimSpace(callID), strings.TrimSpace(owner)
	if callID == "" || owner == "" || expiresAt.IsZero() {
		return fmt.Errorf("%w: call id, owner, and expiration are required", ErrAPICallInvalidInput)
	}
	now := time.Now()
	result := model.DB().Model(&model.APICall{}).
		Where("id = ? AND status IN ?", callID, []model.APICallStatus{model.APICallStatusReceived, model.APICallStatusInProgress}).
		Where("lease_owner = '' OR lease_owner = ? OR lease_expires_at IS NULL OR lease_expires_at <= ?", owner, now).
		Updates(map[string]any{"lease_owner": owner, "lease_expires_at": &expiresAt})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		var count int64
		if err := model.DB().Model(&model.APICall{}).Where("id = ?", callID).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return ErrAPICallNotFound
		}
		return ErrAPICallLeaseUnavailable
	}
	return nil
}

func (s *APICallService) RenewCallLease(callID, owner string, expiresAt time.Time) error {
	result := model.DB().Model(&model.APICall{}).
		Where("id = ? AND lease_owner = ? AND status IN ?", callID, owner, []model.APICallStatus{model.APICallStatusReceived, model.APICallStatusInProgress}).
		Update("lease_expires_at", &expiresAt)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrAPICallLeaseUnavailable
	}
	return nil
}

func (s *APICallService) ReleaseCallLease(callID, owner string) error {
	if strings.TrimSpace(callID) == "" || strings.TrimSpace(owner) == "" {
		return nil
	}
	return model.DB().Model(&model.APICall{}).
		Where("id = ? AND lease_owner = ?", callID, owner).
		Updates(map[string]any{"lease_owner": "", "lease_expires_at": nil}).Error
}

// ReconcileStaleForegroundCalls terminates abandoned foreground executions and
// idempotently refunds reservations that never reached settlement.
func (s *APICallService) ReconcileStaleForegroundCalls(ctx context.Context, cutoff time.Time, limit int) (int, error) {
	if cutoff.IsZero() {
		cutoff = time.Now().Add(-time.Hour)
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}

	var calls []model.APICall
	now := time.Now()
	err := model.DB().WithContext(ctx).
		Where(
			"(background = ? AND resource_type <> ? AND status IN ? AND ((lease_expires_at IS NULL AND (error_code = ? OR updated_at <= ?)) OR lease_expires_at <= ?)) "+
				"OR (background = ? AND resource_type <> ? AND status IN ? AND reserved_amount > final_cost + refunded_amount) "+
				"OR (status = ? AND error_code = ?)",
			false,
			"task",
			[]model.APICallStatus{model.APICallStatusReceived, model.APICallStatusInProgress},
			callCompletionPendingCode,
			cutoff,
			now,
			false,
			"task",
			[]model.APICallStatus{model.APICallStatusFailed, model.APICallStatusCancelled},
			model.APICallStatusFailed,
			staleCallPendingCode,
		).
		Order("updated_at ASC").Limit(limit).Find(&calls).Error
	if err != nil {
		return 0, err
	}

	reconciled := 0
	var reconciliationErr error
	for index := range calls {
		if err := ctx.Err(); err != nil {
			return reconciled, errors.Join(reconciliationErr, err)
		}
		call := &calls[index]
		if call.Status != model.APICallStatusFailed && call.Status != model.APICallStatusCancelled {
			claimed, err := claimStaleForegroundCall(ctx, call.ID, cutoff)
			if err != nil {
				reconciliationErr = errors.Join(reconciliationErr, fmt.Errorf("claim stale call %s: %w", call.ID, err))
				continue
			}
			if !claimed {
				continue
			}
			if err := model.DB().WithContext(ctx).First(call, "id = ?", call.ID).Error; err != nil {
				reconciliationErr = errors.Join(reconciliationErr, fmt.Errorf("reload reconciled call %s: %w", call.ID, err))
				continue
			}
			if call.Status == model.APICallStatusCompleted {
				reconciled++
				continue
			}
		}
		if err := refundUnsettledCallReservations(ctx, call.ID); err != nil {
			reconciliationErr = errors.Join(reconciliationErr, fmt.Errorf("refund stale call %s: %w", call.ID, err))
			continue
		}
		if call.Status == model.APICallStatusFailed && call.ErrorCode == staleCallPendingCode {
			result := model.DB().WithContext(ctx).Model(&model.APICall{}).
				Where("id = ? AND status = ? AND error_code = ?", call.ID, model.APICallStatusFailed, staleCallPendingCode).
				Updates(map[string]any{
					"error_code":    staleCallFinalCode,
					"error_message": "Execution stopped before a terminal result was persisted",
				})
			if result.Error != nil {
				reconciliationErr = errors.Join(reconciliationErr, fmt.Errorf("finalize stale call %s: %w", call.ID, result.Error))
				continue
			}
		}
		reconciled++
	}
	return reconciled, reconciliationErr
}

func claimStaleForegroundCall(ctx context.Context, callID string, cutoff time.Time) (bool, error) {
	claimed := false
	err := model.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var call model.APICall
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&call, "id = ?", callID).Error; err != nil {
			return err
		}
		if call.Status == model.APICallStatusFailed && call.ErrorCode == staleCallPendingCode {
			claimed = true
			return nil
		}
		now := time.Now()
		leaseActive := call.LeaseExpiresAt != nil && call.LeaseExpiresAt.After(now)
		leaseMissingButFresh := call.LeaseExpiresAt == nil && call.UpdatedAt.After(cutoff) && call.ErrorCode != callCompletionPendingCode
		if call.Background || call.ResourceType == "task" || leaseActive || leaseMissingButFresh ||
			(call.Status != model.APICallStatusReceived && call.Status != model.APICallStatusInProgress) {
			return nil
		}

		if call.ErrorCode == callCompletionPendingCode {
			var intent callCompletionIntent
			if err := json.Unmarshal(call.ErrorParam, &intent); err == nil {
				completeErr := NewAPICallService().CompleteCallTx(tx, call.ID, &CompleteCallRequest{
					FinalAttemptID: intent.FinalAttemptID,
					InputTokens:    intent.InputTokens, OutputTokens: intent.OutputTokens, TotalTokens: intent.TotalTokens,
					CachedInputTokens: intent.CachedInputTokens, ReasoningOutputTokens: intent.ReasoningOutputTokens,
					UsageJSON: datatypes.JSON(intent.UsageJSON), ProviderResponseID: intent.ProviderResponseID,
					HTTPStatus: intent.HTTPStatus, CompleteStartedAttempt: true,
				})
				if completeErr == nil {
					claimed = true
					return nil
				}
				if !errors.Is(completeErr, ErrAPICallAttemptNotFound) && !errors.Is(completeErr, ErrAPICallInvalidTransition) {
					return completeErr
				}
			}
		}

		var latest model.APICallAttempt
		latestErr := tx.Where("call_id = ?", call.ID).Order("attempt_no DESC").First(&latest).Error
		if latestErr != nil && !errors.Is(latestErr, gorm.ErrRecordNotFound) {
			return latestErr
		}

		var startedAttempts []model.APICallAttempt
		if err := tx.Where("call_id = ? AND status = ?", call.ID, model.APICallAttemptStatusStarted).
			Find(&startedAttempts).Error; err != nil {
			return err
		}
		for attemptIndex := range startedAttempts {
			attempt := &startedAttempts[attemptIndex]
			if err := tx.Model(&model.APICallAttempt{}).Where("id = ? AND status = ?", attempt.ID, model.APICallAttemptStatusStarted).
				Updates(map[string]any{
					"status":          model.APICallAttemptStatusFailed,
					"error_type":      "server_error",
					"error_code":      staleCallFinalCode,
					"error_message":   "Execution stopped before a terminal result was persisted",
					"error_retryable": true,
					"completed_at":    now,
					"duration_ms":     elapsedMilliseconds(attempt.StartedAt, now),
				}).Error; err != nil {
				return err
			}
		}
		attemptID := uint(0)
		if latestErr == nil {
			attemptID = latest.ID
		}
		if err := NewAPICallService().FailCallTx(tx, call.ID, &FailCallRequest{
			FinalAttemptID: attemptID,
			HTTPStatus:     500,
			ErrorType:      "server_error",
			ErrorCode:      staleCallPendingCode,
			ErrorMessage:   "Execution stopped before a terminal result was persisted; billing reconciliation is pending",
			ErrorRetryable: true,
		}); err != nil {
			return err
		}
		if call.ResourceType == "response" && call.ResourceID != "" {
			errorJSON := datatypes.JSON(`{"code":"execution_abandoned","message":"Execution stopped before a terminal result was persisted","type":"server_error"}`)
			if err := tx.Model(&model.AIResponse{}).
				Where("id = ? AND status IN ?", call.ResourceID, []string{"queued", "in_progress", "result_ready", "finalizing"}).
				Updates(map[string]any{
					"status": "failed", "error_json": errorJSON, "completed_at": now,
					"lease_owner": "", "lease_expires_at": nil,
				}).Error; err != nil {
				return err
			}
		}
		claimed = true
		return nil
	})
	return claimed, err
}

func refundUnsettledCallReservations(ctx context.Context, callID string) error {
	var reservations []model.BillingLog
	if err := model.DB().WithContext(ctx).
		Where("call_id = ? AND phase = ? AND type = ?", callID, model.BillingPhaseReserve, model.BillingTypeDeduct).
		Order("id ASC").Find(&reservations).Error; err != nil {
		return err
	}
	billing := NewBillingService()
	for index := range reservations {
		reservation := &reservations[index]
		settlementKey := staleSettlementKey(reservation.IdempotentKey)
		if err := billing.SettleReservationWithBillingContext(
			reservation.TokenID,
			reservation.UserID,
			reservation.Amount,
			decimal.Zero,
			settlementKey,
			BillingContext{
				CallID: callID, AttemptID: reservation.AttemptID,
				Phase: model.BillingPhaseRefund, PricingSnapshot: reservation.PricingSnapshot,
			},
		); err != nil {
			return err
		}
	}
	return nil
}

func staleSettlementKey(reserveKey string) string {
	if strings.HasSuffix(reserveKey, ":reserve") {
		return strings.TrimSuffix(reserveKey, ":reserve") + ":settle"
	}
	if reserveKey == "" {
		return ""
	}
	return reserveKey + ":stale_settle"
}

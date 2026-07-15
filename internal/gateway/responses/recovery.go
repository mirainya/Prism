package responses

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/pkg/queue"
	"gorm.io/gorm"
)

var recoverResponseBackground = queue.RecoverResponseBackground

// RequeuePendingBackground restores records left between record creation,
// billing reservation, and queue insertion.
func RequeuePendingBackground(ctx context.Context) (int, error) {
	return requeueBackground(ctx)
}

// RequeueQueuedBackground repairs Redis task loss without changing records
// that another worker may currently own.
func RequeueQueuedBackground(ctx context.Context) (int, error) {
	return requeueBackground(ctx)
}

func requeueBackground(ctx context.Context) (int, error) {
	const batchSize = 100
	lastID := ""
	recovered := 0
	var recoveryErr error

	for {
		now := time.Now()
		var records []model.AIResponse
		query := model.DB().WithContext(ctx).
			Where("background = ? AND status IN ? AND id > ?", true, []string{"queued", "in_progress", "result_ready", "finalizing"}, lastID).
			Where("status = ? OR lease_owner = '' OR lease_expires_at IS NULL OR lease_expires_at <= ?", "queued", now).
			Order("id ASC").Limit(batchSize)
		if err := query.Find(&records).Error; err != nil {
			return recovered, errors.Join(recoveryErr, err)
		}
		if len(records) == 0 {
			return recovered, recoveryErr
		}

		for index := range records {
			record := &records[index]
			lastID = record.ID
			if err := ctx.Err(); err != nil {
				return recovered, errors.Join(recoveryErr, err)
			}
			if record.Status != "queued" {
				targetStatus := "queued"
				if (record.Status == "result_ready" || record.Status == "finalizing") && len(record.ResponseJSON) > 0 {
					targetStatus = "result_ready"
				}
				result := model.DB().WithContext(ctx).Model(&model.AIResponse{}).
					Where("id = ? AND background = ? AND status = ?", record.ID, true, record.Status).
					Where("lease_owner = '' OR lease_expires_at IS NULL OR lease_expires_at <= ?", now).
					Updates(map[string]any{
						"status": targetStatus, "completed_at": nil,
						"lease_owner": "", "lease_expires_at": nil,
					})
				if result.Error != nil {
					recoveryErr = errors.Join(recoveryErr, fmt.Errorf("reset background response %s: %w", record.ID, result.Error))
					continue
				}
				if result.RowsAffected == 0 {
					continue
				}
			}
			if err := recoverResponseBackground(record.ID); err != nil {
				recoveryErr = errors.Join(recoveryErr, fmt.Errorf("requeue background response %s: %w", record.ID, err))
				continue
			}
			recovered++
		}
	}
}

// ReconcilePendingResponseRefunds completes idempotent refunds that were
// interrupted by a transient database failure.
func ReconcilePendingResponseRefunds(ctx context.Context) (int, error) {
	var records []model.AIResponse
	if err := model.DB().WithContext(ctx).
		Where("status IN ?", []string{"refund_pending_failed", "refund_pending_cancelled"}).
		Order("id ASC").Find(&records).Error; err != nil {
		return 0, err
	}
	billing := service.NewBillingService()
	reconciled := 0
	var reconciliationErr error
	for index := range records {
		record := &records[index]
		if err := reconcileV2BackgroundReservations(billing, record); err != nil {
			reconciliationErr = errors.Join(reconciliationErr, fmt.Errorf("refund response %s: %w", record.ID, err))
			continue
		}
		terminalProjection, err := terminalResponseConversationOutputRequest(record, nil, false)
		if err != nil {
			reconciliationErr = errors.Join(reconciliationErr, fmt.Errorf("prepare response projection %s: %w", record.ID, err))
			continue
		}
		target := "failed"
		if record.Status == "refund_pending_cancelled" {
			target = "cancelled"
		}
		now := time.Now()
		updated := false
		err = model.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			result := tx.Model(&model.AIResponse{}).
				Where("id = ? AND status = ?", record.ID, record.Status).
				Updates(map[string]any{
					"status": target, "completed_at": &now,
					"lease_owner": "", "lease_expires_at": nil,
				})
			if result.Error != nil || result.RowsAffected == 0 {
				return result.Error
			}
			updated = true
			if record.CallID == "" {
				return nil
			}
			calls := service.NewAPICallService()
			if target == "cancelled" {
				return calls.CancelCallTx(tx, record.CallID, &service.CancelCallRequest{
					FinalAttemptID: latestResponseAttemptIDTx(tx, record.CallID),
					ErrorType:      "cancelled_error", ErrorCode: "response_cancelled",
					ErrorMessage:           "Response was cancelled",
					ConversationProjection: terminalProjection,
				})
			}
			return calls.FailCallTx(tx, record.CallID, &service.FailCallRequest{
				FinalAttemptID: latestResponseAttemptIDTx(tx, record.CallID),
				HTTPStatus:     502, ErrorType: "server_error", ErrorCode: "response_failed",
				ErrorMessage:           "Response failed",
				ConversationProjection: terminalProjection,
			})
		})
		if err != nil {
			reconciliationErr = errors.Join(reconciliationErr, fmt.Errorf("finish response refund %s: %w", record.ID, err))
			continue
		}
		if updated {
			reconciled++
			projectResponseConversationBestEffort(record)
		}
	}
	return reconciled, reconciliationErr
}

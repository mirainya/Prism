package responses

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/pkg/queue"
)

var recoverResponseBackground = queue.RecoverResponseBackground

// RequeuePendingBackground restores records left between record creation,
// billing reservation, and queue insertion.
func RequeuePendingBackground(ctx context.Context) (int, error) {
	return requeueBackground(ctx, []string{"queued", "in_progress", "finalizing"}, true)
}

// RequeueQueuedBackground repairs Redis task loss without changing records
// that another worker may currently own.
func RequeueQueuedBackground(ctx context.Context) (int, error) {
	return requeueBackground(ctx, []string{"queued"}, false)
}

func requeueBackground(ctx context.Context, statuses []string, resetActive bool) (int, error) {
	const batchSize = 100
	lastID := ""
	recovered := 0
	var recoveryErr error

	for {
		var records []model.AIResponse
		query := model.DB().WithContext(ctx).
			Where("background = ? AND status IN ? AND id > ?", true, statuses, lastID).
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
			if resetActive && record.Status != "queued" {
				result := model.DB().WithContext(ctx).Model(&model.AIResponse{}).
					Where("id = ? AND background = ? AND status = ?", record.ID, true, record.Status).
					Updates(map[string]any{"status": "queued", "completed_at": nil})
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
		reservation := loadResponseReservation(billing, record)
		if reservation != nil {
			if err := reservation.cancel(); err != nil {
				reconciliationErr = errors.Join(reconciliationErr, fmt.Errorf("refund response %s: %w", record.ID, err))
				continue
			}
		}
		target := "failed"
		if record.Status == "refund_pending_cancelled" {
			target = "cancelled"
		}
		now := time.Now()
		result := model.DB().WithContext(ctx).Model(&model.AIResponse{}).
			Where("id = ? AND status = ?", record.ID, record.Status).
			Updates(map[string]any{"status": target, "completed_at": &now})
		if result.Error != nil {
			reconciliationErr = errors.Join(reconciliationErr, fmt.Errorf("finish response refund %s: %w", record.ID, result.Error))
			continue
		}
		if result.RowsAffected > 0 {
			reconciled++
		}
	}
	return reconciled, reconciliationErr
}

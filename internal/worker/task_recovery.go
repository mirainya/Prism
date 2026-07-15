package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/mirainya/Prism/internal/model"
)

const taskSubmitRecoveryBatchSize = 500

// RecoverPendingTaskSubmissions restores submit queue entries from durable Task
// rows. Deterministic Asynq IDs and SQL worker leases make repeated scans safe.
func RecoverPendingTaskSubmissions(ctx context.Context) (int, error) {
	now := time.Now()
	lastID := uint(0)
	recovered := 0

	for {
		if err := ctx.Err(); err != nil {
			return recovered, err
		}
		var tasks []model.Task
		err := model.DB().
			Select("id").
			Where("id > ?", lastID).
			Where("status IN ?", []model.TaskStatus{
				model.TaskStatusPending,
				model.TaskStatusProcessing,
				model.TaskStatusFinalizing,
			}).
			Where("(status = ? OR submit_checkpoint IS NOT NULL)", model.TaskStatusPending).
			Where("worker_lease_expires_at IS NULL OR worker_lease_expires_at <= ?", now).
			Order("id ASC").
			Limit(taskSubmitRecoveryBatchSize).
			Find(&tasks).Error
		if err != nil {
			return recovered, fmt.Errorf("query recoverable task submissions: %w", err)
		}
		if len(tasks) == 0 {
			return recovered, nil
		}
		for _, task := range tasks {
			if err := recoverTaskSubmit(task.ID); err != nil {
				return recovered, fmt.Errorf("recover task submit %d: %w", task.ID, err)
			}
			recovered++
			lastID = task.ID
		}
		if len(tasks) < taskSubmitRecoveryBatchSize {
			return recovered, nil
		}
	}
}

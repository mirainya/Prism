package worker

import (
	"context"
	"fmt"
	"strings"

	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/video"
	"github.com/mirainya/Prism/pkg/queue"
)

const videoSubmitRecoveryBatchSize = 500

func RecoverPendingVideoWork(ctx context.Context) (int, error) {
	lastID := ""
	recovered := 0
	for {
		if err := ctx.Err(); err != nil {
			return recovered, err
		}
		var tasks []video.VideoTask
		err := model.DB().WithContext(ctx).Select("id", "status", "poll_count").
			Where("status IN ? AND id > ?", []video.VideoTaskStatus{
				video.VideoTaskStatusQueued, video.VideoTaskStatusSubmitted, video.VideoTaskStatusTracking,
			}, lastID).
			Order("id ASC").Limit(videoSubmitRecoveryBatchSize).Find(&tasks).Error
		if err != nil {
			if isMissingVideoTaskTable(err) {
				return recovered, nil
			}
			return recovered, fmt.Errorf("query recoverable video submissions: %w", err)
		}
		if len(tasks) == 0 {
			return recovered, nil
		}
		for _, task := range tasks {
			var err error
			if task.Status == video.VideoTaskStatusQueued {
				err = queue.RecoverVideoSubmit(task.ID)
			} else {
				err = queue.RecoverVideoPoll(task.ID, task.PollCount)
			}
			if err != nil {
				return recovered, fmt.Errorf("recover video work %s: %w", task.ID, err)
			}
			recovered++
			lastID = task.ID
		}
		if len(tasks) < videoSubmitRecoveryBatchSize {
			return recovered, nil
		}
	}
}

func RecoverPendingVideoSubmissions(ctx context.Context) (int, error) {
	return RecoverPendingVideoWork(ctx)
}

func isMissingVideoTaskTable(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no such table: video_tasks") ||
		strings.Contains(message, "table 'video_tasks' doesn't exist")
}

package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"gorm.io/gorm"
)

const (
	TaskWorkerStageSubmit = "submit"
	TaskWorkerStagePoll   = "poll"

	defaultTaskWorkerLeaseDuration  = 45 * time.Second
	defaultTaskWorkerLeaseHeartbeat = 15 * time.Second
)

var (
	ErrTaskWorkerLeaseBusy = errors.New("task worker lease busy")
	ErrTaskWorkerLeaseLost = errors.New("task worker lease lost")
)

type TaskWorkerLease struct {
	taskID uint
	owner  string
	stage  string

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	duration  time.Duration
	heartbeat time.Duration

	errMu sync.Mutex
	err   error
}

func AcquireTaskWorkerLease(ctx context.Context, taskID uint, stage string) (*TaskWorkerLease, bool, error) {
	return AcquireTaskWorkerLeaseWithOptions(
		ctx,
		taskID,
		stage,
		defaultTaskWorkerLeaseDuration,
		defaultTaskWorkerLeaseHeartbeat,
	)
}

func AcquireTaskWorkerLeaseWithOptions(
	ctx context.Context,
	taskID uint,
	stage string,
	duration time.Duration,
	heartbeat time.Duration,
) (*TaskWorkerLease, bool, error) {
	stage = strings.TrimSpace(stage)
	if taskID == 0 || stage == "" || duration <= 0 || heartbeat <= 0 || heartbeat >= duration {
		return nil, false, fmt.Errorf("invalid task worker lease parameters")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	now := time.Now()
	owner := GenerateRequestID()
	expiresAt := now.Add(duration)
	query := model.DB().Model(&model.Task{}).
		Where("id = ? AND status NOT IN ?", taskID, []model.TaskStatus{
			model.TaskStatusSuccess,
			model.TaskStatusFailed,
			model.TaskStatusCancelled,
		})
	switch stage {
	case TaskWorkerStageSubmit:
		query = query.Where("status = ? OR submit_checkpoint IS NOT NULL", model.TaskStatusPending)
	case TaskWorkerStagePoll:
		query = query.Where("status IN ?", []model.TaskStatus{model.TaskStatusProcessing, model.TaskStatusFinalizing})
	default:
		return nil, false, fmt.Errorf("unsupported task worker lease stage %q", stage)
	}
	result := query.
		Where(
			"worker_lease_owner = '' OR worker_lease_owner IS NULL OR worker_lease_expires_at IS NULL OR worker_lease_expires_at <= ?",
			now,
		).
		Updates(map[string]any{
			"worker_lease_owner":      owner,
			"worker_lease_stage":      stage,
			"worker_lease_expires_at": &expiresAt,
		})
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected == 0 {
		var count int64
		if err := model.DB().Model(&model.Task{}).Where("id = ?", taskID).Count(&count).Error; err != nil {
			return nil, false, err
		}
		if count == 0 {
			return nil, false, ErrTaskNotFound
		}
		return nil, false, nil
	}

	leaseCtx, cancel := context.WithCancel(ctx)
	lease := &TaskWorkerLease{
		taskID: taskID, owner: owner, stage: stage,
		ctx: leaseCtx, cancel: cancel, done: make(chan struct{}),
		duration: duration, heartbeat: heartbeat,
	}
	go lease.renewLoop()
	return lease, true, nil
}

func (l *TaskWorkerLease) Context() context.Context {
	if l == nil || l.ctx == nil {
		return context.Background()
	}
	return l.ctx
}

func (l *TaskWorkerLease) Owner() string {
	if l == nil {
		return ""
	}
	return l.owner
}

func (l *TaskWorkerLease) Stage() string {
	if l == nil {
		return ""
	}
	return l.stage
}

func (l *TaskWorkerLease) Err() error {
	if l == nil {
		return nil
	}
	l.errMu.Lock()
	defer l.errMu.Unlock()
	return l.err
}

func (l *TaskWorkerLease) Check() error {
	if l == nil {
		return ErrTaskWorkerLeaseLost
	}
	if err := l.Err(); err != nil {
		return err
	}
	var count int64
	err := model.DB().Model(&model.Task{}).
		Where("id = ? AND worker_lease_owner = ? AND worker_lease_stage = ? AND worker_lease_expires_at > ?",
			l.taskID, l.owner, l.stage, time.Now()).
		Count(&count).Error
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrTaskWorkerLeaseLost
	}
	return nil
}

func (l *TaskWorkerLease) Stop() error {
	if l == nil {
		return nil
	}
	l.cancel()
	<-l.done

	result := model.DB().Model(&model.Task{}).
		Where("id = ? AND worker_lease_owner = ? AND worker_lease_stage = ?", l.taskID, l.owner, l.stage).
		Updates(map[string]any{
			"worker_lease_owner":      "",
			"worker_lease_stage":      "",
			"worker_lease_expires_at": nil,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		return nil
	}

	var task model.Task
	if err := model.DB().Select("status", "worker_lease_owner").First(&task, l.taskID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrTaskNotFound
		}
		return err
	}
	if task.Status.IsTerminal() || task.WorkerLeaseOwner == "" {
		return nil
	}
	return ErrTaskWorkerLeaseLost
}

func (l *TaskWorkerLease) renewLoop() {
	defer close(l.done)
	ticker := time.NewTicker(l.heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := renewTaskWorkerLease(l.taskID, l.owner, l.stage, time.Now().Add(l.duration)); err != nil {
				l.errMu.Lock()
				l.err = err
				l.errMu.Unlock()
				l.cancel()
				return
			}
		case <-l.ctx.Done():
			return
		}
	}
}

func renewTaskWorkerLease(taskID uint, owner, stage string, expiresAt time.Time) error {
	result := model.DB().Model(&model.Task{}).
		Where("id = ? AND worker_lease_owner = ? AND worker_lease_stage = ?", taskID, owner, stage).
		UpdateColumn("worker_lease_expires_at", &expiresAt)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrTaskWorkerLeaseLost
	}
	return nil
}

func (s *TaskService) CurrentTaskPollRound(taskID uint, owner string, pollCount int) (bool, error) {
	var count int64
	err := model.DB().Model(&model.Task{}).
		Where("id = ? AND worker_lease_owner = ? AND worker_lease_stage = ? AND worker_lease_expires_at > ? AND poll_cursor = ?",
			taskID, owner, TaskWorkerStagePoll, time.Now(), pollCount).
		Count(&count).Error
	return count > 0, err
}

// AdoptLegacyTaskPollRound claims the queued poll sequence of a task that
// predates the persistent poll cursor migration.
func (s *TaskService) AdoptLegacyTaskPollRound(taskID uint, owner string, pollCount int) (bool, error) {
	if pollCount < 0 {
		return false, nil
	}
	result := model.DB().Model(&model.Task{}).
		Where("id = ? AND worker_lease_owner = ? AND worker_lease_stage = ? AND worker_lease_expires_at > ? AND poll_cursor = -1",
			taskID, owner, TaskWorkerStagePoll, time.Now()).
		UpdateColumn("poll_cursor", pollCount)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// AdoptQueuedTaskPollRound repairs the crash window where the next Redis poll
// was enqueued but the previous worker did not persist its cursor advance.
func (s *TaskService) AdoptQueuedTaskPollRound(taskID uint, owner string, pollCount int) (bool, error) {
	if pollCount <= 0 {
		return false, nil
	}
	result := model.DB().Model(&model.Task{}).
		Where("id = ? AND worker_lease_owner = ? AND worker_lease_stage = ? AND worker_lease_expires_at > ? AND poll_cursor = ?",
			taskID, owner, TaskWorkerStagePoll, time.Now(), pollCount-1).
		UpdateColumn("poll_cursor", pollCount)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func (s *TaskService) CompleteTaskPollRound(taskID uint, owner string, pollCount int) error {
	result := model.DB().Model(&model.Task{}).
		Where("id = ? AND worker_lease_owner = ? AND worker_lease_stage = ? AND worker_lease_expires_at > ? AND poll_cursor = ?",
			taskID, owner, TaskWorkerStagePoll, time.Now(), pollCount).
		UpdateColumn("poll_cursor", pollCount+1)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrTaskWorkerLeaseLost
	}
	return nil
}

package video

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

const (
	VideoWorkerStageSubmit = "submit"
	VideoWorkerStagePoll   = "poll"

	defaultVideoWorkerLeaseDuration  = 45 * time.Second
	defaultVideoWorkerLeaseHeartbeat = 15 * time.Second
)

var (
	ErrVideoWorkerLeaseBusy   = errors.New("video worker lease busy")
	ErrVideoWorkerLeaseLost   = errors.New("video worker lease lost")
	ErrVideoTaskNotFound      = errors.New("video task not found")
	ErrVideoTaskNotExecutable = errors.New("video task is no longer executable")
)

type VideoSubmitCheckpoint struct {
	Version   int       `json:"version"`
	State     string    `json:"state"`
	RequestID string    `json:"request_id"`
	AttemptID uint      `json:"attempt_id,omitempty"`
	Attempts  int       `json:"attempts"`
	StartedAt time.Time `json:"started_at"`
}

type VideoWorkerLease struct {
	taskID string
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

func AcquireVideoWorkerLease(ctx context.Context, taskID, stage string) (*VideoWorkerLease, bool, error) {
	return AcquireVideoWorkerLeaseWithOptions(
		ctx, taskID, stage,
		defaultVideoWorkerLeaseDuration,
		defaultVideoWorkerLeaseHeartbeat,
	)
}

func AcquireVideoWorkerLeaseWithOptions(
	ctx context.Context,
	taskID, stage string,
	duration, heartbeat time.Duration,
) (*VideoWorkerLease, bool, error) {
	if taskID == "" || duration <= 0 || heartbeat <= 0 || heartbeat >= duration {
		return nil, false, fmt.Errorf("invalid video worker lease parameters")
	}
	statuses, err := videoWorkerStageStatuses(stage)
	if err != nil {
		return nil, false, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	now := time.Now()
	owner := generateID()
	expiresAt := now.Add(duration)
	result := model.DB().WithContext(ctx).Model(&VideoTask{}).
		Where("id = ? AND status IN ?", taskID, statuses).
		Where("worker_lease = '' OR worker_lease IS NULL OR worker_lease_expires_at IS NULL OR worker_lease_expires_at <= ?", now).
		Updates(map[string]any{
			"worker_lease":            owner,
			"worker_lease_stage":      stage,
			"worker_lease_expires_at": &expiresAt,
		})
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected == 0 {
		var count int64
		if err := model.DB().WithContext(ctx).Model(&VideoTask{}).Where("id = ?", taskID).Count(&count).Error; err != nil {
			return nil, false, err
		}
		if count == 0 {
			return nil, false, ErrVideoTaskNotFound
		}
		return nil, false, nil
	}

	leaseCtx, cancel := context.WithCancel(ctx)
	lease := &VideoWorkerLease{
		taskID: taskID, owner: owner, stage: stage,
		ctx: leaseCtx, cancel: cancel, done: make(chan struct{}),
		duration: duration, heartbeat: heartbeat,
	}
	go lease.renewLoop()
	return lease, true, nil
}

func (l *VideoWorkerLease) Context() context.Context {
	if l == nil || l.ctx == nil {
		return context.Background()
	}
	return l.ctx
}

func (l *VideoWorkerLease) Owner() string {
	if l == nil {
		return ""
	}
	return l.owner
}

func (l *VideoWorkerLease) Check() error {
	if l == nil {
		return ErrVideoWorkerLeaseLost
	}
	if err := l.leaseError(); err != nil {
		return err
	}
	var count int64
	err := model.DB().Model(&VideoTask{}).
		Where("id = ? AND worker_lease = ? AND worker_lease_stage = ? AND worker_lease_expires_at > ?",
			l.taskID, l.owner, l.stage, time.Now()).
		Count(&count).Error
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrVideoWorkerLeaseLost
	}
	return nil
}

func (l *VideoWorkerLease) Stop() error {
	if l == nil {
		return nil
	}
	l.cancel()
	<-l.done
	result := model.DB().Model(&VideoTask{}).
		Where("id = ? AND worker_lease = ? AND worker_lease_stage = ?", l.taskID, l.owner, l.stage).
		Updates(map[string]any{
			"worker_lease":            "",
			"worker_lease_stage":      "",
			"worker_lease_expires_at": nil,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrVideoWorkerLeaseLost
	}
	return nil
}

func (l *VideoWorkerLease) renewLoop() {
	defer close(l.done)
	ticker := time.NewTicker(l.heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := time.Now()
			result := model.DB().Model(&VideoTask{}).
				Where("id = ? AND worker_lease = ? AND worker_lease_stage = ? AND worker_lease_expires_at > ?",
					l.taskID, l.owner, l.stage, now).
				UpdateColumn("worker_lease_expires_at", now.Add(l.duration))
			if result.Error != nil || result.RowsAffected == 0 {
				err := result.Error
				if err == nil {
					err = ErrVideoWorkerLeaseLost
				}
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

func (l *VideoWorkerLease) leaseError() error {
	l.errMu.Lock()
	defer l.errMu.Unlock()
	return l.err
}

func DecodeVideoSubmitCheckpoint(raw datatypes.JSON) (*VideoSubmitCheckpoint, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var checkpoint VideoSubmitCheckpoint
	if err := json.Unmarshal(raw, &checkpoint); err != nil {
		return nil, err
	}
	if checkpoint.Version != 1 || checkpoint.RequestID == "" || checkpoint.Attempts < 1 {
		return nil, errors.New("invalid video submit checkpoint")
	}
	return &checkpoint, nil
}

func SaveVideoSubmitCheckpoint(ctx context.Context, taskID, leaseOwner string, checkpoint *VideoSubmitCheckpoint) error {
	if checkpoint == nil {
		return errors.New("video submit checkpoint is required")
	}
	raw, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}
	result := model.DB().WithContext(ctx).Model(&VideoTask{}).
		Where("id = ? AND status = ? AND worker_lease = ? AND worker_lease_stage = ? AND worker_lease_expires_at > ?",
			taskID, VideoTaskStatusQueued, leaseOwner, VideoWorkerStageSubmit, time.Now()).
		UpdateColumn("submit_checkpoint", datatypes.JSON(raw))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrVideoWorkerLeaseLost
	}
	return nil
}

func CommitVideoSubmission(
	ctx context.Context,
	taskID, leaseOwner, providerTaskID string,
	attemptID uint,
) error {
	return model.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		result := tx.Model(&VideoTask{}).
			Where("id = ? AND status = ? AND worker_lease = ? AND worker_lease_stage = ? AND worker_lease_expires_at > ?",
				taskID, VideoTaskStatusQueued, leaseOwner, VideoWorkerStageSubmit, now).
			Updates(map[string]any{
				"provider_task_id":  providerTaskID,
				"submitted_at":      &now,
				"status":            VideoTaskStatusSubmitted,
				"submit_checkpoint": nil,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			var task VideoTask
			if err := tx.Select("status", "worker_lease").First(&task, "id = ?", taskID).Error; err != nil {
				return err
			}
			if task.Status.IsTerminal() {
				return ErrVideoTaskNotExecutable
			}
			return ErrVideoWorkerLeaseLost
		}
		if attemptID > 0 && providerTaskID != "" {
			if err := tx.Model(&model.APICallAttempt{}).Where("id = ?", attemptID).
				Update("provider_response_id", providerTaskID).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func videoWorkerStageStatuses(stage string) ([]VideoTaskStatus, error) {
	switch stage {
	case VideoWorkerStageSubmit:
		return []VideoTaskStatus{VideoTaskStatusQueued}, nil
	case VideoWorkerStagePoll:
		return []VideoTaskStatus{VideoTaskStatusSubmitted, VideoTaskStatusTracking}, nil
	default:
		return nil, fmt.Errorf("unsupported video worker lease stage %q", stage)
	}
}

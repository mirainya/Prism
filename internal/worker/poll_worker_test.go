package worker

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider"
	"github.com/mirainya/Prism/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

var pollWorkerDBCounter int64

func TestIsRetryablePollError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"network error no status", fmt.Errorf("connection refused"), true},
		{"read timeout no status", fmt.Errorf("do request: context deadline exceeded"), true},
		{"408 request timeout", &domain.UpstreamError{StatusCode: 408}, true},
		{"429 rate limited", &domain.UpstreamError{StatusCode: 429}, true},
		{"500 upstream error", &domain.UpstreamError{StatusCode: 500}, true},
		{"503 unavailable", &domain.UpstreamError{StatusCode: 503}, true},
		{"400 bad request", &domain.UpstreamError{StatusCode: 400}, false},
		{"401 unauthorized", &domain.UpstreamError{StatusCode: 401}, false},
		{"403 forbidden", &domain.UpstreamError{StatusCode: 403}, false},
		{"404 not found", &domain.UpstreamError{StatusCode: 404}, false},
		{"422 unprocessable", &domain.UpstreamError{StatusCode: 422}, false},
		{"wrapped 500 retryable", fmt.Errorf("poll: %w", &domain.UpstreamError{StatusCode: 500}), true},
		{"wrapped 404 fatal", fmt.Errorf("poll: %w", &domain.UpstreamError{StatusCode: 404}), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isRetryablePollError(c.err); got != c.want {
				t.Errorf("isRetryablePollError(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

func TestEnqueuePollResultClaimsFinalizingBeforeQueue(t *testing.T) {
	dsn := fmt.Sprintf("file:poll-worker-%d?mode=memory&cache=shared", atomic.AddInt64(&pollWorkerDBCounter, 1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.Task{}); err != nil {
		t.Fatalf("migrate task: %v", err)
	}
	model.SetDB(db)
	previousLogger := logger.L
	logger.L = zap.NewNop()
	previousEnqueue := enqueueUpload
	t.Cleanup(func() {
		logger.L = previousLogger
		enqueueUpload = previousEnqueue
	})

	task := &model.Task{TaskNo: "poll-finalizing", Status: model.TaskStatusProcessing}
	if err := db.Create(task).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}
	queueErr := errors.New("queue unavailable")
	calls := 0
	enqueueUpload = func(taskID uint, originURL string, urls []string, revisedPrompt ...string) error {
		calls++
		var current model.Task
		if err := model.DB().First(&current, taskID).Error; err != nil {
			t.Fatalf("reload task during enqueue: %v", err)
		}
		if current.Status != model.TaskStatusFinalizing {
			t.Fatalf("status during enqueue = %s, want finalizing", current.Status)
		}
		if calls == 1 {
			return queueErr
		}
		return nil
	}
	result := provider.ProgressResult{Status: provider.StatusSuccess, URLs: []string{"https://result.example/image.png"}}

	if err := enqueuePollResult(task.ID, result); !errors.Is(err, queueErr) {
		t.Fatalf("first enqueue error = %v, want %v", err, queueErr)
	}
	committed, err := taskService.UpdateTaskFail(task.ID, "late upstream failure")
	if err != nil {
		t.Fatalf("record late failure: %v", err)
	}
	if committed {
		t.Fatal("late failure replaced finalizing poll result")
	}
	if err := enqueuePollResult(task.ID, result); err != nil {
		t.Fatalf("retry enqueue poll result: %v", err)
	}
	if calls != 2 {
		t.Fatalf("enqueue calls = %d, want 2", calls)
	}
}

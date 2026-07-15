package service

import (
	"errors"
	"testing"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/config"
)

func TestTaskTokenOwnershipKeepsPublicUserScope(t *testing.T) {
	db := setupTestDB(t)
	user := &model.User{Username: "task-owner-" + GenerateTaskNo(), Status: 1}
	if err := db.Create(user).Error; err != nil {
		t.Fatal(err)
	}
	firstToken := &model.Token{UserID: user.ID, Key: "task-token-one-" + GenerateTaskNo(), Status: 1}
	secondToken := &model.Token{UserID: user.ID, Key: "task-token-two-" + GenerateTaskNo(), Status: 1}
	if err := db.Create(firstToken).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(secondToken).Error; err != nil {
		t.Fatal(err)
	}
	task := &model.Task{
		TaskNo: GenerateTaskNo(), UserID: user.ID, TokenID: secondToken.ID,
		Status: model.TaskStatusPending,
	}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}

	service := NewTaskService()
	if _, err := service.GetTaskByNoAndUser(task.TaskNo, user.ID); err != nil {
		t.Fatalf("user-scoped lookup changed: %v", err)
	}
	if _, err := service.GetTaskByNoUserAndToken(task.TaskNo, user.ID, firstToken.ID); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("cross-token lookup error = %v, want ErrTaskNotFound", err)
	}
	if err := service.CancelTaskByToken(task.TaskNo, user.ID, firstToken.ID); err == nil {
		t.Fatal("cross-token cancellation unexpectedly succeeded")
	}
	if err := db.First(task, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != model.TaskStatusPending {
		t.Fatalf("cross-token cancellation changed status to %s", task.Status)
	}
	if err := service.CancelTaskByToken(task.TaskNo, user.ID, secondToken.ID); err != nil {
		t.Fatalf("owned cancellation failed: %v", err)
	}
}

func TestTaskProgressIsBoundedAndMonotonic(t *testing.T) {
	db := setupTestDB(t)
	task := &model.Task{TaskNo: GenerateTaskNo(), Status: model.TaskStatusPending, Progress: 10}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}

	service := NewTaskService()
	if err := service.UpdateTaskProgress(task.ID, 60); err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateTaskProgress(task.ID, 20); err != nil {
		t.Fatal(err)
	}
	if err := db.First(task, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if task.Progress != 60 {
		t.Fatalf("regressed progress = %d, want 60", task.Progress)
	}

	if err := service.UpdateTaskProgress(task.ID, 150); err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateTaskProgress(task.ID, -10); err != nil {
		t.Fatal(err)
	}
	if err := db.First(task, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if task.Progress != 100 {
		t.Fatalf("bounded progress = %d, want 100", task.Progress)
	}
}

func TestProcessingUpdatesPreserveStartedAt(t *testing.T) {
	db := setupTestDB(t)
	task := &model.Task{TaskNo: GenerateTaskNo(), Status: model.TaskStatusPending}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}

	service := NewTaskService()
	if err := service.UpdateTaskStatus(task.ID, model.TaskStatusProcessing, "vendor-one"); err != nil {
		t.Fatal(err)
	}
	if err := db.First(task, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if task.StartedAt == nil {
		t.Fatal("started_at was not set")
	}
	startedAt := *task.StartedAt
	time.Sleep(2 * time.Millisecond)
	if err := service.UpdateTaskStatus(task.ID, model.TaskStatusProcessing, "vendor-one"); err != nil {
		t.Fatal(err)
	}
	if err := db.First(task, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if task.StartedAt == nil || !task.StartedAt.Equal(startedAt) {
		t.Fatalf("started_at changed from %s to %v", startedAt, task.StartedAt)
	}
}

func TestTerminalTaskBodyPolicyFollowsPayloadRetention(t *testing.T) {
	previous := config.C
	t.Cleanup(func() { config.C = previous })

	config.C = &config.Config{Observability: config.ObservabilityConfig{RetainAPICallPayloads: true}}
	retained := map[string]any{"status": model.TaskStatusSuccess}
	applyTerminalTaskBodyPolicy(retained)
	if _, exists := retained["request_params"]; exists {
		t.Fatal("retained task request was cleared")
	}

	config.C.Observability.RetainAPICallPayloads = false
	cleared := map[string]any{"status": model.TaskStatusSuccess}
	applyTerminalTaskBodyPolicy(cleared)
	if _, exists := cleared["request_params"]; !exists {
		t.Fatal("non-retained task request was not cleared")
	}
	if _, exists := cleared["mapped_params"]; !exists {
		t.Fatal("non-retained mapped params were not cleared")
	}
}

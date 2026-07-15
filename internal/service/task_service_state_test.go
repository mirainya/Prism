package service

import (
	"errors"
	"testing"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"gorm.io/datatypes"
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

func TestCancelTaskPreservesParams(t *testing.T) {
	db := setupTestDB(t)
	params := datatypes.JSON(`{"prompt":"keep for task history"}`)
	task := &model.Task{
		TaskNo:        GenerateTaskNo(),
		UserID:        42,
		Status:        model.TaskStatusPending,
		RequestParams: params,
		MappedParams:  params,
	}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}

	if err := NewTaskService().CancelTask(task.TaskNo, task.UserID); err != nil {
		t.Fatal(err)
	}
	if err := db.First(task, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != model.TaskStatusCancelled {
		t.Fatalf("status = %s, want cancelled", task.Status)
	}
	if len(task.RequestParams) == 0 || len(task.MappedParams) == 0 {
		t.Fatalf("cancelled task lost params: request=%s mapped=%s", task.RequestParams, task.MappedParams)
	}
}

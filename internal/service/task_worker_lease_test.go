package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"github.com/shopspring/decimal"
	"gorm.io/datatypes"
)

func TestTaskSubmitCheckpointSettlementCostSupportsLegacyData(t *testing.T) {
	fallback := decimal.RequireFromString("1.25")
	cost, err := (&TaskSubmitCheckpoint{}).SettlementCost(fallback)
	if err != nil || !cost.Equal(fallback) {
		t.Fatalf("legacy checkpoint cost = %s err=%v, want %s", cost, err, fallback)
	}

	want := decimal.RequireFromString("0.375")
	cost, err = (&TaskSubmitCheckpoint{FinalCost: want.String()}).SettlementCost(fallback)
	if err != nil || !cost.Equal(want) {
		t.Fatalf("checkpoint cost = %s err=%v, want %s", cost, err, want)
	}
}

func TestTaskWorkerLeaseIsExclusiveAndRenewed(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.Task{}); err != nil {
		t.Fatal(err)
	}
	task := &model.Task{TaskNo: GenerateTaskNo(), Status: model.TaskStatusProcessing}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}

	lease, acquired, err := AcquireTaskWorkerLeaseWithOptions(
		context.Background(), task.ID, TaskWorkerStagePoll, 120*time.Millisecond, 30*time.Millisecond,
	)
	if err != nil || !acquired {
		t.Fatalf("first lease acquired=%v err=%v", acquired, err)
	}
	defer func() { _ = lease.Stop() }()

	if _, acquired, err := AcquireTaskWorkerLease(context.Background(), task.ID, TaskWorkerStageSubmit); err != nil || acquired {
		t.Fatalf("overlapping lease acquired=%v err=%v", acquired, err)
	}
	time.Sleep(180 * time.Millisecond)
	if err := lease.Check(); err != nil {
		t.Fatalf("renewed lease expired: %v", err)
	}
	if err := lease.Stop(); err != nil {
		t.Fatal(err)
	}
	lease = nil

	replacement, acquired, err := AcquireTaskWorkerLease(context.Background(), task.ID, TaskWorkerStagePoll)
	if err != nil || !acquired {
		t.Fatalf("replacement lease acquired=%v err=%v", acquired, err)
	}
	if err := replacement.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestTaskWorkerLeaseEnforcesStageState(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.Task{}); err != nil {
		t.Fatal(err)
	}
	pending := &model.Task{TaskNo: GenerateTaskNo(), Status: model.TaskStatusPending}
	processing := &model.Task{TaskNo: GenerateTaskNo(), Status: model.TaskStatusProcessing}
	checkpointed := &model.Task{
		TaskNo: GenerateTaskNo(), Status: model.TaskStatusProcessing,
		SubmitCheckpoint: datatypes.JSON(`{"version":1,"lease_owner":"expired"}`),
	}
	if err := db.Create(&[]*model.Task{pending, processing, checkpointed}).Error; err != nil {
		t.Fatal(err)
	}

	if _, acquired, err := AcquireTaskWorkerLease(context.Background(), pending.ID, TaskWorkerStagePoll); err != nil || acquired {
		t.Fatalf("pending task acquired poll lease=%v err=%v", acquired, err)
	}
	if _, acquired, err := AcquireTaskWorkerLease(context.Background(), processing.ID, TaskWorkerStageSubmit); err != nil || acquired {
		t.Fatalf("processing task without checkpoint acquired submit lease=%v err=%v", acquired, err)
	}
	lease, acquired, err := AcquireTaskWorkerLease(context.Background(), checkpointed.ID, TaskWorkerStageSubmit)
	if err != nil || !acquired {
		t.Fatalf("checkpoint recovery lease acquired=%v err=%v", acquired, err)
	}
	if err := lease.Stop(); err != nil && !errors.Is(err, ErrTaskWorkerLeaseLost) {
		t.Fatal(err)
	}
}

func TestResolveInFlightTaskSubmitLeavesCallbackFinalizationUntouched(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.Task{}); err != nil {
		t.Fatal(err)
	}
	task := &model.Task{
		TaskNo: GenerateTaskNo(), Status: model.TaskStatusFinalizing,
		SubmitCheckpoint: datatypes.JSON(`{"version":1,"lease_owner":"expired","state":"in_flight","attempt_id":7,"final_cost":"1"}`),
	}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}
	lease, acquired, err := AcquireTaskWorkerLease(context.Background(), task.ID, TaskWorkerStageSubmit)
	if err != nil || !acquired {
		t.Fatalf("acquire submit recovery lease = %v/%v", acquired, err)
	}
	defer func() { _ = lease.Stop() }()

	callbackOwned, err := NewTaskService().ResolveInFlightTaskSubmit(task.ID, lease)
	if err != nil || !callbackOwned {
		t.Fatalf("resolve callback-owned submit = %v/%v", callbackOwned, err)
	}
	var current model.Task
	if err := db.First(&current, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if current.Status != model.TaskStatusFinalizing || current.ErrorMessage != "" || len(current.SubmitCheckpoint) != 0 {
		t.Fatalf("callback finalization changed = status %s error %q checkpoint %q",
			current.Status, current.ErrorMessage, current.SubmitCheckpoint)
	}
}

func TestTaskWorkerLeaseRejectsExpiredFencedWrites(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.Task{}); err != nil {
		t.Fatal(err)
	}
	expiredAt := time.Now().Add(-time.Second)
	pollTask := &model.Task{
		TaskNo: GenerateTaskNo(), Status: model.TaskStatusProcessing, PollCursor: 2,
		WorkerLeaseOwner: "expired-poll", WorkerLeaseStage: TaskWorkerStagePoll,
		WorkerLeaseExpiresAt: &expiredAt,
	}
	submitTask := &model.Task{
		TaskNo: GenerateTaskNo(), Status: model.TaskStatusProcessing,
		SubmitCheckpoint: datatypes.JSON(`{"version":1,"lease_owner":"expired-submit"}`),
		WorkerLeaseOwner: "expired-submit", WorkerLeaseStage: TaskWorkerStageSubmit,
		WorkerLeaseExpiresAt: &expiredAt,
	}
	if err := db.Create(&[]*model.Task{pollTask, submitTask}).Error; err != nil {
		t.Fatal(err)
	}

	current, err := NewTaskService().CurrentTaskPollRound(pollTask.ID, "expired-poll", 2)
	if err != nil || current {
		t.Fatalf("expired poll round current=%v err=%v", current, err)
	}
	if err := NewTaskService().CompleteTaskPollRound(pollTask.ID, "expired-poll", 2); !errors.Is(err, ErrTaskWorkerLeaseLost) {
		t.Fatalf("expired poll completion error = %v", err)
	}
	if err := NewTaskService().CommitTaskSubmitProcessing(submitTask.ID, "expired-submit", "vendor-task"); !errors.Is(err, ErrTaskWorkerLeaseLost) {
		t.Fatalf("expired submit commit error = %v", err)
	}
	if err := NewTaskService().ClearTaskSubmitCheckpoint(submitTask.ID, "expired-submit"); !errors.Is(err, ErrTaskWorkerLeaseLost) {
		t.Fatalf("expired checkpoint clear error = %v", err)
	}

	replacement, acquired, err := AcquireTaskWorkerLease(context.Background(), pollTask.ID, TaskWorkerStagePoll)
	if err != nil || !acquired {
		t.Fatalf("replacement lease acquired=%v err=%v", acquired, err)
	}
	if err := replacement.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestTaskWorkerLeaseAdoptsLegacyPollCursorOnce(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.Task{}); err != nil {
		t.Fatal(err)
	}
	task := &model.Task{TaskNo: GenerateTaskNo(), Status: model.TaskStatusProcessing, PollCursor: -1}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}
	lease, acquired, err := AcquireTaskWorkerLease(context.Background(), task.ID, TaskWorkerStagePoll)
	if err != nil || !acquired {
		t.Fatalf("acquire legacy poll lease = %v/%v", acquired, err)
	}
	defer func() { _ = lease.Stop() }()

	adopted, err := NewTaskService().AdoptLegacyTaskPollRound(task.ID, lease.Owner(), 7)
	if err != nil || !adopted {
		t.Fatalf("adopt legacy poll round = %v/%v", adopted, err)
	}
	current, err := NewTaskService().CurrentTaskPollRound(task.ID, lease.Owner(), 7)
	if err != nil || !current {
		t.Fatalf("adopted poll round current = %v/%v", current, err)
	}
	adopted, err = NewTaskService().AdoptLegacyTaskPollRound(task.ID, lease.Owner(), 8)
	if err != nil || adopted {
		t.Fatalf("second legacy adoption = %v/%v", adopted, err)
	}
}

func TestTaskWorkerLeaseAdoptsEnqueuedPollAfterCursorWriteLoss(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.Task{}); err != nil {
		t.Fatal(err)
	}
	task := &model.Task{TaskNo: GenerateTaskNo(), Status: model.TaskStatusProcessing, PollCursor: 3}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}
	lease, acquired, err := AcquireTaskWorkerLease(context.Background(), task.ID, TaskWorkerStagePoll)
	if err != nil || !acquired {
		t.Fatalf("acquire poll lease = %v/%v", acquired, err)
	}
	defer func() { _ = lease.Stop() }()

	adopted, err := NewTaskService().AdoptQueuedTaskPollRound(task.ID, lease.Owner(), 4)
	if err != nil || !adopted {
		t.Fatalf("adopt queued poll round = %v/%v", adopted, err)
	}
	current, err := NewTaskService().CurrentTaskPollRound(task.ID, lease.Owner(), 4)
	if err != nil || !current {
		t.Fatalf("adopted queued poll current = %v/%v", current, err)
	}
	adopted, err = NewTaskService().AdoptQueuedTaskPollRound(task.ID, lease.Owner(), 6)
	if err != nil || adopted {
		t.Fatalf("skipped queued poll adoption = %v/%v", adopted, err)
	}
}

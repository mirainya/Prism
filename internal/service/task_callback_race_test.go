package service

import (
	"errors"
	"testing"

	"github.com/mirainya/Prism/internal/model"
)

func TestTaskCallbackUpdatesDoNotReviveTerminalTask(t *testing.T) {
	setupTestDB(t)
	task := &model.Task{
		TaskNo:   GenerateTaskNo(),
		Status:   model.TaskStatusFailed,
		Progress: 100,
	}
	if err := model.DB().Create(task).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}

	service := NewTaskService()
	if err := service.UpdateTaskStatus(task.ID, model.TaskStatusProcessing, "vendor-late"); err != nil {
		t.Fatalf("update terminal status: %v", err)
	}
	if err := service.UpdateTaskProgress(task.ID, 10); err != nil {
		t.Fatalf("update terminal progress: %v", err)
	}
	if err := service.BindVendorTaskID(task.ID, "vendor-direct-late"); err != nil {
		t.Fatalf("bind terminal vendor task ID: %v", err)
	}

	var got model.Task
	if err := model.DB().First(&got, task.ID).Error; err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if got.Status != model.TaskStatusFailed || got.Progress != 100 || got.VendorTaskID != "" {
		t.Fatalf("terminal task changed: status=%s progress=%d vendor_task_id=%q",
			got.Status, got.Progress, got.VendorTaskID)
	}
}

func TestBindVendorTaskIDRejectsMismatch(t *testing.T) {
	setupTestDB(t)
	task := &model.Task{TaskNo: GenerateTaskNo(), Status: model.TaskStatusProcessing}
	if err := model.DB().Create(task).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}

	service := NewTaskService()
	if err := service.BindVendorTaskID(task.ID, "vendor-1"); err != nil {
		t.Fatalf("bind vendor task ID: %v", err)
	}
	if err := service.BindVendorTaskID(task.ID, "vendor-1"); err != nil {
		t.Fatalf("repeat vendor task ID: %v", err)
	}
	if err := service.BindVendorTaskID(task.ID, "vendor-2"); !errors.Is(err, ErrVendorTaskMismatch) {
		t.Fatalf("mismatch error = %v, want %v", err, ErrVendorTaskMismatch)
	}
}

func TestFinalizingStatusIsExposedAsProcessing(t *testing.T) {
	if got := model.TaskStatusFinalizing.Public(); got != model.TaskStatusProcessing {
		t.Fatalf("public finalizing status = %s, want %s", got, model.TaskStatusProcessing)
	}
}

func TestUploadCompletionRequiresFinalizingState(t *testing.T) {
	setupTestDB(t)
	account := &model.ChannelAccount{CurrentTasks: 1}
	if err := model.DB().Create(account).Error; err != nil {
		t.Fatalf("create account: %v", err)
	}
	task := &model.Task{TaskNo: GenerateTaskNo(), AccountID: account.ID, Status: model.TaskStatusProcessing}
	if err := model.DB().Create(task).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}

	service := NewTaskService()
	committed, err := service.CompleteTaskUpload(task.ID, map[string]any{"url": "result"}, task.Cost)
	if err != nil {
		t.Fatalf("complete unclaimed upload: %v", err)
	}
	if committed {
		t.Fatal("upload completed without finalizing claim")
	}
	ready, err := service.BeginTaskFinalization(task.ID)
	if err != nil || !ready {
		t.Fatalf("begin finalization: ready=%v err=%v", ready, err)
	}
	committed, err = service.CompleteTaskUpload(task.ID, map[string]any{"url": "result"}, task.Cost)
	if err != nil || !committed {
		t.Fatalf("complete claimed upload: committed=%v err=%v", committed, err)
	}
	if err := model.DB().First(account, account.ID).Error; err != nil {
		t.Fatalf("reload account: %v", err)
	}
	if account.CurrentTasks != 0 {
		t.Fatalf("account current_tasks = %d, want 0", account.CurrentTasks)
	}
}

func TestReleaseAccountSlotIsPersistentAndIdempotent(t *testing.T) {
	setupTestDB(t)
	account := &model.ChannelAccount{CurrentTasks: 2}
	if err := model.DB().Create(account).Error; err != nil {
		t.Fatalf("create account: %v", err)
	}
	task := &model.Task{
		TaskNo:    GenerateTaskNo(),
		AccountID: account.ID,
		Status:    model.TaskStatusProcessing,
	}
	if err := model.DB().Create(task).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}

	service := NewTaskService()
	if err := service.ReleaseAccountSlot(task.ID); err != nil {
		t.Fatalf("release account slot: %v", err)
	}
	var releasedTask model.Task
	var releasedAccount model.ChannelAccount
	if err := model.DB().First(&releasedTask, task.ID).Error; err != nil {
		t.Fatalf("reload released task: %v", err)
	}
	if err := model.DB().First(&releasedAccount, account.ID).Error; err != nil {
		t.Fatalf("reload released account: %v", err)
	}
	if !releasedTask.AccountSlotReleased {
		t.Fatal("account slot release gate was not persisted")
	}
	if releasedAccount.CurrentTasks != 1 {
		t.Fatalf("released account current_tasks = %d, want 1", releasedAccount.CurrentTasks)
	}
	if err := service.ReleaseAccountSlot(task.ID); err != nil {
		t.Fatalf("repeat account slot release: %v", err)
	}
	if _, err := service.UpdateTaskFail(task.ID, "late failure"); err != nil {
		t.Fatalf("commit terminal state after release: %v", err)
	}

	var gotTask model.Task
	var gotAccount model.ChannelAccount
	if err := model.DB().First(&gotTask, task.ID).Error; err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if err := model.DB().First(&gotAccount, account.ID).Error; err != nil {
		t.Fatalf("reload account: %v", err)
	}
	if !gotTask.AccountSlotReleased || gotTask.Status != model.TaskStatusFailed {
		t.Fatalf("task state = status %s, released %v", gotTask.Status, gotTask.AccountSlotReleased)
	}
	if gotAccount.CurrentTasks != 1 {
		t.Fatalf("account current_tasks = %d, want 1", gotAccount.CurrentTasks)
	}
}

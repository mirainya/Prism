package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mirainya/Prism/internal/model"
	"gorm.io/datatypes"
)

const callbackTestMapping = `{
	"task_id": "payload.job_id",
	"status": "payload.status",
	"progress": "payload.progress",
	"status_mapping": {
		"running": "processing",
		"failed": "fail"
	}
}`

func seedCallbackTestTask(t *testing.T, status model.TaskStatus) (*model.Channel, *model.ChannelAccount, *model.Endpoint, *model.Task) {
	t.Helper()
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.Channel{}, &model.Endpoint{}); err != nil {
		t.Fatalf("migrate callback models: %v", err)
	}

	channel := &model.Channel{
		Type:    "callback-test",
		Name:    "Callback Test",
		BaseURL: "https://upstream.example",
		Status:  1,
	}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}

	account := &model.ChannelAccount{
		ChannelID: channel.ID,
		Name:      "Callback Account",
		APIKey:    "test-key",
		Status:    1,
	}
	if err := db.Create(account).Error; err != nil {
		t.Fatalf("create account: %v", err)
	}

	endpoint := &model.Endpoint{
		ModelCode:       "callback-image",
		ChannelID:       channel.ID,
		AccountID:       account.ID,
		Protocol:        model.ProtocolCustom,
		RequestPath:     "/submit",
		RequestMethod:   "POST",
		ContentType:     "application/json",
		VendorModel:     "vendor-image",
		InteractionMode: model.ModeCallback,
		CallbackMapping: datatypes.JSON([]byte(callbackTestMapping)),
		Status:          1,
	}
	if err := db.Create(endpoint).Error; err != nil {
		t.Fatalf("create endpoint: %v", err)
	}

	task := &model.Task{
		TaskNo:       GenerateTaskNo(),
		ModelCode:    endpoint.ModelCode,
		ChannelID:    channel.ID,
		EndpointID:   endpoint.ID,
		AccountID:    account.ID,
		VendorTaskID: "vendor-expected",
		Status:       status,
		Progress:     10,
	}
	if err := db.Create(task).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}

	return channel, account, endpoint, task
}

func callbackBody(vendorTaskID, status string, progress int) map[string]any {
	return map[string]any{
		"payload": map[string]any{
			"job_id":   vendorTaskID,
			"status":   status,
			"progress": progress,
		},
	}
}

func TestHandleCallbackRejectsEndpointChannelMismatch(t *testing.T) {
	channel, _, endpoint, task := seedCallbackTestTask(t, model.TaskStatusProcessing)
	db := model.DB()

	otherChannel := &model.Channel{
		Type:    "callback-other",
		Name:    "Other Channel",
		BaseURL: "https://other.example",
		Status:  1,
	}
	if err := db.Create(otherChannel).Error; err != nil {
		t.Fatalf("create other channel: %v", err)
	}
	if err := db.Model(endpoint).Update("channel_id", otherChannel.ID).Error; err != nil {
		t.Fatalf("move endpoint to other channel: %v", err)
	}

	err := NewUnifiedService().HandleCallback(context.Background(), task, callbackBody(task.VendorTaskID, "running", 45))
	if err == nil || !strings.Contains(err.Error(), "endpoint does not belong") {
		t.Fatalf("expected endpoint/channel mismatch, got %v", err)
	}

	var got model.Task
	if err := db.First(&got, task.ID).Error; err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if got.ChannelID != channel.ID || got.Status != model.TaskStatusProcessing || got.Progress != 10 {
		t.Fatalf("task changed after rejected callback: channel=%d status=%s progress=%d", got.ChannelID, got.Status, got.Progress)
	}
}

func TestHandleCallbackRejectsMappedVendorTaskMismatch(t *testing.T) {
	_, _, _, task := seedCallbackTestTask(t, model.TaskStatusProcessing)

	err := NewUnifiedService().HandleCallback(context.Background(), task, callbackBody("vendor-other", "running", 60))
	if !errors.Is(err, ErrVendorTaskMismatch) {
		t.Fatalf("expected ErrVendorTaskMismatch, got %v", err)
	}

	var got model.Task
	if err := model.DB().First(&got, task.ID).Error; err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if got.VendorTaskID != task.VendorTaskID || got.Status != model.TaskStatusProcessing || got.Progress != 10 {
		t.Fatalf("task changed after vendor mismatch: vendor=%q status=%s progress=%d", got.VendorTaskID, got.Status, got.Progress)
	}
}

func TestHandleCallbackUpdatesProcessingProgress(t *testing.T) {
	_, _, _, task := seedCallbackTestTask(t, model.TaskStatusProcessing)

	err := NewUnifiedService().HandleCallback(context.Background(), task, callbackBody(task.VendorTaskID, "running", 42))
	if err != nil {
		t.Fatalf("handle callback: %v", err)
	}

	var got model.Task
	if err := model.DB().First(&got, task.ID).Error; err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if got.Status != model.TaskStatusProcessing || got.Progress != 42 || got.VendorTaskID != task.VendorTaskID {
		t.Fatalf("unexpected task state: vendor=%q status=%s progress=%d", got.VendorTaskID, got.Status, got.Progress)
	}
}

func TestHandleCallbackProcessingPreventsLateSubmitFailure(t *testing.T) {
	_, _, _, task := seedCallbackTestTask(t, model.TaskStatusPending)

	if err := NewUnifiedService().HandleCallback(
		context.Background(), task, callbackBody(task.VendorTaskID, "running", 25),
	); err != nil {
		t.Fatalf("handle processing callback: %v", err)
	}

	committed, err := NewTaskService().UpdateTaskSubmitFail(task.ID, "late submit transport error")
	if err != nil {
		t.Fatalf("record late submit failure: %v", err)
	}
	if committed {
		t.Fatal("late submit failure replaced an acknowledged callback task")
	}

	var got model.Task
	if err := model.DB().First(&got, task.ID).Error; err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if got.Status != model.TaskStatusProcessing || got.Progress != 25 {
		t.Fatalf("task state = %s/%d, want processing/25", got.Status, got.Progress)
	}
}

func TestHandleCallbackSuccessQueueFailureRemainsRetryable(t *testing.T) {
	_, _, _, task := seedCallbackTestTask(t, model.TaskStatusProcessing)
	queueErr := errors.New("queue unavailable")
	calls := 0
	previousEnqueue := enqueueCallbackUpload
	enqueueCallbackUpload = func(uint, string, []string, ...string) error {
		calls++
		if calls == 1 {
			return queueErr
		}
		return nil
	}
	t.Cleanup(func() { enqueueCallbackUpload = previousEnqueue })

	service := NewUnifiedService()
	err := service.HandleCallback(
		context.Background(), task, callbackBody(task.VendorTaskID, "SUCCESS", 100),
	)
	if !errors.Is(err, queueErr) {
		t.Fatalf("queue error = %v, want %v", err, queueErr)
	}

	var afterQueueError model.Task
	if err := model.DB().First(&afterQueueError, task.ID).Error; err != nil {
		t.Fatalf("reload task after queue error: %v", err)
	}
	if afterQueueError.Status != model.TaskStatusFinalizing || afterQueueError.Progress != 100 || afterQueueError.Refunded {
		t.Fatalf("task after queue error = status %s, progress %d, refunded %v",
			afterQueueError.Status, afterQueueError.Progress, afterQueueError.Refunded)
	}

	if err := service.HandleCallback(
		context.Background(), task, callbackBody(task.VendorTaskID, "failed", 100),
	); err != nil {
		t.Fatalf("handle late failure callback: %v", err)
	}
	if err := service.HandleCallback(
		context.Background(), task, callbackBody(task.VendorTaskID, "SUCCESS", 100),
	); err != nil {
		t.Fatalf("retry success callback: %v", err)
	}
	if calls != 2 {
		t.Fatalf("upload enqueue calls = %d, want 2", calls)
	}

	var got model.Task
	if err := model.DB().First(&got, task.ID).Error; err != nil {
		t.Fatalf("reload finalizing task: %v", err)
	}
	if got.Status != model.TaskStatusFinalizing || got.ErrorMessage != "" {
		t.Fatalf("late failure changed task: status=%s error=%q", got.Status, got.ErrorMessage)
	}
}

func TestHandleCallbackRejectsUnknownStatus(t *testing.T) {
	_, _, _, task := seedCallbackTestTask(t, model.TaskStatusProcessing)

	err := NewUnifiedService().HandleCallback(
		context.Background(), task, callbackBody(task.VendorTaskID, "mystery", 77),
	)
	if !errors.Is(err, ErrInvalidCallbackStatus) {
		t.Fatalf("callback error = %v, want %v", err, ErrInvalidCallbackStatus)
	}

	var got model.Task
	if err := model.DB().First(&got, task.ID).Error; err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if got.Status != model.TaskStatusProcessing || got.Progress != 10 {
		t.Fatalf("unknown callback changed task: status=%s progress=%d", got.Status, got.Progress)
	}
}

func TestHandleCallbackTerminalTaskIsNoOp(t *testing.T) {
	for _, status := range []model.TaskStatus{
		model.TaskStatusSuccess,
		model.TaskStatusFailed,
		model.TaskStatusCancelled,
	} {
		t.Run(string(status), func(t *testing.T) {
			_, _, _, task := seedCallbackTestTask(t, status)

			err := NewUnifiedService().HandleCallback(context.Background(), task, callbackBody("vendor-other", "failed", 99))
			if err != nil {
				t.Fatalf("terminal callback should be acknowledged: %v", err)
			}

			var got model.Task
			if err := model.DB().First(&got, task.ID).Error; err != nil {
				t.Fatalf("reload task: %v", err)
			}
			if got.Status != status || got.Progress != 10 || got.VendorTaskID != task.VendorTaskID {
				t.Fatalf("terminal task changed: vendor=%q status=%s progress=%d", got.VendorTaskID, got.Status, got.Progress)
			}
		})
	}
}

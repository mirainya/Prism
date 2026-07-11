package service

import (
	"errors"
	"testing"

	"github.com/mirainya/Prism/internal/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

func TestReserveInitialCapabilityTaskCommitsBillingAccountAndTaskTogether(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.Channel{}); err != nil {
		t.Fatalf("migrate channel: %v", err)
	}
	user, token := seedBillingAccount(t, decimal.NewFromInt(10), decimal.NewFromInt(10))
	channel := &model.Channel{Type: "atomic-reservation-success", Status: 1}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	account := &model.ChannelAccount{ChannelID: channel.ID, Status: 1, Weight: 10}
	if err := db.Create(account).Error; err != nil {
		t.Fatalf("create account: %v", err)
	}
	endpoint := model.Endpoint{
		BaseModel: model.BaseModel{ID: 31},
		ChannelID: channel.ID,
		AccountID: account.ID,
	}
	cost := decimal.NewFromInt(2)

	task, chosen, gotChannel, selected, err := NewUnifiedService().reserveInitialCapabilityTask(
		&InvokeRequest{
			UserID:     user.ID,
			TokenID:    token.ID,
			Capability: "image-test",
			Params:     map[string]any{"prompt": "test"},
		},
		[]model.Endpoint{endpoint},
		cost,
	)
	if err != nil {
		t.Fatalf("reserve initial task: %v", err)
	}
	if chosen.ID != endpoint.ID || gotChannel.ID != channel.ID || selected.ID != account.ID {
		t.Fatalf("selection = endpoint %d channel %d account %d", chosen.ID, gotChannel.ID, selected.ID)
	}
	if task.EndpointID != endpoint.ID || task.ChannelID != channel.ID || task.AccountID != account.ID {
		t.Fatalf("task assignment = endpoint %d channel %d account %d", task.EndpointID, task.ChannelID, task.AccountID)
	}

	var gotAccount model.ChannelAccount
	if err := db.First(&gotAccount, account.ID).Error; err != nil {
		t.Fatalf("reload account: %v", err)
	}
	if gotAccount.CurrentTasks != 1 {
		t.Fatalf("current_tasks = %d, want 1", gotAccount.CurrentTasks)
	}
	assertBillingBalances(t, user.ID, token.ID, decimal.NewFromInt(8), decimal.NewFromInt(8), cost)
	assertBillingLogCount(t, task.TaskNo+":reserve", 1)

	committed, err := NewTaskService().UpdateTaskFail(task.ID, "test failure")
	if err != nil {
		t.Fatalf("fail reserved task: %v", err)
	}
	if !committed {
		t.Fatal("reserved task failure was not committed")
	}
	assertBillingBalances(t, user.ID, token.ID, decimal.NewFromInt(10), decimal.NewFromInt(10), decimal.Zero)
	assertBillingLogCount(t, task.TaskNo, 1)
	if err := db.First(&gotAccount, account.ID).Error; err != nil {
		t.Fatalf("reload released account: %v", err)
	}
	if gotAccount.CurrentTasks != 0 {
		t.Fatalf("released current_tasks = %d, want 0", gotAccount.CurrentTasks)
	}
}

func TestReserveInitialCapabilityTaskRollsBackBillingWhenNoAccountExists(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.Channel{}); err != nil {
		t.Fatalf("migrate channel: %v", err)
	}
	user, token := seedBillingAccount(t, decimal.NewFromInt(10), decimal.NewFromInt(10))
	channel := &model.Channel{Type: "atomic-reservation-no-account", Status: 1}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	account := &model.ChannelAccount{
		ChannelID:    channel.ID,
		Status:       1,
		MaxTasks:     1,
		CurrentTasks: 1,
	}
	if err := db.Create(account).Error; err != nil {
		t.Fatalf("create account: %v", err)
	}
	endpoint := model.Endpoint{
		BaseModel: model.BaseModel{ID: 32},
		ChannelID: channel.ID,
		AccountID: account.ID,
	}

	_, _, _, _, err := NewUnifiedService().reserveInitialCapabilityTask(
		&InvokeRequest{
			UserID:     user.ID,
			TokenID:    token.ID,
			Capability: "image-test",
			Params:     map[string]any{"prompt": "test"},
		},
		[]model.Endpoint{endpoint},
		decimal.NewFromInt(3),
	)
	if !errors.Is(err, errNoAvailableAccount) {
		t.Fatalf("reserve without account error = %v, want %v", err, errNoAvailableAccount)
	}

	assertBillingBalances(t, user.ID, token.ID, decimal.NewFromInt(10), decimal.NewFromInt(10), decimal.Zero)
	assertInitialReservationRollback(t, db, account.ID, 1)
}

func TestReserveInitialCapabilityTaskRollsBackWhenTaskCreationFails(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.Channel{}); err != nil {
		t.Fatalf("migrate channel: %v", err)
	}
	user, token := seedBillingAccount(t, decimal.NewFromInt(10), decimal.NewFromInt(10))
	channel := &model.Channel{Type: "atomic-reservation-create-fail", Status: 1}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	account := &model.ChannelAccount{ChannelID: channel.ID, Status: 1, Weight: 10}
	if err := db.Create(account).Error; err != nil {
		t.Fatalf("create account: %v", err)
	}
	endpoint := model.Endpoint{
		BaseModel: model.BaseModel{ID: 33},
		ChannelID: channel.ID,
		AccountID: account.ID,
	}

	_, _, _, _, err := NewUnifiedService().reserveInitialCapabilityTask(
		&InvokeRequest{
			UserID:     user.ID,
			TokenID:    token.ID,
			Capability: "image-test",
			Params:     map[string]any{"invalid": make(chan int)},
		},
		[]model.Endpoint{endpoint},
		decimal.NewFromInt(3),
	)
	if err == nil {
		t.Fatal("reserve with invalid task params unexpectedly succeeded")
	}

	assertBillingBalances(t, user.ID, token.ID, decimal.NewFromInt(10), decimal.NewFromInt(10), decimal.Zero)
	assertInitialReservationRollback(t, db, account.ID, 0)
}

func assertInitialReservationRollback(t *testing.T, db *gorm.DB, accountID uint, expectedCurrentTasks int) {
	t.Helper()
	var account model.ChannelAccount
	if err := db.First(&account, accountID).Error; err != nil {
		t.Fatalf("reload account: %v", err)
	}
	if account.CurrentTasks != expectedCurrentTasks {
		t.Fatalf("current_tasks = %d, want %d", account.CurrentTasks, expectedCurrentTasks)
	}
	var taskCount int64
	if err := db.Model(&model.Task{}).Count(&taskCount).Error; err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	if taskCount != 0 {
		t.Fatalf("task count = %d, want 0", taskCount)
	}
	var logCount int64
	if err := db.Model(&model.BillingLog{}).Count(&logCount).Error; err != nil {
		t.Fatalf("count billing logs: %v", err)
	}
	if logCount != 0 {
		t.Fatalf("billing log count = %d, want 0", logCount)
	}
}

func TestSelectAndAssignAccountForEndpointRejectsTerminalTask(t *testing.T) {
	db := setupTestDB(t)
	account := &model.ChannelAccount{
		ChannelID: 7,
		Status:    1,
		Weight:    10,
	}
	if err := db.Create(account).Error; err != nil {
		t.Fatalf("create account: %v", err)
	}
	task := &model.Task{
		TaskNo:              GenerateTaskNo(),
		ChannelID:           account.ChannelID,
		AccountID:           account.ID,
		Status:              model.TaskStatusFailed,
		AccountSlotReleased: true,
	}
	if err := db.Create(task).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}
	ep := &model.Endpoint{
		BaseModel: model.BaseModel{ID: 19},
		ChannelID: account.ChannelID,
		AccountID: account.ID,
	}

	_, err := NewUnifiedService().selectAndAssignAccountForEndpoint(task.ID, ep, nil)
	if !errors.Is(err, ErrTaskNotExecutable) {
		t.Fatalf("select terminal task error = %v, want %v", err, ErrTaskNotExecutable)
	}

	var gotAccount model.ChannelAccount
	if err := db.First(&gotAccount, account.ID).Error; err != nil {
		t.Fatalf("reload account: %v", err)
	}
	if gotAccount.CurrentTasks != 0 {
		t.Fatalf("current_tasks = %d, want 0", gotAccount.CurrentTasks)
	}
}

func TestSelectAndAssignAccountForEndpointReacquiresReleasedAccountOnce(t *testing.T) {
	db := setupTestDB(t)
	account := &model.ChannelAccount{
		ChannelID: 8,
		Status:    1,
		Weight:    10,
	}
	if err := db.Create(account).Error; err != nil {
		t.Fatalf("create account: %v", err)
	}
	task := &model.Task{
		TaskNo:              GenerateTaskNo(),
		ChannelID:           account.ChannelID,
		EndpointID:          11,
		AccountID:           account.ID,
		Status:              model.TaskStatusProcessing,
		AccountSlotReleased: true,
	}
	if err := db.Create(task).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}
	ep := &model.Endpoint{
		BaseModel: model.BaseModel{ID: 20},
		ChannelID: account.ChannelID,
		AccountID: account.ID,
	}
	svc := NewUnifiedService()

	selected, err := svc.selectAndAssignAccountForEndpoint(task.ID, ep, nil)
	if err != nil {
		t.Fatalf("select released account: %v", err)
	}
	if selected.ID != account.ID {
		t.Fatalf("selected account = %d, want %d", selected.ID, account.ID)
	}
	if _, err := svc.selectAndAssignAccountForEndpoint(task.ID, ep, nil); !errors.Is(err, ErrTaskNotExecutable) {
		t.Fatalf("second selection error = %v, want %v", err, ErrTaskNotExecutable)
	}

	var gotTask model.Task
	if err := db.First(&gotTask, task.ID).Error; err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if gotTask.AccountSlotReleased {
		t.Fatal("account slot gate remained released")
	}
	if gotTask.EndpointID != ep.ID || gotTask.ChannelID != ep.ChannelID || gotTask.AccountID != account.ID {
		t.Fatalf("task assignment = endpoint %d channel %d account %d", gotTask.EndpointID, gotTask.ChannelID, gotTask.AccountID)
	}

	var gotAccount model.ChannelAccount
	if err := db.First(&gotAccount, account.ID).Error; err != nil {
		t.Fatalf("reload account: %v", err)
	}
	if gotAccount.CurrentTasks != 1 {
		t.Fatalf("current_tasks = %d, want 1", gotAccount.CurrentTasks)
	}
	if err := svc.ensureTaskAccountExecutable(task.ID, ep, account.ID); err != nil {
		t.Fatalf("assigned task should be executable: %v", err)
	}
}

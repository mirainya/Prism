package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider"
	"github.com/mirainya/Prism/pkg/config"
	"github.com/shopspring/decimal"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func TestInvokeRecordsFailedCallWhenNoEndpointIsAvailable(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.Endpoint{}); err != nil {
		t.Fatalf("migrate endpoint: %v", err)
	}
	request := &InvokeRequest{
		UserID: 18, TokenID: 29, Capability: "missing-capability",
		Endpoint: "/v1/capabilities/missing-capability", Operation: "capability.invoke",
		Params: map[string]any{"prompt": "test"},
	}

	_, err := NewUnifiedService().Invoke(context.Background(), request)
	if err == nil {
		t.Fatal("invoke without endpoint unexpectedly succeeded")
	}
	if !strings.HasPrefix(request.CallID, "call_") || request.RequestID == "" {
		t.Fatalf("generated identity = call %q request %q", request.CallID, request.RequestID)
	}

	var call model.APICall
	if err := db.First(&call, "id = ?", request.CallID).Error; err != nil {
		t.Fatalf("load failed call: %v", err)
	}
	if call.Status != model.APICallStatusFailed || call.ErrorCode != "model_unavailable" ||
		call.RequestID != request.RequestID || call.Endpoint != request.Endpoint ||
		call.ResourceType != "" || call.ResourceID != "" {
		t.Fatalf("failed call = %#v", call)
	}
}

func TestInvokeRejectsUnsafeCallbackURLAndRecordsBadRequest(t *testing.T) {
	db := setupTestDB(t)
	request := &InvokeRequest{
		UserID: 18, TokenID: 29, Capability: "image-test",
		Endpoint: "/v1/capabilities/image-test", Operation: "capability.invoke",
		CallbackURL: "http://127.0.0.1/internal", Params: map[string]any{"prompt": "test"},
	}

	_, err := NewUnifiedService().Invoke(context.Background(), request)
	if !errors.Is(err, ErrInvalidCallbackURL) {
		t.Fatalf("invoke error = %v, want ErrInvalidCallbackURL", err)
	}
	var call model.APICall
	if err := db.First(&call, "id = ?", request.CallID).Error; err != nil {
		t.Fatal(err)
	}
	if call.Status != model.APICallStatusFailed || call.HTTPStatus != http.StatusBadRequest ||
		call.ErrorType != "invalid_request_error" || call.ErrorCode != "invalid_callback_url" {
		t.Fatalf("invalid callback call = %#v", call)
	}
}

func TestFindEndpointsDoesNotIgnoreUnknownRequestedChannel(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.Channel{}, &model.Endpoint{}); err != nil {
		t.Fatal(err)
	}
	channel := &model.Channel{Type: "available-channel", Status: 1}
	if err := db.Create(channel).Error; err != nil {
		t.Fatal(err)
	}
	endpoint := &model.Endpoint{ChannelID: channel.ID, ModelCode: "image-test", Status: 1}
	if err := db.Create(endpoint).Error; err != nil {
		t.Fatal(err)
	}

	_, err := NewUnifiedService().findEndpointsForCapability(&InvokeRequest{
		Capability: "image-test", Channel: "missing-channel",
	})
	if err == nil || !strings.Contains(err.Error(), "no available channel") {
		t.Fatalf("find endpoints error = %v, want unavailable channel", err)
	}
}

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
		BaseModel:  model.BaseModel{ID: 31},
		ChannelID:  channel.ID,
		AccountID:  account.ID,
		InputPrice: decimal.NewFromInt(2),
	}
	cost := decimal.NewFromInt(2)
	invokeReq := &InvokeRequest{
		UserID:     user.ID,
		TokenID:    token.ID,
		CallID:     "call_capability_reservation",
		RequestID:  "request-capability-reservation",
		Endpoint:   "/v1/images/generations",
		Operation:  "images.generate",
		Capability: "image-test",
		Model:      "image-model",
		Params:     map[string]any{"prompt": "test"},
	}

	task, chosen, gotChannel, selected, err := NewUnifiedService().reserveInitialCapabilityTask(
		invokeReq,
		[]model.Endpoint{endpoint},
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
	if task.CallID != invokeReq.CallID {
		t.Fatalf("task call_id = %q, want %q", task.CallID, invokeReq.CallID)
	}
	var call model.APICall
	if err := db.First(&call, "id = ?", invokeReq.CallID).Error; err != nil {
		t.Fatalf("load api call: %v", err)
	}
	if call.RequestID != invokeReq.RequestID || call.Endpoint != invokeReq.Endpoint ||
		call.Operation != invokeReq.Operation || call.Model != invokeReq.Model ||
		call.Status != model.APICallStatusReceived || call.ResourceType != "task" ||
		call.ResourceID != task.TaskNo || !call.ReservedAmount.Equal(cost) {
		t.Fatalf("unexpected api call: %#v", call)
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
	assertCapabilityBillingContext(t, task.TaskNo+":reserve", task.CallID, model.BillingPhaseReserve)

	committed, err := NewTaskService().UpdateTaskFail(task.ID, "test failure")
	if err != nil {
		t.Fatalf("fail reserved task: %v", err)
	}
	if !committed {
		t.Fatal("reserved task failure was not committed")
	}
	assertBillingBalances(t, user.ID, token.ID, decimal.NewFromInt(10), decimal.NewFromInt(10), decimal.Zero)
	assertBillingLogCount(t, task.TaskNo, 1)
	assertCapabilityBillingContext(t, task.TaskNo, task.CallID, model.BillingPhaseRefund)
	if err := db.First(&call, "id = ?", task.CallID).Error; err != nil {
		t.Fatalf("reload failed api call: %v", err)
	}
	if call.Status != model.APICallStatusFailed || call.ErrorCode != "task_failed" ||
		!call.RefundedAmount.Equal(cost) || !call.FinalCost.IsZero() {
		t.Fatalf("failed api call = %#v", call)
	}
	var failedTask model.Task
	if err := db.First(&failedTask, task.ID).Error; err != nil {
		t.Fatalf("reload failed task: %v", err)
	}
	if len(failedTask.RequestParams) != 0 || len(failedTask.MappedParams) != 0 {
		t.Fatalf("terminal task retained params: request=%s mapped=%s", failedTask.RequestParams, failedTask.MappedParams)
	}
	if err := db.First(&gotAccount, account.ID).Error; err != nil {
		t.Fatalf("reload released account: %v", err)
	}
	if gotAccount.CurrentTasks != 0 {
		t.Fatalf("released current_tasks = %d, want 0", gotAccount.CurrentTasks)
	}
}

func TestCapabilityTaskSuccessSettlesAndCompletesCallOnce(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.Channel{}); err != nil {
		t.Fatalf("migrate channel: %v", err)
	}
	user, token := seedBillingAccount(t, decimal.NewFromInt(10), decimal.NewFromInt(10))
	channel := &model.Channel{Type: "capability-success", Status: 1}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	account := &model.ChannelAccount{ChannelID: channel.ID, Status: 1, Weight: 10}
	if err := db.Create(account).Error; err != nil {
		t.Fatalf("create account: %v", err)
	}
	endpoint := model.Endpoint{
		BaseModel:       model.BaseModel{ID: 34},
		ChannelID:       channel.ID,
		AccountID:       account.ID,
		InteractionMode: model.ModeSync,
		InputPrice:      decimal.NewFromInt(3),
	}
	reserved := decimal.NewFromInt(3)
	actual := decimal.NewFromInt(2)
	request := &InvokeRequest{
		UserID: user.ID, TokenID: token.ID,
		CallID: "call_capability_success", RequestID: "request-capability-success",
		Capability: "image-test", Params: map[string]any{"prompt": "test"},
	}
	task, _, _, _, err := NewUnifiedService().reserveInitialCapabilityTask(
		request, []model.Endpoint{endpoint},
	)
	if err != nil {
		t.Fatalf("reserve task: %v", err)
	}

	committed, err := NewTaskService().UpdateTaskSuccess(
		task.ID,
		map[string]any{"url": "https://example.test/result.png"},
		actual,
	)
	if err != nil || !committed {
		t.Fatalf("complete task = committed %v, err %v", committed, err)
	}
	committed, err = NewTaskService().UpdateTaskSuccess(task.ID, map[string]any{"url": "ignored"}, actual)
	if err != nil || committed {
		t.Fatalf("repeat completion = committed %v, err %v", committed, err)
	}

	assertBillingBalances(t, user.ID, token.ID, decimal.NewFromInt(8), decimal.NewFromInt(8), actual)
	assertBillingLogCount(t, task.TaskNo+":settle", 1)
	assertCapabilityBillingContext(t, task.TaskNo+":settle", task.CallID, model.BillingPhaseSettle)
	var call model.APICall
	if err := db.First(&call, "id = ?", task.CallID).Error; err != nil {
		t.Fatalf("load completed call: %v", err)
	}
	if call.Status != model.APICallStatusCompleted || !call.ReservedAmount.Equal(reserved) ||
		!call.FinalCost.Equal(actual) || !call.RefundedAmount.Equal(reserved.Sub(actual)) {
		t.Fatalf("completed call amounts/status = %#v", call)
	}
	var completedTask model.Task
	if err := db.First(&completedTask, task.ID).Error; err != nil {
		t.Fatalf("load completed task: %v", err)
	}
	if completedTask.Status != model.TaskStatusSuccess || !completedTask.Cost.Equal(actual) ||
		len(completedTask.RequestParams) != 0 || len(completedTask.MappedParams) != 0 {
		t.Fatalf("completed task = %#v", completedTask)
	}
}

func TestCapabilityReservationUsesActuallySelectedEndpointPrice(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.Channel{}); err != nil {
		t.Fatalf("migrate channel: %v", err)
	}
	user, token := seedBillingAccount(t, decimal.NewFromInt(10), decimal.NewFromInt(10))
	channel := &model.Channel{Type: "selected-endpoint-price", Status: 1}
	if err := db.Create(channel).Error; err != nil {
		t.Fatal(err)
	}
	fullAccount := &model.ChannelAccount{ChannelID: channel.ID, Status: 1, MaxTasks: 1, CurrentTasks: 1}
	availableAccount := &model.ChannelAccount{ChannelID: channel.ID, Status: 1, Weight: 10}
	if err := db.Create(fullAccount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(availableAccount).Error; err != nil {
		t.Fatal(err)
	}
	unavailable := model.Endpoint{
		BaseModel: model.BaseModel{ID: 35}, ChannelID: channel.ID, AccountID: fullAccount.ID,
		ModelCode: "expensive", VendorModel: "vendor-expensive", InputPrice: decimal.NewFromInt(5),
	}
	selected := model.Endpoint{
		BaseModel: model.BaseModel{ID: 36}, ChannelID: channel.ID, AccountID: availableAccount.ID,
		ModelCode: "affordable", VendorModel: "vendor-affordable", InputPrice: decimal.NewFromInt(2),
	}
	request := &InvokeRequest{
		UserID: user.ID, TokenID: token.ID, CallID: "call_selected_price", RequestID: "request-selected-price",
		Capability: "image-test", Params: map[string]any{"prompt": "test"},
	}

	task, chosen, _, _, err := NewUnifiedService().reserveInitialCapabilityTask(
		request, []model.Endpoint{unavailable, selected},
	)
	if err != nil {
		t.Fatalf("reserve selected endpoint: %v", err)
	}
	if chosen.ID != selected.ID || !task.Cost.Equal(selected.InputPrice) {
		t.Fatalf("chosen endpoint/cost = %d/%s", chosen.ID, task.Cost)
	}
	assertBillingBalances(t, user.ID, token.ID, decimal.NewFromInt(8), decimal.NewFromInt(8), selected.InputPrice)
	var call model.APICall
	if err := db.First(&call, "id = ?", task.CallID).Error; err != nil {
		t.Fatal(err)
	}
	if !call.ReservedAmount.Equal(selected.InputPrice) {
		t.Fatalf("reserved amount = %s, want %s", call.ReservedAmount, selected.InputPrice)
	}
	var reserveLog model.BillingLog
	if err := db.Where("idempotent_key = ?", task.TaskNo+":reserve").First(&reserveLog).Error; err != nil {
		t.Fatal(err)
	}
	var snapshot map[string]any
	if err := json.Unmarshal(reserveLog.PricingSnapshot, &snapshot); err != nil {
		t.Fatalf("decode pricing snapshot: %v", err)
	}
	if snapshot["endpoint_id"] != float64(selected.ID) || snapshot["reserved_cost"] != selected.InputPrice.String() {
		t.Fatalf("pricing snapshot = %#v", snapshot)
	}
}

func TestSynchronousFallbackSettlesUsingSuccessfulEndpointPrice(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.Channel{}, &model.Endpoint{}); err != nil {
		t.Fatalf("migrate capability routes: %v", err)
	}
	previousConfig := config.C
	config.C = &config.Config{}
	provider.InitHTTPClient()
	t.Cleanup(func() { config.C = previousConfig })

	firstCalls := 0
	firstServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		firstCalls++
		http.Error(w, `{"error":{"message":"temporary failure"}}`, http.StatusInternalServerError)
	}))
	defer firstServer.Close()
	secondCalls := 0
	secondServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		secondCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"https://result.example/fallback.png"}`))
	}))
	defer secondServer.Close()

	user, token := seedBillingAccount(t, decimal.NewFromInt(10), decimal.NewFromInt(10))
	firstChannel := &model.Channel{Type: "fallback-first", BaseURL: firstServer.URL, Status: 1}
	secondChannel := &model.Channel{Type: "fallback-second", BaseURL: secondServer.URL, Status: 1}
	if err := db.Create(firstChannel).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(secondChannel).Error; err != nil {
		t.Fatal(err)
	}
	firstAccount := &model.ChannelAccount{ChannelID: firstChannel.ID, Status: 1, Weight: 10}
	secondAccount := &model.ChannelAccount{ChannelID: secondChannel.ID, Status: 1, Weight: 10}
	if err := db.Create(firstAccount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(secondAccount).Error; err != nil {
		t.Fatal(err)
	}
	mapping := datatypes.JSON(`{"output_url":"url"}`)
	firstEndpoint := model.Endpoint{
		ModelCode: "image-fallback", ChannelID: firstChannel.ID, AccountID: firstAccount.ID,
		VendorModel: "vendor-first", InteractionMode: model.ModeSync, Status: 1,
		RequestPath: "/generate", RequestMethod: http.MethodPost, ContentType: "application/json",
		ResponseMapping: mapping, InputPrice: decimal.RequireFromString("5.12345678"),
	}
	secondEndpoint := model.Endpoint{
		ModelCode: "image-fallback", ChannelID: secondChannel.ID, AccountID: secondAccount.ID,
		VendorModel: "vendor-second", InteractionMode: model.ModeSync, Status: 1,
		RequestPath: "/generate", RequestMethod: http.MethodPost, ContentType: "application/json",
		ResponseMapping: mapping, InputPrice: decimal.RequireFromString("2.12345678"),
	}
	if err := db.Create(&firstEndpoint).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&secondEndpoint).Error; err != nil {
		t.Fatal(err)
	}
	request := &InvokeRequest{
		UserID: user.ID, TokenID: token.ID, CallID: "call_sync_fallback", RequestID: "request-sync-fallback",
		Capability: "image-fallback", Model: "image-fallback", Params: map[string]any{"prompt": "test"},
	}
	task, chosen, channel, account, err := NewUnifiedService().reserveInitialCapabilityTask(
		request, []model.Endpoint{firstEndpoint, secondEndpoint},
	)
	if err != nil {
		t.Fatalf("reserve fallback task: %v", err)
	}
	if chosen.ID != firstEndpoint.ID || !task.Cost.Equal(firstEndpoint.InputPrice) {
		t.Fatalf("initial route/cost = %d/%s", chosen.ID, task.Cost)
	}
	response, err := NewUnifiedService().executeSyncWithFallback(
		context.Background(), task, request, []model.Endpoint{firstEndpoint, secondEndpoint},
		chosen, &channel, account,
	)
	if err != nil {
		t.Fatalf("execute fallback task: %v", err)
	}
	if response.Status != string(model.TaskStatusSuccess) || firstCalls != 1 || secondCalls != 1 {
		t.Fatalf("fallback response/calls = %#v / %d / %d", response, firstCalls, secondCalls)
	}

	var storedTask model.Task
	var call model.APICall
	if err := db.First(&storedTask, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&call, "id = ?", task.CallID).Error; err != nil {
		t.Fatal(err)
	}
	if storedTask.EndpointID != secondEndpoint.ID || !storedTask.Cost.Equal(secondEndpoint.InputPrice) {
		t.Fatalf("successful route/cost = %d/%s", storedTask.EndpointID, storedTask.Cost)
	}
	if !call.ReservedAmount.Equal(firstEndpoint.InputPrice) || !call.FinalCost.Equal(secondEndpoint.InputPrice) ||
		!call.RefundedAmount.Equal(firstEndpoint.InputPrice.Sub(secondEndpoint.InputPrice)) {
		t.Fatalf("fallback call amounts = %s/%s/%s", call.ReservedAmount, call.FinalCost, call.RefundedAmount)
	}
	expectedBalance := decimal.NewFromInt(10).Sub(secondEndpoint.InputPrice)
	assertBillingBalances(t, user.ID, token.ID, expectedBalance, expectedBalance, secondEndpoint.InputPrice)

	attempts := loadCallAttemptsForServiceTest(t, db, task.CallID)
	if len(attempts) != 2 || attempts[0].Status != model.APICallAttemptStatusFailed ||
		attempts[1].Status != model.APICallAttemptStatusCompleted || call.FinalAttemptID != attempts[1].ID {
		t.Fatalf("fallback attempts = %#v", attempts)
	}
	assertCapabilityBillingContext(t, task.TaskNo+":reserve", task.CallID, model.BillingPhaseReserve)
	assertCapabilityBillingContext(t, task.TaskNo+":settle", task.CallID, model.BillingPhaseSettle)
	var settleLog model.BillingLog
	if err := db.Where("idempotent_key = ?", task.TaskNo+":settle").First(&settleLog).Error; err != nil {
		t.Fatal(err)
	}
	if settleLog.AttemptID != attempts[1].ID {
		t.Fatalf("settlement attempt = %d, want %d", settleLog.AttemptID, attempts[1].ID)
	}
	var snapshot map[string]any
	if err := json.Unmarshal(settleLog.PricingSnapshot, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot["endpoint_id"] != float64(secondEndpoint.ID) || snapshot["reserved_cost"] != secondEndpoint.InputPrice.String() {
		t.Fatalf("settlement pricing snapshot = %#v", snapshot)
	}
}

func TestAsyncSynchronousCapabilityUsesDurableQueue(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.Channel{}, &model.Endpoint{}); err != nil {
		t.Fatalf("migrate capability routes: %v", err)
	}
	previousConfig := config.C
	config.C = &config.Config{}
	provider.InitHTTPClient()
	t.Cleanup(func() { config.C = previousConfig })
	previousEnqueue := enqueueCapabilityTask
	var queuedTaskID uint
	enqueueCapabilityTask = func(taskID uint) error {
		queuedTaskID = taskID
		return nil
	}
	t.Cleanup(func() { enqueueCapabilityTask = previousEnqueue })

	var upstreamCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"https://result.example/async.png"}`))
	}))
	defer server.Close()

	user, token := seedBillingAccount(t, decimal.NewFromInt(10), decimal.NewFromInt(10))
	channel := &model.Channel{Type: "async-sync", BaseURL: server.URL, Status: 1}
	if err := db.Create(channel).Error; err != nil {
		t.Fatal(err)
	}
	account := &model.ChannelAccount{ChannelID: channel.ID, Status: 1, Weight: 10}
	if err := db.Create(account).Error; err != nil {
		t.Fatal(err)
	}
	endpoint := model.Endpoint{
		ModelCode: "image-async-sync", ChannelID: channel.ID, AccountID: account.ID,
		VendorModel: "vendor-async-sync", InteractionMode: model.ModeSync, Status: 1,
		RequestPath: "/generate", RequestMethod: http.MethodPost, ContentType: "application/json",
		ResponseMapping: datatypes.JSON(`{"output_url":"url"}`), InputPrice: decimal.NewFromInt(1),
	}
	if err := db.Create(&endpoint).Error; err != nil {
		t.Fatal(err)
	}

	response, err := NewUnifiedService().Invoke(context.Background(), &InvokeRequest{
		UserID: user.ID, TokenID: token.ID, Capability: endpoint.ModelCode, Model: endpoint.ModelCode,
		Params: map[string]any{"prompt": "test"}, Async: true,
	})
	if err != nil {
		t.Fatalf("invoke asynchronous synchronous capability: %v", err)
	}
	if response.Status != string(model.TaskStatusPending) {
		t.Fatalf("initial asynchronous response = %#v", response)
	}

	var task model.Task
	if err := db.Where("task_no = ?", response.TaskID).First(&task).Error; err != nil {
		t.Fatal(err)
	}
	if upstreamCalls.Load() != 0 || queuedTaskID != task.ID || task.Status != model.TaskStatusPending ||
		len(task.SubmitCheckpoint) != 0 || task.WorkerLeaseOwner != "" {
		t.Fatalf("queued task = queue ID %d calls %d status %s checkpoint %q lease %q",
			queuedTaskID, upstreamCalls.Load(), task.Status, task.SubmitCheckpoint, task.WorkerLeaseOwner)
	}
}

func TestInitialEnqueueFailureKeepsDurableTaskIntent(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.Channel{}, &model.Endpoint{}); err != nil {
		t.Fatal(err)
	}
	user, token := seedBillingAccount(t, decimal.NewFromInt(10), decimal.NewFromInt(10))
	channel := &model.Channel{Type: "durable-enqueue", Status: 1}
	if err := db.Create(channel).Error; err != nil {
		t.Fatal(err)
	}
	account := &model.ChannelAccount{ChannelID: channel.ID, Status: 1, Weight: 10}
	if err := db.Create(account).Error; err != nil {
		t.Fatal(err)
	}
	endpoint := &model.Endpoint{
		ModelCode: "durable-enqueue-model", ChannelID: channel.ID, AccountID: account.ID,
		InteractionMode: model.ModePoll, Status: 1, InputPrice: decimal.NewFromInt(2),
	}
	if err := db.Create(endpoint).Error; err != nil {
		t.Fatal(err)
	}
	previousEnqueue := enqueueCapabilityTask
	enqueueCapabilityTask = func(uint) error { return errors.New("redis unavailable") }
	t.Cleanup(func() { enqueueCapabilityTask = previousEnqueue })

	response, err := NewUnifiedService().Invoke(context.Background(), &InvokeRequest{
		UserID: user.ID, TokenID: token.ID, Capability: endpoint.ModelCode, Model: endpoint.ModelCode,
		Params: map[string]any{"prompt": "test"},
	})
	if err != nil {
		t.Fatalf("invoke returned enqueue error: %v", err)
	}
	if response.Status != string(model.TaskStatusPending) {
		t.Fatalf("response status = %q, want pending", response.Status)
	}

	var task model.Task
	if err := db.Where("task_no = ?", response.TaskID).First(&task).Error; err != nil {
		t.Fatal(err)
	}
	var call model.APICall
	if err := db.First(&call, "id = ?", response.CallID).Error; err != nil {
		t.Fatal(err)
	}
	var storedToken model.Token
	if err := db.First(&storedToken, token.ID).Error; err != nil {
		t.Fatal(err)
	}
	var storedAccount model.ChannelAccount
	if err := db.First(&storedAccount, account.ID).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != model.TaskStatusPending || task.Refunded ||
		call.Status != model.APICallStatusReceived || !call.ReservedAmount.Equal(endpoint.InputPrice) ||
		!storedToken.Balance.Equal(decimal.NewFromInt(8)) || storedAccount.CurrentTasks != 1 {
		t.Fatalf("durable intent = task %#v call %#v token balance %s current_tasks %d",
			task, call, storedToken.Balance, storedAccount.CurrentTasks)
	}
}

func TestSynchronousFallbackLedgerFailureQueuesCheckpointRecovery(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.Channel{}, &model.Endpoint{}); err != nil {
		t.Fatalf("migrate capability routes: %v", err)
	}
	previousConfig := config.C
	config.C = &config.Config{}
	provider.InitHTTPClient()
	t.Cleanup(func() { config.C = previousConfig })

	firstCalls := 0
	firstServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		firstCalls++
		http.Error(w, `{"error":{"message":"temporary failure"}}`, http.StatusInternalServerError)
	}))
	defer firstServer.Close()
	secondCalls := 0
	secondServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		secondCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"https://result.example/recover.png"}`))
	}))
	defer secondServer.Close()

	user, token := seedBillingAccount(t, decimal.NewFromInt(10), decimal.NewFromInt(10))
	firstChannel := &model.Channel{Type: "recovery-first", BaseURL: firstServer.URL, Status: 1}
	secondChannel := &model.Channel{Type: "recovery-second", BaseURL: secondServer.URL, Status: 1}
	if err := db.Create(firstChannel).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(secondChannel).Error; err != nil {
		t.Fatal(err)
	}
	firstAccount := &model.ChannelAccount{ChannelID: firstChannel.ID, Status: 1, Weight: 10}
	secondAccount := &model.ChannelAccount{ChannelID: secondChannel.ID, Status: 1, Weight: 10}
	if err := db.Create(firstAccount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(secondAccount).Error; err != nil {
		t.Fatal(err)
	}
	mapping := datatypes.JSON(`{"output_url":"url"}`)
	firstEndpoint := model.Endpoint{
		ModelCode: "image-recovery", ChannelID: firstChannel.ID, AccountID: firstAccount.ID,
		VendorModel: "vendor-first", InteractionMode: model.ModeSync, Status: 1,
		RequestPath: "/generate", RequestMethod: http.MethodPost, ContentType: "application/json",
		ResponseMapping: mapping, InputPrice: decimal.RequireFromString("5.12345678"),
	}
	secondEndpoint := model.Endpoint{
		ModelCode: "image-recovery", ChannelID: secondChannel.ID, AccountID: secondAccount.ID,
		VendorModel: "vendor-second", InteractionMode: model.ModeSync, Status: 1,
		RequestPath: "/generate", RequestMethod: http.MethodPost, ContentType: "application/json",
		ResponseMapping: mapping, InputPrice: decimal.RequireFromString("2.12345678"),
	}
	if err := db.Create(&firstEndpoint).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&secondEndpoint).Error; err != nil {
		t.Fatal(err)
	}

	request := &InvokeRequest{
		UserID: user.ID, TokenID: token.ID, CallID: "call_sync_recovery", RequestID: "request-sync-recovery",
		Capability: "image-recovery", Model: "image-recovery", Params: map[string]any{"prompt": "test"},
	}
	task, chosen, channel, account, err := NewUnifiedService().reserveInitialCapabilityTask(
		request, []model.Endpoint{firstEndpoint, secondEndpoint},
	)
	if err != nil {
		t.Fatalf("reserve recovery task: %v", err)
	}

	previousFinish := finishSynchronousCapabilityAttempt
	previousEnqueue := enqueueCapabilitySubmitRecovery
	finishSynchronousCapabilityAttempt = func(
		task *model.Task,
		channel *model.Channel,
		endpoint *model.Endpoint,
		attempt *model.APICallAttempt,
		stage string,
		metadata CapabilityAttemptMetadata,
		requestErr error,
	) error {
		if requestErr != nil {
			return FinishCapabilityAttempt(task, channel, endpoint, attempt, stage, metadata, requestErr)
		}
		return errors.New("temporary ledger write failure")
	}
	enqueuedTaskID := uint(0)
	enqueueCapabilitySubmitRecovery = func(taskID uint) error {
		enqueuedTaskID = taskID
		return nil
	}
	t.Cleanup(func() {
		finishSynchronousCapabilityAttempt = previousFinish
		enqueueCapabilitySubmitRecovery = previousEnqueue
	})

	response, err := NewUnifiedService().executeSyncWithFallback(
		context.Background(), task, request, []model.Endpoint{firstEndpoint, secondEndpoint},
		chosen, &channel, account,
	)
	if err == nil || response != nil {
		t.Fatalf("ledger failure response/error = %#v / %v", response, err)
	}
	if firstCalls != 1 || secondCalls != 1 {
		t.Fatalf("upstream calls = first %d second %d, want one each", firstCalls, secondCalls)
	}
	if enqueuedTaskID != task.ID {
		t.Fatalf("recovery task ID = %d, want %d", enqueuedTaskID, task.ID)
	}

	var storedTask model.Task
	if err := db.First(&storedTask, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	checkpoint, err := DecodeTaskSubmitCheckpoint(storedTask.SubmitCheckpoint)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint == nil || checkpoint.FinalCost != secondEndpoint.InputPrice.String() ||
		len(checkpoint.URLs) != 1 || checkpoint.URLs[0] != "https://result.example/recover.png" {
		t.Fatalf("recovery checkpoint = %#v", checkpoint)
	}
	if storedTask.Status != model.TaskStatusPending || storedTask.EndpointID != secondEndpoint.ID ||
		storedTask.WorkerLeaseOwner != "" {
		t.Fatalf("recovery task state = status %s endpoint %d lease %q",
			storedTask.Status, storedTask.EndpointID, storedTask.WorkerLeaseOwner)
	}
	attempts := loadCallAttemptsForServiceTest(t, db, task.CallID)
	if len(attempts) != 2 || attempts[0].Status != model.APICallAttemptStatusFailed ||
		attempts[1].Status != model.APICallAttemptStatusStarted || checkpoint.AttemptID != attempts[1].ID {
		t.Fatalf("recovery attempts = %#v checkpoint %#v", attempts, checkpoint)
	}
}

func TestSynchronousSuccessCheckpointWriteFailureDoesNotRepeatUpstream(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.Channel{}, &model.Endpoint{}); err != nil {
		t.Fatalf("migrate capability routes: %v", err)
	}
	previousConfig := config.C
	config.C = &config.Config{}
	provider.InitHTTPClient()
	t.Cleanup(func() { config.C = previousConfig })

	var upstreamCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"https://result.example/unconfirmed-sync.png"}`))
	}))
	defer server.Close()

	user, token := seedBillingAccount(t, decimal.NewFromInt(10), decimal.NewFromInt(10))
	channel := &model.Channel{Type: "sync-checkpoint-failure", BaseURL: server.URL, Status: 1}
	if err := db.Create(channel).Error; err != nil {
		t.Fatal(err)
	}
	account := &model.ChannelAccount{ChannelID: channel.ID, Status: 1, Weight: 10}
	if err := db.Create(account).Error; err != nil {
		t.Fatal(err)
	}
	endpoint := model.Endpoint{
		ModelCode: "image-sync-checkpoint", ChannelID: channel.ID, AccountID: account.ID,
		VendorModel: "vendor-sync-checkpoint", InteractionMode: model.ModeSync, Status: 1,
		RequestPath: "/generate", RequestMethod: http.MethodPost, ContentType: "application/json",
		ResponseMapping: datatypes.JSON(`{"output_url":"url"}`), InputPrice: decimal.NewFromInt(1),
	}
	if err := db.Create(&endpoint).Error; err != nil {
		t.Fatal(err)
	}
	request := &InvokeRequest{
		UserID: user.ID, TokenID: token.ID, CallID: "call_sync_checkpoint_failure",
		RequestID: "request-sync-checkpoint-failure", Capability: endpoint.ModelCode,
		Model: endpoint.ModelCode, Params: map[string]any{"prompt": "test"},
	}
	task, chosen, selectedChannel, selectedAccount, err := NewUnifiedService().reserveInitialCapabilityTask(
		request, []model.Endpoint{endpoint},
	)
	if err != nil {
		t.Fatalf("reserve capability task: %v", err)
	}

	previousSave := saveSynchronousSubmitCheckpoint
	previousEnqueue := enqueueCapabilitySubmitRecovery
	saveSynchronousSubmitCheckpoint = func(taskID uint, leaseOwner string, checkpoint *TaskSubmitCheckpoint) error {
		if checkpoint.IsSucceeded() {
			return errors.New("simulated successful checkpoint write failure")
		}
		return NewTaskService().SaveTaskSubmitCheckpoint(taskID, leaseOwner, checkpoint)
	}
	enqueueCalls := 0
	enqueueCapabilitySubmitRecovery = func(taskID uint) error {
		enqueueCalls++
		if taskID != task.ID {
			t.Fatalf("recovery task ID = %d, want %d", taskID, task.ID)
		}
		return nil
	}
	t.Cleanup(func() {
		saveSynchronousSubmitCheckpoint = previousSave
		enqueueCapabilitySubmitRecovery = previousEnqueue
	})

	response, err := NewUnifiedService().executeSyncWithFallback(
		context.Background(), task, request, []model.Endpoint{endpoint},
		chosen, &selectedChannel, selectedAccount,
	)
	if err == nil || response != nil {
		t.Fatalf("checkpoint failure response/error = %#v / %v", response, err)
	}
	var checkpointed model.Task
	if err := db.First(&checkpointed, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	checkpoint, err := DecodeTaskSubmitCheckpoint(checkpointed.SubmitCheckpoint)
	if err != nil || checkpoint == nil || !checkpoint.IsInFlight() {
		t.Fatalf("in-flight checkpoint = %#v err=%v", checkpoint, err)
	}

	response, err = NewUnifiedService().executeSyncWithFallback(
		context.Background(), task, request, []model.Endpoint{endpoint},
		chosen, &selectedChannel, selectedAccount,
	)
	if !errors.Is(err, ErrTaskSubmitOutcomeUnknown) || response != nil {
		t.Fatalf("ambiguous recovery response/error = %#v / %v", response, err)
	}
	if upstreamCalls.Load() != 1 || enqueueCalls != 1 {
		t.Fatalf("recovery calls = upstream %d queue %d, want 1/1", upstreamCalls.Load(), enqueueCalls)
	}
	var failed model.Task
	if err := db.First(&failed, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if failed.Status != model.TaskStatusFailed || !strings.Contains(failed.ErrorMessage, TaskSubmitOutcomeUnknownMessage) {
		t.Fatalf("ambiguous task = status %s error %q", failed.Status, failed.ErrorMessage)
	}
}

func loadCallAttemptsForServiceTest(t *testing.T, db *gorm.DB, callID string) []model.APICallAttempt {
	t.Helper()
	var attempts []model.APICallAttempt
	if err := db.Where("call_id = ?", callID).Order("attempt_no ASC").Find(&attempts).Error; err != nil {
		t.Fatal(err)
	}
	return attempts
}

func TestCapabilityTaskFailureRollsBackWhenCallTransitionFails(t *testing.T) {
	db := setupTestDB(t)
	task := seedTask(t, decimal.NewFromInt(1), decimal.NewFromInt(10), 1)
	call, err := NewAPICallService().StartCall(&StartCallRequest{
		ID: "call_terminal_before_task", UserID: task.UserID, TokenID: task.TokenID,
		ResourceType: "task", ResourceID: task.TaskNo, ReservedAmount: task.Cost,
	})
	if err != nil {
		t.Fatalf("create call: %v", err)
	}
	if err := NewAPICallService().CompleteCall(call.ID, nil); err != nil {
		t.Fatalf("pre-complete call: %v", err)
	}
	requestParams := []byte(`{"prompt":"must remain"}`)
	if err := db.Model(&model.Task{}).Where("id = ?", task.ID).Updates(map[string]any{
		"call_id":        call.ID,
		"request_params": requestParams,
		"mapped_params":  requestParams,
	}).Error; err != nil {
		t.Fatalf("link task call: %v", err)
	}
	task.CallID = call.ID

	committed, err := NewTaskService().UpdateTaskFail(task.ID, "late task failure")
	if committed || !errors.Is(err, ErrAPICallInvalidTransition) {
		t.Fatalf("failure transition = committed %v, err %v", committed, err)
	}

	var storedTask model.Task
	var storedCall model.APICall
	if err := db.First(&storedTask, task.ID).Error; err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if err := db.First(&storedCall, "id = ?", call.ID).Error; err != nil {
		t.Fatalf("reload call: %v", err)
	}
	if storedTask.Status != model.TaskStatusProcessing || storedTask.Refunded ||
		len(storedTask.RequestParams) == 0 || len(storedTask.MappedParams) == 0 {
		t.Fatalf("task terminal transaction was not rolled back: %#v", storedTask)
	}
	if storedCall.Status != model.APICallStatusCompleted || !storedCall.RefundedAmount.IsZero() {
		t.Fatalf("call refund was not rolled back: %#v", storedCall)
	}
	assertBillingBalances(t, task.UserID, task.TokenID, decimal.NewFromInt(10), decimal.NewFromInt(10), task.Cost)
	assertBillingLogCount(t, task.TaskNo, 0)
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
		BaseModel:  model.BaseModel{ID: 32},
		ChannelID:  channel.ID,
		AccountID:  account.ID,
		InputPrice: decimal.NewFromInt(3),
	}

	_, _, _, _, err := NewUnifiedService().reserveInitialCapabilityTask(
		&InvokeRequest{
			UserID:     user.ID,
			TokenID:    token.ID,
			Capability: "image-test",
			Params:     map[string]any{"prompt": "test"},
		},
		[]model.Endpoint{endpoint},
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
		BaseModel:  model.BaseModel{ID: 33},
		ChannelID:  channel.ID,
		AccountID:  account.ID,
		InputPrice: decimal.NewFromInt(3),
	}

	_, _, _, _, err := NewUnifiedService().reserveInitialCapabilityTask(
		&InvokeRequest{
			UserID:     user.ID,
			TokenID:    token.ID,
			Capability: "image-test",
			Params:     map[string]any{"invalid": make(chan int)},
		},
		[]model.Endpoint{endpoint},
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
	var callCount int64
	if err := db.Model(&model.APICall{}).Count(&callCount).Error; err != nil {
		t.Fatalf("count api calls: %v", err)
	}
	if callCount != 0 {
		t.Fatalf("api call count = %d, want 0", callCount)
	}
}

func assertCapabilityBillingContext(t *testing.T, idempotentKey, callID, phase string) {
	t.Helper()
	var entry model.BillingLog
	if err := model.DB().Where("idempotent_key = ?", idempotentKey).First(&entry).Error; err != nil {
		t.Fatalf("load billing log %q: %v", idempotentKey, err)
	}
	if entry.CallID != callID || entry.Phase != phase {
		t.Fatalf("billing context for %q = call %q phase %q", idempotentKey, entry.CallID, entry.Phase)
	}
	if phase == model.BillingPhaseReserve && len(entry.PricingSnapshot) == 0 {
		t.Fatalf("reserve billing context for %q has no pricing snapshot", idempotentKey)
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

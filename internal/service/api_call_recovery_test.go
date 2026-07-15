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

func TestReconcileStaleForegroundCallCompletesPendingRefund(t *testing.T) {
	db := setupTestDB(t)
	user, token := seedBillingAccount(t, decimal.NewFromInt(10), decimal.NewFromInt(10))
	calls := NewAPICallService()
	call, err := calls.StartCall(&StartCallRequest{
		UserID: user.ID, TokenID: token.ID, Endpoint: "/v1/chat/completions",
		Operation: "chat.completions", Model: "test-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := calls.MarkCallRunning(call.ID); err != nil {
		t.Fatal(err)
	}
	attempt, err := calls.StartAttempt(&StartAttemptRequest{
		CallID: call.ID, ChannelID: 1, KeyID: 2, Transport: model.UpstreamTransportOpenAIChat,
	})
	if err != nil {
		t.Fatal(err)
	}
	reserved := decimal.NewFromInt(1)
	if err := NewBillingService().DeductWithBillingContext(
		token.ID, user.ID, reserved, "stale-call:reserve",
		BillingContext{CallID: call.ID, AttemptID: attempt.ID, Phase: model.BillingPhaseReserve},
	); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := db.Model(&model.APICall{}).Where("id = ?", call.ID).UpdateColumn("updated_at", old).Error; err != nil {
		t.Fatal(err)
	}
	claimed, err := claimStaleForegroundCall(context.Background(), call.ID, time.Now().Add(-time.Hour))
	if err != nil || !claimed {
		t.Fatalf("claim stale call: claimed=%v err=%v", claimed, err)
	}
	assertBillingBalances(t, user.ID, token.ID, decimal.NewFromInt(9), decimal.NewFromInt(9), reserved)

	reconciled, err := calls.ReconcileStaleForegroundCalls(context.Background(), time.Now().Add(-time.Hour), 100)
	if err != nil || reconciled != 1 {
		t.Fatalf("reconcile stale call: count=%d err=%v", reconciled, err)
	}
	assertBillingBalances(t, user.ID, token.ID, decimal.NewFromInt(10), decimal.NewFromInt(10), decimal.Zero)

	var storedCall model.APICall
	if err := db.First(&storedCall, "id = ?", call.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedCall.Status != model.APICallStatusFailed || storedCall.ErrorCode != staleCallFinalCode ||
		!equalMoney(storedCall.RefundedAmount, reserved) {
		t.Fatalf("stale call = %#v", storedCall)
	}
	var storedAttempt model.APICallAttempt
	if err := db.First(&storedAttempt, attempt.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedAttempt.Status != model.APICallAttemptStatusFailed || storedAttempt.ErrorCode != staleCallFinalCode {
		t.Fatalf("stale attempt = %#v", storedAttempt)
	}
	assertBillingLogCount(t, "stale-call:settle", 1)
}

func TestReconcileStaleForegroundCallsSkipsBackgroundAndTasks(t *testing.T) {
	db := setupTestDB(t)
	calls := NewAPICallService()
	background, err := calls.StartCall(&StartCallRequest{
		UserID: 1, TokenID: 2, Endpoint: "/v1/responses", Operation: "responses",
		Model: "test", Background: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	taskCall, err := calls.StartCall(&StartCallRequest{
		UserID: 1, TokenID: 2, Endpoint: "/v1/capabilities/image", Operation: "capability.invoke",
		Model: "image", ResourceType: "task", ResourceID: "task_test",
	})
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := db.Model(&model.APICall{}).Where("id IN ?", []string{background.ID, taskCall.ID}).UpdateColumn("updated_at", old).Error; err != nil {
		t.Fatal(err)
	}
	reconciled, err := calls.ReconcileStaleForegroundCalls(context.Background(), time.Now().Add(-time.Hour), 100)
	if err != nil || reconciled != 0 {
		t.Fatalf("unexpected reconciliation: count=%d err=%v", reconciled, err)
	}
}

func TestReconcileStaleForegroundCallsHonorsActiveLease(t *testing.T) {
	db := setupTestDB(t)
	calls := NewAPICallService()
	call, err := calls.StartCall(&StartCallRequest{
		UserID: 1, TokenID: 2, Endpoint: "/v1/chat/completions",
		Operation: "chat.completions", Model: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := calls.MarkCallRunning(call.ID); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := db.Model(&model.APICall{}).Where("id = ?", call.ID).UpdateColumn("updated_at", old).Error; err != nil {
		t.Fatal(err)
	}
	owner := "active-worker"
	if err := calls.AcquireCallLease(call.ID, owner, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	reconciled, err := calls.ReconcileStaleForegroundCalls(context.Background(), time.Now().Add(-time.Hour), 100)
	if err != nil || reconciled != 0 {
		t.Fatalf("active lease reconciled: count=%d err=%v", reconciled, err)
	}
	if err := calls.RenewCallLease(call.ID, owner, time.Now().Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := calls.ReleaseCallLease(call.ID, owner); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileStaleForegroundCallFailsLinkedResponse(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.AIResponse{}); err != nil {
		t.Fatal(err)
	}
	response := model.AIResponse{
		ID: "resp_stale_foreground", UserID: 1, TokenID: 2, Model: "test",
		Status: "in_progress", Store: true, IdempotencyKey: "stale-foreground",
	}
	calls := NewAPICallService()
	call, err := calls.StartCall(&StartCallRequest{
		UserID: response.UserID, TokenID: response.TokenID, Endpoint: "/v1/responses",
		Operation: "responses", Model: response.Model, ResourceType: "response", ResourceID: response.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	response.CallID = call.ID
	if err := db.Create(&response).Error; err != nil {
		t.Fatal(err)
	}
	if err := calls.MarkCallRunning(call.ID); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := db.Model(&model.APICall{}).Where("id = ?", call.ID).UpdateColumn("updated_at", old).Error; err != nil {
		t.Fatal(err)
	}
	reconciled, err := calls.ReconcileStaleForegroundCalls(context.Background(), time.Now().Add(-time.Hour), 100)
	if err != nil || reconciled != 1 {
		t.Fatalf("reconcile linked response: count=%d err=%v", reconciled, err)
	}
	if err := db.First(&response, "id = ?", response.ID).Error; err != nil {
		t.Fatal(err)
	}
	if response.Status != "failed" || len(response.ErrorJSON) == 0 {
		t.Fatalf("linked response=%#v", response)
	}
}

func TestReconcilePendingCallCompletionIntent(t *testing.T) {
	db := setupTestDB(t)
	calls := NewAPICallService()
	call, err := calls.StartCall(&StartCallRequest{
		UserID: 1, TokenID: 2, Endpoint: "/v1/messages", Operation: "messages", Model: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := calls.MarkCallRunning(call.ID); err != nil {
		t.Fatal(err)
	}
	attempt, err := calls.StartAttempt(&StartAttemptRequest{
		CallID: call.ID, ChannelID: 1, KeyID: 2, Transport: model.UpstreamTransportAnthropic,
	})
	if err != nil {
		t.Fatal(err)
	}
	completion := &CompleteCallRequest{
		FinalAttemptID: attempt.ID, CompleteStartedAttempt: true,
		HTTPStatus: 200, InputTokens: 6, OutputTokens: 2, TotalTokens: 8,
		ProviderResponseID: "provider-intent",
		UsageJSON:          datatypes.JSON(`{"input_tokens":6,"output_tokens":2,"total_tokens":8}`),
	}
	if err := persistCallCompletionIntent(call.ID, completion, errors.New("temporary database failure")); err != nil {
		t.Fatal(err)
	}

	reconciled, err := calls.ReconcileStaleForegroundCalls(context.Background(), time.Now().Add(-time.Hour), 100)
	if err != nil || reconciled != 1 {
		t.Fatalf("reconcile completion intent: count=%d err=%v", reconciled, err)
	}
	var storedCall model.APICall
	if err := db.First(&storedCall, "id = ?", call.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedCall.Status != model.APICallStatusCompleted || storedCall.ErrorCode != "" || storedCall.TotalTokens != 8 {
		t.Fatalf("reconciled call = %#v", storedCall)
	}
	var storedAttempt model.APICallAttempt
	if err := db.First(&storedAttempt, attempt.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedAttempt.Status != model.APICallAttemptStatusCompleted || storedAttempt.ProviderResponseID != "provider-intent" {
		t.Fatalf("reconciled attempt = %#v", storedAttempt)
	}
}

func TestReconcileFailedForegroundCallRefundsOutstandingReservation(t *testing.T) {
	db := setupTestDB(t)
	user, token := seedBillingAccount(t, decimal.NewFromInt(10), decimal.NewFromInt(10))
	calls := NewAPICallService()
	call, err := calls.StartCall(&StartCallRequest{
		UserID: user.ID, TokenID: token.ID, Endpoint: "/v1/chat/completions", Operation: "chat.completions", Model: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := calls.MarkCallRunning(call.ID); err != nil {
		t.Fatal(err)
	}
	attempt, err := calls.StartAttempt(&StartAttemptRequest{
		CallID: call.ID, ChannelID: 1, KeyID: 2, Transport: model.UpstreamTransportOpenAIChat,
	})
	if err != nil {
		t.Fatal(err)
	}
	reserved := decimal.NewFromInt(1)
	if err := NewBillingService().DeductWithBillingContext(
		token.ID, user.ID, reserved, "failed-call:reserve",
		BillingContext{CallID: call.ID, AttemptID: attempt.ID, Phase: model.BillingPhaseReserve},
	); err != nil {
		t.Fatal(err)
	}
	if err := calls.FailAttempt(attempt.ID, &FailAttemptRequest{
		HTTPStatus: 500, ErrorType: "server_error", ErrorCode: "upstream_failed", ErrorMessage: "upstream failed",
	}); err != nil {
		t.Fatal(err)
	}
	if err := calls.FailCall(call.ID, &FailCallRequest{
		FinalAttemptID: attempt.ID, HTTPStatus: 500,
		ErrorType: "server_error", ErrorCode: "settlement_failed", ErrorMessage: "settlement failed",
	}); err != nil {
		t.Fatal(err)
	}
	assertBillingBalances(t, user.ID, token.ID, decimal.NewFromInt(9), decimal.NewFromInt(9), reserved)

	reconciled, err := calls.ReconcileStaleForegroundCalls(context.Background(), time.Now().Add(-time.Hour), 100)
	if err != nil || reconciled != 1 {
		t.Fatalf("reconcile failed call reservation: count=%d err=%v", reconciled, err)
	}
	assertBillingBalances(t, user.ID, token.ID, decimal.NewFromInt(10), decimal.NewFromInt(10), decimal.Zero)
	var stored model.APICall
	if err := db.First(&stored, "id = ?", call.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != model.APICallStatusFailed || stored.ErrorCode != "settlement_failed" || !stored.RefundedAmount.Equal(reserved) {
		t.Fatalf("reconciled failed call = %#v", stored)
	}
	assertBillingLogCount(t, "failed-call:settle", 1)
}

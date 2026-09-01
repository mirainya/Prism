package service

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/config"
	"github.com/shopspring/decimal"
	"gorm.io/datatypes"
)

func TestAPICallServiceLifecycleIsIdempotent(t *testing.T) {
	setupAPICallServiceTestDB(t)
	service := NewAPICallService()

	call, err := service.StartCall(&StartCallRequest{
		RequestID: "req-lifecycle",
		UserID:    11,
		TokenID:   21,
		Endpoint:  "/v1/responses",
		Operation: "responses.create",
		Model:     "gpt-test",
		IsStream:  true,
		Store:     true,
	})
	if err != nil {
		t.Fatalf("start call: %v", err)
	}
	if call.ID == "" || call.Status != model.APICallStatusReceived {
		t.Fatalf("unexpected new call: %+v", call)
	}

	for i := 0; i < 2; i++ {
		if err := service.MarkCallRunning(call.ID); err != nil {
			t.Fatalf("mark call running attempt %d: %v", i+1, err)
		}
	}

	attempt, err := service.StartAttempt(&StartAttemptRequest{
		CallID:      call.ID,
		AbilityID:   31,
		ChannelID:   41,
		KeyID:       51,
		Protocol:    model.ProtocolOpenAI,
		VendorModel: "vendor-test",
		Transport:   model.UpstreamTransportOpenAIResponses,
		RequestPath: "/v1/responses",
	})
	if err != nil {
		t.Fatalf("start attempt: %v", err)
	}
	if attempt.AttemptNo != 1 {
		t.Fatalf("attempt number = %d, want 1", attempt.AttemptNo)
	}

	for i := 0; i < 2; i++ {
		if err := service.MarkAttemptFirstByte(attempt.ID); err != nil {
			t.Fatalf("mark first byte attempt %d: %v", i+1, err)
		}
	}

	completion := &CompleteAttemptRequest{
		HTTPStatus:            200,
		InputTokens:           8,
		OutputTokens:          5,
		TotalTokens:           13,
		CachedInputTokens:     2,
		ReasoningOutputTokens: 1,
		UsageJSON:             []byte(`{"input_tokens":8,"output_tokens":5}`),
		ProviderResponseID:    "provider-response-1",
	}
	for i := 0; i < 2; i++ {
		if err := service.CompleteAttempt(attempt.ID, completion); err != nil {
			t.Fatalf("complete attempt %d: %v", i+1, err)
		}
	}

	callCompletion := &CompleteCallRequest{
		FinalAttemptID: attempt.ID,
		FinalCost:      decimal.RequireFromString("0.125"),
		RefundedAmount: decimal.RequireFromString("0.025"),
	}
	for i := 0; i < 2; i++ {
		if err := service.CompleteCall(call.ID, callCompletion); err != nil {
			t.Fatalf("complete call %d: %v", i+1, err)
		}
	}
	if err := service.FailCall(call.ID, &FailCallRequest{ErrorCode: "late_failure"}); !errors.Is(err, ErrAPICallInvalidTransition) {
		t.Fatalf("fail completed call error = %v, want invalid transition", err)
	}

	var storedCall model.APICall
	if err := model.DB().First(&storedCall, "id = ?", call.ID).Error; err != nil {
		t.Fatalf("load call: %v", err)
	}
	if storedCall.Status != model.APICallStatusCompleted || storedCall.AttemptCount != 1 {
		t.Fatalf("unexpected stored call status/count: %+v", storedCall)
	}
	if storedCall.FinalAttemptID != attempt.ID || storedCall.InputTokens != 8 || storedCall.TotalTokens != 13 {
		t.Fatalf("attempt usage was not copied to call: %+v", storedCall)
	}
	if storedCall.FirstByteAt == nil {
		t.Fatal("call first byte timestamp was not recorded")
	}
	if !storedCall.FinalCost.Equal(decimal.RequireFromString("0.125")) {
		t.Fatalf("final cost = %s", storedCall.FinalCost)
	}

	var storedAttempt model.APICallAttempt
	if err := model.DB().First(&storedAttempt, attempt.ID).Error; err != nil {
		t.Fatalf("load attempt: %v", err)
	}
	if storedAttempt.Status != model.APICallAttemptStatusCompleted || storedAttempt.FirstByteAt == nil {
		t.Fatalf("unexpected stored attempt: %+v", storedAttempt)
	}
}

func TestCompleteCallRejectsNonCompletedFinalAttempt(t *testing.T) {
	setupAPICallServiceTestDB(t)
	calls := NewAPICallService()
	call := startAPICallForTest(t, calls, 12, false, "req-invalid-final-attempt", "test-model")
	attempt, err := calls.StartAttempt(&StartAttemptRequest{
		CallID: call.ID, ChannelID: 3, Transport: model.UpstreamTransportOpenAIChat,
	})
	if err != nil {
		t.Fatal(err)
	}

	err = calls.CompleteCall(call.ID, &CompleteCallRequest{FinalAttemptID: attempt.ID})
	if !errors.Is(err, ErrAPICallInvalidTransition) {
		t.Fatalf("complete call error = %v, want invalid transition", err)
	}
	if err := calls.CompleteCall(call.ID, nil); !errors.Is(err, ErrAPICallInvalidTransition) {
		t.Fatalf("complete call without final attempt error = %v, want invalid transition", err)
	}

	var stored model.APICall
	if err := model.DB().First(&stored, "id = ?", call.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != model.APICallStatusInProgress || stored.FinalAttemptID != 0 {
		t.Fatalf("call changed after rejected completion: %#v", stored)
	}
}

func TestCompleteCallCanAtomicallyCompleteStartedFinalAttempt(t *testing.T) {
	setupAPICallServiceTestDB(t)
	calls := NewAPICallService()
	call := startAPICallForTest(t, calls, 12, false, "req-recover-final-attempt", "test-model")
	attempt, err := calls.StartAttempt(&StartAttemptRequest{
		CallID: call.ID, ChannelID: 3, Transport: model.UpstreamTransportOpenAIChat,
	})
	if err != nil {
		t.Fatal(err)
	}

	err = calls.CompleteCall(call.ID, &CompleteCallRequest{
		FinalAttemptID: attempt.ID, CompleteStartedAttempt: true,
		HTTPStatus: 200, InputTokens: 7, OutputTokens: 5, TotalTokens: 12,
		ProviderResponseID: "provider-recovered",
		UsageJSON:          datatypes.JSON(`{"input_tokens":7,"output_tokens":5,"total_tokens":12}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	var storedCall model.APICall
	if err := model.DB().First(&storedCall, "id = ?", call.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedCall.Status != model.APICallStatusCompleted || storedCall.FinalAttemptID != attempt.ID || storedCall.TotalTokens != 12 {
		t.Fatalf("completed call = %#v", storedCall)
	}
	var storedAttempt model.APICallAttempt
	if err := model.DB().First(&storedAttempt, attempt.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedAttempt.Status != model.APICallAttemptStatusCompleted || storedAttempt.ProviderResponseID != "provider-recovered" || storedAttempt.TotalTokens != 12 {
		t.Fatalf("completed attempt = %#v", storedAttempt)
	}
}

func TestCallTerminalTransitionsEnforceLeaseOwner(t *testing.T) {
	setupAPICallServiceTestDB(t)
	calls := NewAPICallService()
	call := startAPICallForTest(t, calls, 12, false, "req-lease-owner", "test-model")
	attempt, err := calls.StartAttempt(&StartAttemptRequest{
		CallID: call.ID, ChannelID: 3, Transport: model.UpstreamTransportOpenAIChat,
	})
	if err != nil {
		t.Fatal(err)
	}
	owner := "terminal-owner"
	if err := calls.AcquireCallLease(call.ID, owner, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	completion := &CompleteCallRequest{
		LeaseOwner: "stale-owner", FinalAttemptID: attempt.ID, CompleteStartedAttempt: true,
		HTTPStatus: 200, TotalTokens: 3,
	}
	if err := calls.CompleteCall(call.ID, completion); !errors.Is(err, ErrAPICallLeaseUnavailable) {
		t.Fatalf("stale owner completion error = %v", err)
	}
	completion.LeaseOwner = owner
	if err := calls.CompleteCall(call.ID, completion); err != nil {
		t.Fatal(err)
	}

	failedCall := startAPICallForTest(t, calls, 13, false, "req-fail-started", "test-model")
	failedAttempt, err := calls.StartAttempt(&StartAttemptRequest{
		CallID: failedCall.ID, ChannelID: 3, Transport: model.UpstreamTransportOpenAIChat,
	})
	if err != nil {
		t.Fatal(err)
	}
	failOwner := "failure-owner"
	if err := calls.AcquireCallLease(failedCall.ID, failOwner, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := calls.FailCall(failedCall.ID, &FailCallRequest{
		LeaseOwner: failOwner, FinalAttemptID: failedAttempt.ID, FailStartedAttempt: true,
		HTTPStatus: 503, ErrorType: "server_error", ErrorCode: "lease_test", ErrorMessage: "failed",
	}); err != nil {
		t.Fatal(err)
	}
	var storedAttempt model.APICallAttempt
	if err := model.DB().First(&storedAttempt, failedAttempt.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedAttempt.Status != model.APICallAttemptStatusFailed || storedAttempt.ErrorCode != "lease_test" {
		t.Fatalf("failed attempt = %#v", storedAttempt)
	}
}

func TestCompleteCallCancelsSupersededStartedAttempts(t *testing.T) {
	setupAPICallServiceTestDB(t)
	calls := NewAPICallService()
	call := startAPICallForTest(t, calls, 14, false, "req-complete-multiple-started", "test-model")
	first, err := calls.StartAttempt(&StartAttemptRequest{
		CallID: call.ID, ChannelID: 1, Transport: model.UpstreamTransportOpenAIChat,
	})
	if err != nil {
		t.Fatal(err)
	}
	final, err := calls.StartAttempt(&StartAttemptRequest{
		CallID: call.ID, ChannelID: 2, Transport: model.UpstreamTransportAnthropic,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := calls.CompleteCall(call.ID, &CompleteCallRequest{
		FinalAttemptID: final.ID, CompleteStartedAttempt: true, HTTPStatus: 200, TotalTokens: 9,
	}); err != nil {
		t.Fatal(err)
	}

	var attempts []model.APICallAttempt
	if err := model.DB().Where("call_id = ?", call.ID).Order("attempt_no ASC").Find(&attempts).Error; err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 {
		t.Fatalf("attempt count=%d, want 2", len(attempts))
	}
	if attempts[0].ID != first.ID || attempts[0].Status != model.APICallAttemptStatusCancelled || attempts[0].ErrorCode != "attempt_superseded" {
		t.Fatalf("superseded attempt=%#v", attempts[0])
	}
	if attempts[1].ID != final.ID || attempts[1].Status != model.APICallAttemptStatusCompleted || attempts[1].TotalTokens != 9 {
		t.Fatalf("final attempt=%#v", attempts[1])
	}
}

func TestFailCallFailsEveryStartedAttempt(t *testing.T) {
	setupAPICallServiceTestDB(t)
	calls := NewAPICallService()
	call := startAPICallForTest(t, calls, 15, false, "req-fail-multiple-started", "test-model")
	for _, channelID := range []uint{1, 2} {
		if _, err := calls.StartAttempt(&StartAttemptRequest{
			CallID: call.ID, ChannelID: channelID, Transport: model.UpstreamTransportOpenAIChat,
		}); err != nil {
			t.Fatal(err)
		}
	}
	var final model.APICallAttempt
	if err := model.DB().Where("call_id = ?", call.ID).Order("attempt_no DESC").First(&final).Error; err != nil {
		t.Fatal(err)
	}
	if err := calls.FailCall(call.ID, &FailCallRequest{
		FinalAttemptID: final.ID, HTTPStatus: 503,
		ErrorType: "server_error", ErrorCode: "upstream_failed", ErrorMessage: "upstream failed",
	}); err != nil {
		t.Fatal(err)
	}

	var attempts []model.APICallAttempt
	if err := model.DB().Where("call_id = ?", call.ID).Order("attempt_no ASC").Find(&attempts).Error; err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 {
		t.Fatalf("attempt count=%d, want 2", len(attempts))
	}
	for i := range attempts {
		if attempts[i].Status != model.APICallAttemptStatusFailed || attempts[i].ErrorCode != "upstream_failed" {
			t.Fatalf("attempt[%d]=%#v", i, attempts[i])
		}
	}
}

func TestAPICallServiceFailureAndCancellationAreIdempotent(t *testing.T) {
	setupAPICallServiceTestDB(t)
	service := NewAPICallService()

	failedCall := startAPICallForTest(t, service, 1, true, "req-failed", "failed-model")
	failedAttempt, err := service.StartAttempt(&StartAttemptRequest{
		CallID:    failedCall.ID,
		ChannelID: 9,
		Transport: model.UpstreamTransportAnthropic,
	})
	if err != nil {
		t.Fatalf("start failed attempt: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := service.FailAttempt(failedAttempt.ID, &FailAttemptRequest{
			HTTPStatus:     503,
			ErrorType:      "server_error",
			ErrorCode:      "model_unavailable",
			ErrorMessage:   "GET https://example.test/run?X-Amz-Signature=secret Authorization: Bearer abc api_key=xyz",
			ErrorRetryable: true,
		}); err != nil {
			t.Fatalf("fail attempt %d: %v", i+1, err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := service.FailCall(failedCall.ID, &FailCallRequest{FinalAttemptID: failedAttempt.ID}); err != nil {
			t.Fatalf("fail call %d: %v", i+1, err)
		}
	}

	cancelledCall := startAPICallForTest(t, service, 2, true, "req-cancelled", "cancelled-model")
	cancelledAttempt, err := service.StartAttempt(&StartAttemptRequest{
		CallID:    cancelledCall.ID,
		ChannelID: 10,
		Transport: model.UpstreamTransportOpenAIChat,
	})
	if err != nil {
		t.Fatalf("start cancelled attempt: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := service.CancelAttempt(cancelledAttempt.ID, &CancelAttemptRequest{
			ErrorType: "cancelled", ErrorCode: "client_cancelled", ErrorMessage: "client disconnected",
		}); err != nil {
			t.Fatalf("cancel attempt %d: %v", i+1, err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := service.CancelCall(cancelledCall.ID, &CancelCallRequest{
			FinalAttemptID:     cancelledAttempt.ID,
			ErrorCode:          "client_cancelled",
			ClientDisconnected: true,
		}); err != nil {
			t.Fatalf("cancel call %d: %v", i+1, err)
		}
	}
	if err := service.CompleteCall(cancelledCall.ID, nil); !errors.Is(err, ErrAPICallInvalidTransition) {
		t.Fatalf("complete cancelled call error = %v, want invalid transition", err)
	}

	var storedFailed model.APICall
	if err := model.DB().First(&storedFailed, "id = ?", failedCall.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedFailed.Status != model.APICallStatusFailed || storedFailed.ErrorCode != "model_unavailable" || !storedFailed.ErrorRetryable {
		t.Fatalf("unexpected failed call: %+v", storedFailed)
	}
	if strings.Contains(storedFailed.ErrorMessage, "secret") || strings.Contains(storedFailed.ErrorMessage, "abc") ||
		strings.Contains(storedFailed.ErrorMessage, "xyz") || !strings.Contains(storedFailed.ErrorMessage, "REDACTED") {
		t.Fatalf("failed call retained credentials: %q", storedFailed.ErrorMessage)
	}

	var storedCancelledAttempt model.APICallAttempt
	if err := model.DB().First(&storedCancelledAttempt, cancelledAttempt.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedCancelledAttempt.Status != model.APICallAttemptStatusCancelled || storedCancelledAttempt.CompletedAt == nil ||
		storedCancelledAttempt.ErrorCode != "client_cancelled" {
		t.Fatalf("unexpected cancelled attempt: %+v", storedCancelledAttempt)
	}
}

func TestAPICallServiceListAndDetailEnforceUserScope(t *testing.T) {
	setupAPICallServiceTestDB(t)
	service := NewAPICallService()

	callOne := startAPICallForTest(t, service, 101, true, "request-one", "model-one")
	attemptOne, err := service.StartAttempt(&StartAttemptRequest{
		CallID:      callOne.ID,
		AbilityID:   301,
		ChannelID:   201,
		KeyID:       401,
		VendorModel: "private-model",
		RequestPath: "/private/upstream",
		Protocol:    model.ProtocolAnthropic,
		Transport:   model.UpstreamTransportAnthropic,
	})
	if err != nil {
		t.Fatalf("start attempt one: %v", err)
	}
	if err := service.CompleteAttempt(attemptOne.ID, &CompleteAttemptRequest{HTTPStatus: 200, ProviderResponseID: "provider-private-id"}); err != nil {
		t.Fatal(err)
	}
	if err := service.CompleteCall(callOne.ID, &CompleteCallRequest{FinalAttemptID: attemptOne.ID}); err != nil {
		t.Fatal(err)
	}

	callTwo := startAPICallForTest(t, service, 102, false, "request-two", "model-two")
	if _, err := service.StartAttempt(&StartAttemptRequest{
		CallID:    callTwo.ID,
		ChannelID: 201,
		Transport: model.UpstreamTransportOpenAIResponses,
	}); err != nil {
		t.Fatalf("start attempt two: %v", err)
	}

	capabilityEndpoint := &model.Endpoint{
		ModelCode:   "image-model",
		ChannelID:   201,
		AccountID:   501,
		VendorModel: "vendor-image-model",
	}
	if err := model.DB().Create(capabilityEndpoint).Error; err != nil {
		t.Fatalf("create capability endpoint: %v", err)
	}
	capabilityCall := startAPICallForTest(t, service, 103, false, "request-capability", "image-model")
	capabilityAttempt, err := service.StartAttempt(&StartAttemptRequest{
		CallID:      capabilityCall.ID,
		RouteKind:   model.APICallRouteCapability,
		Stage:       model.APICallStageSubmit,
		EndpointID:  capabilityEndpoint.ID,
		AccountID:   capabilityEndpoint.AccountID,
		VendorModel: capabilityEndpoint.VendorModel,
	})
	if err != nil {
		t.Fatalf("start capability attempt: %v", err)
	}
	if err := service.CompleteAttempt(capabilityAttempt.ID, &CompleteAttemptRequest{HTTPStatus: 200}); err != nil {
		t.Fatal(err)
	}
	if err := service.CompleteCall(capabilityCall.ID, &CompleteCallRequest{FinalAttemptID: capabilityAttempt.ID}); err != nil {
		t.Fatal(err)
	}
	videoCall := startAPICallForTest(t, service, 104, false, "request-video", "minimax-h3")
	videoAttempt, err := service.StartAttempt(&StartAttemptRequest{
		CallID: videoCall.ID, RouteKind: model.APICallRouteVideo, Stage: model.APICallStageSubmit,
		ChannelID: 202, Transport: model.UpstreamTransportVideoGeneration,
	})
	if err != nil {
		t.Fatalf("start video attempt: %v", err)
	}
	if err := service.CompleteAttempt(videoAttempt.ID, &CompleteAttemptRequest{HTTPStatus: 200}); err != nil {
		t.Fatal(err)
	}
	if err := service.CompleteCall(videoCall.ID, &CompleteCallRequest{FinalAttemptID: videoAttempt.ID}); err != nil {
		t.Fatal(err)
	}

	payload := &model.APICallPayload{
		CallID: callOne.ID,
		Kind:   model.APICallPayloadRequest,
		Data:   []byte(`{"input":"hello"}`),
	}
	if err := service.RecordPayload(payload); err != nil {
		t.Fatalf("record payload: %v", err)
	}
	if payload.ID == 0 {
		t.Fatal("stored payload did not receive an id")
	}
	skippedPayload := &model.APICallPayload{
		CallID: callTwo.ID,
		Kind:   model.APICallPayloadRequest,
		Data:   []byte(`{"input":"private"}`),
	}
	if err := service.RecordPayload(skippedPayload); err != nil {
		t.Fatalf("skip disabled payload: %v", err)
	}
	if skippedPayload.ID != 0 {
		t.Fatalf("payload was stored while call retention was disabled: %d", skippedPayload.ID)
	}

	billing := &model.BillingLog{
		IdempotentKey:   "call-one:settle",
		TokenID:         callOne.TokenID,
		UserID:          callOne.UserID,
		CallID:          callOne.ID,
		AttemptID:       attemptOne.ID,
		Phase:           model.BillingPhaseSettle,
		PricingSnapshot: datatypes.JSON(`{"input_price":"1"}`),
		Amount:          decimal.RequireFromString("0.25"),
		Type:            model.BillingTypeDeduct,
		Status:          "success",
	}
	if err := model.DB().Create(billing).Error; err != nil {
		t.Fatalf("create billing log: %v", err)
	}

	userList, err := service.ListCalls(&ListCallsRequest{
		ActorUserID: 101,
		UserID:      102,
		RouteKind:   model.APICallRouteCapability,
		ChannelID:   999,
		Transport:   model.UpstreamTransportOpenAIResponses,
		PageSize:    500,
	})
	if err != nil {
		t.Fatalf("list user calls: %v", err)
	}
	if userList.Total != 1 || len(userList.Items) != 1 || userList.Items[0].ID != callOne.ID {
		t.Fatalf("user isolation failed: %+v", userList)
	}
	if userList.PageSize != 100 {
		t.Fatalf("page size = %d, want capped at 100", userList.PageSize)
	}

	adminList, err := service.ListCalls(&ListCallsRequest{
		IsAdmin:   true,
		CallID:    callOne.ID,
		RouteKind: model.APICallRouteGatewayV2,
		ChannelID: 201,
		Transport: model.UpstreamTransportAnthropic,
		RequestID: "request-one",
		Status:    model.APICallStatusCompleted,
		StartDate: time.Now().Format("2006-01-02"),
		EndDate:   time.Now().Format("2006-01-02"),
	})
	if err != nil {
		t.Fatalf("list filtered admin calls: %v", err)
	}
	if adminList.Total != 1 || len(adminList.Items) != 1 || adminList.Items[0].ID != callOne.ID {
		t.Fatalf("admin filters failed: %+v", adminList)
	}
	capabilityList, err := service.ListCalls(&ListCallsRequest{
		IsAdmin:   true,
		RouteKind: model.APICallRouteCapability,
		ChannelID: 201,
	})
	if err != nil {
		t.Fatalf("list capability calls by channel: %v", err)
	}
	if capabilityList.Total != 1 || len(capabilityList.Items) != 1 || capabilityList.Items[0].ID != capabilityCall.ID {
		t.Fatalf("capability channel filter failed: %+v", capabilityList)
	}
	videoList, err := service.ListCalls(&ListCallsRequest{
		IsAdmin: true, RouteKind: model.APICallRouteVideo, ChannelID: 202,
	})
	if err != nil {
		t.Fatalf("list video calls by channel: %v", err)
	}
	if videoList.Total != 1 || len(videoList.Items) != 1 || videoList.Items[0].ID != videoCall.ID {
		t.Fatalf("video channel filter failed: %+v", videoList)
	}
	channelList, err := service.ListCalls(&ListCallsRequest{IsAdmin: true, ChannelID: 201})
	if err != nil {
		t.Fatalf("list calls across route kinds by channel: %v", err)
	}
	if channelList.Total != 3 {
		t.Fatalf("cross-route channel filter total = %d, want 3", channelList.Total)
	}
	if _, err := service.ListCalls(&ListCallsRequest{StartDate: "not-a-date", IsAdmin: true}); !errors.Is(err, ErrAPICallInvalidInput) {
		t.Fatalf("invalid date error = %v", err)
	}

	if _, err := service.GetCallDetail(callOne.ID, 102, false); !errors.Is(err, ErrAPICallNotFound) {
		t.Fatalf("cross-user detail error = %v, want not found", err)
	}
	detail, err := service.GetCallDetail(callOne.ID, 101, false)
	if err != nil {
		t.Fatalf("get owner detail: %v", err)
	}
	if len(detail.Attempts) != 1 || len(detail.BillingLogs) != 1 || len(detail.Payloads) != 0 {
		t.Fatalf("incomplete call detail: %+v", detail)
	}
	if detail.Attempts[0].KeyID != 0 || detail.Attempts[0].ChannelID != 0 ||
		detail.Attempts[0].Protocol != "" || detail.Attempts[0].Transport != "" ||
		detail.Attempts[0].VendorModel != "" || detail.Attempts[0].RequestPath != "" ||
		detail.Attempts[0].ProviderResponseID != "" ||
		detail.BillingLogs[0].IdempotentKey != "" || len(detail.BillingLogs[0].PricingSnapshot) != 0 {
		t.Fatalf("owner detail exposed internal routing data: %+v", detail)
	}
	adminDetail, err := service.GetCallDetail(callOne.ID, 0, true)
	if err != nil {
		t.Fatalf("get admin detail: %v", err)
	}
	if len(adminDetail.Payloads) != 1 || adminDetail.Payloads[0].Data != `{"input":"hello"}` ||
		adminDetail.Attempts[0].KeyID != 401 || len(adminDetail.BillingLogs[0].PricingSnapshot) == 0 {
		t.Fatalf("admin detail is incomplete: %+v", adminDetail)
	}
}

func TestAPICallServiceListUsesSnapshotAcrossPages(t *testing.T) {
	setupAPICallServiceTestDB(t)
	service := NewAPICallService()
	base := time.Now().Add(-time.Hour)
	for index, callID := range []string{"call-page-1", "call-page-2", "call-page-3"} {
		createdAt := base.Add(time.Duration(index) * time.Minute)
		call := &model.APICall{
			ID:        callID,
			UserID:    1,
			Endpoint:  "/v1/responses",
			Model:     "snapshot-model",
			Status:    model.APICallStatusCompleted,
			StartedAt: createdAt,
			CreatedAt: createdAt,
		}
		if err := model.DB().Create(call).Error; err != nil {
			t.Fatalf("create call %s: %v", callID, err)
		}
	}

	firstPage, err := service.ListCalls(&ListCallsRequest{IsAdmin: true, Page: 1, PageSize: 2})
	if err != nil {
		t.Fatalf("list first page: %v", err)
	}
	if firstPage.Total != 3 || len(firstPage.Items) != 2 ||
		firstPage.Items[0].ID != "call-page-3" || firstPage.Items[1].ID != "call-page-2" {
		t.Fatalf("unexpected first page: %+v", firstPage)
	}
	snapshot, err := time.Parse(time.RFC3339Nano, firstPage.SnapshotAt)
	if err != nil {
		t.Fatalf("parse snapshot %q: %v", firstPage.SnapshotAt, err)
	}
	if snapshot.Nanosecond()%int(time.Millisecond) != 0 {
		t.Fatalf("snapshot is not a millisecond boundary: %s", firstPage.SnapshotAt)
	}

	sameMillisecondCreatedAt := snapshot.Add(500 * time.Microsecond).Truncate(time.Millisecond)
	newCall := &model.APICall{
		ID:        "call-page-new",
		UserID:    1,
		Endpoint:  "/v1/responses",
		Model:     "snapshot-model",
		Status:    model.APICallStatusCompleted,
		StartedAt: sameMillisecondCreatedAt,
		CreatedAt: sameMillisecondCreatedAt,
	}
	if err := model.DB().Create(newCall).Error; err != nil {
		t.Fatalf("create new call: %v", err)
	}

	secondPage, err := service.ListCalls(&ListCallsRequest{
		IsAdmin: true, Page: 2, PageSize: 2, SnapshotAt: firstPage.SnapshotAt,
	})
	if err != nil {
		t.Fatalf("list second page: %v", err)
	}
	if secondPage.SnapshotAt != firstPage.SnapshotAt || secondPage.Total != 3 ||
		len(secondPage.Items) != 1 || secondPage.Items[0].ID != "call-page-1" {
		t.Fatalf("snapshot page drifted: %+v", secondPage)
	}

	if _, err := service.ListCalls(&ListCallsRequest{IsAdmin: true, SnapshotAt: "invalid"}); !errors.Is(err, ErrAPICallInvalidInput) {
		t.Fatalf("invalid snapshot error = %v", err)
	}
}

func TestAPICallPayloadRetentionRedactsTruncatesAndExpires(t *testing.T) {
	setupAPICallServiceTestDB(t)
	service := NewAPICallService()
	retain := true
	expiresAt := time.Now().Add(time.Hour)
	call, err := service.StartCall(&StartCallRequest{
		UserID: 1, TokenID: 2, Endpoint: "/v1/responses", Operation: "responses",
		Model: "test", RetainPayload: &retain, PayloadExpiresAt: &expiresAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"api_key":"secret","apiKey":"camel-secret","accessToken":"camel-token","prompt":"hello","max_tokens":128,"url":"https://url-user:url-password@example.test/file?X-Amz-Signature=secret&part=1","message":"download https://embedded-user:embedded-password@example.test/file?X-Goog-Credential=embedded-credential&X-Goog-Signature=embedded-signature","image":"data:image/png;base64,` + strings.Repeat("a", 1100) + `"}`)
	payload := &model.APICallPayload{CallID: call.ID, Kind: model.APICallPayloadRequest, Data: original}
	if err := service.RecordPayload(payload); err != nil {
		t.Fatal(err)
	}
	var stored model.APICallPayload
	if err := model.DB().First(&stored, payload.ID).Error; err != nil {
		t.Fatal(err)
	}
	text := string(stored.Data)
	if stored.OriginalBytes != int64(len(original)) || stored.ExpiresAt == nil ||
		strings.Contains(text, "secret") || strings.Contains(text, "camel-token") ||
		strings.Contains(text, "embedded-credential") || strings.Contains(text, "url-user") ||
		strings.Contains(text, "embedded-user") || !strings.Contains(text, "[REDACTED]") ||
		!strings.Contains(text, "[OMITTED]") || !strings.Contains(text, `"prompt":"hello"`) ||
		!strings.Contains(text, `"max_tokens":128`) {
		t.Fatalf("stored payload was not sanitized: %+v %s", stored, text)
	}

	large := &model.APICallPayload{
		CallID: call.ID, Kind: model.APICallPayloadResponse,
		Data: bytes.Repeat([]byte("x"), config.DefaultAPICallPayloadMaxBytes+10),
	}
	if err := service.RecordPayload(large); err != nil {
		t.Fatal(err)
	}
	if !large.Truncated || len(large.Data) != config.DefaultAPICallPayloadMaxBytes {
		t.Fatalf("large payload was not truncated: len=%d truncated=%v", len(large.Data), large.Truncated)
	}

	past := time.Now().Add(-time.Hour)
	expired := &model.APICallPayload{CallID: call.ID, Kind: "expired", Data: []byte(`{}`), ExpiresAt: &past}
	if err := service.RecordPayload(expired); err != nil {
		t.Fatal(err)
	}
	deleted, err := service.DeleteExpiredPayloads(time.Now(), 100)
	if err != nil || deleted != 1 {
		t.Fatalf("delete expired payloads = %d, %v", deleted, err)
	}
	var count int64
	if err := model.DB().Model(&model.APICallPayload{}).Where("id = ?", expired.ID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("expired payload remains: count=%d err=%v", count, err)
	}
}

func TestAPICallPayloadCaptureHonorsRetentionAndTracksOriginalSize(t *testing.T) {
	setupAPICallServiceTestDB(t)
	callService := NewAPICallService()
	retained := true
	call, err := callService.StartCall(&StartCallRequest{
		UserID: 1, TokenID: 2, Endpoint: "/v1/messages", Operation: "anthropic.messages",
		Model: "test", RetainPayload: &retained,
	})
	if err != nil {
		t.Fatal(err)
	}
	capture, err := callService.NewPayloadCapture(
		call.ID, 0, model.APICallPayloadResponse, "text/event-stream",
	)
	if err != nil || capture == nil {
		t.Fatalf("create retained capture: capture=%v err=%v", capture, err)
	}
	data := bytes.Repeat([]byte("x"), config.DefaultAPICallPayloadMaxBytes+17)
	if written, err := capture.Write(data); err != nil || written != len(data) {
		t.Fatalf("capture write = %d, %v", written, err)
	}
	if err := capture.Save(); err != nil {
		t.Fatal(err)
	}

	var stored model.APICallPayload
	if err := model.DB().Where("call_id = ? AND kind = ?", call.ID, model.APICallPayloadResponse).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.OriginalBytes != int64(len(data)) || !stored.Truncated || len(stored.Data) != config.DefaultAPICallPayloadMaxBytes {
		t.Fatalf("unexpected capture metadata: original=%d truncated=%v len=%d", stored.OriginalBytes, stored.Truncated, len(stored.Data))
	}

	retained = false
	disabled, err := callService.StartCall(&StartCallRequest{
		UserID: 1, TokenID: 2, Endpoint: "/v1/messages", Operation: "anthropic.messages",
		Model: "test", RetainPayload: &retained,
	})
	if err != nil {
		t.Fatal(err)
	}
	capture, err = callService.NewPayloadCapture(disabled.ID, 0, model.APICallPayloadResponse, "text/event-stream")
	if err != nil || capture != nil {
		t.Fatalf("disabled capture = %v, %v", capture, err)
	}
}

func TestAPICallPayloadEncryptionAndSSESanitization(t *testing.T) {
	setupAPICallServiceTestDB(t)
	previousConfig := config.C
	config.C = &config.Config{Observability: config.ObservabilityConfig{
		APICallPayloadEncryptionKey: "payload-encryption-test-secret",
	}}
	t.Cleanup(func() { config.C = previousConfig })

	callService := NewAPICallService()
	retain := true
	call, err := callService.StartCall(&StartCallRequest{
		UserID: 1, TokenID: 2, Endpoint: "/v1/responses", Operation: "responses",
		Model: "test", RetainPayload: &retain,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte("data: Bearer top-secret\n\ndata: https://example.test/result?X-Amz-Signature=private&part=1\n\n")
	payload := &model.APICallPayload{
		CallID: call.ID, Kind: model.APICallPayloadResponse,
		ContentType: "text/event-stream", Data: raw,
	}
	if err := callService.RecordPayload(payload); err != nil {
		t.Fatal(err)
	}
	var stored model.APICallPayload
	if err := model.DB().First(&stored, payload.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !stored.Encrypted || bytes.Contains(stored.Data, []byte("top-secret")) || bytes.Contains(stored.Data, []byte("private")) {
		t.Fatalf("payload was not encrypted: encrypted=%v data=%q", stored.Encrypted, stored.Data)
	}

	detail, err := callService.GetCallDetail(call.ID, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Payloads) != 1 || strings.Contains(detail.Payloads[0].Data, "top-secret") ||
		strings.Contains(detail.Payloads[0].Data, "private") || !strings.Contains(detail.Payloads[0].Data, "[REDACTED]") {
		t.Fatalf("decrypted payload was not sanitized: %+v", detail.Payloads)
	}
}

func setupAPICallServiceTestDB(t *testing.T) {
	t.Helper()
	db := setupTestDB(t)
	if err := db.AutoMigrate(
		&model.APICall{},
		&model.APICallAttempt{},
		&model.APICallPayload{},
		&model.Endpoint{},
	); err != nil {
		t.Fatalf("migrate api call ledger: %v", err)
	}
}

func startAPICallForTest(
	t *testing.T,
	service *APICallService,
	userID uint,
	store bool,
	requestID string,
	modelName string,
) *model.APICall {
	t.Helper()
	call, err := service.StartCall(&StartCallRequest{
		RequestID:     requestID,
		UserID:        userID,
		TokenID:       userID + 1000,
		Endpoint:      "/v1/responses",
		Operation:     "responses.create",
		Model:         modelName,
		Store:         store,
		RetainPayload: boolPointer(store),
	})
	if err != nil {
		t.Fatalf("start call: %v", err)
	}
	return call
}

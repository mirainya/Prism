package model

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func TestAPICallLedgerModelsMigrateAndPersist(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(
		&APICall{},
		&APICallAttempt{},
		&APICallPayload{},
		&BillingLog{},
		&Task{},
		&AIResponse{},
		&Conversation{},
		&Message{},
	); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		model  any
		column string
	}{
		{&BillingLog{}, "pricing_snapshot"},
		{&Task{}, "call_id"},
		{&AIResponse{}, "call_id"},
		{&Conversation{}, "call_id"},
		{&Message{}, "call_id"},
	} {
		if !database.Migrator().HasColumn(check.model, check.column) {
			t.Fatalf("%T is missing %s", check.model, check.column)
		}
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	call := APICall{
		ID:             "call_ledger_test",
		RequestID:      "request-ledger-test",
		UserID:         1,
		TokenID:        2,
		Endpoint:       "/v1/responses",
		Operation:      "responses",
		Model:          "test-model",
		Status:         APICallStatusCompleted,
		InputTokens:    10,
		OutputTokens:   5,
		TotalTokens:    15,
		UsageJSON:      datatypes.JSON(`{"input_tokens":10,"output_tokens":5,"total_tokens":15}`),
		ReservedAmount: decimal.RequireFromString("0.02000000"),
		FinalCost:      decimal.RequireFromString("0.01500000"),
		RefundedAmount: decimal.RequireFromString("0.00500000"),
		HTTPStatus:     200,
		StartedAt:      now,
		CompletedAt:    &now,
		DurationMs:     25,
	}
	if err := database.Create(&call).Error; err != nil {
		t.Fatal(err)
	}

	attempt := APICallAttempt{
		CallID:             call.ID,
		AttemptNo:          1,
		AbilityID:          3,
		ChannelID:          4,
		KeyID:              5,
		Protocol:           ProtocolOpenAI,
		VendorModel:        "provider-model",
		Transport:          UpstreamTransportOpenAIResponses,
		Status:             APICallAttemptStatusCompleted,
		HTTPStatus:         200,
		ProviderResponseID: "resp_provider",
		StartedAt:          now,
		CompletedAt:        &now,
	}
	if err := database.Create(&attempt).Error; err != nil {
		t.Fatal(err)
	}
	payload := APICallPayload{
		CallID:        call.ID,
		AttemptID:     attempt.ID,
		Kind:          APICallPayloadResponse,
		ContentType:   "application/json",
		Data:          []byte(`{"id":"resp_provider"}`),
		OriginalBytes: 22,
	}
	if err := database.Create(&payload).Error; err != nil {
		t.Fatal(err)
	}
	billing := BillingLog{
		IdempotentKey:   "ledger-test",
		TokenID:         call.TokenID,
		UserID:          call.UserID,
		CallID:          call.ID,
		AttemptID:       attempt.ID,
		Phase:           BillingPhaseSettle,
		PricingSnapshot: datatypes.JSON(`{"input_price":"1.0"}`),
		Amount:          call.FinalCost,
		Type:            BillingTypeDeduct,
		Status:          "success",
	}
	if err := database.Create(&billing).Error; err != nil {
		t.Fatal(err)
	}

	var stored APICall
	if err := database.First(&stored, "id = ?", call.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != APICallStatusCompleted || stored.TotalTokens != 15 || !stored.FinalCost.Equal(call.FinalCost) {
		t.Fatalf("stored call = %#v", stored)
	}
	var attemptCount, payloadCount, billingCount int64
	database.Model(&APICallAttempt{}).Where("call_id = ?", call.ID).Count(&attemptCount)
	database.Model(&APICallPayload{}).Where("call_id = ?", call.ID).Count(&payloadCount)
	database.Model(&BillingLog{}).Where("call_id = ?", call.ID).Count(&billingCount)
	if attemptCount != 1 || payloadCount != 1 || billingCount != 1 {
		t.Fatalf("attempts=%d payloads=%d billing=%d", attemptCount, payloadCount, billingCount)
	}
}

func TestAPICallAttemptNumberIsUniquePerCall(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&APICallAttempt{}); err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	first := APICallAttempt{CallID: "call_unique", AttemptNo: 1, Status: APICallAttemptStatusStarted, StartedAt: started}
	if err := database.Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	duplicate := APICallAttempt{CallID: first.CallID, AttemptNo: first.AttemptNo, Status: APICallAttemptStatusStarted, StartedAt: started}
	if err := database.Create(&duplicate).Error; err == nil {
		t.Fatal("expected duplicate attempt number to fail")
	}
}

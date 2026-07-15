package service

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/mirainya/Prism/internal/model"
	"github.com/shopspring/decimal"
	"gorm.io/datatypes"
)

func TestBillingServiceDeductWithKeyRejectsInsufficientTokenBalance(t *testing.T) {
	setupTestDB(t)
	user, token := seedBillingAccount(t, decimal.NewFromInt(10), decimal.NewFromInt(2))

	err := NewBillingService().DeductWithKey(
		token.ID,
		user.ID,
		decimal.NewFromInt(3),
		"insufficient-token:reserve",
	)
	if !errors.Is(err, ErrInsufficientTokenBalance) {
		t.Fatalf("DeductWithKey error = %v, want %v", err, ErrInsufficientTokenBalance)
	}

	assertBillingBalances(t, user.ID, token.ID, decimal.NewFromInt(10), decimal.NewFromInt(2), decimal.Zero)
	assertBillingLogCount(t, "insufficient-token:reserve", 0)
}

func TestBillingServiceDeductWithKeyRollsBackWhenUserBalanceIsInsufficient(t *testing.T) {
	setupTestDB(t)
	user, token := seedBillingAccount(t, decimal.NewFromInt(1), decimal.NewFromInt(10))

	err := NewBillingService().DeductWithKey(
		token.ID,
		user.ID,
		decimal.NewFromInt(2),
		"insufficient-user:reserve",
	)
	if !errors.Is(err, ErrInsufficientUserBalance) {
		t.Fatalf("DeductWithKey error = %v, want %v", err, ErrInsufficientUserBalance)
	}

	assertBillingBalances(t, user.ID, token.ID, decimal.NewFromInt(1), decimal.NewFromInt(10), decimal.Zero)
	assertBillingLogCount(t, "insufficient-user:reserve", 0)
}

func TestBillingServiceSettleReservationRefundsCancellation(t *testing.T) {
	setupTestDB(t)
	user, token := seedBillingAccount(t, decimal.NewFromInt(10), decimal.NewFromInt(10))
	service := NewBillingService()
	reserved := decimal.NewFromInt(3)

	if err := service.DeductWithKey(token.ID, user.ID, reserved, "cancelled:reserve"); err != nil {
		t.Fatalf("reserve usage: %v", err)
	}
	if err := service.SettleReservation(token.ID, user.ID, reserved, decimal.Zero, "cancelled:settle"); err != nil {
		t.Fatalf("cancel reservation: %v", err)
	}

	assertBillingBalances(t, user.ID, token.ID, decimal.NewFromInt(10), decimal.NewFromInt(10), decimal.Zero)
	assertBillingLogCount(t, "cancelled:reserve", 1)
	assertBillingLogCount(t, "cancelled:settle", 1)
}

func TestBillingServiceWritesAccountBalanceEntries(t *testing.T) {
	setupTestDB(t)
	user, token := seedBillingAccount(t, decimal.NewFromInt(10), decimal.NewFromInt(10))
	billing := NewBillingService()
	reserved := decimal.NewFromInt(3)
	if err := billing.DeductWithKey(token.ID, user.ID, reserved, "ledger:reserve"); err != nil {
		t.Fatal(err)
	}
	if err := billing.SettleReservation(token.ID, user.ID, reserved, decimal.NewFromInt(2), "ledger:settle"); err != nil {
		t.Fatal(err)
	}

	var tokenEntries []model.BalanceEntry
	if err := model.DB().Where("account_type = ? AND account_id = ?", model.BalanceAccountToken, token.ID).
		Order("id ASC").Find(&tokenEntries).Error; err != nil {
		t.Fatal(err)
	}
	if len(tokenEntries) != 2 {
		t.Fatalf("token entries = %d", len(tokenEntries))
	}
	if tokenEntries[0].Direction != model.BalanceDirectionDebit || tokenEntries[0].Category != BalanceCategoryDeduction ||
		!tokenEntries[0].BalanceBefore.Equal(decimal.NewFromInt(10)) || !tokenEntries[0].BalanceAfter.Equal(decimal.NewFromInt(7)) {
		t.Fatalf("reserve entry = %#v", tokenEntries[0])
	}
	if tokenEntries[1].Direction != model.BalanceDirectionCredit || tokenEntries[1].Category != BalanceCategorySettlement ||
		!tokenEntries[1].BalanceBefore.Equal(decimal.NewFromInt(7)) || !tokenEntries[1].BalanceAfter.Equal(decimal.NewFromInt(8)) {
		t.Fatalf("settlement entry = %#v", tokenEntries[1])
	}

	var userEntryCount int64
	if err := model.DB().Model(&model.BalanceEntry{}).
		Where("account_type = ? AND account_id = ?", model.BalanceAccountUser, user.ID).
		Count(&userEntryCount).Error; err != nil {
		t.Fatal(err)
	}
	if userEntryCount != 2 {
		t.Fatalf("user entries = %d", userEntryCount)
	}
}

func TestBillingServiceSettleReservationAdjustsToActualCost(t *testing.T) {
	tests := []struct {
		name            string
		initialBalance  decimal.Decimal
		reserved        decimal.Decimal
		actual          decimal.Decimal
		expectedBalance decimal.Decimal
	}{
		{
			name:            "refunds unused reservation",
			initialBalance:  decimal.NewFromInt(10),
			reserved:        decimal.NewFromInt(4),
			actual:          decimal.RequireFromString("1.5"),
			expectedBalance: decimal.RequireFromString("8.5"),
		},
		{
			name:            "records usage above reservation",
			initialBalance:  decimal.NewFromInt(3),
			reserved:        decimal.NewFromInt(2),
			actual:          decimal.NewFromInt(4),
			expectedBalance: decimal.NewFromInt(-1),
		},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setupTestDB(t)
			user, token := seedBillingAccount(t, test.initialBalance, test.initialBalance)
			service := NewBillingService()
			keyPrefix := fmt.Sprintf("adjust-%d", index)

			if err := service.DeductWithKey(token.ID, user.ID, test.reserved, keyPrefix+":reserve"); err != nil {
				t.Fatalf("reserve usage: %v", err)
			}
			if err := service.SettleReservation(token.ID, user.ID, test.reserved, test.actual, keyPrefix+":settle"); err != nil {
				t.Fatalf("settle reservation: %v", err)
			}

			assertBillingBalances(t, user.ID, token.ID, test.expectedBalance, test.expectedBalance, test.actual)
		})
	}
}

func TestBillingServiceSettleReservationIsIdempotent(t *testing.T) {
	setupTestDB(t)
	user, token := seedBillingAccount(t, decimal.NewFromInt(10), decimal.NewFromInt(10))
	service := NewBillingService()
	reserved := decimal.NewFromInt(3)
	actual := decimal.NewFromInt(1)

	if err := service.DeductWithKey(token.ID, user.ID, reserved, "idempotent:reserve"); err != nil {
		t.Fatalf("reserve usage: %v", err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := service.SettleReservation(token.ID, user.ID, reserved, actual, "idempotent:settle"); err != nil {
			t.Fatalf("settle reservation attempt %d: %v", attempt+1, err)
		}
	}

	assertBillingBalances(t, user.ID, token.ID, decimal.NewFromInt(9), decimal.NewFromInt(9), actual)
	assertBillingLogCount(t, "idempotent:settle", 1)
}

func TestBillingServicePreservesEightDecimalPlaces(t *testing.T) {
	setupTestDB(t)
	initial := decimal.RequireFromString("1.00000000")
	reserved := decimal.RequireFromString("0.12345678")
	actual := decimal.RequireFromString("0.01234567")
	expected := decimal.RequireFromString("0.98765433")
	user, token := seedBillingAccount(t, initial, initial)
	billing := NewBillingService()

	if err := billing.DeductWithKey(token.ID, user.ID, reserved, "precision:reserve"); err != nil {
		t.Fatal(err)
	}
	if err := billing.SettleReservation(token.ID, user.ID, reserved, actual, "precision:settle"); err != nil {
		t.Fatal(err)
	}

	assertBillingBalances(t, user.ID, token.ID, expected, expected, actual)
	var logs []model.BillingLog
	if err := model.DB().Where("idempotent_key IN ?", []string{"precision:reserve", "precision:settle"}).Order("id ASC").Find(&logs).Error; err != nil {
		t.Fatal(err)
	}
	if len(logs) != 2 || !equalMoney(logs[0].Amount, reserved) || !equalMoney(logs[1].Amount, reserved.Sub(actual)) {
		t.Fatalf("precision billing logs = %#v", logs)
	}
}

func TestBillingServiceConcurrentDeductDoesNotOverspend(t *testing.T) {
	db := setupTestDB(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql DB: %v", err)
	}
	// A single SQLite connection makes the concurrency test deterministic while
	// still exercising simultaneous callers through the service transaction.
	sqlDB.SetMaxOpenConns(1)

	const (
		workers         = 20
		affordableCalls = 5
	)
	user, token := seedBillingAccount(t, decimal.NewFromInt(affordableCalls), decimal.NewFromInt(affordableCalls))
	service := NewBillingService()
	results := make(chan error, workers)
	var start sync.WaitGroup
	start.Add(1)

	var workersDone sync.WaitGroup
	workersDone.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func(worker int) {
			defer workersDone.Done()
			start.Wait()
			results <- service.DeductWithKey(
				token.ID,
				user.ID,
				decimal.NewFromInt(1),
				fmt.Sprintf("concurrent-%d:reserve", worker),
			)
		}(worker)
	}

	start.Done()
	workersDone.Wait()
	close(results)

	succeeded := 0
	insufficient := 0
	for result := range results {
		switch {
		case result == nil:
			succeeded++
		case errors.Is(result, ErrInsufficientTokenBalance):
			insufficient++
		default:
			t.Fatalf("unexpected concurrent deduction error: %v", result)
		}
	}
	if succeeded != affordableCalls {
		t.Fatalf("successful deductions = %d, want %d", succeeded, affordableCalls)
	}
	if insufficient != workers-affordableCalls {
		t.Fatalf("insufficient deductions = %d, want %d", insufficient, workers-affordableCalls)
	}

	assertBillingBalances(t, user.ID, token.ID, decimal.Zero, decimal.Zero, decimal.NewFromInt(affordableCalls))
}

func TestBillingServiceContextLinksLedgerAndPreservesSettledAmounts(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.APICall{}, &model.APICallAttempt{}); err != nil {
		t.Fatal(err)
	}
	user, token := seedBillingAccount(t, decimal.NewFromInt(10), decimal.NewFromInt(10))
	callService := NewAPICallService()
	call, err := callService.StartCall(&StartCallRequest{
		UserID: user.ID, TokenID: token.ID, Endpoint: "openai_responses",
		Operation: "responses", Model: "public",
	})
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := callService.StartAttempt(&StartAttemptRequest{
		CallID: call.ID, ChannelID: 2, KeyID: 3, Transport: model.UpstreamTransportOpenAIResponses,
	})
	if err != nil {
		t.Fatal(err)
	}
	billing := NewBillingService()
	reserved := decimal.NewFromInt(4)
	actual := decimal.RequireFromString("1.5")
	context := BillingContext{
		CallID: call.ID, AttemptID: attempt.ID, Phase: model.BillingPhaseReserve,
		PricingSnapshot: datatypes.JSON(`{"price_mode":"token","input_price":"1"}`),
	}
	if err := billing.DeductWithBillingContext(
		token.ID, user.ID, reserved, t.Name()+":reserve", context,
	); err != nil {
		t.Fatal(err)
	}
	context.Phase = model.BillingPhaseSettle
	if err := billing.SettleReservationWithBillingContext(
		token.ID, user.ID, reserved, actual, t.Name()+":settle", context,
	); err != nil {
		t.Fatal(err)
	}
	if err := callService.CompleteAttempt(attempt.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := callService.CompleteCall(call.ID, &CompleteCallRequest{FinalAttemptID: attempt.ID}); err != nil {
		t.Fatal(err)
	}

	var stored model.APICall
	if err := db.First(&stored, "id = ?", call.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !stored.ReservedAmount.Equal(reserved) || !stored.FinalCost.Equal(actual) ||
		!stored.RefundedAmount.Equal(decimal.RequireFromString("2.5")) {
		t.Fatalf("call amounts=%s/%s/%s", stored.ReservedAmount, stored.FinalCost, stored.RefundedAmount)
	}
	var logs []model.BillingLog
	if err := db.Where("call_id = ?", call.ID).Order("id ASC").Find(&logs).Error; err != nil {
		t.Fatal(err)
	}
	if len(logs) != 2 || logs[0].AttemptID != attempt.ID || logs[0].Phase != model.BillingPhaseReserve ||
		logs[1].Phase != model.BillingPhaseSettle || len(logs[1].PricingSnapshot) == 0 {
		t.Fatalf("billing logs=%#v", logs)
	}
}

func seedBillingAccount(t *testing.T, userBalance, tokenBalance decimal.Decimal) (*model.User, *model.Token) {
	t.Helper()
	uniqueID := GenerateTaskNo()
	user := &model.User{
		Username: "billing-user-" + uniqueID,
		Balance:  userBalance,
		Status:   1,
	}
	if err := model.DB().Create(user).Error; err != nil {
		t.Fatalf("create billing user: %v", err)
	}

	token := &model.Token{
		UserID:  user.ID,
		Key:     "billing-token-" + uniqueID,
		Balance: tokenBalance,
		Status:  1,
	}
	if err := model.DB().Create(token).Error; err != nil {
		t.Fatalf("create billing token: %v", err)
	}
	return user, token
}

func assertBillingBalances(
	t *testing.T,
	userID uint,
	tokenID uint,
	expectedUserBalance decimal.Decimal,
	expectedTokenBalance decimal.Decimal,
	expectedTotalUsed decimal.Decimal,
) {
	t.Helper()
	var user model.User
	if err := model.DB().First(&user, userID).Error; err != nil {
		t.Fatalf("reload billing user: %v", err)
	}
	var token model.Token
	if err := model.DB().First(&token, tokenID).Error; err != nil {
		t.Fatalf("reload billing token: %v", err)
	}
	if !equalMoney(user.Balance, expectedUserBalance) {
		t.Errorf("user balance = %s, want %s", user.Balance, expectedUserBalance)
	}
	if !equalMoney(token.Balance, expectedTokenBalance) {
		t.Errorf("token balance = %s, want %s", token.Balance, expectedTokenBalance)
	}
	if !equalMoney(token.TotalUsed, expectedTotalUsed) {
		t.Errorf("token total used = %s, want %s", token.TotalUsed, expectedTotalUsed)
	}
}

func equalMoney(left, right decimal.Decimal) bool {
	return left.Round(8).Equal(right.Round(8))
}

func assertBillingLogCount(t *testing.T, idempotentKey string, expected int64) {
	t.Helper()
	var count int64
	if err := model.DB().Model(&model.BillingLog{}).
		Where("idempotent_key = ?", idempotentKey).
		Count(&count).Error; err != nil {
		t.Fatalf("count billing logs: %v", err)
	}
	if count != expected {
		t.Errorf("billing log count for %q = %d, want %d", idempotentKey, count, expected)
	}
}

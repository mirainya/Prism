package service

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/mirainya/Prism/internal/model"
	"github.com/shopspring/decimal"
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
	if !user.Balance.Equal(expectedUserBalance) {
		t.Errorf("user balance = %s, want %s", user.Balance, expectedUserBalance)
	}
	if !token.Balance.Equal(expectedTokenBalance) {
		t.Errorf("token balance = %s, want %s", token.Balance, expectedTokenBalance)
	}
	if !token.TotalUsed.Equal(expectedTotalUsed) {
		t.Errorf("token total used = %s, want %s", token.TotalUsed, expectedTotalUsed)
	}
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

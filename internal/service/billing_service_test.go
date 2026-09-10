package service

import (
	"errors"
	"testing"

	"github.com/mirainya/Prism/internal/model"
	"github.com/shopspring/decimal"
)

func TestLegacyBillingRejectsMutationsWithoutWritingBalances(t *testing.T) {
	db := setupTestDB(t)
	user := &model.User{Username: "legacy-billing-owner", Balance: decimal.NewFromInt(10), Status: 1}
	if err := db.Create(user).Error; err != nil {
		t.Fatal(err)
	}
	token := &model.Token{UserID: user.ID, Selector: "legacy-disabled-token", Balance: decimal.NewFromInt(10), Status: 1}
	if err := db.Create(token).Error; err != nil {
		t.Fatal(err)
	}

	billing := NewBillingService()
	for name, err := range map[string]error{
		"deduct": billing.Deduct(token.ID, user.ID, decimal.NewFromInt(1)),
		"refund": billing.Refund(token.ID, user.ID, decimal.NewFromInt(1)),
		"settle": billing.SettleReservation(token.ID, user.ID, decimal.NewFromInt(1), decimal.NewFromInt(1), "legacy"),
	} {
		if !errors.Is(err, ErrLegacyBillingDisabled) {
			t.Fatalf("%s error=%v", name, err)
		}
	}

	var storedUser model.User
	var storedToken model.Token
	if err := db.First(&storedUser, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&storedToken, token.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !storedUser.Balance.Equal(decimal.NewFromInt(10)) || !storedToken.Balance.Equal(decimal.NewFromInt(10)) || !storedToken.TotalUsed.IsZero() {
		t.Fatalf("legacy balances changed: user=%s token=%s used=%s", storedUser.Balance, storedToken.Balance, storedToken.TotalUsed)
	}
	var entries int64
	if err := db.Model(&model.BalanceEntry{}).Count(&entries).Error; err != nil || entries != 0 {
		t.Fatalf("legacy entries=%d err=%v", entries, err)
	}
}

func TestLegacyBillingKeepsZeroAmountNoOp(t *testing.T) {
	billing := NewBillingService()
	if err := billing.Deduct(1, 1, decimal.Zero); err != nil {
		t.Fatal(err)
	}
	if err := billing.Refund(1, 1, decimal.Zero); err != nil {
		t.Fatal(err)
	}
	if err := billing.SettleReservation(1, 1, decimal.Zero, decimal.Zero, ""); err != nil {
		t.Fatal(err)
	}
}

package service

import (
	"testing"

	"github.com/mirainya/Prism/internal/model"
	"github.com/shopspring/decimal"
)

func TestTokenAndUserCreditsWriteBalanceEntries(t *testing.T) {
	setupTestDB(t)
	user := &model.User{Username: "balance-entry-owner", Status: 1}
	if err := model.DB().Create(user).Error; err != nil {
		t.Fatal(err)
	}
	tokenService := NewTokenService()
	created, err := tokenService.CreateToken(user.ID, &CreateTokenReq{Name: "primary", Balance: decimal.NewFromInt(5)})
	if err != nil {
		t.Fatal(err)
	}
	tokenID, ok := created["id"].(uint)
	if !ok || tokenID == 0 {
		t.Fatalf("created token id = %#v", created["id"])
	}
	if _, err := tokenService.RechargeToken(user.ID, tokenID, decimal.NewFromInt(2)); err != nil {
		t.Fatal(err)
	}
	if err := NewUserService().RechargeUserBy(99, user.ID, decimal.NewFromInt(3)); err != nil {
		t.Fatal(err)
	}

	var tokenEntries []model.BalanceEntry
	if err := model.DB().Where("account_type = ? AND account_id = ?", model.BalanceAccountToken, tokenID).
		Order("id ASC").Find(&tokenEntries).Error; err != nil {
		t.Fatal(err)
	}
	if len(tokenEntries) != 2 || tokenEntries[0].Category != BalanceCategoryInitialCredit ||
		tokenEntries[1].Category != BalanceCategoryRecharge ||
		!tokenEntries[1].BalanceBefore.Equal(decimal.NewFromInt(5)) ||
		!tokenEntries[1].BalanceAfter.Equal(decimal.NewFromInt(7)) {
		t.Fatalf("token entries = %#v", tokenEntries)
	}

	var userEntry model.BalanceEntry
	if err := model.DB().Where("account_type = ? AND account_id = ?", model.BalanceAccountUser, user.ID).
		First(&userEntry).Error; err != nil {
		t.Fatal(err)
	}
	if userEntry.ActorUserID != 99 || userEntry.Category != BalanceCategoryRecharge ||
		!userEntry.BalanceBefore.IsZero() || !userEntry.BalanceAfter.Equal(decimal.NewFromInt(3)) {
		t.Fatalf("user entry = %#v", userEntry)
	}
}

func TestRechargeRejectsNonPositiveAmountWithoutLedgerEntry(t *testing.T) {
	setupTestDB(t)
	user := &model.User{Username: "balance-entry-invalid", Status: 1}
	if err := model.DB().Create(user).Error; err != nil {
		t.Fatal(err)
	}
	if err := NewUserService().RechargeUser(user.ID, decimal.Zero); err != ErrInvalidBalanceAmount {
		t.Fatalf("error = %v", err)
	}
	var count int64
	if err := model.DB().Model(&model.BalanceEntry{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("entries=%d err=%v", count, err)
	}
}

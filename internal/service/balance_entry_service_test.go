package service

import (
	"testing"

	"github.com/mirainya/Prism/internal/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

func setupUnifiedFundsSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	statements := []string{
		`CREATE TABLE billing_system_state (id INTEGER PRIMARY KEY, currency_code TEXT NOT NULL, currency_version INTEGER NOT NULL)`,
		`INSERT INTO billing_system_state(id,currency_code,currency_version) VALUES (1,'CNY',1)`,
		`CREATE TABLE billing_accounts (id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER NOT NULL UNIQUE, currency_code TEXT NOT NULL, currency_version INTEGER NOT NULL, posted_balance TEXT NOT NULL DEFAULT '0', held_amount TEXT NOT NULL DEFAULT '0', credit_limit TEXT NOT NULL DEFAULT '0', status TEXT NOT NULL, state_version INTEGER NOT NULL DEFAULT 1, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL)`,
		`CREATE TABLE ledger_accounts (id INTEGER PRIMARY KEY AUTOINCREMENT, billing_account_id INTEGER, system_account_code TEXT, currency_code TEXT NOT NULL, currency_version INTEGER NOT NULL, balance TEXT NOT NULL DEFAULT '0', created_at DATETIME NOT NULL)`,
		`CREATE TABLE billing_account_state_events (id INTEGER PRIMARY KEY AUTOINCREMENT, billing_account_id INTEGER NOT NULL, state_version INTEGER NOT NULL, old_state TEXT, new_state TEXT NOT NULL, reason_code TEXT NOT NULL, created_at DATETIME NOT NULL)`,
		`CREATE TABLE token_budget_policies (id INTEGER PRIMARY KEY AUTOINCREMENT, token_id INTEGER NOT NULL, policy_code TEXT NOT NULL, window_kind TEXT NOT NULL, limit_amount TEXT, period_seconds INTEGER, timezone_name TEXT NOT NULL, algorithm_version INTEGER NOT NULL, created_at DATETIME NOT NULL)`,
		`CREATE TABLE token_budget_policy_activations (id INTEGER PRIMARY KEY AUTOINCREMENT, token_id INTEGER NOT NULL, policy_id INTEGER NOT NULL, activation_seq INTEGER NOT NULL, predecessor_id INTEGER, effective_at DATETIME NOT NULL, created_at DATETIME NOT NULL)`,
		`CREATE TABLE token_budget_windows (id INTEGER PRIMARY KEY AUTOINCREMENT, token_id INTEGER NOT NULL, policy_id INTEGER NOT NULL, activation_id INTEGER NOT NULL, window_start DATETIME NOT NULL, window_end DATETIME, limit_amount TEXT, used_amount TEXT NOT NULL DEFAULT '0', held_amount TEXT NOT NULL DEFAULT '0', created_at DATETIME NOT NULL)`,
		`CREATE TABLE token_budget_adjustment_events (id INTEGER PRIMARY KEY AUTOINCREMENT, budget_window_id INTEGER NOT NULL, adjustment_seq INTEGER NOT NULL, amount_delta TEXT NOT NULL, source_type TEXT NOT NULL, source_key TEXT NOT NULL, created_at DATETIME NOT NULL, UNIQUE(source_type,source_key))`,
		`CREATE TABLE token_auth_state_events (id INTEGER PRIMARY KEY AUTOINCREMENT, token_id INTEGER NOT NULL, auth_version INTEGER NOT NULL, old_status INTEGER NOT NULL, new_status INTEGER NOT NULL, reason_code TEXT NOT NULL, actor_user_id INTEGER NOT NULL, created_at DATETIME NOT NULL, UNIQUE(token_id,auth_version))`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create unified funds schema: %v", err)
		}
	}
}

func TestDeleteTokenRevokesAndVersionsAuthorization(t *testing.T) {
	db := setupTestDB(t)
	setupUnifiedFundsSchema(t, db)
	user := &model.User{Username: "token-revoke-owner", Status: 1}
	if err := db.Create(user).Error; err != nil {
		t.Fatal(err)
	}
	created, err := NewTokenService().CreateToken(user.ID, &CreateTokenReq{Name: "revoke", Balance: decimal.Zero})
	if err != nil {
		t.Fatal(err)
	}
	tokenID := created["id"].(uint)
	if err := NewTokenService().DeleteToken(user.ID, tokenID); err != nil {
		t.Fatal(err)
	}
	var token model.Token
	if err := db.Unscoped().First(&token, tokenID).Error; err != nil {
		t.Fatal(err)
	}
	if token.Status != 0 || token.RevokedAt == nil || token.AuthVersion != 2 || token.DeletedAt.Valid {
		t.Fatalf("revoked token=%+v", token)
	}
	var events int64
	if err := db.Table("token_auth_state_events").Where("token_id=? AND auth_version=2", tokenID).Count(&events).Error; err != nil || events != 1 {
		t.Fatalf("auth events=%d err=%v", events, err)
	}
}

func TestTokenCreditsUseUnifiedLifetimeBudget(t *testing.T) {
	db := setupTestDB(t)
	setupUnifiedFundsSchema(t, db)
	user := &model.User{Username: "unified-budget-owner", Status: 1}
	if err := db.Create(user).Error; err != nil {
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

	var stored model.Token
	if err := db.First(&stored, tokenID).Error; err != nil {
		t.Fatal(err)
	}
	if !stored.Balance.IsZero() || !stored.TotalUsed.IsZero() {
		t.Fatalf("legacy token amounts changed: balance=%s used=%s", stored.Balance, stored.TotalUsed)
	}

	recharged, err := tokenService.RechargeToken(user.ID, tokenID, decimal.NewFromInt(2))
	if err != nil {
		t.Fatal(err)
	}
	if !recharged.Balance.Equal(decimal.NewFromInt(7)) || !recharged.TotalUsed.IsZero() {
		t.Fatalf("unified token funds = %+v", recharged)
	}

	var limit string
	if err := db.Raw(`SELECT limit_amount FROM token_budget_windows WHERE token_id=?`, tokenID).Scan(&limit).Error; err != nil {
		t.Fatal(err)
	}
	if value, err := decimal.NewFromString(limit); err != nil || !value.Equal(decimal.NewFromInt(7)) {
		t.Fatalf("budget limit=%q err=%v", limit, err)
	}

	var legacyEntries int64
	if err := db.Model(&model.BalanceEntry{}).Count(&legacyEntries).Error; err != nil || legacyEntries != 0 {
		t.Fatalf("legacy balance entries=%d err=%v", legacyEntries, err)
	}
}

func TestRechargeRejectsNonPositiveAmountWithoutLegacyWrites(t *testing.T) {
	db := setupTestDB(t)
	user := &model.User{Username: "balance-entry-invalid", Status: 1}
	if err := db.Create(user).Error; err != nil {
		t.Fatal(err)
	}
	if err := NewUserService().RechargeUser(user.ID, decimal.Zero); err != ErrInvalidBalanceAmount {
		t.Fatalf("error = %v", err)
	}
	var count int64
	if err := db.Model(&model.BalanceEntry{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("entries=%d err=%v", count, err)
	}
}

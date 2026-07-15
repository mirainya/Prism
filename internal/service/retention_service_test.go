package service

import (
	"testing"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"github.com/shopspring/decimal"
)

func TestRetentionServiceDeletesTerminalCallGraphInBatches(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.APICallPayload{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	old := now.AddDate(0, 0, -100)
	oldCall := model.APICall{
		ID: "call-retention-old", UserID: 1, TokenID: 1, Endpoint: "/v1/responses",
		Operation: "responses", Model: "test", Status: model.APICallStatusCompleted,
		StartedAt: old, CompletedAt: &old, CreatedAt: old, UpdatedAt: old,
	}
	freshCall := model.APICall{
		ID: "call-retention-fresh", UserID: 1, TokenID: 1, Endpoint: "/v1/responses",
		Operation: "responses", Model: "test", Status: model.APICallStatusCompleted,
		StartedAt: now, CompletedAt: &now, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&[]model.APICall{oldCall, freshCall}).Error; err != nil {
		t.Fatal(err)
	}
	attempt := model.APICallAttempt{CallID: oldCall.ID, AttemptNo: 1, Status: model.APICallAttemptStatusCompleted, StartedAt: old}
	if err := db.Create(&attempt).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.APICallPayload{CallID: oldCall.ID, AttemptID: attempt.ID, Kind: model.APICallPayloadResponse}).Error; err != nil {
		t.Fatal(err)
	}

	deleted, err := NewRetentionService().DeleteExpiredCallMetadata(now.AddDate(0, 0, -90), 1)
	if err != nil || deleted != 1 {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
	for table, value := range map[string]any{
		"call": &model.APICall{}, "attempt": &model.APICallAttempt{}, "payload": &model.APICallPayload{},
	} {
		var count int64
		query := db.Model(value)
		if table == "call" {
			query = query.Where("id = ?", oldCall.ID)
		} else {
			query = query.Where("call_id = ?", oldCall.ID)
		}
		if err := query.Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("%s count=%d err=%v", table, count, err)
		}
	}
	var freshCount int64
	if err := db.Model(&model.APICall{}).Where("id = ?", freshCall.ID).Count(&freshCount).Error; err != nil || freshCount != 1 {
		t.Fatalf("fresh count=%d err=%v", freshCount, err)
	}
}

func TestRetentionServiceAppliesIndependentMetadataWindows(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.APIAccessLog{}, &model.AuditEvent{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	old := now.AddDate(0, 0, -40)
	if err := db.Create(&model.APIAccessLog{Method: "GET", Path: "/v1/models", Route: "/v1/models", CreatedAt: old}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.AuditEvent{Action: "POST /api/tokens", Outcome: "success", CreatedAt: old}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.BalanceEntry{
		EntryKey: "balance-retention", AccountType: model.BalanceAccountUser, AccountID: 1,
		UserID: 1, Direction: model.BalanceDirectionCredit, Category: BalanceCategoryRecharge,
		Amount: decimal.NewFromInt(1), BalanceBefore: decimal.Zero, BalanceAfter: decimal.NewFromInt(1), CreatedAt: old,
	}).Error; err != nil {
		t.Fatal(err)
	}

	retention := NewRetentionService()
	if deleted, err := retention.DeleteExpiredAPIAccessLogs(now.AddDate(0, 0, -30), 10); err != nil || deleted != 1 {
		t.Fatalf("access deleted=%d err=%v", deleted, err)
	}
	if deleted, err := retention.DeleteExpiredAuditEvents(now.AddDate(0, 0, -180), 10); err != nil || deleted != 0 {
		t.Fatalf("audit deleted=%d err=%v", deleted, err)
	}
	if deleted, err := retention.DeleteExpiredBalanceEntries(now.AddDate(0, 0, -365), 10); err != nil || deleted != 0 {
		t.Fatalf("balance deleted=%d err=%v", deleted, err)
	}
}

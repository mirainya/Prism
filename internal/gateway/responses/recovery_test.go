package responses

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	"github.com/shopspring/decimal"
)

func TestRequeuePendingBackgroundRestoresNonTerminalRecords(t *testing.T) {
	db := setupResponsesLifecycleDB(t, &model.AIResponse{})
	now := time.Now()
	records := []model.AIResponse{
		backgroundRecoveryRecord("resp_queued", "queued", now),
		backgroundRecoveryRecord("resp_progress", "in_progress", now),
		backgroundRecoveryRecord("resp_finalizing", "finalizing", now),
		backgroundRecoveryRecord("resp_completed", "completed", now),
		backgroundRecoveryRecord("resp_foreground", "queued", now),
	}
	records[4].Background = false
	if err := db.Create(&records).Error; err != nil {
		t.Fatal(err)
	}

	previousRecover := recoverResponseBackground
	var recoveredIDs []string
	recoverResponseBackground = func(id string) error {
		recoveredIDs = append(recoveredIDs, id)
		return nil
	}
	t.Cleanup(func() { recoverResponseBackground = previousRecover })

	count, err := RequeuePendingBackground(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("recovered count = %d, want 3", count)
	}
	slices.Sort(recoveredIDs)
	want := []string{"resp_finalizing", "resp_progress", "resp_queued"}
	if !slices.Equal(recoveredIDs, want) {
		t.Fatalf("recovered IDs = %v, want %v", recoveredIDs, want)
	}
	expectedStatus := map[string]string{
		"resp_queued": "queued", "resp_progress": "queued", "resp_finalizing": "result_ready",
	}
	for _, id := range want {
		var record model.AIResponse
		if err := db.First(&record, "id = ?", id).Error; err != nil {
			t.Fatal(err)
		}
		if record.Status != expectedStatus[id] {
			t.Fatalf("response %s status = %s, want %s", id, record.Status, expectedStatus[id])
		}
	}
}

func TestRequeuePendingBackgroundContinuesAfterEnqueueError(t *testing.T) {
	setupResponsesLifecycleDB(t, &model.AIResponse{})
	now := time.Now()
	records := []model.AIResponse{
		backgroundRecoveryRecord("resp_a", "queued", now),
		backgroundRecoveryRecord("resp_b", "queued", now),
	}
	if err := model.DB().Create(&records).Error; err != nil {
		t.Fatal(err)
	}

	previousRecover := recoverResponseBackground
	var calls []string
	recoverResponseBackground = func(id string) error {
		calls = append(calls, id)
		if id == "resp_a" {
			return errors.New("redis unavailable")
		}
		return nil
	}
	t.Cleanup(func() { recoverResponseBackground = previousRecover })

	count, err := RequeuePendingBackground(context.Background())
	if err == nil || count != 1 {
		t.Fatalf("count = %d, error = %v; want one recovery and an error", count, err)
	}
	if !slices.Equal(calls, []string{"resp_a", "resp_b"}) {
		t.Fatalf("recover calls = %v", calls)
	}
}

func TestRequeueQueuedBackgroundDoesNotResetActiveRecords(t *testing.T) {
	db := setupResponsesLifecycleDB(t, &model.AIResponse{})
	now := time.Now()
	records := []model.AIResponse{
		backgroundRecoveryRecord("resp_runtime_queued", "queued", now),
		backgroundRecoveryRecord("resp_runtime_active", "in_progress", now),
	}
	leaseExpiresAt := now.Add(time.Hour)
	records[1].LeaseOwner = "active-worker"
	records[1].LeaseExpiresAt = &leaseExpiresAt
	if err := db.Create(&records).Error; err != nil {
		t.Fatal(err)
	}
	previousRecover := recoverResponseBackground
	var recoveredIDs []string
	recoverResponseBackground = func(id string) error {
		recoveredIDs = append(recoveredIDs, id)
		return nil
	}
	t.Cleanup(func() { recoverResponseBackground = previousRecover })

	count, err := RequeueQueuedBackground(context.Background())
	if err != nil || count != 1 || !slices.Equal(recoveredIDs, []string{"resp_runtime_queued"}) {
		t.Fatalf("count=%d ids=%v err=%v", count, recoveredIDs, err)
	}
	var active model.AIResponse
	if err := db.First(&active, "id = ?", "resp_runtime_active").Error; err != nil {
		t.Fatal(err)
	}
	if active.Status != "in_progress" {
		t.Fatalf("active status = %s", active.Status)
	}
}

func TestReconcilePendingResponseRefunds(t *testing.T) {
	db := setupResponsesLifecycleDB(t, &model.User{}, &model.Token{}, &model.BillingLog{}, &model.BalanceEntry{}, &model.AIResponse{})
	user := model.User{Username: "refund-recovery", Balance: decimal.NewFromInt(10), Status: 1}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	token := model.Token{UserID: user.ID, Key: "refund-recovery-token", Balance: decimal.NewFromInt(10), Status: 1}
	if err := db.Create(&token).Error; err != nil {
		t.Fatal(err)
	}
	record := backgroundRecoveryRecord("resp_refund", "refund_pending_failed", time.Now())
	record.UserID = user.ID
	record.TokenID = token.ID
	if err := db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.NewBillingService().DeductWithKey(token.ID, user.ID, decimal.NewFromInt(2), record.ID+":reserve"); err != nil {
		t.Fatal(err)
	}

	count, err := ReconcilePendingResponseRefunds(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("reconciled = %d, want 1", count)
	}
	if err := db.First(&record, "id = ?", record.ID).Error; err != nil {
		t.Fatal(err)
	}
	if record.Status != "failed" {
		t.Fatalf("status = %s, want failed", record.Status)
	}
	if err := db.First(&token, token.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !token.Balance.Equal(decimal.NewFromInt(10)) {
		t.Fatalf("token balance = %s, want 10", token.Balance)
	}
}

func backgroundRecoveryRecord(id, status string, createdAt time.Time) model.AIResponse {
	return model.AIResponse{
		ID: id, UserID: 1, TokenID: 1, Model: "test", Status: status,
		Background: true, Store: true, RequestJSON: []byte(`{"model":"test","input":"hello","background":true}`),
		InputItems: []byte(`"hello"`), ResponseJSON: []byte(`{"status":"queued"}`),
		IdempotencyKey: "recovery-" + id, CreatedAt: createdAt,
	}
}

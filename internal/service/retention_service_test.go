package service

import (
	"strings"
	"testing"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

func TestPendingConversationProjectionPredicateUsesMySQL57CompatibleCorrelation(t *testing.T) {
	predicate := pendingConversationProjectionPredicate("")
	if strings.Contains(predicate, "JOIN api_calls AS latest_call ON") {
		t.Fatal("latest conversation call must not reference an outer query from JOIN ON")
	}
	for _, fragment := range []string{
		"OR EXISTS (",
		"FROM api_calls AS latest_call",
		"WHERE latest_call.id = conversations.call_id",
		"projection.previous_response_id = latest_call.resource_id",
	} {
		if !strings.Contains(predicate, fragment) {
			t.Fatalf("predicate is missing %q", fragment)
		}
	}
}

func TestRetentionServiceKeepsPendingProjectionCallUntilProjectionCompletes(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.APICallPayload{}, &model.ConversationProjectionOutbox{}); err != nil {
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
	if err := db.Create(&model.ConversationProjectionOutbox{
		CallID: oldCall.ID, CanonicalInput: []byte(`[]`), InputReady: true,
		CreatedAt: old, UpdatedAt: old,
	}).Error; err != nil {
		t.Fatal(err)
	}

	deleted, err := NewRetentionService().DeleteExpiredCallMetadata(now.AddDate(0, 0, -90), 1)
	if err != nil || deleted != 0 {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
	for table, value := range map[string]any{
		"call": &model.APICall{}, "attempt": &model.APICallAttempt{}, "payload": &model.APICallPayload{},
		"projection": &model.ConversationProjectionOutbox{},
	} {
		var count int64
		query := db.Model(value)
		if table == "call" {
			query = query.Where("id = ?", oldCall.ID)
		} else {
			query = query.Where("call_id = ?", oldCall.ID)
		}
		if err := query.Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("%s count=%d err=%v", table, count, err)
		}
	}
	if err := db.Where("call_id = ?", oldCall.ID).Delete(&model.ConversationProjectionOutbox{}).Error; err != nil {
		t.Fatal(err)
	}
	deleted, err = NewRetentionService().DeleteExpiredCallMetadata(now.AddDate(0, 0, -90), 1)
	if err != nil || deleted != 1 {
		t.Fatalf("deleted after projection=%d err=%v", deleted, err)
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
			t.Fatalf("%s count after projection=%d err=%v", table, count, err)
		}
	}
	var freshCount int64
	if err := db.Model(&model.APICall{}).Where("id = ?", freshCall.ID).Count(&freshCount).Error; err != nil || freshCount != 1 {
		t.Fatalf("fresh count=%d err=%v", freshCount, err)
	}
}

func TestRetentionServiceKeepsLatestCallReferencedByConversation(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.APICallPayload{}, &model.Conversation{}); err != nil {
		t.Fatal(err)
	}
	cutoff := time.Now().AddDate(0, 0, -90)
	old := cutoff.AddDate(0, 0, -10)
	completedAt := old
	calls := []model.APICall{
		{
			ID: "call-retention-conversation-latest", UserID: 1, TokenID: 2,
			ResourceType: "response", ResourceID: "resp_retention_latest", Status: model.APICallStatusCompleted,
			StartedAt: old, CompletedAt: &completedAt, CreatedAt: old, UpdatedAt: old,
		},
		{
			ID: "call-retention-conversation-unreferenced", UserID: 1, TokenID: 2,
			ResourceType: "response", ResourceID: "resp_retention_unreferenced", Status: model.APICallStatusCompleted,
			StartedAt: old, CompletedAt: &completedAt, CreatedAt: old.Add(time.Second), UpdatedAt: old,
		},
	}
	if err := db.Create(&calls).Error; err != nil {
		t.Fatal(err)
	}
	conversation := model.Conversation{
		UserID: 1, TokenID: 2, CallID: calls[0].ID, Title: "latest response mapping", Status: 1,
		BaseModel: model.BaseModel{CreatedAt: old, UpdatedAt: time.Now()},
	}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}

	deleted, err := NewRetentionService().DeleteExpiredCallMetadata(cutoff, 1)
	if err != nil || deleted != 1 {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
	assertRowCount(t, &model.APICall{}, "id = ?", []any{calls[0].ID}, 1)
	assertRowCount(t, &model.APICall{}, "id = ?", []any{calls[1].ID}, 0)
}

func TestRetentionServiceRechecksLatestConversationCallInsideTransaction(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.APICallPayload{}, &model.Conversation{}); err != nil {
		t.Fatal(err)
	}
	cutoff := time.Now().AddDate(0, 0, -90)
	old := cutoff.AddDate(0, 0, -10)
	completedAt := old
	call := model.APICall{
		ID: "call-retention-latest-recheck", UserID: 1, TokenID: 2,
		ResourceType: "response", ResourceID: "resp_retention_latest_recheck", Status: model.APICallStatusCompleted,
		StartedAt: old, CompletedAt: &completedAt, CreatedAt: old, UpdatedAt: old,
	}
	if err := db.Create(&call).Error; err != nil {
		t.Fatal(err)
	}
	staleCandidates := []string{call.ID}
	conversation := model.Conversation{
		UserID: 1, TokenID: 2, CallID: call.ID, Title: "latest call recheck", Status: 1,
		BaseModel: model.BaseModel{CreatedAt: old, UpdatedAt: time.Now()},
	}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}

	deleted, err := deleteExpiredCallCandidates(db, cutoff, staleCandidates, true, true)
	if err != nil || deleted != 0 {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
	assertRowCount(t, &model.APICall{}, "id = ?", []any{call.ID}, 1)
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

func TestRetentionServiceKeepsConversationReferencedByProjectionOutbox(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(
		&model.Conversation{}, &model.Message{}, &model.ConversationTurn{},
		&model.ConversationItem{}, &model.ConversationProjectionOutbox{},
	); err != nil {
		t.Fatal(err)
	}
	old := time.Now().AddDate(0, 0, -100)
	conversations := []model.Conversation{
		{UserID: 1, TokenID: 1, Title: "pending projection", Status: 1, BaseModel: model.BaseModel{CreatedAt: old, UpdatedAt: old}},
		{UserID: 1, TokenID: 1, Title: "unreferenced", Status: 1, BaseModel: model.BaseModel{CreatedAt: old, UpdatedAt: old}},
	}
	if err := db.Create(&conversations).Error; err != nil {
		t.Fatal(err)
	}
	entry := model.ConversationProjectionOutbox{
		CallID: "call-retention-conversation", ConversationID: conversations[0].ID,
		CanonicalInput: []byte(`[]`), InputReady: true, CreatedAt: old, UpdatedAt: old,
	}
	if err := db.Create(&entry).Error; err != nil {
		t.Fatal(err)
	}

	deleted, err := NewRetentionService().DeleteExpiredConversationHistory(time.Now().AddDate(0, 0, -90), 10)
	if err != nil || deleted != 1 {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
	var kept int64
	if err := db.Model(&model.Conversation{}).Where("id = ?", conversations[0].ID).Count(&kept).Error; err != nil || kept != 1 {
		t.Fatalf("referenced conversation count=%d err=%v", kept, err)
	}
	var removed int64
	if err := db.Model(&model.Conversation{}).Where("id = ?", conversations[1].ID).Count(&removed).Error; err != nil || removed != 0 {
		t.Fatalf("unreferenced conversation count=%d err=%v", removed, err)
	}
}

func TestRetentionServiceKeepsRequestLogReferencedByProjectionOutbox(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.ChannelRequestLog{}); err != nil {
		t.Fatal(err)
	}
	cutoff := time.Now().AddDate(0, 0, -90)
	old := cutoff.AddDate(0, 0, -10)
	logs := []model.ChannelRequestLog{
		{TaskNo: "projection-request-log", BaseModel: model.BaseModel{CreatedAt: old, UpdatedAt: old}},
		{TaskNo: "expired-request-log", BaseModel: model.BaseModel{CreatedAt: old.Add(time.Second), UpdatedAt: old}},
	}
	if err := db.Create(&logs).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ConversationProjectionOutbox{
		CallID: "call-retention-request-log", RequestLogID: logs[0].ID,
		CanonicalInput: []byte(`[]`), InputReady: true, CreatedAt: old, UpdatedAt: old,
	}).Error; err != nil {
		t.Fatal(err)
	}

	deleted, err := NewRetentionService().DeleteExpiredRequestLogs(cutoff, 1)
	if err != nil || deleted != 1 {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
	assertRowCount(t, &model.ChannelRequestLog{}, "id = ?", []any{logs[0].ID}, 1)
	assertRowCount(t, &model.ChannelRequestLog{}, "id = ?", []any{logs[1].ID}, 0)
}

func TestRetentionServiceKeepsProjectionCallLogBeforeOutputIsStaged(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.ChannelRequestLog{}); err != nil {
		t.Fatal(err)
	}
	cutoff := time.Now().AddDate(0, 0, -90)
	old := cutoff.AddDate(0, 0, -10)
	requestLog := model.ChannelRequestLog{
		CallID: "call-retention-output-pending", TaskNo: "output-pending",
		BaseModel: model.BaseModel{CreatedAt: old, UpdatedAt: old},
	}
	if err := db.Create(&requestLog).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ConversationProjectionOutbox{
		CallID: "call-retention-output-pending", RequestLogID: 0,
		CanonicalInput: []byte(`[]`), InputReady: true, CreatedAt: old, UpdatedAt: old,
	}).Error; err != nil {
		t.Fatal(err)
	}

	deleted, err := NewRetentionService().DeleteExpiredRequestLogs(cutoff, 1)
	if err != nil || deleted != 0 {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
	assertRowCount(t, &model.ChannelRequestLog{}, "id = ?", []any{requestLog.ID}, 1)
}

func TestRetentionServiceRechecksRequestLogProjectionInsideTransaction(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.ChannelRequestLog{}); err != nil {
		t.Fatal(err)
	}
	cutoff := time.Now().AddDate(0, 0, -90)
	old := cutoff.AddDate(0, 0, -10)
	requestLog := model.ChannelRequestLog{
		TaskNo:    "request-log-recheck",
		BaseModel: model.BaseModel{CreatedAt: old, UpdatedAt: old},
	}
	if err := db.Create(&requestLog).Error; err != nil {
		t.Fatal(err)
	}
	staleCandidates := []uint{requestLog.ID}
	if err := db.Create(&model.ConversationProjectionOutbox{
		CallID: "call-retention-request-log-recheck", RequestLogID: requestLog.ID,
		CanonicalInput: []byte(`[]`), InputReady: true, CreatedAt: old, UpdatedAt: old,
	}).Error; err != nil {
		t.Fatal(err)
	}

	deleted, err := deleteExpiredRequestLogCandidates(db, cutoff, staleCandidates, true)
	if err != nil || deleted != 0 {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
	assertRowCount(t, &model.ChannelRequestLog{}, "id = ?", []any{requestLog.ID}, 1)
}

func TestRetentionServiceKeepsImplicitPreviousResponseConversations(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(
		&model.Conversation{}, &model.Message{}, &model.ConversationTurn{}, &model.ConversationItem{},
		&model.AIResponse{},
	); err != nil {
		t.Fatal(err)
	}
	cutoff := time.Now().AddDate(0, 0, -90)
	old := cutoff.AddDate(0, 0, -10)
	conversations := []model.Conversation{
		{
			UserID: 1, TokenID: 2, Title: "provider response", Status: 1,
			ProviderResponseID: "provider-retention", BaseModel: model.BaseModel{CreatedAt: old, UpdatedAt: old},
		},
		{
			UserID: 1, TokenID: 2, Title: "public response", Status: 1, CallID: "call-retention-failed-latest",
			BaseModel: model.BaseModel{CreatedAt: old, UpdatedAt: old},
		},
		{
			UserID: 1, TokenID: 2, Title: "unreferenced", Status: 1,
			BaseModel: model.BaseModel{CreatedAt: old, UpdatedAt: old},
		},
	}
	if err := db.Create(&conversations).Error; err != nil {
		t.Fatal(err)
	}
	createRetentionAPICall(t, db, model.APICall{
		ID: "call-retention-response-original", UserID: 1, TokenID: 2,
		ResourceType: "response", ResourceID: "resp_retention_public", ConversationID: conversations[1].ID,
		Status: model.APICallStatusCompleted, StartedAt: old, CreatedAt: old, UpdatedAt: old,
	})
	createRetentionAPICall(t, db, model.APICall{
		ID: "call-retention-failed-latest", UserID: 1, TokenID: 2,
		ResourceType: "response", ResourceID: "resp_retention_failed", ConversationID: conversations[1].ID,
		Status: model.APICallStatusFailed, StartedAt: old, CreatedAt: old, UpdatedAt: old,
	})
	turn := model.ConversationTurn{
		ConversationID: conversations[1].ID, Sequence: 1, CallID: "call-retention-response-original",
		Model: "model-a", Status: model.ConversationTurnCompleted, CreatedAt: old, UpdatedAt: old,
	}
	if err := db.Create(&turn).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.AIResponse{
		ID: "resp_retention_public", UserID: 1, TokenID: 2, CallID: turn.CallID,
		Model: "model-a", Status: "completed", Store: true,
		IdempotencyKey: "internal:resp_retention_public", CreatedAt: old,
	}).Error; err != nil {
		t.Fatal(err)
	}
	for _, call := range []model.APICall{
		{
			ID: "call-retention-provider-pending", UserID: 1, TokenID: 2,
			Status: model.APICallStatusReceived, ProjectConversation: true, StartedAt: old, CreatedAt: old, UpdatedAt: old,
		},
		{
			ID: "call-retention-resource-pending", UserID: 1, TokenID: 2,
			Status: model.APICallStatusReceived, ProjectConversation: true, StartedAt: old, CreatedAt: old, UpdatedAt: old,
		},
	} {
		createRetentionAPICall(t, db, call)
	}
	entries := []model.ConversationProjectionOutbox{
		{
			CallID: "call-retention-provider-pending", PreviousResponseID: "provider-retention",
			CanonicalInput: []byte(`[]`), InputReady: true, CreatedAt: old, UpdatedAt: old,
		},
		{
			CallID: "call-retention-resource-pending", PreviousResponseID: "resp_retention_public",
			CanonicalInput: []byte(`[]`), InputReady: true, CreatedAt: old, UpdatedAt: old,
		},
	}
	if err := db.Create(&entries).Error; err != nil {
		t.Fatal(err)
	}

	deleted, err := NewRetentionService().DeleteExpiredConversationHistory(cutoff, 1)
	if err != nil || deleted != 1 {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
	assertRowCount(t, &model.Conversation{}, "id IN ?", []any{[]uint{conversations[0].ID, conversations[1].ID}}, 2)
	assertRowCount(t, &model.Conversation{}, "id = ?", []any{conversations[2].ID}, 0)
}

func TestRetentionServiceScopesImplicitPreviousResponseProtection(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(
		&model.Conversation{}, &model.Message{}, &model.ConversationTurn{}, &model.ConversationItem{},
	); err != nil {
		t.Fatal(err)
	}
	cutoff := time.Now().AddDate(0, 0, -90)
	old := cutoff.AddDate(0, 0, -10)
	conversations := []model.Conversation{
		{UserID: 1, TokenID: 2, Title: "ambiguous one", Status: 1, ProviderResponseID: "shared-provider", BaseModel: model.BaseModel{CreatedAt: old, UpdatedAt: old}},
		{UserID: 1, TokenID: 2, Title: "ambiguous two", Status: 1, ProviderResponseID: "shared-provider", BaseModel: model.BaseModel{CreatedAt: old, UpdatedAt: old}},
		{UserID: 9, TokenID: 9, Title: "other owner", Status: 1, ProviderResponseID: "shared-provider", BaseModel: model.BaseModel{CreatedAt: old, UpdatedAt: old}},
		{UserID: 1, TokenID: 2, Title: "empty identifier", Status: 1, BaseModel: model.BaseModel{CreatedAt: old, UpdatedAt: old}},
	}
	if err := db.Create(&conversations).Error; err != nil {
		t.Fatal(err)
	}
	createRetentionAPICall(t, db, model.APICall{
		ID: "call-retention-ambiguous", UserID: 1, TokenID: 2,
		Status: model.APICallStatusReceived, ProjectConversation: true, StartedAt: old, CreatedAt: old, UpdatedAt: old,
	})
	entries := []model.ConversationProjectionOutbox{
		{
			CallID: "call-retention-ambiguous", PreviousResponseID: "shared-provider",
			CanonicalInput: []byte(`[]`), InputReady: true, CreatedAt: old, UpdatedAt: old,
		},
		{
			CallID: "call-retention-empty-previous", PreviousResponseID: "",
			CanonicalInput: []byte(`[]`), InputReady: true, CreatedAt: old, UpdatedAt: old,
		},
	}
	if err := db.Create(&entries).Error; err != nil {
		t.Fatal(err)
	}

	deleted, err := NewRetentionService().DeleteExpiredConversationHistory(cutoff, 10)
	if err != nil || deleted != 2 {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
	assertRowCount(t, &model.Conversation{}, "id IN ?", []any{[]uint{conversations[0].ID, conversations[1].ID}}, 2)
	assertRowCount(t, &model.Conversation{}, "id IN ?", []any{[]uint{conversations[2].ID, conversations[3].ID}}, 0)
}

func TestRetentionServiceRechecksImplicitConversationProjectionInsideTransaction(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(
		&model.Conversation{}, &model.Message{}, &model.ConversationTurn{}, &model.ConversationItem{},
	); err != nil {
		t.Fatal(err)
	}
	cutoff := time.Now().AddDate(0, 0, -90)
	old := cutoff.AddDate(0, 0, -10)
	conversation := model.Conversation{
		UserID: 1, TokenID: 2, Title: "transaction recheck", Status: 1,
		ProviderResponseID: "provider-transaction-recheck",
		BaseModel:          model.BaseModel{CreatedAt: old, UpdatedAt: old},
	}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Message{
		ConversationID: conversation.ID, Role: model.RoleUser, Content: "retained", CreatedAt: old,
	}).Error; err != nil {
		t.Fatal(err)
	}
	staleCandidates := []uint{conversation.ID}
	createRetentionAPICall(t, db, model.APICall{
		ID: "call-retention-conversation-recheck", UserID: 1, TokenID: 2,
		Status: model.APICallStatusReceived, ProjectConversation: true, StartedAt: old, CreatedAt: old, UpdatedAt: old,
	})
	if err := db.Create(&model.ConversationProjectionOutbox{
		CallID: "call-retention-conversation-recheck", PreviousResponseID: "provider-transaction-recheck",
		CanonicalInput: []byte(`[]`), InputReady: true, CreatedAt: old, UpdatedAt: old,
	}).Error; err != nil {
		t.Fatal(err)
	}

	deleted, err := deleteExpiredConversationCandidates(db, cutoff, staleCandidates, true)
	if err != nil || deleted != 0 {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
	assertRowCount(t, &model.Conversation{}, "id = ?", []any{conversation.ID}, 1)
	assertRowCount(t, &model.Message{}, "conversation_id = ?", []any{conversation.ID}, 1)
}

func TestRetentionServiceWorksWithoutProjectionOutboxTable(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(
		&model.ChannelRequestLog{}, &model.Conversation{}, &model.Message{},
		&model.ConversationTurn{}, &model.ConversationItem{},
	); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrator().DropTable(&model.ConversationProjectionOutbox{}); err != nil {
		t.Fatal(err)
	}
	cutoff := time.Now().AddDate(0, 0, -90)
	old := cutoff.AddDate(0, 0, -10)
	requestLog := model.ChannelRequestLog{
		TaskNo: "no-projection-table", BaseModel: model.BaseModel{CreatedAt: old, UpdatedAt: old},
	}
	conversation := model.Conversation{
		UserID: 1, TokenID: 2, Title: "no projection table", Status: 1,
		BaseModel: model.BaseModel{CreatedAt: old, UpdatedAt: old},
	}
	if err := db.Create(&requestLog).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}

	if deleted, err := NewRetentionService().DeleteExpiredRequestLogs(cutoff, 10); err != nil || deleted != 1 {
		t.Fatalf("request logs deleted=%d err=%v", deleted, err)
	}
	if deleted, err := NewRetentionService().DeleteExpiredConversationHistory(cutoff, 10); err != nil || deleted != 1 {
		t.Fatalf("conversations deleted=%d err=%v", deleted, err)
	}
}

func createRetentionAPICall(t *testing.T, db *gorm.DB, call model.APICall) {
	t.Helper()
	if err := db.Create(&call).Error; err != nil {
		t.Fatal(err)
	}
}

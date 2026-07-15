package service

import (
	"testing"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"gorm.io/datatypes"
)

func TestRetentionDeletesExpiredTaskAndConversationHistory(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(
		&model.Task{},
		&model.Conversation{},
		&model.Message{},
		&model.ConversationTurn{},
		&model.ConversationItem{},
	); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -100)
	fresh := now.AddDate(0, 0, -10)

	oldConversation := model.Conversation{
		UserID: 1, TokenID: 1, Title: "old", Status: 1,
		BaseModel: model.BaseModel{CreatedAt: old, UpdatedAt: old},
	}
	freshConversation := model.Conversation{
		UserID: 1, TokenID: 1, Title: "fresh", Status: 1,
		BaseModel: model.BaseModel{CreatedAt: fresh, UpdatedAt: fresh},
	}
	if err := db.Create(&oldConversation).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&freshConversation).Error; err != nil {
		t.Fatal(err)
	}
	oldTurn := model.ConversationTurn{
		ConversationID: oldConversation.ID, Sequence: 1,
		Status: model.ConversationTurnCompleted, CreatedAt: old, UpdatedAt: old,
	}
	if err := db.Create(&oldTurn).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ConversationItem{
		ConversationID: oldConversation.ID, TurnID: oldTurn.ID, TurnSequence: 1,
		Direction: model.ConversationItemInput, Ordinal: 0,
		CanonicalJSON: datatypes.JSON(`{"type":"message","role":"user"}`), CreatedAt: old,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Message{
		ConversationID: oldConversation.ID, Role: model.RoleUser, Content: "old", CreatedAt: old,
	}).Error; err != nil {
		t.Fatal(err)
	}

	completedOld := old
	completedFresh := fresh
	tasks := []model.Task{
		{TaskNo: "old-terminal", Status: model.TaskStatusSuccess, CompletedAt: &completedOld, BaseModel: model.BaseModel{CreatedAt: old, UpdatedAt: old}},
		{TaskNo: "fresh-terminal", Status: model.TaskStatusFailed, CompletedAt: &completedFresh, BaseModel: model.BaseModel{CreatedAt: fresh, UpdatedAt: fresh}},
		{TaskNo: "old-active", Status: model.TaskStatusProcessing, BaseModel: model.BaseModel{CreatedAt: old, UpdatedAt: old}},
	}
	if err := db.Create(&tasks).Error; err != nil {
		t.Fatal(err)
	}

	retention := NewRetentionService()
	cutoff := now.AddDate(0, 0, -90)
	if deleted, err := retention.DeleteExpiredConversationHistory(cutoff, 100); err != nil || deleted != 1 {
		t.Fatalf("conversation deletion count=%d err=%v", deleted, err)
	}
	if deleted, err := retention.DeleteExpiredTaskHistory(cutoff, 100); err != nil || deleted != 1 {
		t.Fatalf("task deletion count=%d err=%v", deleted, err)
	}

	assertRowCount(t, &model.Conversation{}, "", nil, 1)
	assertRowCount(t, &model.ConversationTurn{}, "", nil, 0)
	assertRowCount(t, &model.ConversationItem{}, "", nil, 0)
	assertRowCount(t, &model.Message{}, "", nil, 0)
	assertRowCount(t, &model.Task{}, "", nil, 2)
}

func assertRowCount(t *testing.T, value any, query string, args []any, want int64) {
	t.Helper()
	db := model.DB().Model(value)
	if query != "" {
		db = db.Where(query, args...)
	}
	var count int64
	if err := db.Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("%T count=%d want=%d", value, count, want)
	}
}

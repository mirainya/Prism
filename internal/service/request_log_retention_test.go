package service

import (
	"testing"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/config"
	"gorm.io/datatypes"
)

func TestClearExpiredBodiesAppliesLegacyPayloadPolicy(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.ChannelRequestLog{}, &model.AIResponse{}); err != nil {
		t.Fatal(err)
	}

	previousConfig := config.C
	config.C = &config.Config{Observability: config.ObservabilityConfig{
		RetainAPICallPayloads: true,
	}}
	t.Cleanup(func() { config.C = previousConfig })

	now := time.Date(2026, 7, 14, 22, 0, 0, 0, time.UTC)
	old := now.Add(-8 * 24 * time.Hour)
	jsonBody := datatypes.JSON(`{"input":"legacy"}`)
	requestLogs := []model.ChannelRequestLog{
		{
			BaseModel: model.BaseModel{CreatedAt: old, UpdatedAt: old},
			TaskNo:    "legacy-old-request-log", RequestBody: string(jsonBody),
		},
		{
			BaseModel: model.BaseModel{CreatedAt: now, UpdatedAt: now},
			TaskNo:    "legacy-fresh-request-log", RequestBody: string(jsonBody),
		},
	}
	if err := db.Create(&requestLogs).Error; err != nil {
		t.Fatal(err)
	}
	tasks := []model.Task{
		{
			TaskNo: "legacy-old-terminal", Status: model.TaskStatusSuccess,
			RequestParams: jsonBody, MappedParams: jsonBody,
			BaseModel: model.BaseModel{CreatedAt: old, UpdatedAt: old}, CompletedAt: &old,
		},
		{
			TaskNo: "legacy-fresh-terminal", Status: model.TaskStatusFailed,
			RequestParams: jsonBody, MappedParams: jsonBody,
			BaseModel: model.BaseModel{CreatedAt: now, UpdatedAt: now}, CompletedAt: &now,
		},
		{
			TaskNo: "legacy-old-pending", Status: model.TaskStatusProcessing,
			RequestParams: jsonBody, MappedParams: jsonBody,
			BaseModel: model.BaseModel{CreatedAt: old, UpdatedAt: old},
		},
	}
	if err := db.Create(&tasks).Error; err != nil {
		t.Fatal(err)
	}

	responses := []model.AIResponse{
		{
			ID: "resp_legacy_old", UserID: 1, TokenID: 1, Model: "test", Status: "completed",
			Store: false, IdempotencyKey: "internal:resp_legacy_old", CreatedAt: old, CompletedAt: &old,
			RequestJSON: jsonBody, InputItems: jsonBody, OutputItems: jsonBody, ResponseJSON: jsonBody, Metadata: jsonBody,
		},
		{
			ID: "resp_legacy_fresh", UserID: 1, TokenID: 1, Model: "test", Status: "completed",
			Store: false, IdempotencyKey: "internal:resp_legacy_fresh", CreatedAt: now, CompletedAt: &now,
			RequestJSON: jsonBody, InputItems: jsonBody, OutputItems: jsonBody, ResponseJSON: jsonBody, Metadata: jsonBody,
		},
		{
			ID: "resp_legacy_stored", UserID: 1, TokenID: 1, Model: "test", Status: "completed",
			Store: true, IdempotencyKey: "internal:resp_legacy_stored", CreatedAt: old, CompletedAt: &old,
			RequestJSON: jsonBody, InputItems: jsonBody, OutputItems: jsonBody, ResponseJSON: jsonBody, Metadata: jsonBody,
		},
		{
			ID: "resp_legacy_active", UserID: 1, TokenID: 1, Model: "test", Status: "in_progress",
			Store: false, IdempotencyKey: "internal:resp_legacy_active", CreatedAt: old,
			RequestJSON: jsonBody, InputItems: jsonBody, Metadata: jsonBody,
		},
	}
	if err := db.Create(&responses).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.AIResponse{}).
		Where("id IN ?", []string{"resp_legacy_old", "resp_legacy_fresh", "resp_legacy_active"}).
		UpdateColumn("store", false).Error; err != nil {
		t.Fatal(err)
	}

	service := NewRequestLogService()
	if _, err := service.ClearExpiredBodies(now, 7*24, 100); err != nil {
		t.Fatal(err)
	}
	assertRequestLogBody(t, "legacy-old-request-log", false)
	assertRequestLogBody(t, "legacy-fresh-request-log", true)
	assertLegacyTaskBody(t, "legacy-old-terminal", false)
	assertLegacyTaskBody(t, "legacy-fresh-terminal", true)
	assertLegacyTaskBody(t, "legacy-old-pending", true)
	assertLegacyResponseBody(t, "resp_legacy_old", false)
	assertLegacyResponseBody(t, "resp_legacy_fresh", true)
	assertLegacyResponseBody(t, "resp_legacy_stored", true)
	assertLegacyResponseBody(t, "resp_legacy_active", true)

	config.C.Observability.RetainAPICallPayloads = false
	if _, err := service.ClearExpiredBodies(now.Add(time.Second), 7*24, 100); err != nil {
		t.Fatal(err)
	}
	assertRequestLogBody(t, "legacy-fresh-request-log", false)
	assertLegacyTaskBody(t, "legacy-fresh-terminal", false)
	assertLegacyTaskBody(t, "legacy-old-pending", true)
	assertLegacyResponseBody(t, "resp_legacy_fresh", false)
	assertLegacyResponseBody(t, "resp_legacy_stored", true)
	assertLegacyResponseBody(t, "resp_legacy_active", true)
}

func assertRequestLogBody(t *testing.T, taskNo string, present bool) {
	t.Helper()
	var requestLog model.ChannelRequestLog
	if err := model.DB().Where("task_no = ?", taskNo).First(&requestLog).Error; err != nil {
		t.Fatal(err)
	}
	if got := requestLog.RequestBody != ""; got != present {
		t.Fatalf("request log %s body present=%v, want %v", taskNo, got, present)
	}
}

func assertLegacyTaskBody(t *testing.T, taskNo string, present bool) {
	t.Helper()
	var task model.Task
	if err := model.DB().Where("task_no = ?", taskNo).First(&task).Error; err != nil {
		t.Fatal(err)
	}
	if got := len(task.RequestParams) > 0 || len(task.MappedParams) > 0; got != present {
		t.Fatalf("task %s body present=%v, want %v", taskNo, got, present)
	}
}

func assertLegacyResponseBody(t *testing.T, id string, present bool) {
	t.Helper()
	var response model.AIResponse
	if err := model.DB().Where("id = ?", id).First(&response).Error; err != nil {
		t.Fatal(err)
	}
	got := len(response.RequestJSON) > 0 || len(response.InputItems) > 0 ||
		len(response.OutputItems) > 0 || len(response.ResponseJSON) > 0 || len(response.Metadata) > 0
	if got != present {
		t.Fatalf("response %s body present=%v, want %v", id, got, present)
	}
}

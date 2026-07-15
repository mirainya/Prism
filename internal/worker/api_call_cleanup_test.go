package worker

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/config"
	"github.com/mirainya/Prism/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func TestAPICallCleanupDrainsAllExpiredRequestLogBodies(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.ChannelRequestLog{}, &model.APICallPayload{}, &model.AIResponseIdempotencyCache{}); err != nil {
		t.Fatal(err)
	}
	model.SetDB(db)
	previousConfig := config.C
	config.C = &config.Config{Observability: config.ObservabilityConfig{RetainAPICallPayloads: true}}
	t.Cleanup(func() { config.C = previousConfig })
	previousLogger := logger.L
	logger.L = zap.NewNop()
	t.Cleanup(func() { logger.L = previousLogger })
	now := time.Date(2026, 7, 14, 20, 0, 0, 0, time.UTC)
	previousNow := timeNow
	timeNow = func() time.Time { return now }
	t.Cleanup(func() { timeNow = previousNow })

	logs := make([]model.ChannelRequestLog, payloadCleanupBatchSize+1)
	for index := range logs {
		logs[index] = model.ChannelRequestLog{
			BaseModel: model.BaseModel{CreatedAt: now.Add(-365 * 24 * time.Hour)},
			TaskNo:    fmt.Sprintf("expired-%d", index), RequestBody: "request", ResponseBody: "response",
		}
	}
	if err := db.CreateInBatches(&logs, 100).Error; err != nil {
		t.Fatal(err)
	}
	fresh := model.ChannelRequestLog{
		BaseModel: model.BaseModel{CreatedAt: now}, TaskNo: "fresh", RequestBody: "keep",
	}
	if err := db.Create(&fresh).Error; err != nil {
		t.Fatal(err)
	}
	expiredCache := model.AIResponseIdempotencyCache{
		TokenID: 1, IdempotencyKey: "expired", RequestHash: "expired",
		Status: model.ResponseIdempotencyCompleted, ExpiresAt: now.Add(-time.Minute),
	}
	freshCache := model.AIResponseIdempotencyCache{
		TokenID: 1, IdempotencyKey: "fresh", RequestHash: "fresh",
		Status: model.ResponseIdempotencyCompleted, ExpiresAt: now.Add(time.Minute),
	}
	if err := db.Create(&expiredCache).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&freshCache).Error; err != nil {
		t.Fatal(err)
	}

	if err := HandleAPICallPayloadCleanup(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	var expiredBodies, freshBodies int64
	if err := db.Model(&model.ChannelRequestLog{}).
		Where("task_no LIKE ? AND (request_body <> '' OR response_body <> '')", "expired-%").
		Count(&expiredBodies).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.ChannelRequestLog{}).
		Where("id = ? AND request_body = ?", fresh.ID, "keep").Count(&freshBodies).Error; err != nil {
		t.Fatal(err)
	}
	if expiredBodies != 0 || freshBodies != 1 {
		t.Fatalf("expired bodies=%d fresh bodies=%d", expiredBodies, freshBodies)
	}
	var expiredCaches, freshCaches int64
	if err := db.Model(&model.AIResponseIdempotencyCache{}).Where("idempotency_key = ?", "expired").Count(&expiredCaches).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.AIResponseIdempotencyCache{}).Where("idempotency_key = ?", "fresh").Count(&freshCaches).Error; err != nil {
		t.Fatal(err)
	}
	if expiredCaches != 0 || freshCaches != 1 {
		t.Fatalf("expired caches=%d fresh caches=%d", expiredCaches, freshCaches)
	}
}

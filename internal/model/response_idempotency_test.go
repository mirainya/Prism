package model

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestResponseIdempotencyKeysAreCaseSensitive(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&AIResponse{}, &AIResponseIdempotencyCache{}); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	responses := []AIResponse{
		{
			ID: "resp_case_upper", UserID: 1, TokenID: 1, Model: "test", Status: "completed",
			IdempotencyKey: "Case-Key", CreatedAt: now,
		},
		{
			ID: "resp_case_lower", UserID: 1, TokenID: 1, Model: "test", Status: "completed",
			IdempotencyKey: "case-key", CreatedAt: now,
		},
	}
	if err := db.Create(&responses).Error; err != nil {
		t.Fatalf("store case-distinct Responses keys: %v", err)
	}

	cache := []AIResponseIdempotencyCache{
		{
			TokenID: 1, IdempotencyKey: "Cache-Key", RequestHash: "upper",
			Status: ResponseIdempotencyPending, ExpiresAt: now.Add(time.Hour),
		},
		{
			TokenID: 1, IdempotencyKey: "cache-key", RequestHash: "lower",
			Status: ResponseIdempotencyPending, ExpiresAt: now.Add(time.Hour),
		},
	}
	if err := db.Create(&cache).Error; err != nil {
		t.Fatalf("store case-distinct cache keys: %v", err)
	}

	var responseCount, cacheCount int64
	if err := db.Model(&AIResponse{}).Where("token_id = ?", 1).Count(&responseCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&AIResponseIdempotencyCache{}).Where("token_id = ?", 1).Count(&cacheCount).Error; err != nil {
		t.Fatal(err)
	}
	if responseCount != 2 || cacheCount != 2 {
		t.Fatalf("responses=%d cache=%d, want 2 each", responseCount, cacheCount)
	}
}

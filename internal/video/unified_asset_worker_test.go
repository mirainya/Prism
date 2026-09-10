package video

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mirainya/Prism/internal/model"
)

func TestUnifiedAssetCleanupWorkerPurgesImmediately(t *testing.T) {
	service, db, _ := newUnifiedAssetTestService(t)
	for index := 0; index < 3; index++ {
		expiresAt := service.now().Add(-time.Minute)
		asset := model.MediaAsset{
			UserID: 20, TokenID: 21, Purpose: "input", ObjectKey: "cleanup-object-" + string(rune('a'+index)),
			StorageLocator: "https://cdn.example.test/cleanup-" + string(rune('a'+index)) + ".png",
			ContentType:    "image/png", ContentLength: 16, SHA256: "c2c657685f810899d4c3d826d8aba2123121062ebb0ffff937bbb76aaa8a93e8",
			State: "active", StateVersion: 2, RetentionUntil: &expiresAt, CreatedAt: service.now(), UpdatedAt: service.now(),
		}
		if err := db.Create(&asset).Error; err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- service.RunCleanup(ctx, UnifiedAssetCleanupPolicy{Interval: time.Hour, BatchSize: 2}, nil)
	}()

	deadline := time.Now().Add(time.Second)
	for {
		var remaining int64
		if err := db.Model(&model.MediaAsset{}).Where("state <> 'deleted'").Count(&remaining).Error; err != nil {
			t.Fatal(err)
		}
		if remaining == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cleanup left %d assets", remaining)
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("worker error=%v", err)
	}
}

func TestUnifiedAssetCleanupWorkerRejectsInvalidPolicy(t *testing.T) {
	service, _, _ := newUnifiedAssetTestService(t)
	for _, policy := range []UnifiedAssetCleanupPolicy{
		{},
		{Interval: time.Second},
		{Interval: time.Second, BatchSize: maxUnifiedAssetCleanupBatchSize + 1},
	} {
		if err := service.RunCleanup(context.Background(), policy, nil); !errors.Is(err, ErrInvalidAsset) {
			t.Fatalf("policy=%#v error=%v", policy, err)
		}
	}
}

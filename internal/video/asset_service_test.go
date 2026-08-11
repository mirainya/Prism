package video

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newAssetTestService(t *testing.T) *AssetService {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&VideoAsset{}); err != nil {
		t.Fatal(err)
	}
	service := NewAssetService(db)
	service.upload = func(context.Context, []byte, string, string) (string, error) {
		return "https://cdn.example.test/video-assets/file.png", nil
	}
	service.validate = func(context.Context, string) error { return nil }
	service.now = func() time.Time { return time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC) }
	return service
}

func TestAssetServiceUploadsAndDeduplicates(t *testing.T) {
	service := newAssetTestService(t)
	req := &CreateAssetRequest{
		TokenID: 7, Kind: "image", ContentType: "image/png",
		Data: []byte("\x89PNG\r\n\x1a\nasset-data"),
	}
	first, err := service.Create(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Create(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || first.Status != VideoAssetStatusReady {
		t.Fatalf("deduplicated assets = %#v, %#v", first, second)
	}
	if first.StoragePath != "https://cdn.example.test/video-assets/file.png" {
		t.Fatalf("storage path = %q", first.StoragePath)
	}
}

func TestAssetServiceEnforcesOwnershipAndExpiry(t *testing.T) {
	service := newAssetTestService(t)
	asset, err := service.Create(context.Background(), &CreateAssetRequest{
		TokenID: 9, Kind: "image", ContentType: "image/png",
		URL: "https://cdn.example.test/reference.png",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetReady(context.Background(), 10, asset.ID); !errors.Is(err, ErrAssetNotFound) {
		t.Fatalf("foreign token error = %v", err)
	}
	service.now = func() time.Time { return asset.ExpiresAt.Add(time.Second) }
	if _, err := service.GetReady(context.Background(), 9, asset.ID); !errors.Is(err, ErrAssetNotFound) {
		t.Fatalf("expired asset error = %v", err)
	}
}

func TestAssetServiceRejectsMismatchedKind(t *testing.T) {
	service := newAssetTestService(t)
	_, err := service.Create(context.Background(), &CreateAssetRequest{
		TokenID: 9, Kind: "video", ContentType: "image/png",
		URL: "https://cdn.example.test/reference.png", DurationSeconds: floatPtr(4),
	})
	if !errors.Is(err, ErrInvalidAsset) {
		t.Fatalf("error = %v, want ErrInvalidAsset", err)
	}
}

func TestAssetServiceAllowsVideoWithoutDuration(t *testing.T) {
	service := newAssetTestService(t)
	asset, err := service.Create(context.Background(), &CreateAssetRequest{
		TokenID: 9, Kind: "video", ContentType: "video/mp4",
		URL: "https://cdn.example.test/reference.mp4",
	})
	if err != nil {
		t.Fatal(err)
	}
	if asset.DurationSeconds != nil {
		t.Fatalf("duration = %v, want nil", asset.DurationSeconds)
	}
}

func floatPtr(value float64) *float64 { return &value }

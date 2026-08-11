package video

import (
	"bytes"
	"context"
	"errors"
	"io"
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
	service.upload = func(_ context.Context, reader io.Reader, _, _ string) (string, error) {
		if _, err := io.Copy(io.Discard, reader); err != nil {
			return "", err
		}
		return "https://cdn.example.test/video-assets/file.png", nil
	}
	service.remove = func(context.Context, string) error { return nil }
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

func TestAssetServiceStreamsUploadAndDeletesManagedFile(t *testing.T) {
	service := newAssetTestService(t)
	data := []byte("\x89PNG\r\n\x1a\nstreamed-asset")
	var uploaded []byte
	service.upload = func(_ context.Context, reader io.Reader, _, _ string) (string, error) {
		var err error
		uploaded, err = io.ReadAll(reader)
		return "https://cdn.example.test/video-assets/stream.png", err
	}
	var deletedURL string
	service.remove = func(_ context.Context, rawURL string) error {
		deletedURL = rawURL
		return nil
	}
	asset, err := service.Create(context.Background(), &CreateAssetRequest{
		TokenID: 11, Kind: "image", ContentType: "image/png",
		Reader: bytes.NewReader(data), SizeBytes: int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(uploaded, data) || asset.SizeBytes != int64(len(data)) {
		t.Fatalf("uploaded=%q size=%d", uploaded, asset.SizeBytes)
	}
	if err := service.Delete(context.Background(), 11, asset.ID); err != nil {
		t.Fatal(err)
	}
	if deletedURL != asset.StoragePath {
		t.Fatalf("deleted URL = %q, want %q", deletedURL, asset.StoragePath)
	}
}

func TestAssetServiceDoesNotDeleteExternalURL(t *testing.T) {
	service := newAssetTestService(t)
	removed := false
	service.remove = func(context.Context, string) error {
		removed = true
		return nil
	}
	asset, err := service.Create(context.Background(), &CreateAssetRequest{
		TokenID: 12, Kind: "image", ContentType: "image/png", URL: "https://external.example/image.png",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(context.Background(), 12, asset.ID); err != nil {
		t.Fatal(err)
	}
	if removed {
		t.Fatal("external URL was deleted from managed storage")
	}
}

func TestAssetServiceCleansPreviouslyExpiredManagedFile(t *testing.T) {
	service := newAssetTestService(t)
	asset, err := service.Create(context.Background(), &CreateAssetRequest{
		TokenID: 13, Kind: "image", ContentType: "image/png", Data: []byte("\x89PNG\r\n\x1a\nlegacy-expired"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.db.Model(asset).Update("status", VideoAssetStatusExpired).Error; err != nil {
		t.Fatal(err)
	}
	var deletedURL string
	service.remove = func(_ context.Context, rawURL string) error {
		deletedURL = rawURL
		return nil
	}
	count, err := service.ExpireReady(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || deletedURL == "" {
		t.Fatalf("cleanup count=%d deleted URL=%q", count, deletedURL)
	}
	var stored VideoAsset
	if err := service.db.First(&stored, "id = ?", asset.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.StoragePath != "" || stored.Status != VideoAssetStatusExpired {
		t.Fatalf("stored asset = %#v", stored)
	}
}

func floatPtr(value float64) *float64 { return &value }

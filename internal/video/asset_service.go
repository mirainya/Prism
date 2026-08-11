package video

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mirainya/Prism/pkg/config"
	"github.com/mirainya/Prism/pkg/filestorage"
	"github.com/mirainya/Prism/pkg/safeurl"
	"gorm.io/gorm"
)

const defaultAssetTTL = 7 * 24 * time.Hour

var (
	ErrAssetNotFound = errors.New("video asset not found")
	ErrAssetNotReady = errors.New("video asset is not ready")
	ErrInvalidAsset  = errors.New("invalid video asset")
	ErrAssetResolver = errors.New("video asset resolver is not supported")
	ErrFileTooLarge  = errors.New("video asset exceeds the configured size limit")
)

type AssetService struct {
	db       *gorm.DB
	upload   func(context.Context, io.Reader, string, string) (string, error)
	remove   func(context.Context, string) error
	validate func(context.Context, string) error
	now      func() time.Time
}

type CreateAssetRequest struct {
	TokenID         uint
	Kind            string
	ContentType     string
	Data            []byte
	Reader          io.Reader
	SizeBytes       int64
	URL             string
	DurationSeconds *float64
}

func NewAssetService(db *gorm.DB) *AssetService {
	return &AssetService{
		db: db, upload: filestorage.TransferReader, remove: filestorage.DeleteURL,
		validate: safeurl.Validate, now: time.Now,
	}
}

func (s *AssetService) Create(ctx context.Context, req *CreateAssetRequest) (*VideoAsset, error) {
	if s == nil || s.db == nil || req == nil || req.TokenID == 0 {
		return nil, fmt.Errorf("%w: token is required", ErrInvalidAsset)
	}
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	if !validAssetKind(kind) {
		return nil, fmt.Errorf("%w: kind must be image, video, or audio", ErrInvalidAsset)
	}
	hasUpload := len(req.Data) > 0 || req.Reader != nil
	if !hasUpload && strings.TrimSpace(req.URL) == "" {
		return nil, fmt.Errorf("%w: file or url is required", ErrInvalidAsset)
	}
	if len(req.Data) > 0 && req.Reader != nil {
		return nil, fmt.Errorf("%w: data and reader are mutually exclusive", ErrInvalidAsset)
	}
	if hasUpload && strings.TrimSpace(req.URL) != "" {
		return nil, fmt.Errorf("%w: file and url are mutually exclusive", ErrInvalidAsset)
	}
	if err := validateDuration(kind, req.DurationSeconds); err != nil {
		return nil, err
	}

	contentType := canonicalContentType(req.ContentType)
	storageURL := strings.TrimSpace(req.URL)
	hash := assetHash(nil, storageURL)
	sizeBytes := int64(0)
	var upload *preparedAssetUpload
	if hasUpload {
		var err error
		upload, err = prepareAssetUpload(req)
		if err != nil {
			return nil, err
		}
		defer upload.cleanup()
		sizeBytes = upload.size
		hash = upload.hash
		detected := upload.detectedContentType
		if contentType == "" || contentType == "application/octet-stream" {
			contentType = detected
		}
		if !mimeMatchesKind(kind, contentType) || (detected != "application/octet-stream" && !mimeMatchesKind(kind, detected)) {
			return nil, fmt.Errorf("%w: content type does not match kind", ErrInvalidAsset)
		}
	} else {
		if contentType == "" || !mimeMatchesKind(kind, contentType) {
			return nil, fmt.Errorf("%w: a matching content_type is required for url assets", ErrInvalidAsset)
		}
		if err := s.validate(ctx, storageURL); err != nil {
			return nil, fmt.Errorf("%w: unsafe url: %v", ErrInvalidAsset, err)
		}
	}

	now := s.now()
	var existing VideoAsset
	err := s.db.WithContext(ctx).
		Where("token_id = ? AND sha256 = ? AND status = ? AND expires_at > ?", req.TokenID, hash, VideoAssetStatusReady, now).
		First(&existing).Error
	if err == nil {
		return &existing, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	var expired VideoAsset
	if err := s.db.WithContext(ctx).
		Where("token_id = ? AND sha256 = ? AND status = ?", req.TokenID, hash, VideoAssetStatusReady).
		First(&expired).Error; err == nil && !expired.ExpiresAt.After(now) {
		if err := s.expireAsset(ctx, &expired); err != nil {
			return nil, err
		}
	}

	if upload != nil {
		var err error
		storageURL, err = s.upload(ctx, upload.reader, contentType, "video-assets")
		if err != nil {
			return nil, fmt.Errorf("upload video asset: %w", err)
		}
	}
	asset := &VideoAsset{
		ID: generateID(), TokenID: req.TokenID, SHA256: hash,
		SizeBytes: sizeBytes, Kind: kind, ContentType: contentType,
		DurationSeconds: req.DurationSeconds, Status: VideoAssetStatusReady,
		StoragePath: storageURL, ExpiresAt: now.Add(defaultAssetTTL), CreatedAt: now,
	}
	if err := s.db.WithContext(ctx).Create(asset).Error; err != nil {
		// A concurrent creator may have committed the same ready asset after
		// the initial lookup. Return that canonical row instead of surfacing a
		// duplicate-key error to the caller.
		if lookupErr := s.db.WithContext(ctx).
			Where("token_id = ? AND sha256 = ? AND status = ? AND expires_at > ?", req.TokenID, hash, VideoAssetStatusReady, now).
			First(&existing).Error; lookupErr == nil {
			s.removeUploadedURL(ctx, storageURL, sizeBytes)
			return &existing, nil
		}
		s.removeUploadedURL(ctx, storageURL, sizeBytes)
		return nil, err
	}
	return asset, nil
}

func (s *AssetService) Get(ctx context.Context, tokenID uint, assetID string) (*VideoAsset, error) {
	if s == nil || s.db == nil || tokenID == 0 || strings.TrimSpace(assetID) == "" {
		return nil, ErrAssetNotFound
	}
	var asset VideoAsset
	if err := s.db.WithContext(ctx).Where("id = ? AND token_id = ?", assetID, tokenID).First(&asset).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAssetNotFound
		}
		return nil, err
	}
	if asset.Status == VideoAssetStatusReady && !asset.ExpiresAt.After(s.now()) {
		_ = s.expireAsset(ctx, &asset)
		asset.Status = VideoAssetStatusExpired
	}
	return &asset, nil
}

func (s *AssetService) GetReady(ctx context.Context, tokenID uint, assetID string) (*VideoAsset, error) {
	asset, err := s.Get(ctx, tokenID, assetID)
	if err != nil {
		return nil, err
	}
	if asset.Status == VideoAssetStatusExpired {
		return nil, ErrAssetNotFound
	}
	if asset.Status != VideoAssetStatusReady {
		return nil, ErrAssetNotReady
	}
	return asset, nil
}

func (s *AssetService) Delete(ctx context.Context, tokenID uint, assetID string) error {
	if s == nil || s.db == nil || tokenID == 0 || strings.TrimSpace(assetID) == "" {
		return ErrAssetNotFound
	}
	var asset VideoAsset
	if err := s.db.WithContext(ctx).Where("id = ? AND token_id = ?", assetID, tokenID).First(&asset).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrAssetNotFound
		}
		return err
	}
	return s.expireAsset(ctx, &asset)
}

func (s *AssetService) ExpireReady(ctx context.Context) (int64, error) {
	if s == nil || s.db == nil || !s.db.Migrator().HasTable(&VideoAsset{}) {
		return 0, nil
	}
	var assets []VideoAsset
	if err := s.db.WithContext(ctx).
		Where("(status = ? AND expires_at <= ?) OR (status = ? AND size_bytes > 0 AND storage_path <> '')",
			VideoAssetStatusReady, s.now(), VideoAssetStatusExpired).
		Order("expires_at ASC").Limit(100).Find(&assets).Error; err != nil {
		return 0, err
	}
	var expired int64
	for index := range assets {
		if err := s.expireAsset(ctx, &assets[index]); err != nil {
			return expired, fmt.Errorf("expire video asset %s: %w", assets[index].ID, err)
		}
		expired++
	}
	return expired, nil
}

type preparedAssetUpload struct {
	reader              io.ReadSeeker
	size                int64
	hash                string
	detectedContentType string
	cleanup             func()
}

func prepareAssetUpload(req *CreateAssetRequest) (*preparedAssetUpload, error) {
	if len(req.Data) > 0 {
		if err := validateAssetSize(int64(len(req.Data))); err != nil {
			return nil, err
		}
		return &preparedAssetUpload{
			reader: bytes.NewReader(req.Data), size: int64(len(req.Data)), hash: assetHash(req.Data, ""),
			detectedContentType: canonicalContentType(http.DetectContentType(req.Data)), cleanup: func() {},
		}, nil
	}
	if req.Reader == nil {
		return nil, fmt.Errorf("%w: file is required", ErrInvalidAsset)
	}
	if req.SizeBytes > 0 {
		if err := validateAssetSize(req.SizeBytes); err != nil {
			return nil, err
		}
	}

	tempFile, err := os.CreateTemp("", "prism-video-asset-*")
	if err != nil {
		return nil, fmt.Errorf("create video asset spool: %w", err)
	}
	cleanup := func() {
		_ = tempFile.Close()
		_ = os.Remove(tempFile.Name())
	}
	hasher := sha256.New()
	size, err := io.Copy(io.MultiWriter(tempFile, hasher), io.LimitReader(req.Reader, MaxAssetUploadBytes()+1))
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("read video asset: %w", err)
	}
	if size == 0 {
		cleanup()
		return nil, fmt.Errorf("%w: file data is empty", ErrInvalidAsset)
	}
	if err := validateAssetSize(size); err != nil {
		cleanup()
		return nil, err
	}
	if req.SizeBytes > 0 && size != req.SizeBytes {
		cleanup()
		return nil, fmt.Errorf("%w: file size changed while reading", ErrInvalidAsset)
	}
	if _, err := tempFile.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, fmt.Errorf("seek video asset: %w", err)
	}
	sample := make([]byte, min(size, 512))
	if _, err := io.ReadFull(tempFile, sample); err != nil {
		cleanup()
		return nil, fmt.Errorf("inspect video asset: %w", err)
	}
	if _, err := tempFile.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, fmt.Errorf("rewind video asset: %w", err)
	}
	return &preparedAssetUpload{
		reader: tempFile, size: size, hash: hex.EncodeToString(hasher.Sum(nil)),
		detectedContentType: canonicalContentType(http.DetectContentType(sample)), cleanup: cleanup,
	}, nil
}

func (s *AssetService) expireAsset(ctx context.Context, asset *VideoAsset) error {
	if asset == nil {
		return ErrAssetNotFound
	}
	managed := asset.SizeBytes > 0 && strings.TrimSpace(asset.StoragePath) != ""
	if asset.Status == VideoAssetStatusExpired && !managed {
		return nil
	}
	if managed && s.remove != nil {
		if err := s.remove(ctx, asset.StoragePath); err != nil {
			return fmt.Errorf("delete stored video asset: %w", err)
		}
	}
	updates := map[string]any{"status": VideoAssetStatusExpired}
	if managed {
		updates["storage_path"] = ""
	}
	result := s.db.WithContext(ctx).Model(&VideoAsset{}).
		Where("id = ? AND token_id = ?", asset.ID, asset.TokenID).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrAssetNotFound
	}
	asset.Status = VideoAssetStatusExpired
	if managed {
		asset.StoragePath = ""
	}
	return nil
}

func (s *AssetService) removeUploadedURL(ctx context.Context, storageURL string, sizeBytes int64) {
	if sizeBytes > 0 && strings.TrimSpace(storageURL) != "" && s.remove != nil {
		_ = s.remove(ctx, storageURL)
	}
}

func validAssetKind(kind string) bool {
	return kind == "image" || kind == "video" || kind == "audio"
}

func canonicalContentType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if parsed, _, err := mime.ParseMediaType(value); err == nil {
		return parsed
	}
	return value
}

func mimeMatchesKind(kind, contentType string) bool {
	return strings.HasPrefix(contentType, kind+"/")
}

func validateDuration(kind string, value *float64) error {
	if value != nil && *value <= 0 {
		return fmt.Errorf("%w: duration_seconds must be positive", ErrInvalidAsset)
	}
	return nil
}

func validateAssetSize(size int64) error {
	if size > MaxAssetUploadBytes() {
		return ErrFileTooLarge
	}
	return nil
}

func MaxAssetUploadBytes() int64 {
	maxMB := 64
	if cfg := config.Get(); cfg != nil && cfg.FileStorage.MaxFileSizeMB > 0 {
		maxMB = cfg.FileStorage.MaxFileSizeMB
	}
	return int64(maxMB) * 1024 * 1024
}

func assetHash(data []byte, rawURL string) string {
	value := data
	if len(value) == 0 {
		value = []byte(strings.TrimSpace(rawURL))
	}
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

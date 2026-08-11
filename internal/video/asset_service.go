package video

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net/http"
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
	upload   func(context.Context, []byte, string, string) (string, error)
	validate func(context.Context, string) error
	now      func() time.Time
}

type CreateAssetRequest struct {
	TokenID         uint
	Kind            string
	ContentType     string
	Data            []byte
	URL             string
	DurationSeconds *float64
}

func NewAssetService(db *gorm.DB) *AssetService {
	return &AssetService{
		db: db, upload: filestorage.TransferBytes,
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
	if len(req.Data) == 0 && strings.TrimSpace(req.URL) == "" {
		return nil, fmt.Errorf("%w: file or url is required", ErrInvalidAsset)
	}
	if len(req.Data) > 0 && strings.TrimSpace(req.URL) != "" {
		return nil, fmt.Errorf("%w: file and url are mutually exclusive", ErrInvalidAsset)
	}
	if err := validateDuration(kind, req.DurationSeconds); err != nil {
		return nil, err
	}

	contentType := canonicalContentType(req.ContentType)
	storageURL := strings.TrimSpace(req.URL)
	data := req.Data
	if len(data) > 0 {
		if err := validateAssetSize(int64(len(data))); err != nil {
			return nil, err
		}
		detected := canonicalContentType(http.DetectContentType(data))
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

	hash := assetHash(data, storageURL)
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
		if err := s.db.WithContext(ctx).Model(&expired).Update("status", VideoAssetStatusExpired).Error; err != nil {
			return nil, err
		}
	}

	if len(data) > 0 {
		var err error
		storageURL, err = s.upload(ctx, data, contentType, "video-assets")
		if err != nil {
			return nil, fmt.Errorf("upload video asset: %w", err)
		}
	}
	asset := &VideoAsset{
		ID: generateID(), TokenID: req.TokenID, SHA256: hash,
		SizeBytes: int64(len(data)), Kind: kind, ContentType: contentType,
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
			return &existing, nil
		}
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
		_ = s.db.WithContext(ctx).Model(&asset).Update("status", VideoAssetStatusExpired).Error
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
	result := s.db.WithContext(ctx).Model(&VideoAsset{}).
		Where("id = ? AND token_id = ?", assetID, tokenID).
		Update("status", VideoAssetStatusExpired)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrAssetNotFound
	}
	return nil
}

func (s *AssetService) ExpireReady(ctx context.Context) (int64, error) {
	if s == nil || s.db == nil || !s.db.Migrator().HasTable(&VideoAsset{}) {
		return 0, nil
	}
	result := s.db.WithContext(ctx).Model(&VideoAsset{}).
		Where("status = ? AND expires_at <= ?", VideoAssetStatusReady, s.now()).
		Update("status", VideoAssetStatusExpired)
	return result.RowsAffected, result.Error
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

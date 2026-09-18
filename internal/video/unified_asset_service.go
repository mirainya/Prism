package video

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/config"
	"github.com/mirainya/Prism/pkg/filestorage"
	"github.com/mirainya/Prism/pkg/safeurl"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrAssetInUse = errors.New("video asset is referenced by a task")

const maxUnifiedAssetCleanupBatchSize = 5000

type UnifiedAssetService struct {
	db               *gorm.DB
	upload           func(context.Context, io.Reader, string, string) (filestorage.UploadResult, error)
	download         func(context.Context, string, int64) (*safeurl.Result, error)
	verify           func(context.Context, string, int64, string) error
	remove           func(context.Context, string) error
	removeWithAPIKey func(context.Context, string, string) error
	now              func() time.Time
}

func NewUnifiedAssetService(db *gorm.DB) *UnifiedAssetService {
	return &UnifiedAssetService{
		db: db, upload: filestorage.UploadReaderAtPath, download: safeurl.Download,
		verify: filestorage.VerifyURL, remove: filestorage.DeleteURL,
		removeWithAPIKey: func(ctx context.Context, apiKey, locator string) error {
			return filestorage.WithAPIKey(apiKey).DeleteURL(ctx, locator)
		},
		now: time.Now,
	}
}

func (s *UnifiedAssetService) Create(ctx context.Context, req *CreateAssetRequest) (*VideoAsset, error) {
	if s == nil || s.db == nil || req == nil || req.UserID == 0 || req.TokenID == 0 {
		return nil, fmt.Errorf("%w: user and token are required", ErrInvalidAsset)
	}
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	if !validAssetKind(kind) {
		return nil, fmt.Errorf("%w: kind must be image, video, or audio", ErrInvalidAsset)
	}
	if err := validateDuration(kind, req.DurationSeconds); err != nil {
		return nil, err
	}
	hasUpload := len(req.Data) > 0 || req.Reader != nil
	hasURL := strings.TrimSpace(req.URL) != ""
	if hasUpload == hasURL || len(req.Data) > 0 && req.Reader != nil {
		return nil, fmt.Errorf("%w: provide exactly one file or url", ErrInvalidAsset)
	}

	contentType := canonicalContentType(req.ContentType)
	input := *req
	if hasURL {
		downloaded, err := s.download(ctx, strings.TrimSpace(req.URL), MaxAssetUploadBytes())
		if err != nil {
			return nil, fmt.Errorf("%w: download url: %v", ErrInvalidAsset, err)
		}
		input.URL = ""
		input.Data = downloaded.Data
		input.Reader = nil
		input.SizeBytes = int64(len(downloaded.Data))
		if contentType == "" || contentType == "application/octet-stream" {
			contentType = canonicalContentType(downloaded.ContentType)
		}
	}
	prepared, err := prepareAssetUpload(&input)
	if err != nil {
		return nil, err
	}
	defer prepared.cleanup()
	if contentType == "" || contentType == "application/octet-stream" {
		contentType = prepared.detectedContentType
	}
	if !mimeMatchesKind(kind, contentType) || prepared.detectedContentType != "application/octet-stream" && !mimeMatchesKind(kind, prepared.detectedContentType) {
		return nil, fmt.Errorf("%w: content type does not match kind", ErrInvalidAsset)
	}

	now := s.now().UTC()
	retentionUntil := now.Add(defaultAssetTTL)
	var existing model.MediaAsset
	result := s.db.WithContext(ctx).Where(
		"user_id=? AND token_id=? AND purpose='input' AND sha256=? AND content_length=? AND state='active' AND retention_until>?",
		req.UserID, req.TokenID, prepared.hash, prepared.size, now,
	).Order("id ASC").First(&existing)
	if result.Error == nil {
		return unifiedVideoAsset(&existing, kind, req.DurationSeconds), nil
	}
	if !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, result.Error
	}

	logicalObjectKey := "video-input:" + uuid.NewString()
	asset := model.MediaAsset{
		UserID: req.UserID, TokenID: req.TokenID, Purpose: "input", ObjectKey: logicalObjectKey,
		ContentType: contentType, ContentLength: uint64(prepared.size), SHA256: prepared.hash,
		State: "staging", StateVersion: 1, RetentionUntil: &retentionUntil, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&asset).Error; err != nil {
			return err
		}
		return tx.Create(&model.MediaAssetStateEvent{MediaAssetID: asset.ID, NewState: "staging", StateVersion: 1, ReasonCode: "video_input_allocated", CreatedAt: now}).Error
	}); err != nil {
		return nil, err
	}

	storagePath := unifiedVideoAssetStoragePath(req.TokenID, asset.ID)
	uploaded, err := s.upload(ctx, prepared.reader, contentType, storagePath)
	if err != nil {
		_ = s.failStagingAsset(context.WithoutCancel(ctx), &asset, "video_input_upload_failed", "")
		return nil, fmt.Errorf("upload video asset: %w", err)
	}
	if err := s.verify(ctx, uploaded.URL, prepared.size, prepared.hash); err != nil {
		_ = s.failStagingAsset(context.WithoutCancel(ctx), &asset, "video_input_verification_failed", uploaded.URL)
		return nil, fmt.Errorf("verify video asset: %w", err)
	}
	objectKey := uploaded.StorageKey()
	if objectKey == "" {
		objectKey = logicalObjectKey
	}
	objectVersion := strings.TrimSpace(uploaded.ObjectID)
	if objectVersion == "" {
		objectVersion = strings.TrimSpace(uploaded.ID)
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current model.MediaAsset
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=? AND token_id=?", asset.ID, req.UserID, req.TokenID).First(&current).Error; err != nil {
			return err
		}
		if current.State != "staging" || current.StateVersion != 1 {
			return ErrAssetNotReady
		}
		updated := tx.Model(&model.MediaAsset{}).Where("id=? AND state='staging' AND state_version=1", asset.ID).Updates(map[string]any{
			"object_key": objectKey, "storage_locator": uploaded.URL, "object_version": objectVersion,
			"state": "active", "state_version": 2, "updated_at": s.now().UTC(),
		})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return ErrAssetNotReady
		}
		oldState := "staging"
		return tx.Create(&model.MediaAssetStateEvent{MediaAssetID: asset.ID, OldState: &oldState, NewState: "active", StateVersion: 2, ReasonCode: "video_input_verified", CreatedAt: s.now().UTC()}).Error
	})
	if err != nil {
		_ = s.failStagingAsset(context.WithoutCancel(ctx), &asset, "video_input_publish_failed", uploaded.URL)
		return nil, err
	}
	asset.ObjectKey, asset.StorageLocator, asset.ObjectVersion = objectKey, uploaded.URL, objectVersion
	asset.State, asset.StateVersion = "active", 2
	return unifiedVideoAsset(&asset, kind, req.DurationSeconds), nil
}

func (s *UnifiedAssetService) Get(ctx context.Context, tokenID uint, rawID string) (*VideoAsset, error) {
	assetID, err := parseUnifiedAssetID(rawID)
	if err != nil || tokenID == 0 {
		return nil, ErrAssetNotFound
	}
	var asset model.MediaAsset
	if err := s.db.WithContext(ctx).Where("id=? AND token_id=? AND purpose='input'", assetID, tokenID).First(&asset).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAssetNotFound
		}
		return nil, err
	}
	if asset.State == "active" && asset.RetentionUntil != nil && !asset.RetentionUntil.After(s.now()) {
		deleteErr := s.Delete(ctx, tokenID, rawID)
		if deleteErr != nil && !errors.Is(deleteErr, ErrAssetInUse) {
			return nil, deleteErr
		}
		// Referenced inputs retain their immutable object until the owning call
		// is removed, but expiry still makes the asset unavailable for new calls.
		asset.State, asset.StorageLocator = "deleted", ""
	}
	return unifiedVideoAsset(&asset, "", nil), nil
}

// PurgeExpired removes unreferenced media objects whose retention window has
// ended, and resumes objects left in deleting after a transient storage error.
func (s *UnifiedAssetService) PurgeExpired(ctx context.Context, limit int) (int, error) {
	if s == nil || s.db == nil || limit <= 0 || limit > maxUnifiedAssetCleanupBatchSize {
		return 0, ErrInvalidAsset
	}
	now := s.now().UTC()
	var assets []model.MediaAsset
	err := s.db.WithContext(ctx).
		Where("purpose IN ? AND ((state IN ? AND retention_until IS NOT NULL AND retention_until<=?) OR state='deleting')", []string{"input", "result", "file"}, []string{"staging", "active"}, now).
		Where(`NOT EXISTS (SELECT 1 FROM gw_media_asset_refs ref WHERE ref.media_asset_id=gw_media_assets.id)
OR (purpose='result' AND state='active'
	AND (SELECT COUNT(*) FROM gw_media_asset_refs ref WHERE ref.media_asset_id=gw_media_assets.id)=1
    AND EXISTS (SELECT 1 FROM gw_media_asset_refs ref JOIN gw_result_deliveries delivery ON delivery.id=ref.result_delivery_id
                WHERE ref.media_asset_id=gw_media_assets.id AND ref.role='result' AND delivery.delivery_mode='managed_copy'
                  AND delivery.media_asset_ref_id=ref.id AND delivery.state='ready')
    AND NOT EXISTS (SELECT 1 FROM gw_media_asset_refs ref WHERE ref.media_asset_id=gw_media_assets.id AND (ref.role<>'result' OR ref.result_delivery_id IS NULL)))
OR (purpose='file' AND state='deleting'
	AND (SELECT COUNT(*) FROM gw_media_asset_refs ref WHERE ref.media_asset_id=gw_media_assets.id)=1
    AND EXISTS (SELECT 1 FROM gw_media_asset_refs ref JOIN gw_file_resources file ON file.id=ref.ai_file_id
                WHERE ref.media_asset_id=gw_media_assets.id AND ref.role='file' AND file.status='deleting')
    AND NOT EXISTS (SELECT 1 FROM gw_media_asset_refs ref WHERE ref.media_asset_id=gw_media_assets.id AND (ref.role<>'file' OR ref.ai_file_id IS NULL)))`).
		Order("retention_until ASC, id ASC").Limit(limit).Find(&assets).Error
	if err != nil {
		return 0, err
	}
	removed := 0
	for index := range assets {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		asset := &assets[index]
		if err := s.purgeExpiredAsset(ctx, asset.ID); err != nil {
			if errors.Is(err, ErrAssetInUse) || errors.Is(err, ErrAssetNotReady) || errors.Is(err, ErrAssetNotFound) {
				continue
			}
			return removed, fmt.Errorf("purge media asset %d: %w", asset.ID, err)
		}
		removed++
	}
	return removed, nil
}

func (s *UnifiedAssetService) purgeExpiredAsset(ctx context.Context, assetID uint64) error {
	if assetID == 0 {
		return ErrAssetNotFound
	}
	var locator string
	var tokenID uint
	var resultAPIKey string
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var asset model.MediaAsset
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", assetID).First(&asset).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAssetNotFound
			}
			return err
		}
		if asset.Purpose != "input" && asset.Purpose != "result" && asset.Purpose != "file" {
			return ErrInvalidAsset
		}
		tokenID = asset.TokenID
		locator = strings.TrimSpace(asset.StorageLocator)
		if locator == "" && (strings.HasPrefix(asset.ObjectKey, "https://") || strings.HasPrefix(asset.ObjectKey, "http://")) {
			locator = strings.TrimSpace(asset.ObjectKey)
		}
		if asset.State == "deleted" {
			return nil
		}
		if asset.State != "deleting" {
			if asset.State != "staging" && asset.State != "active" || asset.RetentionUntil == nil || asset.RetentionUntil.After(s.now().UTC()) {
				return ErrAssetNotReady
			}
		}
		var references []model.MediaAssetRef
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("media_asset_id=?", assetID).Order("id ASC").Find(&references).Error; err != nil {
			return err
		}
		if asset.Purpose == "result" && locator != "" {
			var lookupErr error
			resultAPIKey, lookupErr = managedResultStorageAPIKey(tx, &asset)
			if lookupErr != nil {
				return lookupErr
			}
		}
		if len(references) != 0 {
			switch asset.Purpose {
			case "result":
				if err := s.releaseExpiredResultAsset(ctx, tx, &asset, references); err != nil {
					return err
				}
			case "file":
				if err := s.validateDeletingFileAsset(ctx, tx, &asset, references); err != nil {
					return err
				}
			default:
				return ErrAssetInUse
			}
		}
		if asset.State == "deleting" {
			return nil
		}
		now := s.now().UTC()
		updated := tx.Model(&model.MediaAsset{}).Where("id=? AND state=? AND state_version=?", assetID, asset.State, asset.StateVersion).
			Updates(map[string]any{"state": "deleting", "state_version": asset.StateVersion + 1, "updated_at": now})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return ErrAssetNotReady
		}
		oldState := asset.State
		return tx.Create(&model.MediaAssetStateEvent{MediaAssetID: assetID, OldState: &oldState, NewState: "deleting", StateVersion: asset.StateVersion + 1, ReasonCode: "media_asset_retention_expired", CreatedAt: now}).Error
	})
	if err != nil {
		return err
	}
	if locator != "" {
		remove := s.remove
		if resultAPIKey != "" {
			remove = func(ctx context.Context, locator string) error {
				return s.removeWithAPIKey(ctx, resultAPIKey, locator)
			}
		}
		if err := remove(ctx, locator); err != nil {
			return err
		}
	}
	return s.finishAssetDeletion(ctx, assetID, tokenID, "media_asset_retention_deleted")
}

func managedResultStorageAPIKey(tx *gorm.DB, asset *model.MediaAsset) (string, error) {
	if tx == nil || asset == nil || asset.Purpose != "result" {
		return "", ErrInvalidAsset
	}
	if asset.AttemptID == nil || *asset.AttemptID == 0 {
		return "", fmt.Errorf("%w: result asset has no execution attempt", ErrInvalidAsset)
	}
	attemptID := *asset.AttemptID
	var snapshot struct {
		APIKey string
	}
	result := tx.Table("gw_api_call_attempts AS attempt").
		Select("api_call.xfs_api_key AS api_key").
		Joins("JOIN gw_api_calls AS api_call ON api_call.id=attempt.call_id").
		Where("attempt.id=? AND api_call.user_id=? AND api_call.token_id=?", attemptID, asset.UserID, asset.TokenID).
		Take(&snapshot)
	if result.Error != nil {
		return "", fmt.Errorf("read managed result storage key: %w", result.Error)
	}
	snapshot.APIKey = strings.TrimSpace(snapshot.APIKey)
	if snapshot.APIKey == "" {
		return "", fmt.Errorf("%w: managed result storage key is empty", ErrInvalidAsset)
	}
	return snapshot.APIKey, nil
}

type resultDeliveryLifecycle struct {
	ID, MediaAssetRefID, StateVersion uint64
	DeliveryMode, State               string
	ExpiresAt                         *time.Time
}

func (s *UnifiedAssetService) releaseExpiredResultAsset(ctx context.Context, tx *gorm.DB, asset *model.MediaAsset, references []model.MediaAssetRef) error {
	if asset == nil || asset.Purpose != "result" || asset.State != "active" || asset.RetentionUntil == nil || asset.RetentionUntil.After(s.now().UTC()) || len(references) != 1 {
		return ErrAssetInUse
	}
	ref := references[0]
	if ref.Role != "result" || ref.ResultDeliveryID == nil || ref.AIFileID != nil || ref.CallID != nil {
		return ErrAssetInUse
	}
	var delivery resultDeliveryLifecycle
	if err := tx.Table("gw_result_deliveries").Clauses(clause.Locking{Strength: "UPDATE"}).
		Select("id,media_asset_ref_id,state_version,delivery_mode,state").
		Where("id=?", *ref.ResultDeliveryID).Take(&delivery).Error; err != nil {
		return err
	}
	if delivery.MediaAssetRefID != ref.ID || delivery.DeliveryMode != "managed_copy" || delivery.State != "ready" {
		return ErrAssetInUse
	}
	now := s.now().UTC()
	updated := tx.Table("gw_result_deliveries").Where("id=? AND media_asset_ref_id=? AND state='ready' AND state_version=?", delivery.ID, ref.ID, delivery.StateVersion).
		Updates(map[string]any{"media_asset_ref_id": nil, "state": "expired", "reason_code": "managed_copy_retention_expired", "expires_at": asset.RetentionUntil.UTC(), "state_version": delivery.StateVersion + 1, "updated_at": now})
	if updated.Error != nil {
		return updated.Error
	}
	if updated.RowsAffected != 1 {
		return ErrAssetNotReady
	}
	if err := tx.Exec(`INSERT INTO gw_state_transition_events(result_delivery_id,old_state,new_state,state_version,reason_code,created_at) VALUES (?,'ready','expired',?,'managed_copy_retention_expired',?)`, delivery.ID, delivery.StateVersion+1, now).Error; err != nil {
		return err
	}
	return tx.Delete(&ref).Error
}

func (s *UnifiedAssetService) validateDeletingFileAsset(ctx context.Context, tx *gorm.DB, asset *model.MediaAsset, references []model.MediaAssetRef) error {
	if asset == nil || asset.Purpose != "file" || asset.State != "deleting" || len(references) != 1 {
		return ErrAssetInUse
	}
	ref := references[0]
	if ref.Role != "file" || ref.AIFileID == nil || ref.CallID != nil || ref.ResultDeliveryID != nil {
		return ErrAssetInUse
	}
	var status string
	if err := tx.Table("gw_file_resources").Select("status").Where("id=? AND user_id=? AND token_id=?", *ref.AIFileID, asset.UserID, asset.TokenID).Scan(&status).Error; err != nil {
		return err
	}
	if status != "deleting" {
		return ErrAssetInUse
	}
	return nil
}

func (s *UnifiedAssetService) Delete(ctx context.Context, tokenID uint, rawID string) error {
	assetID, err := parseUnifiedAssetID(rawID)
	if err != nil || tokenID == 0 {
		return ErrAssetNotFound
	}
	var locator string
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var asset model.MediaAsset
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND token_id=? AND purpose='input'", assetID, tokenID).First(&asset).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAssetNotFound
			}
			return err
		}
		locator = asset.StorageLocator
		switch asset.State {
		case "deleted", "deleting":
			return nil
		case "active", "staging":
		default:
			return ErrAssetNotReady
		}
		var references int64
		if err := tx.Model(&model.MediaAssetRef{}).Where("media_asset_id=?", assetID).Count(&references).Error; err != nil {
			return err
		}
		if references != 0 {
			return ErrAssetInUse
		}
		updated := tx.Model(&model.MediaAsset{}).Where("id=? AND state=? AND state_version=?", assetID, asset.State, asset.StateVersion).
			Updates(map[string]any{"state": "deleting", "state_version": asset.StateVersion + 1, "updated_at": s.now().UTC()})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return ErrAssetNotReady
		}
		oldState := asset.State
		return tx.Create(&model.MediaAssetStateEvent{MediaAssetID: assetID, OldState: &oldState, NewState: "deleting", StateVersion: asset.StateVersion + 1, ReasonCode: "video_input_delete_requested", CreatedAt: s.now().UTC()}).Error
	})
	if err != nil {
		return err
	}
	if locator != "" {
		if err := s.remove(ctx, locator); err != nil {
			return err
		}
	}
	return s.finishAssetDeletion(ctx, assetID, tokenID, "video_input_deleted")
}

func (s *UnifiedAssetService) failStagingAsset(ctx context.Context, asset *model.MediaAsset, reason, locator string) error {
	if asset == nil || asset.ID == 0 {
		return ErrAssetNotFound
	}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		updated := tx.Model(&model.MediaAsset{}).Where("id=? AND state='staging'", asset.ID).
			Updates(map[string]any{"storage_locator": locator, "state": "deleting", "state_version": gorm.Expr("state_version + 1"), "updated_at": s.now().UTC()})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return ErrAssetNotReady
		}
		oldState := "staging"
		return tx.Create(&model.MediaAssetStateEvent{MediaAssetID: asset.ID, OldState: &oldState, NewState: "deleting", StateVersion: 2, ReasonCode: reason, CreatedAt: s.now().UTC()}).Error
	}); err != nil {
		return err
	}
	if locator != "" {
		if err := s.remove(ctx, locator); err != nil {
			return err
		}
	}
	return s.finishAssetDeletion(ctx, asset.ID, asset.TokenID, reason)
}

func (s *UnifiedAssetService) finishAssetDeletion(ctx context.Context, assetID uint64, tokenID uint, reason string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var asset model.MediaAsset
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND token_id=?", assetID, tokenID).First(&asset).Error; err != nil {
			return err
		}
		if asset.State == "deleted" {
			return nil
		}
		if asset.State != "deleting" {
			return ErrAssetNotReady
		}
		var fileRef *model.MediaAssetRef
		if asset.Purpose == "file" {
			var references []model.MediaAssetRef
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("media_asset_id=?", assetID).Find(&references).Error; err != nil {
				return err
			}
			if len(references) > 1 || len(references) == 1 && (references[0].Role != "file" || references[0].AIFileID == nil || references[0].CallID != nil || references[0].ResultDeliveryID != nil) {
				return ErrAssetInUse
			}
			if len(references) == 1 {
				var file model.AIFile
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=? AND token_id=?", *references[0].AIFileID, asset.UserID, asset.TokenID).First(&file).Error; err != nil {
					return err
				}
				if file.Status != "deleting" {
					return ErrAssetInUse
				}
				fileRef = &references[0]
			}
		}
		updated := tx.Model(&model.MediaAsset{}).Where("id=? AND state='deleting' AND state_version=?", assetID, asset.StateVersion).
			Updates(map[string]any{"storage_locator": nil, "state": "deleted", "state_version": asset.StateVersion + 1, "updated_at": s.now().UTC()})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return ErrAssetNotReady
		}
		oldState := "deleting"
		if err := tx.Create(&model.MediaAssetStateEvent{MediaAssetID: assetID, OldState: &oldState, NewState: "deleted", StateVersion: asset.StateVersion + 1, ReasonCode: reason, CreatedAt: s.now().UTC()}).Error; err != nil {
			return err
		}
		if fileRef != nil {
			if err := tx.Delete(fileRef).Error; err != nil {
				return err
			}
			return tx.Model(&model.AIFile{}).Where("id=? AND user_id=? AND token_id=? AND status='deleting'", *fileRef.AIFileID, asset.UserID, asset.TokenID).
				Updates(map[string]any{"status": "deleted", "updated_at": s.now().UTC()}).Error
		}
		return nil
	})
}

func unifiedVideoAsset(asset *model.MediaAsset, kind string, duration *float64) *VideoAsset {
	if asset == nil {
		return nil
	}
	if slash := strings.IndexByte(asset.ContentType, '/'); slash > 0 {
		kind = asset.ContentType[:slash]
	}
	status := VideoAssetStatusUploading
	if asset.State == "active" {
		status = VideoAssetStatusReady
	} else if asset.State == "deleted" || asset.State == "deleting" {
		status = VideoAssetStatusExpired
	}
	expires := time.Time{}
	if asset.RetentionUntil != nil {
		expires = *asset.RetentionUntil
	}
	return &VideoAsset{ID: strconv.FormatUint(asset.ID, 10), TokenID: asset.TokenID, SHA256: asset.SHA256,
		SizeBytes: int64(asset.ContentLength), Kind: kind, ContentType: asset.ContentType, DurationSeconds: duration,
		Status: status, StoragePath: asset.StorageLocator, ExpiresAt: expires, CreatedAt: asset.CreatedAt}
}

func parseUnifiedAssetID(raw string) (uint64, error) {
	return strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
}

func unifiedVideoAssetStoragePath(tokenID uint, assetID uint64) string {
	root := ""
	if cfg := config.Get(); cfg != nil {
		root = strings.Trim(strings.TrimSpace(cfg.FileStorage.UploadPath), "/")
	}
	if root != "" {
		root += "/"
	}
	return root + "video-inputs/" + strconv.FormatUint(uint64(tokenID), 10) + "/" + strconv.FormatUint(assetID, 10) + "/"
}

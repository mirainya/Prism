package runtime

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/gateway/delivery"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/pkg/config"
)

func (d *AsyncDispatcher) recoverManagedCopy(ctx context.Context, item repository.OutboxItem, target repository.DeliveryRecoveryTarget) error {
	if d == nil || d.service == nil || target.Mode != "managed_copy" || target.SourceKind != "remote_url" || target.SourceBlobID == 0 || target.SourceSequence == 0 {
		return &PermanentDispatchError{Code: "invalid_managed_copy_recovery"}
	}
	sourceURL, err := d.openBlob(ctx, target.SourceBlobID, fmt.Sprintf("delivery:%d:source:%d", target.DeliveryID, target.SourceSequence), "gateway-result-source", false)
	if err != nil {
		return err
	}
	defer clear(sourceURL)
	role, err := managedCopyRecoveryRole(target.ResourceKind, target.Ordinal)
	if err != nil {
		return err
	}
	copy, err := d.service.prepareManagedResultCopyAt(ctx, target.AttemptID, target.Ordinal, delivery.RemoteResult{Role: role, URL: string(sourceURL)})
	if err != nil {
		return err
	}
	commitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 4*time.Second)
	defer cancel()
	return d.service.ApplyManagedCopyRecovery(commitCtx, item, copy)
}

func managedCopyRecoveryRole(resourceKind string, ordinal uint32) (string, error) {
	switch resourceKind {
	case delivery.ResourceCapabilityTask:
		return "image", nil
	case delivery.ResourceVideoTask:
		if ordinal == 0 {
			return "video", nil
		}
		if ordinal == 1 {
			return "thumbnail", nil
		}
	}
	return "", &PermanentDispatchError{Code: "invalid_managed_copy_ordinal"}
}

// prepareManagedResultCopyAt retries exactly one result ordinal. The object
// key remains the one fixed by the original Attempt and ordinal.
func (s *Service) prepareManagedResultCopyAt(ctx context.Context, attemptID uint64, ordinal uint32, source delivery.RemoteResult) (ManagedCopy, error) {
	if s == nil || s.Store == nil || attemptID == 0 || !delivery.ValidSource(source) {
		return ManagedCopy{}, &PermanentDispatchError{Code: "invalid_managed_copy_source"}
	}
	storage, err := s.Store.ReadAttemptFileStorage(ctx, attemptID)
	if err != nil {
		return ManagedCopy{}, err
	}
	if storage.APIKey == "" {
		return ManagedCopy{}, &PermanentDispatchError{Code: "managed_copy_storage_unconfigured"}
	}
	maxBytes := int64(64 << 20)
	if config.C != nil && config.C.FileStorage.MaxFileSizeMB > 0 {
		maxBytes = int64(config.C.FileStorage.MaxFileSizeMB) * 1024 * 1024
	}
	var data []byte
	var contentType string
	if source.URL != "" {
		downloaded, downloadErr := downloadManagedResult(ctx, source.URL, maxBytes)
		if downloadErr != nil {
			if ctx.Err() != nil {
				return ManagedCopy{}, ctx.Err()
			}
			return ManagedCopy{}, fmt.Errorf("managed copy download: %w", downloadErr)
		}
		data = downloaded.Data
		contentType = strings.TrimSpace(strings.Split(downloaded.ContentType, ";")[0])
	} else {
		if int64(len(source.InlineData)) > maxBytes {
			return ManagedCopy{}, &PermanentDispatchError{Code: "managed_copy_result_too_large"}
		}
		data = source.InlineData
		contentType = strings.TrimSpace(strings.Split(source.ContentType, ";")[0])
	}
	defer clear(data)
	if len(data) == 0 {
		return ManagedCopy{}, &PermanentDispatchError{Code: "managed_copy_result_empty"}
	}
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	contentType = strings.ToLower(contentType)
	if !validManagedResultMIME(source.Role, contentType, http.DetectContentType(data)) {
		return ManagedCopy{}, &PermanentDispatchError{Code: "managed_copy_content_type_mismatch"}
	}
	hash := sha256.Sum256(data)
	digest := hex.EncodeToString(hash[:])
	logicalKey := fmt.Sprintf("gateway-result:%d:%d", attemptID, ordinal)
	retentionUntil := time.Now().UTC().Add(config.ResourceHistoryRetentionDuration())
	var asset repository.ManagedCopyAssetRecord
	err = s.Store.WithTx(ctx, func(tx *sql.Tx) error {
		var reserveErr error
		asset, reserveErr = s.Store.ReserveManagedCopyAsset(ctx, tx, attemptID, repository.MediaAssetInput{
			UserID: storage.UserID, TokenID: storage.TokenID, Purpose: "result", ObjectKey: logicalKey,
			ContentType: contentType, ContentLength: uint64(len(data)), SHA256: digest, RetentionUntil: &retentionUntil,
		})
		return reserveErr
	})
	if err != nil {
		return ManagedCopy{}, fmt.Errorf("reserve managed result: %w", err)
	}
	if asset.State == "active" {
		return ManagedCopy{}, repository.ErrConflict
	}
	if asset.StorageLocator == "" {
		uploaded, uploadErr := uploadManagedResult(ctx, storage.APIKey, data, contentType, managedResultStoragePath(asset.ID), managedResultStorageFilename(digest, contentType))
		if uploadErr != nil {
			if ctx.Err() != nil {
				return ManagedCopy{}, ctx.Err()
			}
			return ManagedCopy{}, fmt.Errorf("managed copy upload: %w", uploadErr)
		}
		locator := uploaded.StorageLocator()
		if locator == "" || len(locator) > 2048 {
			deleteManagedResultBestEffort(ctx, storage.APIKey, locator)
			return ManagedCopy{}, fmt.Errorf("managed copy upload returned an invalid locator")
		}
		if verifyErr := verifyManagedResult(ctx, storage.APIKey, locator, int64(len(data)), digest); verifyErr != nil {
			deleteManagedResultBestEffort(ctx, storage.APIKey, locator)
			if ctx.Err() != nil {
				return ManagedCopy{}, ctx.Err()
			}
			return ManagedCopy{}, fmt.Errorf("verify managed copy upload: %w", verifyErr)
		}
		objectVersion := strings.TrimSpace(uploaded.ObjectID)
		if objectVersion == "" {
			objectVersion = strings.TrimSpace(uploaded.ID)
		}
		if recordErr := s.Store.WithTx(ctx, func(tx *sql.Tx) error {
			return s.Store.RecordManagedCopyUpload(ctx, tx, asset.ID, locator, objectVersion)
		}); recordErr != nil {
			deleteManagedResultBestEffort(ctx, storage.APIKey, locator)
			return ManagedCopy{}, fmt.Errorf("record managed result: %w", recordErr)
		}
		asset.StorageLocator, asset.ObjectVersion = locator, objectVersion
	} else if verifyErr := verifyManagedResult(ctx, storage.APIKey, asset.StorageLocator, int64(asset.ContentLength), asset.SHA256); verifyErr != nil {
		if ctx.Err() != nil {
			return ManagedCopy{}, ctx.Err()
		}
		return ManagedCopy{}, fmt.Errorf("verify existing managed copy: %w", verifyErr)
	}
	return managedCopyFromAsset(asset), nil
}

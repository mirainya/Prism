package runtime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/gateway/delivery"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/pkg/config"
	"github.com/mirainya/Prism/pkg/filestorage"
)

var importManagedResult = func(ctx context.Context, apiKey, sourceURL, storagePath, originalFilename string) (filestorage.UploadResult, error) {
	return filestorage.WithAPIKey(apiKey).ImportURLAtPath(ctx, sourceURL, storagePath, originalFilename)
}

func (s *Service) prepareManagedURLCopy(ctx context.Context, storage repository.TokenFileStorage, attemptID uint64, ordinal uint32, source delivery.RemoteResult, maxBytes int64) (managedCopyPreparation, error) {
	logicalKey := fmt.Sprintf("gateway-result:%d:%d", attemptID, ordinal)
	existing, err := s.Store.ReadManagedCopyAsset(ctx, attemptID, logicalKey)
	if err == nil {
		if existing.StorageLocator != "" {
			return managedCopyPreparation{ManagedCopy: managedCopyFromAsset(existing)}, nil
		}
	} else if !errors.Is(err, repository.ErrNotFound) {
		return managedCopyPreparation{}, err
	}

	uploaded, err := importManagedResult(
		ctx,
		storage.APIKey,
		source.URL,
		managedResultImportStoragePath(attemptID, ordinal),
		managedResultImportFilename(source),
	)
	if err != nil {
		if ctx.Err() != nil {
			return managedCopyPreparation{}, ctx.Err()
		}
		return managedCopyPreparation{FailureReason: delivery.ManagedCopyUploadFailed}, nil
	}
	locator := uploaded.StorageLocator()
	cleanup := func() {
		deleteManagedResultBestEffort(ctx, storage.APIKey, locator)
	}
	if locator == "" || len(locator) > 2048 {
		cleanup()
		return managedCopyPreparation{FailureReason: delivery.ManagedCopyUploadFailed}, nil
	}
	if uploaded.Size <= 0 {
		cleanup()
		return managedCopyPreparation{FailureReason: delivery.ManagedCopyEmpty}, nil
	}
	if maxBytes > 0 && uploaded.Size > maxBytes {
		cleanup()
		return managedCopyPreparation{FailureReason: delivery.ManagedCopySizeExceeded}, nil
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(uploaded.ContentType, ";")[0]))
	if !validManagedResultMIME(source.Role, contentType, contentType) {
		cleanup()
		return managedCopyPreparation{FailureReason: delivery.ManagedCopyContentTypeInvalid}, nil
	}
	digest := uploaded.SHA256()
	if digest == "" {
		cleanup()
		return managedCopyPreparation{FailureReason: delivery.ManagedCopyVerificationFailed}, nil
	}

	retentionUntil := time.Now().UTC().Add(config.ResourceHistoryRetentionDuration())
	var asset repository.ManagedCopyAssetRecord
	err = s.Store.WithTx(ctx, func(tx *sql.Tx) error {
		var reserveErr error
		asset, reserveErr = s.Store.ReserveManagedCopyAsset(ctx, tx, attemptID, repository.MediaAssetInput{
			UserID: storage.UserID, TokenID: storage.TokenID, Purpose: "result", ObjectKey: logicalKey,
			ContentType: contentType, ContentLength: uint64(uploaded.Size), SHA256: digest, RetentionUntil: &retentionUntil,
		})
		return reserveErr
	})
	if err != nil {
		cleanup()
		return managedCopyPreparation{}, fmt.Errorf("reserve managed result: %w", err)
	}
	if asset.State == "active" || asset.StorageLocator != "" {
		if asset.StorageLocator == "" {
			cleanup()
			return managedCopyPreparation{}, repository.ErrConflict
		}
		if asset.StorageLocator != locator {
			cleanup()
		}
		return managedCopyPreparation{ManagedCopy: managedCopyFromAsset(asset)}, nil
	}

	objectVersion := strings.TrimSpace(uploaded.ObjectID)
	if objectVersion == "" {
		objectVersion = strings.TrimSpace(uploaded.ID)
	}
	if err := s.Store.WithTx(ctx, func(tx *sql.Tx) error {
		return s.Store.RecordManagedCopyUpload(ctx, tx, asset.ID, locator, objectVersion)
	}); err != nil {
		cleanup()
		return managedCopyPreparation{}, fmt.Errorf("record managed result: %w", err)
	}
	asset.StorageLocator, asset.ObjectVersion = locator, objectVersion
	return managedCopyPreparation{ManagedCopy: managedCopyFromAsset(asset)}, nil
}

func managedResultImportStoragePath(attemptID uint64, ordinal uint32) string {
	prefix := ""
	if config.C != nil {
		prefix = strings.Trim(strings.TrimSpace(config.C.FileStorage.UploadPath), "/")
		if prefix != "" {
			prefix += "/"
		}
	}
	return fmt.Sprintf("%sgateway-results/%d/%d/", prefix, attemptID, ordinal)
}

func managedResultImportFilename(source delivery.RemoteResult) string {
	if parsed, err := url.Parse(source.URL); err == nil {
		filename := path.Base(parsed.Path)
		if filename != "." && filename != "/" && len(filename) <= 255 &&
			!strings.ContainsAny(filename, "/\\\r\n\x00") && validManagedResultFilenameExtension(source.Role, path.Ext(filename)) {
			return filename
		}
	}
	switch source.Role {
	case "video":
		return "result.mp4"
	case "image", "thumbnail":
		return "result.png"
	default:
		return "result.bin"
	}
}

func validManagedResultFilenameExtension(role, extension string) bool {
	extension = strings.ToLower(extension)
	switch role {
	case "video":
		switch extension {
		case ".mp4", ".m4v", ".webm", ".mov", ".avi", ".mkv", ".flv", ".mpg", ".mpeg":
			return true
		}
	case "image", "thumbnail":
		switch extension {
		case ".jpg", ".jpeg", ".jpe", ".png", ".gif", ".webp", ".bmp", ".svg", ".ico", ".tif", ".tiff", ".avif", ".heic", ".heif":
			return true
		}
	}
	return false
}

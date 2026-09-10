package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/api/openaierror"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/pkg/config"
	"github.com/mirainya/Prism/pkg/filestorage"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	maxPublicFileBytes         int64 = 64 * 1024 * 1024
	defaultTokenFileQuotaBytes int64 = config.DefaultFileStorageMaxTotalSizeMB * 1024 * 1024
	defaultFileListLimit             = 10000
	fileMetadataColumns              = "id, user_id, token_id, filename, purpose, bytes, mime_type, status, created_at"
	zeroSHA256                       = "0000000000000000000000000000000000000000000000000000000000000000"
)

var (
	errFileNotFound      = errors.New("file not found")
	errFileQuotaExceeded = errors.New("file storage quota exceeded")
	fileQuotaLocks       [256]sync.Mutex
	uploadFileObject     = filestorage.UploadReaderAtPath
	openFileObject       = filestorage.OpenDownload
	verifyFileObject     = filestorage.VerifyURL
	deleteFileObject     = filestorage.DeleteURL
)

type fileObject struct {
	ID        string `json:"id"`
	Object    string `json:"object"`
	Bytes     int64  `json:"bytes"`
	CreatedAt int64  `json:"created_at"`
	Filename  string `json:"filename"`
	Purpose   string `json:"purpose"`
	Status    string `json:"status"`
}

func UploadFile(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxPublicFileBytes+1024*1024)
	token := middleware.GetToken(c)
	if token == nil || token.ID == 0 || token.UserID == 0 {
		openaierror.Write(c, http.StatusUnauthorized, "Invalid authentication token", "invalid_request_error", nil, "invalid_api_key")
		return
	}
	cfg := config.Get()
	if cfg == nil || strings.TrimSpace(cfg.FileStorage.BaseURL) == "" || strings.TrimSpace(cfg.FileStorage.APIKey) == "" {
		openaierror.Write(c, http.StatusServiceUnavailable, "file storage is unavailable", "server_error", nil, "file_storage_unavailable")
		return
	}
	fileHeader, err := c.FormFile("file")
	if err != nil {
		param := "file"
		openaierror.InvalidRequest(c, "file is required", &param, "missing_required_parameter")
		return
	}
	if fileHeader.Size <= 0 || fileHeader.Size > maxPublicFileBytes {
		param := "file"
		openaierror.InvalidRequest(c, "file size is invalid or exceeds 64 MiB", &param, "file_too_large")
		return
	}
	purpose := strings.TrimSpace(c.PostForm("purpose"))
	if purpose == "" {
		purpose = "assistants"
	}
	if !supportedFilePurpose(purpose) {
		param := "purpose"
		openaierror.InvalidRequest(c, "unsupported file purpose", &param, "invalid_value")
		return
	}
	source, err := fileHeader.Open()
	if err != nil {
		openaierror.Write(c, 500, "failed to open upload", "server_error", nil, "file_error")
		return
	}
	defer source.Close()
	prefix := make([]byte, 512)
	prefixBytes, err := io.ReadFull(source, prefix)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		openaierror.Write(c, 400, "failed to read upload", "invalid_request_error", nil, "file_too_large")
		return
	}
	prefix = prefix[:prefixBytes]
	mimeType := strings.TrimSpace(strings.Split(fileHeader.Header.Get("Content-Type"), ";")[0])
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = http.DetectContentType(prefix)
	}
	if len(mimeType) > 120 {
		param := "file"
		openaierror.InvalidRequest(c, "file content type is invalid", &param, "invalid_value")
		return
	}
	now := time.Now().UTC()
	filename := filepath.Base(fileHeader.Filename)
	if filename == "." || filename == "" {
		filename = "upload"
	}
	record := model.AIFile{ID: "file_" + strings.ReplaceAll(uuid.NewString(), "-", ""), UserID: token.UserID, TokenID: token.ID, Filename: filename, Purpose: purpose, Bytes: fileHeader.Size, MimeType: mimeType, Status: "uploading", CreatedAt: now, UpdatedAt: now}
	asset := model.MediaAsset{
		UserID: token.UserID, TokenID: token.ID, Purpose: "file",
		ObjectKey: "staging:" + uuid.NewString(), ContentType: mimeType,
		ContentLength: uint64(fileHeader.Size), SHA256: zeroSHA256,
		State: "staging", StateVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	// The token row serializes quota admission across application instances.
	err = withTokenFileTransaction(token.ID, func(tx *gorm.DB) error {
		var used int64
		if err := tx.Model(&model.AIFile{}).
			Where("token_id = ? AND status IN ?", token.ID, []string{"uploading", "processed", "deleting"}).
			Select("COALESCE(SUM(bytes), 0)").
			Scan(&used).Error; err != nil {
			return err
		}
		quota := tokenFileQuotaBytes()
		if record.Bytes > quota || used > quota-record.Bytes {
			return errFileQuotaExceeded
		}
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		if err := tx.Create(&asset).Error; err != nil {
			return err
		}
		fileID := record.ID
		ref := model.MediaAssetRef{MediaAssetID: asset.ID, UserID: token.UserID, TokenID: token.ID, Role: "file", Ordinal: 0, AIFileID: &fileID, CreatedAt: now}
		if err := tx.Create(&ref).Error; err != nil {
			return err
		}
		return tx.Exec(`INSERT INTO gw_media_asset_state_events(media_asset_id,old_state,new_state,state_version,reason_code,created_at) VALUES (?,NULL,'staging',1,'file_upload_allocated',?)`, asset.ID, now).Error
	})
	if errors.Is(err, errFileQuotaExceeded) {
		openaierror.Write(c, http.StatusBadRequest, "Token file storage quota exceeded", "invalid_request_error", nil, "file_storage_quota_exceeded")
		return
	}
	if err != nil {
		openaierror.Write(c, 500, "failed to store upload", "server_error", nil, "file_error")
		return
	}
	digest := sha256.New()
	count := &countingWriter{}
	stream := io.TeeReader(io.MultiReader(bytes.NewReader(prefix), source), io.MultiWriter(digest, count))
	storageRoot := strings.Trim(strings.TrimSpace(cfg.FileStorage.UploadPath), "/")
	if storageRoot != "" {
		storageRoot += "/"
	}
	storagePath := storageRoot + "files/" + strconv.FormatUint(uint64(token.ID), 10) + "/" + record.ID + "/"
	uploaded, err := uploadFileObject(c.Request.Context(), stream, mimeType, storagePath)
	if err != nil || count.Bytes != record.Bytes || (uploaded.Size > 0 && uploaded.Size != record.Bytes) {
		if uploaded.URL != "" {
			_ = deleteFileObject(context.WithoutCancel(c.Request.Context()), uploaded.URL)
		}
		_ = failFileUpload(context.WithoutCancel(c.Request.Context()), record.ID, asset.ID, token.ID, "file_upload_failed")
		openaierror.Write(c, http.StatusBadGateway, "failed to store upload", "server_error", nil, "file_storage_error")
		return
	}
	objectSHA256 := hex.EncodeToString(digest.Sum(nil))
	if err := verifyFileObject(c.Request.Context(), uploaded.URL, record.Bytes, objectSHA256); err != nil {
		_ = deleteFileObject(context.WithoutCancel(c.Request.Context()), uploaded.URL)
		_ = failFileUpload(context.WithoutCancel(c.Request.Context()), record.ID, asset.ID, token.ID, "file_object_verification_failed")
		openaierror.Write(c, http.StatusBadGateway, "failed to verify stored upload", "server_error", nil, "file_storage_integrity_error")
		return
	}
	objectVersion := strings.TrimSpace(uploaded.ObjectID)
	if objectVersion == "" {
		objectVersion = strings.TrimSpace(uploaded.ID)
	}
	err = withTokenFileTransaction(token.ID, func(tx *gorm.DB) error {
		tx = tx.WithContext(c.Request.Context())
		var current model.AIFile
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND token_id = ?", record.ID, token.ID).First(&current).Error; err != nil {
			return err
		}
		if current.Status != "uploading" {
			return errors.New("file upload state changed")
		}
		result := tx.Model(&model.MediaAsset{}).
			Where("id = ? AND state = ? AND state_version = ?", asset.ID, "staging", 1).
			Updates(map[string]any{
				"object_key": uploaded.StorageKey(), "storage_locator": uploaded.URL,
				"object_version": objectVersion, "content_type": mimeType,
				"content_length": uint64(count.Bytes), "sha256": objectSHA256,
				"state": "active", "state_version": 2, "updated_at": time.Now().UTC(),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("media asset upload state changed")
		}
		if err := tx.Exec(`INSERT INTO gw_media_asset_state_events(media_asset_id,old_state,new_state,state_version,reason_code,created_at) VALUES (?,'staging','active',2,'file_upload_verified',?)`, asset.ID, time.Now().UTC()).Error; err != nil {
			return err
		}
		return tx.Model(&model.AIFile{}).Where("id = ? AND status = ?", record.ID, "uploading").Updates(map[string]any{"status": "processed", "updated_at": time.Now().UTC()}).Error
	})
	if err != nil {
		_ = deleteFileObject(context.WithoutCancel(c.Request.Context()), uploaded.URL)
		_ = failFileUpload(context.WithoutCancel(c.Request.Context()), record.ID, asset.ID, token.ID, "file_publish_failed")
		openaierror.Write(c, http.StatusInternalServerError, "failed to publish upload", "server_error", nil, "file_error")
		return
	}
	record.Status = "processed"
	c.JSON(http.StatusOK, toFileObject(&record))
}

func ListFiles(c *gin.Context) {
	limit := defaultFileListLimit
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 10000 {
			param := "limit"
			openaierror.InvalidRequest(c, "limit must be between 1 and 10000", &param, "invalid_value")
			return
		}
		limit = parsed
	}
	order := strings.ToLower(strings.TrimSpace(c.DefaultQuery("order", "desc")))
	if order != "asc" && order != "desc" {
		param := "order"
		openaierror.InvalidRequest(c, "order must be asc or desc", &param, "invalid_value")
		return
	}

	tokenID := middleware.GetTokenID(c)
	query := model.DB().Select(fileMetadataColumns).Where("token_id = ? AND status = ?", tokenID, "processed")
	if purpose := strings.TrimSpace(c.Query("purpose")); purpose != "" {
		query = query.Where("purpose = ?", purpose)
	}
	if after := strings.TrimSpace(c.Query("after")); after != "" {
		var cursor model.AIFile
		if err := model.DB().Select("id, created_at").Where("id = ? AND token_id = ?", after, tokenID).First(&cursor).Error; err != nil {
			param := "after"
			openaierror.InvalidRequest(c, "after is not a valid file cursor", &param, "invalid_value")
			return
		}
		operator := "<"
		if order == "asc" {
			operator = ">"
		}
		query = query.Where("created_at "+operator+" ? OR (created_at = ? AND id "+operator+" ?)", cursor.CreatedAt, cursor.CreatedAt, cursor.ID)
	}
	query = query.Order("created_at " + order + ", id " + order).Limit(limit + 1)
	var records []model.AIFile
	if err := query.Find(&records).Error; err != nil {
		openaierror.Write(c, 500, "failed to list files", "server_error", nil, "file_error")
		return
	}
	hasMore := len(records) > limit
	if hasMore {
		records = records[:limit]
	}
	data := make([]fileObject, 0, len(records))
	for i := range records {
		data = append(data, toFileObject(&records[i]))
	}
	response := gin.H{"object": "list", "data": data, "has_more": hasMore}
	if len(records) > 0 {
		response["first_id"] = records[0].ID
		response["last_id"] = records[len(records)-1].ID
	}
	c.JSON(http.StatusOK, response)
}

func supportedFilePurpose(purpose string) bool {
	switch purpose {
	case "assistants", "batch", "evals", "fine-tune", "user_data", "vision":
		return true
	default:
		return false
	}
}

func GetFile(c *gin.Context) {
	record, ok := loadOwnedFileMetadata(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, toFileObject(record))
}

func GetFileContent(c *gin.Context) {
	record, err := service.LoadOwnedAIFile(c.Request.Context(), middleware.GetTokenID(c), c.Param("id"), false)
	if err != nil {
		writeFileNotFound(c)
		return
	}
	object, err := openFileObject(c.Request.Context(), record.ObjectURL)
	if err != nil {
		openaierror.Write(c, http.StatusBadGateway, "failed to read file content", "server_error", nil, "file_storage_error")
		return
	}
	defer object.Body.Close()
	contentType := record.MimeType
	if contentType == "" {
		contentType = strings.TrimSpace(strings.Split(object.Header.Get("Content-Type"), ";")[0])
	}
	c.DataFromReader(http.StatusOK, record.Bytes, contentType, object.Body, map[string]string{
		"Content-Disposition": `attachment; filename="` + strings.ReplaceAll(record.Filename, `"`, "") + `"`,
	})
}

func DeleteFile(c *gin.Context) {
	tokenID := middleware.GetTokenID(c)
	plan, err := prepareFileDelete(c.Request.Context(), tokenID, c.Param("id"))
	if errors.Is(err, errFileNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
		writeFileNotFound(c)
		return
	}
	if err != nil {
		openaierror.Write(c, 500, "failed to delete file", "server_error", nil, "file_error")
		return
	}
	if plan.DeleteObject {
		if err := deleteFileObject(c.Request.Context(), plan.ObjectURL); err != nil {
			openaierror.Write(c, http.StatusBadGateway, "failed to delete file content", "server_error", nil, "file_storage_error")
			return
		}
		if err := finishFileDelete(c.Request.Context(), tokenID, plan); err != nil {
			openaierror.Write(c, 500, "failed to finalize file deletion", "server_error", nil, "file_error")
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"id": c.Param("id"), "object": "file", "deleted": true})
}

func loadOwnedFileMetadata(c *gin.Context) (*model.AIFile, bool) {
	var record model.AIFile
	if err := model.DB().Select(fileMetadataColumns).Where("id = ? AND token_id = ? AND status = ?", c.Param("id"), middleware.GetTokenID(c), "processed").First(&record).Error; err != nil {
		writeFileNotFound(c)
		return nil, false
	}
	return &record, true
}

type countingWriter struct{ Bytes int64 }

func (w *countingWriter) Write(data []byte) (int, error) {
	w.Bytes += int64(len(data))
	return len(data), nil
}

type fileDeletePlan struct {
	FileID       string
	MediaAssetID uint64
	ObjectURL    string
	DeleteObject bool
}

func failFileUpload(ctx context.Context, fileID string, assetID uint64, tokenID uint, reason string) error {
	return withTokenFileTransaction(tokenID, func(tx *gorm.DB) error {
		tx = tx.WithContext(ctx)
		var asset model.MediaAsset
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND token_id = ?", assetID, tokenID).First(&asset).Error; err != nil {
			return err
		}
		if asset.State != "staging" {
			return nil
		}
		now := time.Now().UTC()
		if err := tx.Model(&model.MediaAsset{}).Where("id = ? AND state = ? AND state_version = ?", asset.ID, "staging", asset.StateVersion).
			Updates(map[string]any{"state": "deleted", "state_version": asset.StateVersion + 2, "updated_at": now}).Error; err != nil {
			return err
		}
		if err := tx.Exec(`INSERT INTO gw_media_asset_state_events(media_asset_id,old_state,new_state,state_version,reason_code,created_at) VALUES (?,'staging','deleting',?,?,?),(?,'deleting','deleted',?,?,?)`,
			asset.ID, asset.StateVersion+1, reason, now, asset.ID, asset.StateVersion+2, reason, now).Error; err != nil {
			return err
		}
		if err := tx.Where("media_asset_id = ? AND ai_file_id = ?", asset.ID, fileID).Delete(&model.MediaAssetRef{}).Error; err != nil {
			return err
		}
		return tx.Model(&model.AIFile{}).Where("id = ? AND token_id = ? AND status = ?", fileID, tokenID, "uploading").Updates(map[string]any{"status": "failed", "updated_at": now}).Error
	})
}

func prepareFileDelete(ctx context.Context, tokenID uint, fileID string) (fileDeletePlan, error) {
	plan := fileDeletePlan{FileID: fileID}
	err := withTokenFileTransaction(tokenID, func(tx *gorm.DB) error {
		tx = tx.WithContext(ctx)
		var file model.AIFile
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND token_id = ?", fileID, tokenID).First(&file).Error; err != nil {
			return errFileNotFound
		}
		if file.Status == "deleted" {
			return nil
		}
		if file.Status != "processed" && file.Status != "deleting" {
			return errFileNotFound
		}
		var ref model.MediaAssetRef
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("ai_file_id = ? AND role = ? AND ordinal = 0", fileID, "file").First(&ref).Error; err != nil {
			return err
		}
		var asset model.MediaAsset
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND token_id = ?", ref.MediaAssetID, tokenID).First(&asset).Error; err != nil {
			return err
		}
		plan.MediaAssetID = asset.ID
		plan.ObjectURL = strings.TrimSpace(asset.StorageLocator)
		if plan.ObjectURL == "" {
			plan.ObjectURL = strings.TrimSpace(asset.ObjectKey)
		}
		if asset.State == "deleted" {
			if err := tx.Delete(&ref).Error; err != nil {
				return err
			}
			return tx.Model(&model.AIFile{}).Where("id = ?", fileID).Updates(map[string]any{"status": "deleted", "updated_at": time.Now().UTC()}).Error
		}
		if file.Status == "deleting" {
			if asset.State != "deleting" {
				return errors.New("file and media deletion states disagree")
			}
			plan.DeleteObject = true
			return nil
		}
		var otherRefs int64
		if err := tx.Model(&model.MediaAssetRef{}).Where("media_asset_id = ? AND id <> ?", asset.ID, ref.ID).Count(&otherRefs).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		if otherRefs > 0 {
			if err := tx.Delete(&ref).Error; err != nil {
				return err
			}
			return tx.Model(&model.AIFile{}).Where("id = ? AND status = ?", fileID, "processed").Updates(map[string]any{"status": "deleted", "updated_at": now}).Error
		}
		if asset.State != "active" || plan.ObjectURL == "" {
			return errors.New("file media asset is not deletable")
		}
		result := tx.Model(&model.MediaAsset{}).Where("id = ? AND state = ? AND state_version = ?", asset.ID, "active", asset.StateVersion).
			Updates(map[string]any{"state": "deleting", "state_version": asset.StateVersion + 1, "updated_at": now})
		if result.Error != nil || result.RowsAffected != 1 {
			if result.Error != nil {
				return result.Error
			}
			return errors.New("media asset deletion state changed")
		}
		if err := tx.Exec(`INSERT INTO gw_media_asset_state_events(media_asset_id,old_state,new_state,state_version,reason_code,created_at) VALUES (?,'active','deleting',?,'file_deleted',?)`, asset.ID, asset.StateVersion+1, now).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.AIFile{}).Where("id = ? AND status = ?", fileID, "processed").Updates(map[string]any{"status": "deleting", "updated_at": now}).Error; err != nil {
			return err
		}
		plan.DeleteObject = true
		return nil
	})
	return plan, err
}

func finishFileDelete(ctx context.Context, tokenID uint, plan fileDeletePlan) error {
	return withTokenFileTransaction(tokenID, func(tx *gorm.DB) error {
		tx = tx.WithContext(ctx)
		var asset model.MediaAsset
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND token_id = ?", plan.MediaAssetID, tokenID).First(&asset).Error; err != nil {
			return err
		}
		if asset.State == "deleted" {
			return tx.Model(&model.AIFile{}).Where("id = ? AND token_id = ?", plan.FileID, tokenID).Updates(map[string]any{"status": "deleted", "updated_at": time.Now().UTC()}).Error
		}
		if asset.State != "deleting" {
			return errors.New("media asset is not deleting")
		}
		now := time.Now().UTC()
		result := tx.Model(&model.MediaAsset{}).Where("id = ? AND state = ? AND state_version = ?", asset.ID, "deleting", asset.StateVersion).
			Updates(map[string]any{"state": "deleted", "state_version": asset.StateVersion + 1, "updated_at": now})
		if result.Error != nil || result.RowsAffected != 1 {
			if result.Error != nil {
				return result.Error
			}
			return errors.New("media asset deletion state changed")
		}
		if err := tx.Exec(`INSERT INTO gw_media_asset_state_events(media_asset_id,old_state,new_state,state_version,reason_code,created_at) VALUES (?,'deleting','deleted',?,'object_deleted',?)`, asset.ID, asset.StateVersion+1, now).Error; err != nil {
			return err
		}
		if err := tx.Where("media_asset_id = ? AND ai_file_id = ?", asset.ID, plan.FileID).Delete(&model.MediaAssetRef{}).Error; err != nil {
			return err
		}
		return tx.Model(&model.AIFile{}).Where("id = ? AND token_id = ? AND status = ?", plan.FileID, tokenID, "deleting").Updates(map[string]any{"status": "deleted", "updated_at": now}).Error
	})
}

func tokenFileQuotaBytes() int64 {
	cfg := config.Get()
	if cfg == nil || cfg.FileStorage.MaxTotalSizeMB <= 0 {
		return defaultTokenFileQuotaBytes
	}
	maxMB := int64(cfg.FileStorage.MaxTotalSizeMB)
	if maxMB > int64(^uint64(0)>>1)/(1024*1024) {
		return int64(^uint64(0) >> 1)
	}
	return maxMB * 1024 * 1024
}

func withTokenFileTransaction(tokenID uint, fn func(*gorm.DB) error) error {
	// 进程内分片锁减少同 Token 的数据库锁竞争；数据库行锁负责多实例一致性。
	lock := &fileQuotaLocks[tokenID%uint(len(fileQuotaLocks))]
	lock.Lock()
	defer lock.Unlock()

	return model.DB().Transaction(func(tx *gorm.DB) error {
		var token model.Token
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").First(&token, tokenID).Error; err != nil {
			return err
		}
		return fn(tx)
	})
}

func writeFileNotFound(c *gin.Context) {
	openaierror.Write(c, http.StatusNotFound, "File not found", "invalid_request_error", nil, "file_not_found")
}

func toFileObject(record *model.AIFile) fileObject {
	return fileObject{ID: record.ID, Object: "file", Bytes: record.Bytes, CreatedAt: record.CreatedAt.Unix(), Filename: record.Filename, Purpose: record.Purpose, Status: record.Status}
}

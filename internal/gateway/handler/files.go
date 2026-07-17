package handler

import (
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
	"github.com/mirainya/Prism/pkg/config"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	maxPublicFileBytes         int64 = 64 * 1024 * 1024
	defaultTokenFileQuotaBytes int64 = config.DefaultFileStorageMaxTotalSizeMB * 1024 * 1024
	defaultFileListLimit             = 10000
	fileMetadataColumns              = "id, user_id, token_id, filename, purpose, bytes, mime_type, status, created_at"
)

var (
	errFileNotFound      = errors.New("file not found")
	errFileQuotaExceeded = errors.New("file storage quota exceeded")
	fileQuotaLocks       [256]sync.Mutex
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
	data, err := io.ReadAll(io.LimitReader(source, maxPublicFileBytes+1))
	if err != nil || int64(len(data)) > maxPublicFileBytes {
		openaierror.Write(c, 400, "failed to read upload", "invalid_request_error", nil, "file_too_large")
		return
	}
	token := middleware.GetToken(c)
	if token == nil || token.ID == 0 {
		openaierror.Write(c, http.StatusUnauthorized, "Invalid authentication token", "invalid_request_error", nil, "invalid_api_key")
		return
	}
	mimeType := fileHeader.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	record := model.AIFile{ID: "file_" + strings.ReplaceAll(uuid.NewString(), "-", ""), UserID: token.UserID, TokenID: token.ID, Filename: filepath.Base(fileHeader.Filename), Purpose: purpose, Bytes: int64(len(data)), MimeType: mimeType, Content: data, Status: "processed", CreatedAt: time.Now()}
	// 配额检查与文件插入共用 Token 行锁，防止并发上传同时通过旧的 used 值。
	err = withTokenFileTransaction(token.ID, func(tx *gorm.DB) error {
		var used int64
		if err := tx.Model(&model.AIFile{}).
			Where("token_id = ?", token.ID).
			Select("COALESCE(SUM(bytes), 0)").
			Scan(&used).Error; err != nil {
			return err
		}
		quota := tokenFileQuotaBytes()
		if record.Bytes > quota || used > quota-record.Bytes {
			return errFileQuotaExceeded
		}
		return tx.Create(&record).Error
	})
	if errors.Is(err, errFileQuotaExceeded) {
		openaierror.Write(c, http.StatusBadRequest, "Token file storage quota exceeded", "invalid_request_error", nil, "file_storage_quota_exceeded")
		return
	}
	if err != nil {
		openaierror.Write(c, 500, "failed to store upload", "server_error", nil, "file_error")
		return
	}
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
	query := model.DB().Select(fileMetadataColumns).Where("token_id = ?", tokenID)
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
	record, ok := loadOwnedFileContent(c)
	if !ok {
		return
	}
	c.Header("Content-Type", record.MimeType)
	c.Header("Content-Disposition", `attachment; filename="`+strings.ReplaceAll(record.Filename, `"`, "")+`"`)
	c.Data(http.StatusOK, record.MimeType, record.Content)
}

func DeleteFile(c *gin.Context) {
	tokenID := middleware.GetTokenID(c)
	err := withTokenFileTransaction(tokenID, func(tx *gorm.DB) error {
		result := tx.Where("id = ? AND token_id = ?", c.Param("id"), tokenID).Delete(&model.AIFile{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errFileNotFound
		}
		return nil
	})
	if errors.Is(err, errFileNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
		writeFileNotFound(c)
		return
	}
	if err != nil {
		openaierror.Write(c, 500, "failed to delete file", "server_error", nil, "file_error")
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": c.Param("id"), "object": "file", "deleted": true})
}

func loadOwnedFileMetadata(c *gin.Context) (*model.AIFile, bool) {
	var record model.AIFile
	if err := model.DB().Select(fileMetadataColumns).Where("id = ? AND token_id = ?", c.Param("id"), middleware.GetTokenID(c)).First(&record).Error; err != nil {
		writeFileNotFound(c)
		return nil, false
	}
	return &record, true
}

func loadOwnedFileContent(c *gin.Context) (*model.AIFile, bool) {
	var record model.AIFile
	if err := model.DB().Select("filename, mime_type, content").Where("id = ? AND token_id = ?", c.Param("id"), middleware.GetTokenID(c)).First(&record).Error; err != nil {
		writeFileNotFound(c)
		return nil, false
	}
	return &record, true
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

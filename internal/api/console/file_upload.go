package console

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/pkg/config"
	"github.com/mirainya/Prism/pkg/errors"
)

// xfsResponse xfilestorage 通用响应
type xfsResponse struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// uploadResult 上传结果
type uploadResult struct {
	URL              string `json:"url"`
	ThURL            string `json:"thUrl,omitempty"`
	Filename         string `json:"filename"`
	OriginalFilename string `json:"originalFilename,omitempty"`
	Size             int64  `json:"size"`
	ContentType      string `json:"contentType"`
}

var imageTypes = []string{"image/png", "image/jpeg", "image/gif", "image/webp"}

// PlaygroundUploadFile POST /api/playground/:token_id/upload
func PlaygroundUploadFile(c *gin.Context) {
	_, ok := getPlaygroundToken(c)
	if !ok {
		return
	}

	cfg := config.C.FileStorage
	if cfg.BaseURL == "" || cfg.APIKey == "" {
		resp.ErrorMsg(c, http.StatusServiceUnavailable, 503, "文件存储服务未配置")
		return
	}

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, "缺少 file 字段"))
		return
	}
	defer file.Close()

	// 校验文件大小
	maxSize := int64(cfg.MaxFileSizeMB) * 1024 * 1024
	if maxSize <= 0 {
		maxSize = 20 * 1024 * 1024
	}
	if header.Size > maxSize {
		resp.ErrorMsg(c, http.StatusRequestEntityTooLarge, 413,
			fmt.Sprintf("文件大小超过限制 (%dMB)", cfg.MaxFileSizeMB))
		return
	}

	// 校验 MIME 类型
	contentType := header.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if len(cfg.AllowedTypes) > 0 && !slices.Contains(cfg.AllowedTypes, contentType) {
		resp.ErrorMsg(c, http.StatusBadRequest, 400,
			fmt.Sprintf("不支持的文件类型: %s", contentType))
		return
	}

	// 构造存储路径: playground/{user_id}/{YYYY-MM}/{images|docs|other}/
	userID := middleware.GetUserID(c)
	now := time.Now()
	subDir := categorizeFile(contentType)
	uploadPath := fmt.Sprintf("%s%d/%s/%s/",
		cfg.UploadPath, userID, now.Format("2006-01"), subDir)

	// 判断是否为图片 → 选择上传端点
	isImage := slices.Contains(imageTypes, contentType)
	var endpoint string
	if isImage {
		endpoint = cfg.BaseURL + "/api/v1/upload-image"
	} else {
		endpoint = cfg.BaseURL + "/api/v1/upload"
	}

	// 构造 multipart 请求
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	part, err := writer.CreateFormFile("file", filepath.Base(header.Filename))
	if err != nil {
		resp.InternalError(c, errors.WithMessage(errors.ErrUploadFailed, "构造请求失败"))
		return
	}
	if _, err := io.Copy(part, file); err != nil {
		resp.InternalError(c, errors.WithMessage(errors.ErrUploadFailed, "读取文件失败"))
		return
	}

	_ = writer.WriteField("path", uploadPath)
	writer.Close()

	// 转发到 xfilestorage
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, endpoint, &buf)
	if err != nil {
		resp.InternalError(c, errors.WithMessage(errors.ErrUploadFailed, "创建请求失败"))
		return
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-Api-Key", cfg.APIKey)

	httpResp, err := http.DefaultClient.Do(req)
	if err != nil {
		resp.InternalError(c, errors.WithMessage(errors.ErrUploadFailed, "请求存储服务失败"))
		return
	}
	defer httpResp.Body.Close()

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		resp.InternalError(c, errors.WithMessage(errors.ErrUploadFailed, "读取响应失败"))
		return
	}

	var xfsResp xfsResponse
	if err := json.Unmarshal(body, &xfsResp); err != nil {
		resp.InternalError(c, errors.WithMessage(errors.ErrUploadFailed, "解析响应失败"))
		return
	}

	if xfsResp.Code != 200 {
		resp.ErrorMsg(c, http.StatusBadGateway, 502,
			fmt.Sprintf("存储服务错误: %s", xfsResp.Message))
		return
	}

	var result uploadResult
	if err := json.Unmarshal(xfsResp.Data, &result); err != nil {
		resp.InternalError(c, errors.WithMessage(errors.ErrUploadFailed, "解析上传结果失败"))
		return
	}

	// 确保 URL 有协议前缀
	if result.URL != "" && !strings.HasPrefix(result.URL, "http://") && !strings.HasPrefix(result.URL, "https://") {
		result.URL = "https://" + result.URL
	}
	if result.ThURL != "" && !strings.HasPrefix(result.ThURL, "http://") && !strings.HasPrefix(result.ThURL, "https://") {
		result.ThURL = "https://" + result.ThURL
	}

	resp.Success(c, result)
}

// categorizeFile 根据 MIME 类型分类目录
func categorizeFile(contentType string) string {
	if slices.Contains(imageTypes, contentType) {
		return "images"
	}
	docTypes := []string{
		"application/pdf",
		"text/plain",
		"text/csv",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	}
	if slices.Contains(docTypes, contentType) {
		return "docs"
	}
	if strings.HasPrefix(contentType, "text/") {
		return "docs"
	}
	return "other"
}

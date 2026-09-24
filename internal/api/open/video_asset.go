package open

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/video"
	perrors "github.com/mirainya/Prism/pkg/errors"
)

func CreateVideoAsset(c *gin.Context) {
	token := middleware.GetToken(c)
	if token == nil {
		resp.ErrorMsg(c, http.StatusUnauthorized, 401, "unauthorized")
		return
	}
	req := &video.CreateAssetRequest{UserID: token.UserID, TokenID: token.ID}
	contentType := c.ContentType()
	switch {
	case strings.HasPrefix(contentType, "multipart/form-data"):
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, video.MaxAssetUploadBytes()+(1<<20))
		file, err := c.FormFile("file")
		if err != nil {
			writeVideoAssetError(c, err)
			return
		}
		if file.Size > video.MaxAssetUploadBytes() {
			writeVideoAssetError(c, video.ErrFileTooLarge)
			return
		}
		opened, err := file.Open()
		if err != nil {
			writeVideoAssetError(c, err)
			return
		}
		defer opened.Close()
		req.Reader = opened
		req.SizeBytes = file.Size
		req.Kind = c.PostForm("kind")
		req.ContentType = file.Header.Get("Content-Type")
		if duration := strings.TrimSpace(c.PostForm("duration_seconds")); duration != "" {
			value, err := strconv.ParseFloat(duration, 64)
			if err != nil {
				writeVideoAssetError(c, err)
				return
			}
			req.DurationSeconds = &value
		}
	case contentType == "application/json":
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
		var body struct {
			URL             string   `json:"url" binding:"required"`
			Kind            string   `json:"kind" binding:"required"`
			ContentType     string   `json:"content_type" binding:"required"`
			DurationSeconds *float64 `json:"duration_seconds"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			writeVideoAssetError(c, err)
			return
		}
		req.URL, req.Kind, req.ContentType = body.URL, body.Kind, body.ContentType
		req.DurationSeconds = body.DurationSeconds
	default:
		resp.BadRequest(c, perrors.WithMessage(perrors.ErrInvalidParams, "use multipart/form-data or application/json"))
		return
	}

	asset, err := video.NewUnifiedAssetService(model.DB()).Create(c.Request.Context(), req)
	if err != nil {
		writeVideoAssetError(c, err)
		return
	}
	resp.Success(c, asset)
}

func GetVideoAsset(c *gin.Context) {
	token := middleware.GetToken(c)
	if token == nil {
		resp.ErrorMsg(c, http.StatusUnauthorized, 401, "unauthorized")
		return
	}
	asset, err := video.NewUnifiedAssetService(model.DB()).Get(c.Request.Context(), token.ID, c.Param("asset_id"))
	if err != nil {
		writeVideoAssetError(c, err)
		return
	}
	resp.Success(c, asset)
}

func DeleteVideoAsset(c *gin.Context) {
	token := middleware.GetToken(c)
	if token == nil {
		resp.ErrorMsg(c, http.StatusUnauthorized, 401, "unauthorized")
		return
	}
	err := video.NewUnifiedAssetService(model.DB()).Delete(c.Request.Context(), token.ID, c.Param("asset_id"))
	if err != nil {
		writeVideoAssetError(c, err)
		return
	}
	resp.Success(c, gin.H{"id": c.Param("asset_id"), "status": "expired"})
}

func writeVideoAssetError(c *gin.Context, err error) {
	middleware.SetAccessErrorDetail(c, video.AssetErrorDetail(err))
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		resp.ErrorMsg(c, http.StatusRequestEntityTooLarge, 413, "video asset request body is too large")
		return
	}
	switch {
	case errors.Is(err, video.ErrAssetNotFound):
		resp.ErrorMsg(c, http.StatusNotFound, 404, "video asset not found")
	case errors.Is(err, video.ErrFileTooLarge):
		resp.ErrorMsg(c, http.StatusRequestEntityTooLarge, 413, "video asset is too large")
	case errors.Is(err, video.ErrInvalidAsset), errors.Is(err, video.ErrAssetNotReady):
		resp.BadRequest(c, perrors.WithMessage(perrors.ErrInvalidParams, "invalid video asset"))
	case errors.Is(err, video.ErrAssetInUse):
		resp.ErrorMsg(c, http.StatusConflict, 409, "video asset is still in use")
	default:
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, "video asset operation failed")
	}
}

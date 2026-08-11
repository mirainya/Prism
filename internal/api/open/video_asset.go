package open

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/video"
	perrors "github.com/mirainya/Prism/pkg/errors"
)

func CreateVideoAsset(c *gin.Context) {
	token := middleware.GetToken(c)
	if token == nil {
		resp.ErrorMsg(c, http.StatusUnauthorized, 401, "unauthorized")
		return
	}
	if videoEngine == nil {
		resp.ErrorMsg(c, http.StatusServiceUnavailable, 503, "video engine unavailable")
		return
	}

	req := &video.CreateAssetRequest{TokenID: token.ID}
	contentType := c.ContentType()
	switch {
	case strings.HasPrefix(contentType, "multipart/form-data"):
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, video.MaxAssetUploadBytes()+(1<<20))
		file, err := c.FormFile("file")
		if err != nil {
			writeVideoAssetError(c, err)
			return
		}
		opened, err := file.Open()
		if err != nil {
			writeVideoAssetError(c, err)
			return
		}
		defer opened.Close()
		data, err := io.ReadAll(io.LimitReader(opened, video.MaxAssetUploadBytes()+1))
		if err != nil {
			writeVideoAssetError(c, err)
			return
		}
		req.Data = data
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

	asset, err := video.NewAssetService(videoEngine.DB()).Create(c.Request.Context(), req)
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
	if videoEngine == nil {
		resp.NotFound(c, perrors.ErrTaskNotFound)
		return
	}
	asset, err := video.NewAssetService(videoEngine.DB()).Get(c.Request.Context(), token.ID, c.Param("asset_id"))
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
	if videoEngine == nil {
		resp.NotFound(c, perrors.ErrTaskNotFound)
		return
	}
	err := video.NewAssetService(videoEngine.DB()).Delete(c.Request.Context(), token.ID, c.Param("asset_id"))
	if err != nil {
		writeVideoAssetError(c, err)
		return
	}
	resp.Success(c, gin.H{"id": c.Param("asset_id"), "status": "expired"})
}

func writeVideoAssetError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, video.ErrAssetNotFound):
		resp.ErrorMsg(c, http.StatusNotFound, 404, err.Error())
	case errors.Is(err, video.ErrFileTooLarge):
		resp.ErrorMsg(c, http.StatusRequestEntityTooLarge, 413, err.Error())
	case errors.Is(err, video.ErrInvalidAsset), errors.Is(err, video.ErrAssetNotReady):
		resp.BadRequest(c, perrors.WithMessage(perrors.ErrInvalidParams, err.Error()))
	default:
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
	}
}

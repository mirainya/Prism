package console

import (
	stderrors "errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/video"
	perrors "github.com/mirainya/Prism/pkg/errors"
)

// PlaygroundCreateVideoAsset POST /api/playground/:token_id/videos/assets
func PlaygroundCreateVideoAsset(c *gin.Context) {
	token, ok := usePlaygroundToken(c)
	if !ok {
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, video.MaxAssetUploadBytes()+(1<<20))
	file, err := c.FormFile("file")
	if err != nil {
		writePlaygroundVideoAssetError(c, fmt.Errorf("%w: file is required", video.ErrInvalidAsset))
		return
	}
	if file.Size > video.MaxAssetUploadBytes() {
		writePlaygroundVideoAssetError(c, video.ErrFileTooLarge)
		return
	}
	opened, err := file.Open()
	if err != nil {
		writePlaygroundVideoAssetError(c, err)
		return
	}
	defer opened.Close()

	data, err := io.ReadAll(io.LimitReader(opened, video.MaxAssetUploadBytes()+1))
	if err != nil {
		writePlaygroundVideoAssetError(c, err)
		return
	}

	request := &video.CreateAssetRequest{
		TokenID:     token.ID,
		Kind:        c.PostForm("kind"),
		ContentType: file.Header.Get("Content-Type"),
		Data:        data,
	}
	if rawDuration := strings.TrimSpace(c.PostForm("duration_seconds")); rawDuration != "" {
		duration, parseErr := strconv.ParseFloat(rawDuration, 64)
		if parseErr != nil {
			writePlaygroundVideoAssetError(c, fmt.Errorf("%w: duration_seconds must be a number", video.ErrInvalidAsset))
			return
		}
		request.DurationSeconds = &duration
	}

	asset, err := video.NewAssetService(model.DB()).Create(c.Request.Context(), request)
	if err != nil {
		writePlaygroundVideoAssetError(c, err)
		return
	}
	resp.Success(c, asset)
}

func writePlaygroundVideoAssetError(c *gin.Context, err error) {
	switch {
	case stderrors.Is(err, video.ErrFileTooLarge):
		resp.ErrorMsg(c, http.StatusRequestEntityTooLarge, 413, err.Error())
	case stderrors.Is(err, video.ErrInvalidAsset), stderrors.Is(err, video.ErrAssetNotReady):
		resp.BadRequest(c, perrors.WithMessage(perrors.ErrInvalidParams, err.Error()))
	default:
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
	}
}

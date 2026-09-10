package console

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/service"
)

// DocsListModels returns models backed by executable routes in the active catalog.
func DocsListModels(c *gin.Context) {
	svc := service.NewQueryService()
	result, err := svc.ListAvailableCapabilities(c.Request.Context(), "", "")
	if err != nil {
		resp.ErrorMsg(c, http.StatusServiceUnavailable, 503, "model catalog is unavailable")
		return
	}
	resp.Success(c, result)
}

// DocsListVideos returns the current public video protocol capabilities.
// Credentials and upstream task details are intentionally excluded.
func DocsListVideos(c *gin.Context) {
	result, err := listVideoModels(c.Request.Context())
	if err != nil {
		resp.ErrorMsg(c, http.StatusServiceUnavailable, 503, "video catalog is unavailable")
		return
	}
	resp.Success(c, result)
}

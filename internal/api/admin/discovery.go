package admin

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
)

// SyncAllModels POST /api/admin/discovery/sync
func SyncAllModels(c *gin.Context) {
	resp.ErrorMsg(c, http.StatusNotImplemented, 501, "discovery sync not implemented")
}

// SyncChannelModels POST /api/admin/discovery/sync/:channel_id
func SyncChannelModels(c *gin.Context) {
	resp.ErrorMsg(c, http.StatusNotImplemented, 501, "discovery sync not implemented")
}

// ListPendingModels GET /api/admin/discovery/pending
func ListPendingModels(c *gin.Context) {
	resp.Success(c, []any{})
}

// ApproveModels POST /api/admin/discovery/approve
func ApproveModels(c *gin.Context) {
	resp.Success(c, gin.H{"affected": 0})
}

// RejectModels POST /api/admin/discovery/reject
func RejectModels(c *gin.Context) {
	resp.Success(c, gin.H{"affected": 0})
}

// GetModelMeta GET /api/admin/models/:code/meta
func GetModelMeta(c *gin.Context) {
	m, err := modelAdminService.GetModel(c.Param("code"))
	if err != nil {
		resp.ErrorMsg(c, http.StatusNotFound, 404, "model not found")
		return
	}
	resp.Success(c, m)
}

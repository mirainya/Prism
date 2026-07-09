package admin

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	pkgErrors "github.com/mirainya/Prism/pkg/errors"
)

var gatewayAdminService = service.NewGatewayAdminService()

// ---------- 渠道 ----------

// ListGwChannels GET /api/admin/gw/channels
func ListGwChannels(c *gin.Context) {
	rows, err := gatewayAdminService.ListChannels()
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	resp.Success(c, rows)
}

// GetGwChannel GET /api/admin/gw/channels/:id
func GetGwChannel(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	ch, err := gatewayAdminService.GetChannel(id)
	if err != nil {
		resp.NotFound(c, pkgErrors.WithMessage(pkgErrors.ErrModelNotFound, err.Error()))
		return
	}
	resp.Success(c, ch)
}

// CreateGwChannel POST /api/admin/gw/channels
func CreateGwChannel(c *gin.Context) {
	var ch model.GwChannel
	if err := c.ShouldBindJSON(&ch); err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	if err := gatewayAdminService.CreateChannel(&ch); err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	resp.Success(c, ch)
}

// UpdateGwChannel PUT /api/admin/gw/channels/:id
func UpdateGwChannel(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	if err := gatewayAdminService.UpdateChannel(id, body); err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	resp.Success(c, gin.H{"updated": true})
}

// DeleteGwChannel DELETE /api/admin/gw/channels/:id
func DeleteGwChannel(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	if err := gatewayAdminService.DeleteChannel(id); err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	resp.Success(c, gin.H{"deleted": true})
}

// ReorderGwChannels POST /api/admin/gw/channels/reorder  body: {"ids":[3,1,2]}
func ReorderGwChannels(c *gin.Context) {
	var body struct {
		IDs []uint `json:"ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	if err := gatewayAdminService.ReorderChannels(body.IDs); err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	resp.Success(c, gin.H{"reordered": true})
}

// ---------- 渠道 key ----------

// ListGwKeys GET /api/admin/gw/channels/:id/keys
func ListGwKeys(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	rows, err := gatewayAdminService.ListKeys(id)
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	resp.Success(c, rows)
}

// CreateGwKey POST /api/admin/gw/keys
func CreateGwKey(c *gin.Context) {
	var key model.GwChannelKey
	if err := c.ShouldBindJSON(&key); err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	if err := gatewayAdminService.CreateKey(&key); err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	resp.Success(c, key)
}

// UpdateGwKey PUT /api/admin/gw/keys/:id
func UpdateGwKey(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	if err := gatewayAdminService.UpdateKey(id, body); err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	resp.Success(c, gin.H{"updated": true})
}

// DeleteGwKey DELETE /api/admin/gw/keys/:id
func DeleteGwKey(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	if err := gatewayAdminService.DeleteKey(id); err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	resp.Success(c, gin.H{"deleted": true})
}

// ---------- 能力 ----------

// ListGwAbilities GET /api/admin/gw/abilities?model=&channel_id=&key_id=
func ListGwAbilities(c *gin.Context) {
	var f service.AbilityFilter
	f.ModelName = c.Query("model")
	if v := c.Query("channel_id"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			f.ChannelID = uint(n)
		}
	}
	if v := c.Query("key_id"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			f.KeyID = uint(n)
		}
	}
	rows, err := gatewayAdminService.ListAbilities(f)
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	resp.Success(c, rows)
}

// UpdateGwAbility PUT /api/admin/gw/abilities/:id
func UpdateGwAbility(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	if err := gatewayAdminService.UpdateAbility(id, body); err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	resp.Success(c, gin.H{"updated": true})
}

// DeleteGwAbility DELETE /api/admin/gw/abilities/:id
func DeleteGwAbility(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	if err := gatewayAdminService.DeleteAbility(id); err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	resp.Success(c, gin.H{"deleted": true})
}

// ---------- 对话模型(可路由模型 + 元数据 + 可用性) ----------

// ListGwModels GET /api/admin/gw/models
func ListGwModels(c *gin.Context) {
	rows, err := gatewayAdminService.ListModels()
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	resp.Success(c, rows)
}

// ReorderGwModels POST /api/admin/gw/models/reorder  body: {"names":["m1","m2"]}
func ReorderGwModels(c *gin.Context) {
	var body struct {
		Names []string `json:"names" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	if err := gatewayAdminService.ReorderModels(body.Names); err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	resp.Success(c, gin.H{"reordered": true})
}

// ---------- 元数据 ----------

// ListGwModelMeta GET /api/admin/gw/model-meta
func ListGwModelMeta(c *gin.Context) {
	rows, err := gatewayAdminService.ListModelMeta()
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	resp.Success(c, rows)
}

// UpsertGwModelMeta PUT /api/admin/gw/model-meta/:model_name
func UpsertGwModelMeta(c *gin.Context) {
	var m model.GwModelMeta
	if err := c.ShouldBindJSON(&m); err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	m.ModelName = c.Param("model_name")
	if err := gatewayAdminService.UpsertModelMeta(&m); err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	resp.Success(c, m)
}

// DeleteGwModelMeta DELETE /api/admin/gw/model-meta/:model_name
func DeleteGwModelMeta(c *gin.Context) {
	if err := gatewayAdminService.DeleteModelMeta(c.Param("model_name")); err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	resp.Success(c, gin.H{"deleted": true})
}

// ---------- 拉取 / 导入 ----------

// DiscoverGwKeyModels GET /api/admin/gw/keys/:id/discover
func DiscoverGwKeyModels(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	items, err := gatewayAdminService.DiscoverKeyModels(c.Request.Context(), id)
	if err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	resp.Success(c, items)
}

// ImportGwKeyModels POST /api/admin/gw/keys/:id/import
func ImportGwKeyModels(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	var req service.GwImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	req.KeyID = id
	res, err := gatewayAdminService.ImportKeyModels(&req)
	if err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	resp.Success(c, res)
}

package admin

import (
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	pkgErrors "github.com/mirainya/Prism/pkg/errors"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

var gatewayAdminService = service.NewGatewayAdminService()

type gwChannelKeyResponse struct {
	ID          uint      `json:"id"`
	ChannelID   uint      `json:"channel_id"`
	Name        string    `json:"name"`
	APIKey      string    `json:"api_key"`
	MaskedKey   string    `json:"masked_key"`
	Weight      int       `json:"weight"`
	Status      int8      `json:"status"`
	MaxConc     int       `json:"max_conc"`
	CurrentConc int       `json:"current_conc"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type createGwChannelKeyRequest struct {
	ChannelID uint   `json:"channel_id"`
	Name      string `json:"name"`
	APIKey    string `json:"api_key"`
	Weight    int    `json:"weight"`
	Status    int8   `json:"status"`
	MaxConc   int    `json:"max_conc"`
}

func (request createGwChannelKeyRequest) model() model.GwChannelKey {
	return model.GwChannelKey{
		ChannelID: request.ChannelID,
		Name:      request.Name,
		APIKey:    request.APIKey,
		Weight:    request.Weight,
		Status:    request.Status,
		MaxConc:   request.MaxConc,
	}
}

func newGwChannelKeyResponse(key *model.GwChannelKey) gwChannelKeyResponse {
	maskedKey := service.MaskCredential(key.APIKey)
	return gwChannelKeyResponse{
		ID: key.ID, ChannelID: key.ChannelID, Name: key.Name,
		APIKey: maskedKey, MaskedKey: maskedKey,
		Weight: key.Weight, Status: key.Status, MaxConc: key.MaxConc,
		CurrentConc: key.CurrentConc, CreatedAt: key.CreatedAt, UpdatedAt: key.UpdatedAt,
	}
}

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
	result := make([]gwChannelKeyResponse, len(rows))
	for i := range rows {
		result[i] = newGwChannelKeyResponse(&rows[i])
	}
	resp.Success(c, result)
}

// CreateGwKey POST /api/admin/gw/keys
func CreateGwKey(c *gin.Context) {
	var request createGwChannelKeyRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	key := request.model()
	if err := gatewayAdminService.CreateKey(&key); err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	resp.Success(c, newGwChannelKeyResponse(&key))
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
		if errors.Is(err, service.ErrGwInvalidCapabilities) {
			resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
			return
		}
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

func ListGwAbilityTransports(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	rows, err := gatewayAdminService.ListAbilityTransports(id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		resp.NotFound(c, pkgErrors.ErrModelNotFound)
		return
	}
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	resp.Success(c, rows)
}

func UpsertGwAbilityTransport(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	var body struct {
		Transport model.UpstreamTransport `json:"transport" binding:"required"`
		Status    *int8                   `json:"status"`
		Config    json.RawMessage         `json:"config"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	status := int8(1)
	if body.Status != nil {
		status = *body.Status
	}
	row, err := gatewayAdminService.UpsertAbilityTransport(id, body.Transport, status, datatypes.JSON(body.Config))
	if errors.Is(err, service.ErrGwInvalidTransport) {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		resp.NotFound(c, pkgErrors.ErrModelNotFound)
		return
	}
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	resp.Success(c, row)
}

func DeleteGwAbilityTransport(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	err = gatewayAdminService.DeleteAbilityTransport(id, model.UpstreamTransport(c.Param("transport")))
	if errors.Is(err, service.ErrGwInvalidTransport) {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		resp.NotFound(c, pkgErrors.ErrModelNotFound)
		return
	}
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	resp.Success(c, gin.H{"deleted": true})
}

func ProbeGwAbilityTransport(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	result, err := gatewayAdminService.ProbeAbilityTransport(c.Request.Context(), id, model.UpstreamTransport(c.Param("transport")))
	if errors.Is(err, service.ErrGwInvalidTransport) {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		resp.NotFound(c, pkgErrors.ErrModelNotFound)
		return
	}
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	resp.Success(c, result)
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

// DeleteGwModel DELETE /api/admin/gw/models?name=xxx
// query string 传名避免 gin 把路径中的 %2F 解码成路径分隔符。
func DeleteGwModel(c *gin.Context) {
	modelName := c.Query("name")
	if modelName == "" {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, "name is required"))
		return
	}
	if err := gatewayAdminService.DeleteModel(modelName); err != nil {
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

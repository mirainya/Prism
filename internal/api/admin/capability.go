package admin

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/service"
	"gorm.io/datatypes"
)

var modelAdminService = service.NewModelAdminService()

// ListCapabilities 模型列表
func ListCapabilities(c *gin.Context) {
	models, err := modelAdminService.ListModels(c.Query("status"))
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}
	resp.Success(c, models)
}

// GetCapability 获取模型详情
func GetCapability(c *gin.Context) {
	m, err := modelAdminService.GetModel(c.Param("code"))
	if err != nil {
		resp.ErrorMsg(c, http.StatusNotFound, 404, "model not found")
		return
	}
	resp.Success(c, m)
}

// CreateCapability 创建模型
func CreateCapability(c *gin.Context) {
	var raw struct {
		service.CreateModelRequest
		StandardParams datatypes.JSON `json:"standard_params"`
	}
	if err := c.ShouldBindJSON(&raw); err != nil {
		resp.ErrorMsg(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	if len(raw.ParamSchema) == 0 && len(raw.StandardParams) > 0 {
		raw.CreateModelRequest.ParamSchema = raw.StandardParams
	}

	m, err := modelAdminService.CreateModel(&raw.CreateModelRequest)
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}

	resp.Success(c, m)
}

// UpdateCapability 更新模型
func UpdateCapability(c *gin.Context) {
	code := c.Param("code")

	var req struct {
		Code           string         `json:"code"`
		Name           string         `json:"name"`
		Type           string         `json:"type"`
		Description    string         `json:"description"`
		ParamSchema    datatypes.JSON `json:"param_schema"`
		StandardParams datatypes.JSON `json:"standard_params"`
		Features       datatypes.JSON `json:"features"`
		Status         *int8          `json:"status"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		resp.ErrorMsg(c, http.StatusBadRequest, 400, err.Error())
		return
	}

	updates := map[string]any{}
	if req.Code != "" {
		updates["code"] = req.Code
	}
	if req.Name != "" {
		updates["name"] = req.Name
	}
	if req.Type != "" {
		updates["type"] = req.Type
	}
	if req.Description != "" {
		updates["description"] = req.Description
	}
	if len(req.ParamSchema) > 0 {
		updates["param_schema"] = req.ParamSchema
	} else if len(req.StandardParams) > 0 {
		updates["param_schema"] = req.StandardParams
	}
	if len(req.Features) > 0 {
		updates["features"] = req.Features
	}
	if req.Status != nil {
		updates["status"] = *req.Status
	}

	m, err := modelAdminService.UpdateModel(code, updates)
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}

	resp.Success(c, m)
}

// ReorderCapabilities 按传入 code 顺序调整能力排序 body: {"codes":["c1","c2"]}
func ReorderCapabilities(c *gin.Context) {
	var body struct {
		Codes []string `json:"codes" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		resp.ErrorMsg(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	if err := modelAdminService.ReorderCapabilities(body.Codes); err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}
	resp.Success(c, gin.H{"reordered": true})
}

// DeleteCapability 删除模型
func DeleteCapability(c *gin.Context) {
	rowsAffected, err := modelAdminService.DeleteModel(c.Param("code"))
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}
	if rowsAffected == 0 {
		resp.ErrorMsg(c, http.StatusNotFound, 404, "model not found")
		return
	}
	resp.Success(c, gin.H{"message": "deleted"})
}

package admin

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/service"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

var capabilityAdminService = service.NewCapabilityAdminService()

// ListCapabilities 能力列表
func ListCapabilities(c *gin.Context) {
	capabilities, err := capabilityAdminService.ListCapabilities(c.Query("status"))
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}
	resp.Success(c, capabilities)
}

// GetCapability 获取能力详情
func GetCapability(c *gin.Context) {
	capability, err := capabilityAdminService.GetCapability(c.Param("code"))
	if err != nil {
		resp.ErrorMsg(c, http.StatusNotFound, 404, "capability not found")
		return
	}
	resp.Success(c, capability)
}

// CreateCapability 创建能力
func CreateCapability(c *gin.Context) {
	var req service.CreateCapabilityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.ErrorMsg(c, http.StatusBadRequest, 400, err.Error())
		return
	}

	capability, err := capabilityAdminService.CreateCapability(&req)
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}

	resp.Success(c, capability)
}

// UpdateCapability 更新能力
func UpdateCapability(c *gin.Context) {
	code := c.Param("code")

	var req struct {
		Code             string         `json:"code"`
		Name             string         `json:"name"`
		Type             string         `json:"type"`
		Description      string         `json:"description"`
		StandardParams   datatypes.JSON `json:"standard_params"`
		StandardResponse datatypes.JSON `json:"standard_response"`
		Status           *int8          `json:"status"`
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
	if len(req.StandardParams) > 0 {
		updates["standard_params"] = req.StandardParams
	}
	if len(req.StandardResponse) > 0 {
		updates["standard_response"] = req.StandardResponse
	}
	if req.Status != nil {
		updates["status"] = *req.Status
	}

	capability, err := capabilityAdminService.UpdateCapability(code, updates)
	if err != nil {
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			resp.ErrorMsg(c, http.StatusNotFound, 404, "capability not found")
		case errors.Is(err, service.ErrCapabilityCodeRequired):
			resp.ErrorMsg(c, http.StatusBadRequest, 400, "capability code is required")
		case errors.Is(err, service.ErrCapabilityCodeConflict):
			resp.ErrorMsg(c, http.StatusConflict, 409, "capability code already exists")
		default:
			resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		}
		return
	}

	resp.Success(c, capability)
}

// DeleteCapability 删除能力
func DeleteCapability(c *gin.Context) {
	rowsAffected, err := capabilityAdminService.DeleteCapability(c.Param("code"))
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}
	if rowsAffected == 0 {
		resp.ErrorMsg(c, http.StatusNotFound, 404, "capability not found")
		return
	}
	resp.Success(c, gin.H{"message": "deleted"})
}

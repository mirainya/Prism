package admin

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/service"
	"gorm.io/datatypes"
)

// ========== ChatModel CRUD ==========

// ListChatModels GET /api/admin/chat-models
func ListChatModels(c *gin.Context) {
	models, err := modelAdminService.ListModelsByType("chat")
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}
	resp.Success(c, models)
}

// GetChatModel GET /api/admin/chat-models/:code
func GetChatModel(c *gin.Context) {
	m, err := modelAdminService.GetModel(c.Param("code"))
	if err != nil {
		resp.ErrorMsg(c, http.StatusNotFound, 404, "model not found")
		return
	}
	resp.Success(c, m)
}

// CreateChatModel POST /api/admin/chat-models
func CreateChatModel(c *gin.Context) {
	var req service.CreateModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.ErrorMsg(c, http.StatusBadRequest, 400, err.Error())
		return
	}

	m, err := modelAdminService.CreateModel(&req)
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}

	resp.Success(c, m)
}

// UpdateChatModel PUT /api/admin/chat-models/:code
func UpdateChatModel(c *gin.Context) {
	code := c.Param("code")

	var req struct {
		Name        string  `json:"name"`
		Provider    string  `json:"provider"`
		Description string  `json:"description"`
		Features    *[]string `json:"features"`
		MaxTokens   *int    `json:"max_tokens"`
		Status      *int8   `json:"status"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		resp.ErrorMsg(c, http.StatusBadRequest, 400, err.Error())
		return
	}

	updates := make(map[string]any)
	if req.Name != "" {
		updates["name"] = req.Name
	}
	if req.Provider != "" {
		updates["provider"] = req.Provider
	}
	if req.Description != "" {
		updates["description"] = req.Description
	}
	if req.Features != nil {
		featuresJSON, _ := json.Marshal(*req.Features)
		updates["features"] = datatypes.JSON(featuresJSON)
	}
	if req.MaxTokens != nil {
		updates["max_tokens"] = *req.MaxTokens
	}
	if req.Status != nil {
		updates["status"] = *req.Status
	}

	_, err := modelAdminService.UpdateModel(code, updates)
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}

	resp.Success(c, nil)
}

// GetChatModelPresets GET /api/admin/chat-models/presets?provider=openai
func GetChatModelPresets(c *gin.Context) {
	provider := c.Query("provider")
	if provider == "" {
		resp.ErrorMsg(c, http.StatusBadRequest, 400, "provider is required")
		return
	}
	presets := modelAdminService.GetPresets(provider)
	resp.Success(c, presets)
}

// QuickSetupChatModels POST /api/admin/chat-models/quick-setup
func QuickSetupChatModels(c *gin.Context) {
	var req service.QuickSetupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.ErrorMsg(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	result, err := modelAdminService.QuickSetup(&req)
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}
	resp.Success(c, result)
}

// DeleteChatModel DELETE /api/admin/chat-models/:code
func DeleteChatModel(c *gin.Context) {
	rowsAffected, err := modelAdminService.DeleteModel(c.Param("code"))
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}
	if rowsAffected == 0 {
		resp.ErrorMsg(c, http.StatusNotFound, 404, "model not found")
		return
	}
	resp.Success(c, nil)
}

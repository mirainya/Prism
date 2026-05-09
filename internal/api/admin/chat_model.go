package admin

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/service"
)

var chatAdminService = service.NewChatAdminService()

// ========== ChatModel CRUD ==========

// ListChatModels GET /api/admin/chat-models
func ListChatModels(c *gin.Context) {
	models, err := chatAdminService.ListChatModels()
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}
	resp.Success(c, models)
}

// GetChatModel GET /api/admin/chat-models/:code
func GetChatModel(c *gin.Context) {
	chatModel, err := chatAdminService.GetChatModel(c.Param("code"))
	if err != nil {
		resp.ErrorMsg(c, http.StatusNotFound, 404, "model not found")
		return
	}
	resp.Success(c, chatModel)
}

// CreateChatModel POST /api/admin/chat-models
func CreateChatModel(c *gin.Context) {
	var req service.CreateChatModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.ErrorMsg(c, http.StatusBadRequest, 400, err.Error())
		return
	}

	chatModel, err := chatAdminService.CreateChatModel(&req)
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}

	resp.Success(c, chatModel)
}

// UpdateChatModel PUT /api/admin/chat-models/:code
func UpdateChatModel(c *gin.Context) {
	code := c.Param("code")

	var req struct {
		Name        string `json:"name"`
		Provider    string `json:"provider"`
		Description string `json:"description"`
		Status      *int8  `json:"status"`
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
	if req.Status != nil {
		updates["status"] = *req.Status
	}

	rowsAffected, err := chatAdminService.UpdateChatModel(code, updates)
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

// GetChatModelPresets GET /api/admin/chat-models/presets?provider=openai
func GetChatModelPresets(c *gin.Context) {
	provider := c.Query("provider")
	if provider == "" {
		resp.ErrorMsg(c, http.StatusBadRequest, 400, "provider is required")
		return
	}
	presets := chatAdminService.GetPresets(provider)
	resp.Success(c, presets)
}

// QuickSetupChatModels POST /api/admin/chat-models/quick-setup
func QuickSetupChatModels(c *gin.Context) {
	var req service.QuickSetupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.ErrorMsg(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	result, err := chatAdminService.QuickSetup(&req)
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}
	resp.Success(c, result)
}

// DeleteChatModel DELETE /api/admin/chat-models/:code
func DeleteChatModel(c *gin.Context) {
	rowsAffected, err := chatAdminService.DeleteChatModel(c.Param("code"))
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

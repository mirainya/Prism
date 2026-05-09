package admin

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/service"
	"github.com/shopspring/decimal"
	"gorm.io/datatypes"
)

// ========== ChatModelChannel CRUD ==========

// ListChatModelChannels GET /api/admin/chat-model-channels
func ListChatModelChannels(c *gin.Context) {
	channels, err := chatAdminService.ListChatModelChannels(c.Query("model_code"), c.Query("channel_id"))
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}
	resp.Success(c, channels)
}

// GetChatModelChannel GET /api/admin/chat-model-channels/:id
func GetChatModelChannel(c *gin.Context) {
	mc, err := chatAdminService.GetChatModelChannel(c.Param("id"))
	if err != nil {
		resp.ErrorMsg(c, http.StatusNotFound, 404, "model channel not found")
		return
	}
	resp.Success(c, mc)
}

// CreateChatModelChannel POST /api/admin/chat-model-channels
func CreateChatModelChannel(c *gin.Context) {
	var req service.CreateChatModelChannelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.ErrorMsg(c, http.StatusBadRequest, 400, err.Error())
		return
	}

	mc, err := chatAdminService.CreateChatModelChannel(&req)
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}

	resp.Success(c, mc)
}

// UpdateChatModelChannel PUT /api/admin/chat-model-channels/:id
func UpdateChatModelChannel(c *gin.Context) {
	id := c.Param("id")

	var req struct {
		VendorModel    string           `json:"vendor_model"`
		Priority       *int             `json:"priority"`
		PriceMode      string           `json:"price_mode"`
		InputPrice     *decimal.Decimal `json:"input_price"`
		OutputPrice    *decimal.Decimal `json:"output_price"`
		RequestPath    string           `json:"request_path"`
		Timeout        *int             `json:"timeout"`
		SupportsStream *bool            `json:"supports_stream"`
		DefaultStream  *bool            `json:"default_stream"`
		ExtraHeaders   *datatypes.JSON  `json:"extra_headers"`
		ExtraConfig    *datatypes.JSON  `json:"extra_config"`
		Status         *int8            `json:"status"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		resp.ErrorMsg(c, http.StatusBadRequest, 400, err.Error())
		return
	}

	updates := make(map[string]any)
	if req.VendorModel != "" {
		updates["vendor_model"] = req.VendorModel
	}
	if req.Priority != nil {
		updates["priority"] = *req.Priority
	}
	if req.PriceMode != "" {
		updates["price_mode"] = req.PriceMode
	}
	if req.InputPrice != nil {
		updates["input_price"] = *req.InputPrice
	}
	if req.OutputPrice != nil {
		updates["output_price"] = *req.OutputPrice
	}
	if req.RequestPath != "" {
		updates["request_path"] = req.RequestPath
	}
	if req.Timeout != nil {
		updates["timeout"] = *req.Timeout
	}
	if req.SupportsStream != nil {
		updates["supports_stream"] = *req.SupportsStream
	}
	if req.DefaultStream != nil {
		updates["default_stream"] = *req.DefaultStream
	}
	if req.ExtraHeaders != nil {
		updates["extra_headers"] = *req.ExtraHeaders
	}
	if req.ExtraConfig != nil {
		updates["extra_config"] = *req.ExtraConfig
	}
	if req.Status != nil {
		updates["status"] = *req.Status
	}

	rowsAffected, err := chatAdminService.UpdateChatModelChannel(id, updates)
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}
	if rowsAffected == 0 {
		resp.ErrorMsg(c, http.StatusNotFound, 404, "model channel not found")
		return
	}

	resp.Success(c, nil)
}

// DeleteChatModelChannel DELETE /api/admin/chat-model-channels/:id
func DeleteChatModelChannel(c *gin.Context) {
	rowsAffected, err := chatAdminService.DeleteChatModelChannel(c.Param("id"))
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}
	if rowsAffected == 0 {
		resp.ErrorMsg(c, http.StatusNotFound, 404, "model channel not found")
		return
	}
	resp.Success(c, nil)
}

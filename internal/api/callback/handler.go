package callback

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/service"
)

var capabilityService = service.NewCapabilityService()

// HandleCapabilityCallback 处理供应商回调
func HandleCapabilityCallback(c *gin.Context) {
	channelType := c.Param("channel_type")

	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		resp.ErrorMsg(c, http.StatusBadRequest, 400, "invalid request body")
		return
	}

	err := capabilityService.HandleCallback(c.Request.Context(), channelType, body)
	if err != nil {
		resp.ErrorMsg(c, http.StatusBadRequest, 400, err.Error())
		return
	}

	resp.Success(c, gin.H{"message": "ok"})
}

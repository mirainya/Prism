package open

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/service"
)

var queryService = service.NewQueryService()

// ListAvailableChannels 列出所有可用渠道
func ListAvailableChannels(c *gin.Context) {
	result, err := queryService.ListAvailableChannels()
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, "failed to get channels")
		return
	}
	resp.Success(c, result)
}

// ListAvailableCapabilities 列出可用能力（返回能力及其支持的渠道）
func ListAvailableCapabilities(c *gin.Context) {
	channelType := c.Query("channel")
	capabilityType := c.Query("type")

	result, err := queryService.ListAvailableCapabilities(channelType, capabilityType)
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, "failed to get capabilities")
		return
	}
	resp.Success(c, result)
}

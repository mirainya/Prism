package console

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/service"
)

var queryServiceConsole = service.NewQueryService()

// ListCapabilityChannels 返回每个能力可用的渠道列表
func ListCapabilityChannels(c *gin.Context) {
	result, err := queryServiceConsole.ListCapabilityChannels()
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, "failed to get capability channels")
		return
	}
	resp.Success(c, result)
}

// ListChatModelChannelsForToken 返回每个 Chat 模型可用的渠道列表
func ListChatModelChannelsForToken(c *gin.Context) {
	result, err := queryServiceConsole.ListChatModelChannelsForToken()
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, "failed to get model channels")
		return
	}
	resp.Success(c, result)
}

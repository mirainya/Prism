package callback

import "github.com/gin-gonic/gin"

// RegisterRoutes 注册 /internal 回调认证路由
func RegisterRoutes(group *gin.RouterGroup) {
	group.POST("/callback/:channel_type", HandleCapabilityCallback)
}

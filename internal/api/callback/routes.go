package callback

import "github.com/gin-gonic/gin"

// RegisterRoutes 注册 /internal 回调认证路由
func RegisterRoutes(group *gin.RouterGroup) {
	group.POST("/callback/v1/:channel_type/:task_no/:signature", HandleCapabilityCallback)
}

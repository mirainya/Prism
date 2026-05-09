package open

import "github.com/gin-gonic/gin"

// RegisterRoutes 注册 /v1 Token 认证路由
func RegisterRoutes(group *gin.RouterGroup) {
	// 查询接口
	group.GET("/channels", ListAvailableChannels)
	group.GET("/capabilities", ListAvailableCapabilities)

	// 统一能力接口
	group.POST("/capabilities/:capability", InvokeCapability)

	// 任务管理
	group.GET("/tasks/:task_no", GetTaskByNo)
	group.POST("/tasks/:task_no/cancel", CancelTask)

	// 兼容旧接口
	group.POST("/images/generations", CreateImageGeneration)
	group.POST("/videos/generations", CreateVideoGeneration)

	// Chat 接口
	group.POST("/chat/completions", ChatCompletions)
	group.GET("/models", ListChatModelsPublic)
}

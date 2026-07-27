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

	// OpenAI 标准图像接口(同步返图,自动适配同步/异步渠道)
	group.POST("/images/generations", CreateImageGenerationOpenAI)
	group.POST("/images/edits", CreateImageEditOpenAI)
	group.POST("/videos/generations", CreateVideoGeneration)

	// Chat 接口
	// 注意: chat/completions 已切到网关 pipeline(见 router.go gw.RegisterChat),
	// 此处只保留模型元数据查询接口。
	group.GET("/models", ListChatModelsPublic)
	group.GET("/models/:code", GetChatModelDetail)
}

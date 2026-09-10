package open

import "github.com/gin-gonic/gin"

// RegisterRoutes 注册 /v1 Token 认证路由
func RegisterRoutes(group *gin.RouterGroup) {
	group.POST("/images/generations", CreateImageGenerationOpenAI)
	group.POST("/images/edits", CreateImageEditOpenAI)
	group.POST("/videos/generations", CreateVideoGeneration)
	group.POST("/videos/estimate", EstimateVideoGeneration)
	group.GET("/videos/generations", ListVideoGenerations)
	group.GET("/videos/generations/:id", GetVideoGeneration)
	group.GET("/videos/generations/:id/queue", GetVideoGenerationQueue)
	group.GET("/videos/queue", ListVideoQueue)
	group.POST("/videos/assets", CreateVideoAsset)
	group.POST("/videos/uploads", CreateVideoAsset)
	group.GET("/videos/assets/:asset_id", GetVideoAsset)
	group.DELETE("/videos/assets/:asset_id", DeleteVideoAsset)

	// Chat 接口
	// 注意: chat/completions 已切到网关 pipeline(见 router.go gw.RegisterChat),
	// 此处只保留模型元数据查询接口。
	group.GET("/models", ListChatModelsPublic)
	group.GET("/models/:code", GetChatModelDetail)
}

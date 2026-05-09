package admin

import "github.com/gin-gonic/gin"

// RegisterRoutes 注册 /api/admin 管理员路由（需要 JWT + Admin 认证）
func RegisterRoutes(group *gin.RouterGroup) {
	// 用户管理
	group.GET("/users", ListUsers)
	group.PUT("/users/:id/role", UpdateUserRole)
	group.PUT("/users/:id/status", UpdateUserStatus)
	group.POST("/users/:id/recharge", RechargeUser)

	// 渠道管理
	group.GET("/channels", ListChannels)
	group.GET("/channels/:id", GetChannel)
	group.POST("/channels", CreateChannel)
	group.PUT("/channels/:id", UpdateChannel)
	group.DELETE("/channels/:id", DeleteChannel)

	// 渠道账号管理
	group.GET("/channel-accounts", ListChannelAccounts)
	group.GET("/channel-accounts/:id", GetChannelAccount)
	group.POST("/channel-accounts", CreateChannelAccount)
	group.PUT("/channel-accounts/:id", UpdateChannelAccount)
	group.DELETE("/channel-accounts/:id", DeleteChannelAccount)

	// 能力管理
	group.GET("/capabilities", ListCapabilities)
	group.GET("/capabilities/:code", GetCapability)
	group.POST("/capabilities", CreateCapability)
	group.PUT("/capabilities/:code", UpdateCapability)
	group.DELETE("/capabilities/:code", DeleteCapability)

	// 渠道能力配置管理
	group.GET("/channel-capabilities", ListChannelCapabilities)
	group.GET("/channel-capabilities/:id", GetChannelCapability)
	group.POST("/channel-capabilities", CreateChannelCapability)
	group.PUT("/channel-capabilities/:id", UpdateChannelCapability)
	group.DELETE("/channel-capabilities/:id", DeleteChannelCapability)

	// 渠道请求日志
	group.GET("/request-logs", ListRequestLogs)
	group.GET("/request-logs/:id", GetRequestLog)
	group.POST("/request-logs/:id/retry", RetryRequest)

	// Chat 模型管理
	group.GET("/chat-models", ListChatModels)
	group.GET("/chat-models/presets", GetChatModelPresets)
	group.POST("/chat-models/quick-setup", QuickSetupChatModels)
	group.GET("/chat-models/:code", GetChatModel)
	group.POST("/chat-models", CreateChatModel)
	group.PUT("/chat-models/:code", UpdateChatModel)
	group.DELETE("/chat-models/:code", DeleteChatModel)

	// Chat 模型渠道映射
	group.GET("/chat-model-channels", ListChatModelChannels)
	group.GET("/chat-model-channels/:id", GetChatModelChannel)
	group.POST("/chat-model-channels", CreateChatModelChannel)
	group.PUT("/chat-model-channels/:id", UpdateChatModelChannel)
	group.DELETE("/chat-model-channels/:id", DeleteChatModelChannel)
}

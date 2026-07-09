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
	group.POST("/channels/reorder", ReorderChannels)
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
	group.GET("/channel-accounts/:id/circuit-states", ListAccountCircuitStates)
	group.DELETE("/channel-accounts/:id/circuit-states/:model_code", ClearAccountCircuitState)

	// 能力管理
	group.GET("/capabilities", ListCapabilities)
	group.POST("/capabilities/reorder", ReorderCapabilities)
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

	// 网关 v2 路由表(gw_*)管理:渠道/key/能力/元数据 + 以 key 为单位拉取导入
	gw := group.Group("/gw")
	{
		gw.GET("/channels", ListGwChannels)
		gw.POST("/channels", CreateGwChannel)
		gw.POST("/channels/reorder", ReorderGwChannels)
		gw.GET("/channels/:id", GetGwChannel)
		gw.PUT("/channels/:id", UpdateGwChannel)
		gw.DELETE("/channels/:id", DeleteGwChannel)
		gw.GET("/channels/:id/keys", ListGwKeys)
		gw.GET("/keys/:id/discover", DiscoverGwKeyModels) // 用该 key 调上游 /v1/models
		gw.POST("/keys/:id/import", ImportGwKeyModels)    // 导入选中模型到 gw_abilities

		gw.POST("/keys", CreateGwKey)
		gw.PUT("/keys/:id", UpdateGwKey)
		gw.DELETE("/keys/:id", DeleteGwKey)

		gw.GET("/abilities", ListGwAbilities) // ?model=&channel_id=&key_id=
		gw.PUT("/abilities/:id", UpdateGwAbility)
		gw.DELETE("/abilities/:id", DeleteGwAbility)

		gw.GET("/models", ListGwModels) // 对话模型页:可路由模型+元数据+可用性
		gw.POST("/models/reorder", ReorderGwModels)
		gw.GET("/model-meta", ListGwModelMeta)
		gw.PUT("/model-meta/:model_name", UpsertGwModelMeta)
		gw.DELETE("/model-meta/:model_name", DeleteGwModelMeta)
	}
}

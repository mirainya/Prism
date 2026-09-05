package admin

import "github.com/gin-gonic/gin"

// RegisterRoutes 注册 /api/admin 管理员路由（需要 JWT + Admin 认证）
func RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/unified-gateway/overview", UnifiedGatewayOverview)
	group.GET("/unified-gateway/catalog", UnifiedGatewayCatalog)
	group.POST("/unified-gateway/catalog/:id/publish", UnifiedGatewayPublishCatalog)
	group.POST("/unified-gateway/catalog/:id/retire", UnifiedGatewayRetireCatalog)
	group.GET("/unified-gateway/credentials", UnifiedGatewayCredentials)
	group.GET("/unified-gateway/calls", UnifiedGatewayCalls)
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
	group.GET("/channel-accounts/:id/discover", DiscoverChannelAccountModels)
	group.POST("/channel-accounts/:id/import", ImportChannelAccountModels)

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
	group.GET("/channel-capabilities/:id/discover", DiscoverEndpointModels)
	group.POST("/channel-capabilities/:id/import", ImportEndpointModels)

	// Endpoint-native aliases. The legacy channel-capabilities paths remain valid.
	group.GET("/endpoints", ListChannelCapabilities)
	group.GET("/endpoints/:id", GetChannelCapability)
	group.POST("/endpoints", CreateChannelCapability)
	group.PUT("/endpoints/:id", UpdateChannelCapability)
	group.DELETE("/endpoints/:id", DeleteChannelCapability)
	group.GET("/endpoints/:id/discover", DiscoverEndpointModels)
	group.POST("/endpoints/:id/import", ImportEndpointModels)

	// 渠道请求日志
	group.GET("/request-logs", ListRequestLogs)
	group.GET("/request-logs/:id", GetRequestLog)

	// 聊天网关路由表(gw_*)管理:渠道/key/能力/元数据 + 以 key 为单位拉取导入
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
		gw.GET("/abilities/:id/transports", ListGwAbilityTransports)
		gw.PUT("/abilities/:id/transports", UpsertGwAbilityTransport)
		gw.DELETE("/abilities/:id/transports/:transport", DeleteGwAbilityTransport)
		gw.POST("/abilities/:id/transports/:transport/check", ProbeGwAbilityTransport)

		gw.GET("/models", ListGwModels) // 对话模型页:可路由模型+元数据+可用性
		gw.POST("/models/reorder", ReorderGwModels)
		gw.DELETE("/models", DeleteGwModel) // ?name=xxx 避免 gin 把%2F decode 成路径分隔符
		gw.GET("/model-meta", ListGwModelMeta)
		gw.PUT("/model-meta/:model_name", UpsertGwModelMeta)
		gw.DELETE("/model-meta/:model_name", DeleteGwModelMeta)
	}

	// 视频引擎管理
	vid := group.Group("/video")
	{
		vid.GET("/channels", ListVideoChannels)
		vid.POST("/channels", CreateVideoChannel)
		vid.GET("/channels/:id", GetVideoChannel)
		vid.GET("/channels/:id/models/discover", DiscoverVideoChannelModels)
		vid.PUT("/channels/:id", UpdateVideoChannel)
		vid.DELETE("/channels/:id", DeleteVideoChannel)
		vid.GET("/channels/:id/keys", ListVideoChannelKeys)
		vid.POST("/channels/:id/keys", CreateVideoChannelKey)
		vid.PUT("/keys/:id", UpdateVideoKey)
		vid.DELETE("/keys/:id", DeleteVideoKey)
		vid.GET("/tasks", ListVideoTasks)
		vid.GET("/tasks/:id", GetVideoTask)
		vid.GET("/stats", GetVideoStats)
	}
}

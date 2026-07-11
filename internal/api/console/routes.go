package console

import "github.com/gin-gonic/gin"

// RegisterAuthRoutes 注册 /api/auth 认证路由（无需登录）
func RegisterAuthRoutes(group *gin.RouterGroup) {
	group.POST("/register", Register)
	group.POST("/login", Login)
	group.POST("/logout", Logout)
}

// RegisterPublicRoutes 注册 /api/public 公开路由（无需登录）
func RegisterPublicRoutes(group *gin.RouterGroup) {
	group.GET("/pricing", GetPricing)
}

// RegisterRoutes 注册 /api 控制台路由（需要 JWT 认证）
func RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/user/me", GetCurrentUser)
	group.PUT("/user/password", ChangePassword)
	group.GET("/tokens", ListMyTokens)
	group.POST("/tokens", CreateToken)
	group.GET("/tokens/:id", GetToken)
	group.PUT("/tokens/:id", UpdateToken)
	group.POST("/tokens/:id/recharge", RechargeToken)
	group.DELETE("/tokens/:id", DeleteToken)
	group.GET("/capability-channels", ListCapabilityChannels)

	// 仪表盘
	group.GET("/dashboard/stats", DashboardStats)
	group.GET("/dashboard/chat-stats", ChatStats)
	group.GET("/tasks", ListTasks)
	group.GET("/tasks/:task_no", GetTaskDetail)

	// 对话记录
	group.GET("/conversations", ListConversations)
	group.GET("/conversations/:id/messages", GetConversationMessages)

	// 文档
	group.GET("/docs/models", DocsListModels)

	// Playground 代理（通过 JWT + token_id，无需原始 API Key）
	group.GET("/playground/:token_id/models", PlaygroundListModels)
	group.GET("/playground/:token_id/capabilities", PlaygroundListCapabilities)
	group.GET("/playground/:token_id/conversations", PlaygroundListConversations)
	group.GET("/playground/:token_id/conversations/:conversation_id/messages", PlaygroundGetConversationMessages)
	group.GET("/playground/:token_id/debug/:request_log_id", PlaygroundGetDebug)
	group.POST("/playground/:token_id/chat/completions", PlaygroundChatCompletions)
	group.POST("/playground/:token_id/responses", PlaygroundResponses)
	group.POST("/playground/:token_id/messages", PlaygroundAnthropicMessages)
	group.POST("/playground/:token_id/upload", PlaygroundUploadFile)
	group.POST("/playground/:token_id/capabilities/:capability", PlaygroundInvokeCapability)
	group.GET("/playground/:token_id/tasks", PlaygroundListTasks)
	group.GET("/playground/:token_id/tasks/:task_no", PlaygroundGetTask)
	group.POST("/playground/:token_id/tasks/:task_no/cancel", PlaygroundCancelTask)
}

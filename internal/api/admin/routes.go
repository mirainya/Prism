package admin

import "github.com/gin-gonic/gin"

// RegisterRoutes 注册 /api/admin 管理员路由（需要 JWT + Admin 认证）
func RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/unified-gateway/overview", UnifiedGatewayOverview)
	group.GET("/unified-gateway/channels", UnifiedChannels)
	group.POST("/unified-gateway/channels", CreateUnifiedChannel)
	group.PUT("/unified-gateway/channels/:id", UpdateUnifiedChannel)
	group.GET("/unified-gateway/channels/:id/pools", UnifiedChannelPools)
	group.GET("/unified-gateway/channels/:id/relations", ListUnifiedChannelRelations)
	group.POST("/unified-gateway/channels/:id/pools", CreateUnifiedChannelPool)
	group.PUT("/unified-gateway/pools/:id", UpdateUnifiedPool)
	// v5 names these A-class edits PATCH operations. Keep the established PUT
	// routes for existing clients and expose the documented method as an alias.
	group.PATCH("/unified-gateway/pools/:id", UpdateUnifiedPool)
	group.PATCH("/unified-gateway/credential-pools/:id", UpdateUnifiedPool)
	group.POST("/unified-gateway/pools/:id/status", TransitionUnifiedPool)
	group.GET("/unified-gateway/catalog", UnifiedGatewayCatalog)
	group.POST("/unified-gateway/catalog", CreateUnifiedCatalogDraft)
	group.GET("/unified-gateway/catalog-sources", ListUnifiedCatalogSources)
	group.GET("/unified-gateway/catalog-sources/options", UnifiedCatalogSourceOptions)
	group.POST("/unified-gateway/catalog-sources", CreateUnifiedCatalogSource)
	group.POST("/unified-gateway/catalog-sources/:source_id/status", TransitionUnifiedCatalogSource)
	group.GET("/unified-gateway/catalog/:id", GetUnifiedCatalogRelease)
	group.GET("/unified-gateway/catalog/:id/discoveries", ListUnifiedCatalogDiscoveries)
	group.POST("/unified-gateway/catalog/:id/catalog-sources/:source_id/discover", ScheduleUnifiedCatalogDiscovery)
	group.GET("/unified-gateway/catalog-discoveries/:snapshot_id/items", ListUnifiedCatalogDiscoveryItems)
	group.POST("/unified-gateway/catalog-discoveries/:snapshot_id/review", ReviewUnifiedCatalogDiscovery)
	group.GET("/unified-gateway/catalog/:id/price-candidates", ListUnifiedCatalogPriceCandidates)
	group.POST("/unified-gateway/catalog-price-candidates/:candidate_id/review", ReviewUnifiedCatalogPriceCandidate)
	group.GET("/unified-gateway/catalog/:id/options", UnifiedCatalogOptions)
	group.GET("/unified-gateway/catalog/:id/model-entries", ListUnifiedCatalogModelEntries)
	group.GET("/unified-gateway/catalog/:id/skus", ListUnifiedCatalogSKUs)
	group.POST("/unified-gateway/catalog/:id/skus", CreateUnifiedCatalogSKU)
	group.PATCH("/unified-gateway/catalog/:id/skus/:sku_id", UpdateUnifiedCatalogSKU)
	group.DELETE("/unified-gateway/catalog/:id/skus/:sku_id", DeleteUnifiedCatalogSKU)
	group.GET("/unified-gateway/catalog/:id/products", ListUnifiedCatalogProducts)
	group.GET("/unified-gateway/catalog/:id/transports/:transport_id/allowed-hosts", ListUnifiedCatalogTransportAllowedHosts)
	group.POST("/unified-gateway/catalog/:id/products", CreateUnifiedCatalogProduct)
	group.DELETE("/unified-gateway/catalog/:id/products/:product_id", DeleteUnifiedCatalogProduct)
	group.GET("/unified-gateway/catalog/:id/rates", ListUnifiedCatalogRates)
	group.POST("/unified-gateway/catalog/:id/rates/:kind", CreateUnifiedCatalogRate)
	group.DELETE("/unified-gateway/catalog/:id/rates/:kind/:rate_id", DeleteUnifiedCatalogRate)
	// Configuration changes are applied to the active catalog in one transaction.
	// The handlers keep the catalog-scoped names for compatibility, but no longer
	// expose fork, publish, prove, activate, or rollback as operator steps.
	group.POST("/unified-gateway/catalog-changes/sell-rate", ChangeUnifiedSellRate)
	group.POST("/unified-gateway/catalog-changes/cost-rate", ChangeUnifiedCostRate)
	group.POST("/unified-gateway/catalog-changes/route-weight", ChangeUnifiedRouteWeight)
	group.POST("/unified-gateway/catalog-changes/sku-variant", ChangeUnifiedSKUVariant)
	group.POST("/unified-gateway/catalog-changes/sku-downstream-paths", ChangeUnifiedSKUDownstreamPaths)
	group.POST("/unified-gateway/catalog-changes/product", ChangeUnifiedProduct)
	group.POST("/unified-gateway/catalog-changes/product-create", CreateUnifiedActiveCatalogProduct)
	group.POST("/unified-gateway/catalog-changes/model-onboard", OnboardUnifiedCatalogModel)
	group.POST("/unified-gateway/catalog-changes/public-model-identities", ChangeUnifiedPublicModelIdentities)
	group.POST("/unified-gateway/catalog-changes/transport-allowed-hosts", ChangeUnifiedTransportAllowedHosts)
	group.POST("/unified-gateway/catalog-changes/transport-timeout", ChangeUnifiedTransportTimeout)
	group.GET("/unified-gateway/credentials", UnifiedGatewayCredentials)
	group.GET("/unified-gateway/currencies", ListUnifiedCurrencies)
	group.POST("/unified-gateway/currencies", CreateUnifiedCurrency)
	group.POST("/unified-gateway/currencies/:code/activate", ActivateUnifiedCurrency)
	group.GET("/unified-gateway/rate-evidence", ListUnifiedRateEvidence)
	group.POST("/unified-gateway/rate-evidence", CreateUnifiedRateEvidence)
	group.POST("/unified-gateway/rate-evidence/:id/review", ReviewUnifiedRateEvidence)
	group.POST("/unified-gateway/pools/:id/credentials", CreateUnifiedCredential)
	group.PUT("/unified-gateway/credentials/:id", UpdateUnifiedCredential)
	group.PATCH("/unified-gateway/credentials/:id", UpdateUnifiedCredential)
	group.POST("/unified-gateway/credentials/:id/status", TransitionUnifiedCredential)
	group.POST("/unified-gateway/offerings/:offering_id/credentials/:credential_id/validations/:kind", RecordUnifiedOfferingValidation)
	group.PATCH("/unified-gateway/offerings/:offering_id/runtime-state", SetUnifiedOfferingRuntimeState)
	group.PATCH("/unified-gateway/model-meta/:model_name", UpdateUnifiedModelMeta)
	group.GET("/unified-gateway/calls", UnifiedGatewayCalls)
	group.GET("/unified-gateway/calls/:id", UnifiedGatewayCallDetail)
	group.GET("/unified-gateway/calls/:id/requests", UnifiedCallRequestLogs)
	group.GET("/unified-gateway/calls/:id/requests/:request_id/payloads", UnifiedRequestLogPayloads)
	// Circuit breaker state. Engine writes gw_route_states and the candidate SQL
	// filters on it, so without these a correctly configured model can return 503
	// for the whole backoff window with nothing to look at.
	group.GET("/unified-gateway/route-states", ListUnifiedRouteStates)
	group.DELETE("/unified-gateway/route-states/:id", ClearUnifiedRouteState)
	group.DELETE("/unified-gateway/credentials/:id/route-states", ClearUnifiedCredentialRouteStates)
	// Adapter manifests. The catalog options endpoint only ever exposed the
	// 5-field Descriptor; everything that says what an adapter accepts and what
	// may be priced on lives in Manifest, which had no HTTP surface at all.
	group.GET("/unified-gateway/adapters", ListUnifiedAdapters)
	group.GET("/unified-gateway/adapters/:code/manifest", GetUnifiedAdapterManifest)
	// Key ↔ model, both directions. The chain runs credential → pool ← offering →
	// route → sku → model, which no endpoint walked; the console had to fan out
	// from the browser for one direction and could not express the other.
	group.GET("/unified-gateway/credentials/:id/models", ListUnifiedCredentialModels)
	group.GET("/unified-gateway/model-credentials", ListUnifiedModelCredentials)
	// 用户管理
	group.GET("/users", ListUsers)
	group.PUT("/users/:id/role", UpdateUserRole)
	group.PUT("/users/:id/status", UpdateUserStatus)
	group.POST("/users/:id/recharge", RechargeUser)

	// 视频任务是统一调用资源的只读投影。
	vid := group.Group("/video")
	{
		vid.GET("/tasks", ListUnifiedVideoTasks)
		vid.GET("/tasks/:id", GetUnifiedVideoTask)
		vid.POST("/tasks/:id/resolve", ResolveUnifiedVideoTask)
		vid.GET("/stats", GetUnifiedVideoStats)
	}
}

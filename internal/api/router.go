package api

import (
	"io"
	"io/fs"
	"net/http"
	"strings"

	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	consolefs "github.com/mirainya/Prism/console"
	"github.com/mirainya/Prism/internal/api/admin"
	"github.com/mirainya/Prism/internal/api/callback"
	"github.com/mirainya/Prism/internal/api/console"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/api/open"
	"github.com/mirainya/Prism/internal/gateway"
	"github.com/mirainya/Prism/internal/gateway/engine"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/video"
	"github.com/mirainya/Prism/pkg/cache"
	"github.com/mirainya/Prism/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func SetupRouter(executionEngine *engine.Engine, videoEngine *video.Engine) *gin.Engine {
	if executionEngine == nil {
		panic("Gateway V2 engine is required")
	}
	r := gin.New()

	// 全局中间件
	r.Use(middleware.CORS())
	r.Use(middleware.RequestID())

	// Gzip 压缩: 压缩控制台静态资源与 JSON API 响应。
	// 排除 AI 调用路径(/v1 流式 SSE)、内部回调与指标端点,避免破坏流式实时性。
	// 注册在 RequestLogger 之前(成为更外层),确保日志中间件捕获到的是未压缩的响应体。
	r.Use(gzip.Gzip(gzip.DefaultCompression, gzip.WithExcludedPaths([]string{
		"/v1",
		"/internal",
		"/metrics",
	})))

	r.Use(middleware.RequestLogger())
	r.Use(middleware.PersistentAccessLogger())
	// Recovery must stay inside the logging middleware so recovered 500s are observable.
	r.Use(gin.Recovery())
	r.Use(middleware.ErrorHandler())

	// 健康检查
	r.GET("/health", healthCheck)

	// Prometheus 指标
	registry := metrics.InitPrometheus()
	r.GET("/metrics", gin.WrapH(promhttp.HandlerFor(registry, promhttp.HandlerOpts{})))

	// 认证接口 (无需登录)
	console.RegisterAuthRoutes(r.Group("/api/auth"))

	// 公开接口 (无需登录)
	console.RegisterPublicRoutes(r.Group("/api/public"))

	// 提前构造网关: playground(console) 复用同一 pipeline,故先建再注入 console。
	gw := gateway.New(executionEngine)
	console.SetChatPipeline(gw.Pipeline())
	console.SetGatewayEngine(executionEngine)

	// 控制台 API (需要 JWT 认证)
	consoleGroup := r.Group("/api")
	consoleGroup.Use(middleware.JWTAuth())
	console.RegisterRoutes(consoleGroup)

	// 管理员专用 API
	adminGroup := r.Group("/api/admin")
	adminGroup.Use(middleware.JWTAuth(), middleware.AdminOnly())
	admin.SetVideoEngine(videoEngine)
	admin.RegisterRoutes(adminGroup)

	// v1 API (Token 鉴权，用于 AI 调用)。chat/completions 已切到网关 pipeline,
	// 其余(capabilities/images/videos/models)仍由 open 处理。
	apiV1 := r.Group("/v1")
	apiV1.Use(middleware.Auth(), middleware.RateLimitByToken())
	gw.RegisterChat(apiV1)
	gw.RegisterAnthropic(apiV1)
	gw.RegisterResponses(apiV1)
	gw.RegisterFiles(apiV1)
	open.InitVideoEngine(videoEngine)
	console.SetVideoEngine(videoEngine)
	open.RegisterRoutes(apiV1)

	// 内部接口 (上游回调)
	internalGroup := r.Group("/internal")
	internalGroup.Use(middleware.CallbackAuth())
	callback.RegisterRoutes(internalGroup)

	// 嵌入的前端静态文件
	distFS, _ := fs.Sub(consolefs.DistFS, "dist")
	fileServer := http.FileServer(http.FS(distFS))

	// 静态资源: 文件名含内容 hash(Vite 产物),可安全长期缓存
	r.GET("/assets/*filepath", func(c *gin.Context) {
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
		fileServer.ServeHTTP(c.Writer, c.Request)
	})

	// SPA 路由: 所有未匹配的路由返回 index.html
	r.NoRoute(func(c *gin.Context) {
		// API 路由返回 404
		if strings.HasPrefix(c.Request.URL.Path, "/api") {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		if strings.HasPrefix(c.Request.URL.Path, "/v1") ||
			// /v2 已退役，保留 API 404，避免回退到 SPA。
			strings.HasPrefix(c.Request.URL.Path, "/v2") ||
			strings.HasPrefix(c.Request.URL.Path, "/internal") {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}

		// 其他路由返回 index.html (SPA)
		indexFile, err := distFS.Open("index.html")
		if err != nil {
			c.String(http.StatusNotFound, "Console not found")
			return
		}
		defer indexFile.Close()

		stat, _ := indexFile.Stat()
		// index.html 引用带 hash 的资源,自身绝不能缓存,否则发版后用户拿到旧引用
		c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
		http.ServeContent(c.Writer, c.Request, "index.html", stat.ModTime(), indexFile.(io.ReadSeeker))
	})

	return r
}

func healthCheck(c *gin.Context) {
	dbOK := true
	if sqlDB, err := model.DB().DB(); err != nil || sqlDB.Ping() != nil {
		dbOK = false
	}
	redisOK := cache.Client.Ping(c).Err() == nil

	status := "ok"
	code := 200
	if !dbOK || !redisOK {
		status = "degraded"
		code = 503
	}
	c.JSON(code, gin.H{"status": status, "db": dbOK, "redis": redisOK})
}

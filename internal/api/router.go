package api

import (
	"io"
	"io/fs"
	"net/http"

	"github.com/gin-gonic/gin"
	consolefs "github.com/mirainya/Prism/console"
	"github.com/mirainya/Prism/internal/api/admin"
	"github.com/mirainya/Prism/internal/api/callback"
	"github.com/mirainya/Prism/internal/api/console"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/api/open"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/cache"
)

func SetupRouter() *gin.Engine {
	r := gin.New()

	// 全局中间件
	r.Use(gin.Recovery())
	r.Use(middleware.CORS())
	r.Use(middleware.RequestID())
	r.Use(middleware.RequestLogger())

	// 健康检查
	r.GET("/health", healthCheck)

	// 认证接口 (无需登录)
	console.RegisterAuthRoutes(r.Group("/api/auth"))

	// 公开接口 (无需登录)
	console.RegisterPublicRoutes(r.Group("/api/public"))

	// 控制台 API (需要 JWT 认证)
	consoleGroup := r.Group("/api")
	consoleGroup.Use(middleware.JWTAuth())
	console.RegisterRoutes(consoleGroup)

	// 管理员专用 API
	adminGroup := r.Group("/api/admin")
	adminGroup.Use(middleware.JWTAuth(), middleware.AdminOnly())
	admin.RegisterRoutes(adminGroup)

	// v1 API (Token 鉴权，用于 AI 调用)
	apiV1 := r.Group("/v1")
	apiV1.Use(middleware.Auth(), middleware.RateLimitByToken())
	open.RegisterRoutes(apiV1)

	// 内部接口 (上游回调)
	internalGroup := r.Group("/internal")
	internalGroup.Use(middleware.CallbackAuth())
	callback.RegisterRoutes(internalGroup)

	// 嵌入的前端静态文件
	distFS, _ := fs.Sub(consolefs.DistFS, "dist")
	fileServer := http.FileServer(http.FS(distFS))

	// 静态资源
	r.GET("/assets/*filepath", gin.WrapH(fileServer))

	// SPA 路由: 所有未匹配的路由返回 index.html
	r.NoRoute(func(c *gin.Context) {
		// API 路由返回 404
		if len(c.Request.URL.Path) >= 4 && c.Request.URL.Path[:4] == "/api" {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		if len(c.Request.URL.Path) >= 3 && c.Request.URL.Path[:3] == "/v1" {
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

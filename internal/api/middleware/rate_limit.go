package middleware

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/pkg/cache"
	"github.com/mirainya/Prism/pkg/config"
)

// RateLimit 基于 Redis 的固定窗口限流中间件
// keyFunc 用于从请求中提取限流 key（如 token ID 或 IP）
func RateLimit(keyFunc func(c *gin.Context) string) gin.HandlerFunc {
	return func(c *gin.Context) {
		cfg := config.C.RateLimit
		if !cfg.Enabled {
			c.Next()
			return
		}

		limit := cfg.RequestsPerMin
		if limit <= 0 {
			limit = 60
		}

		key := fmt.Sprintf("rate_limit:%s", keyFunc(c))
		ctx := c.Request.Context()

		count, err := cache.Client.Incr(ctx, key).Result()
		if err != nil {
			// Redis 异常时放行，不影响业务
			c.Next()
			return
		}

		if count == 1 {
			cache.Client.Expire(ctx, key, time.Minute)
		}

		c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", limit))
		c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", max(0, int64(limit)-count)))

		if count > int64(limit) {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"code":    429,
				"message": "rate limit exceeded, please try again later",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// RateLimitByToken 按 Token ID 限流（用于 v1 API）
func RateLimitByToken() gin.HandlerFunc {
	return RateLimit(func(c *gin.Context) string {
		if tokenID, exists := c.Get("token_id"); exists {
			return fmt.Sprintf("token:%v", tokenID)
		}
		return "ip:" + c.ClientIP()
	})
}

// RateLimitByIP 按 IP 限流（用于 auth API）
func RateLimitByIP() gin.HandlerFunc {
	return RateLimit(func(c *gin.Context) string {
		return "ip:" + c.ClientIP()
	})
}

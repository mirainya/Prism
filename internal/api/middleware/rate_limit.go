package middleware

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/pkg/cache"
	"github.com/mirainya/Prism/pkg/config"
)

// rateLimitScript 原子递增计数并在首次创建时设置过期时间(秒),返回当前计数
// 用 Lua 保证 INCR 与 EXPIRE 的原子性,避免两条命令间 Redis 故障导致 key 无过期时间
const rateLimitScript = `
local c = redis.call('INCR', KEYS[1])
if c == 1 then
  redis.call('EXPIRE', KEYS[1], ARGV[1])
end
return c
`

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

		// 原子 INCR + 首次设置过期,避免 Expire 单独失败导致 key 永不过期→永久限流
		count, err := cache.Client.Eval(ctx, rateLimitScript, []string{key}, 60).Int64()
		if err != nil {
			// Redis 异常时放行，不影响业务
			c.Next()
			return
		}

		c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", limit))
		c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", max(0, int64(limit)-count)))

		if count > int64(limit) {
			writeGatewayProtocolError(c, http.StatusTooManyRequests, "rate limit exceeded, please try again later", "rate_limit_error", "rate_limit_exceeded")
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

package middleware

import (
	"github.com/gin-gonic/gin"
)

func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Authorization, X-Request-ID, Idempotency-Key, Anthropic-Version, Anthropic-Beta, X-Prism-Conversation-ID, X-Prism-Thinking-Level")
		c.Header("Access-Control-Expose-Headers", "X-Request-ID, X-Prism-Call-ID, X-Prism-Request-Log-ID, X-Prism-Conversation-ID")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}

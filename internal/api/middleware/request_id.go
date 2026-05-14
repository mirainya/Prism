package middleware

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const RequestIDKey = "X-Request-ID"

type requestIDKeyType struct{}

var ctxRequestIDKey = requestIDKeyType{}

// RequestID 从请求头读取 X-Request-ID，没有则生成 UUID，写入 context 和响应头
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader(RequestIDKey)
		if requestID == "" {
			requestID = uuid.New().String()
		}
		c.Set(RequestIDKey, requestID)
		c.Header(RequestIDKey, requestID)
		// 写入 Go context 供 service 层使用
		ctx := context.WithValue(c.Request.Context(), ctxRequestIDKey, requestID)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// GetRequestID 从 context 获取 request ID
func GetRequestID(ctx context.Context) string {
	if id, ok := ctx.Value(ctxRequestIDKey).(string); ok {
		return id
	}
	return ""
}

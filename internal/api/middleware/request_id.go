package middleware

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const RequestIDKey = "X-Request-ID"

const maxRequestIDLength = 128

type requestIDKeyType struct{}

var ctxRequestIDKey = requestIDKeyType{}

// RequestID 从请求头读取 X-Request-ID，没有则生成 UUID，写入 context 和响应头
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := normalizeRequestID(c.GetHeader(RequestIDKey))
		c.Set(RequestIDKey, requestID)
		c.Header(RequestIDKey, requestID)
		// 写入 Go context 供 service 层使用
		ctx := context.WithValue(c.Request.Context(), ctxRequestIDKey, requestID)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

func normalizeRequestID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return uuid.NewString()
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return uuid.NewString()
		}
	}
	if len([]rune(value)) <= maxRequestIDLength {
		return value
	}
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("req_%x", sum[:16])
}

// GetRequestID 从 context 获取 request ID
func GetRequestID(ctx context.Context) string {
	if id, ok := ctx.Value(ctxRequestIDKey).(string); ok {
		return id
	}
	return ""
}

package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/pkg/logger"
	"github.com/mirainya/Prism/pkg/metrics"
	"go.uber.org/zap"
)

const (
	maxRequestBodyLogBytes  = 4096
	maxResponseBodyLogBytes = 1024
)

var sensitiveRequestPaths = map[string]struct{}{
	"/api/auth/login":    {},
	"/api/auth/register": {},
	"/api/user/password": {},
}

var sensitiveJSONKeys = map[string]struct{}{
	"authorization": {},
	"credential":    {},
	"key":           {},
	"passwd":        {},
	"password":      {},
	"pwd":           {},
	"secret":        {},
	"token":         {},
}

type replayReadCloser struct {
	io.Reader
	io.Closer
}

type responseWriter struct {
	gin.ResponseWriter
	body       *bytes.Buffer
	statusCode int
}

func (w *responseWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *responseWriter) Write(b []byte) (int, error) {
	if w.statusCode >= 400 {
		remaining := maxResponseBodyLogBytes - w.body.Len()
		if remaining > 0 {
			captured := b
			if len(captured) > remaining {
				captured = captured[:remaining]
			}
			_, _ = w.body.Write(captured)
		}
	}
	return w.ResponseWriter.Write(b)
}

func RequestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		requestPath := c.Request.URL.Path
		query := sanitizeAccessQuery(c.Request.URL.Query())

		// 读取请求体
		var requestBody []byte
		if shouldLogRequestBody(c.Request.Method, requestPath, c.GetHeader("Content-Type")) && c.Request.Body != nil {
			originalBody := c.Request.Body
			requestBody, _ = io.ReadAll(io.LimitReader(originalBody, maxRequestBodyLogBytes))
			c.Request.Body = &replayReadCloser{
				Reader: io.MultiReader(bytes.NewReader(requestBody), originalBody),
				Closer: originalBody,
			}
		}

		// 包装 ResponseWriter 以捕获响应
		rw := &responseWriter{
			ResponseWriter: c.Writer,
			body:           bytes.NewBuffer(nil),
		}
		c.Writer = rw

		// 处理请求
		c.Next()

		// 记录指标
		latency := time.Since(start)
		status := c.Writer.Status()
		statusStr := strconv.Itoa(status)
		path := c.FullPath()
		if path == "" {
			path = requestPath
		}

		metrics.APIRequestTotal.Inc(c.Request.Method, path, statusStr)
		metrics.APIRequestDuration.Observe(latency.Seconds(), c.Request.Method, path)

		// 记录日志
		fields := []zap.Field{
			zap.String("method", c.Request.Method),
			zap.String("path", path),
			zap.String("query", query),
			zap.Int("status", status),
			zap.Duration("latency", latency),
			zap.String("ip", c.ClientIP()),
		}

		// 附加 request_id
		if reqID, exists := c.Get(RequestIDKey); exists {
			fields = append(fields, zap.String("request_id", reqID.(string)))
		}

		// 非 GET 请求记录请求体
		if len(requestBody) > 0 && len(requestBody) < maxRequestBodyLogBytes {
			if sanitizedBody, ok := sanitizeRequestBody(requestBody); ok {
				fields = append(fields, zap.String("request", sanitizedBody))
			}
		}

		// 错误响应记录响应体
		if status >= 400 && rw.body.Len() > 0 {
			fields = append(fields, zap.String("response", rw.body.String()))
		}

		if status >= 500 {
			logger.Error("request error", fields...)
		} else if status >= 400 {
			logger.Warn("request warning", fields...)
		} else {
			logger.Info("request completed", fields...)
		}
	}
}

func shouldLogRequestBody(method, path, contentType string) bool {
	if method == http.MethodGet {
		return false
	}
	if isAIRequestPath(path) {
		return false
	}
	_, sensitive := sensitiveRequestPaths[strings.TrimRight(path, "/")]
	return !sensitive && isJSONContentType(contentType)
}

func isAIRequestPath(path string) bool {
	path = strings.TrimRight(path, "/")
	return path == "/v1" || strings.HasPrefix(path, "/v1/") ||
		path == "/api/playground" || strings.HasPrefix(path, "/api/playground/") ||
		path == "/internal/callback" || strings.HasPrefix(path, "/internal/callback/")
}

func isJSONContentType(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	return mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
}

func sanitizeRequestBody(body []byte) (string, bool) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return "", false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return "", false
	}
	redactSensitiveJSONFields(value)
	sanitized, err := json.Marshal(value)
	if err != nil {
		return "", false
	}
	return string(sanitized), true
}

func redactSensitiveJSONFields(value any) {
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			if isSensitiveJSONKey(key) {
				value[key] = "[REDACTED]"
				continue
			}
			redactSensitiveJSONFields(child)
		}
	case []any:
		for _, child := range value {
			redactSensitiveJSONFields(child)
		}
	}
}

func isSensitiveJSONKey(key string) bool {
	originalKey := strings.TrimSpace(key)
	normalized := strings.Map(func(r rune) rune {
		switch r {
		case '_', '-', ' ', '.':
			return -1
		default:
			return r
		}
	}, strings.ToLower(originalKey))

	if _, sensitive := sensitiveJSONKeys[normalized]; sensitive {
		return true
	}
	return strings.Contains(normalized, "password") ||
		strings.Contains(normalized, "secret") ||
		strings.Contains(normalized, "token") ||
		strings.Contains(normalized, "apikey") ||
		strings.Contains(normalized, "authorization") ||
		strings.Contains(normalized, "credential") ||
		strings.Contains(normalized, "signature") ||
		strings.Contains(normalized, "cookie") ||
		normalized == "sig" ||
		hasKeyFieldSuffix(originalKey)
}

func hasKeyFieldSuffix(key string) bool {
	lowerKey := strings.ToLower(key)
	return lowerKey == "key" ||
		strings.HasSuffix(lowerKey, "_key") ||
		strings.HasSuffix(lowerKey, "-key") ||
		strings.HasSuffix(lowerKey, ".key") ||
		strings.HasSuffix(lowerKey, " key") ||
		strings.HasSuffix(key, "Key") ||
		strings.HasSuffix(key, "KEY")
}

package middleware

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

const contextKeySkipPersistentAccessLog = "skip_persistent_access_log"

// SkipPersistentAccessLog marks a matched read-only route as excluded from persistence.
func SkipPersistentAccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(contextKeySkipPersistentAccessLog, true)
		c.Next()
	}
}

// PersistentAccessLogger records API metadata after authentication and request handling.
func PersistentAccessLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !shouldPersistAccess(c.Request.URL.Path) {
			c.Next()
			return
		}
		startedAt := time.Now()
		c.Next()
		if c.GetBool(contextKeySkipPersistentAccessLog) || !model.HasDB() {
			return
		}

		accessLog, auditEvent := buildPersistentRecords(c, startedAt)
		if err := model.DB().Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(accessLog).Error; err != nil {
				return err
			}
			if auditEvent != nil {
				return tx.Create(auditEvent).Error
			}
			return nil
		}); err != nil {
			logger.Error("failed to persist API access record",
				zap.String("request_id", accessLog.RequestID), zap.Error(err))
		}
	}
}

func buildPersistentRecords(c *gin.Context, startedAt time.Time) (*model.APIAccessLog, *model.AuditEvent) {
	path := c.Request.URL.Path
	route := c.FullPath()
	if route == "" {
		route = path
	}
	storedPath := path
	if routeHasSensitiveParameter(route) {
		storedPath = route
	}
	userID := GetUserID(c)
	tokenID := GetTokenID(c)
	actorType := "anonymous"
	if token := GetToken(c); token != nil {
		actorType = "token"
		if userID == 0 {
			userID = token.UserID
		}
	} else if userID > 0 {
		actorType = "user"
	} else if strings.HasPrefix(path, "/internal/") {
		actorType = "service"
	}
	status := c.Writer.Status()
	requestID := GetRequestID(c.Request.Context())
	access := &model.APIAccessLog{
		RequestID:  requestID,
		CallID:     c.Writer.Header().Get("X-Prism-Call-ID"),
		UserID:     userID,
		TokenID:    tokenID,
		ActorType:  actorType,
		Method:     c.Request.Method,
		Path:       truncateAccessValue(storedPath, 500),
		Route:      truncateAccessValue(route, 500),
		Query:      sanitizeAccessQuery(c.Request.URL.Query()),
		StatusCode: status,
		DurationMs: time.Since(startedAt).Milliseconds(),
		IP:         truncateAccessValue(c.ClientIP(), 64),
		UserAgent:  truncateAccessValue(c.Request.UserAgent(), 512),
		ErrorCode:  accessErrorCode(c),
		CreatedAt:  startedAt,
	}
	if !shouldCreateAuditEvent(c.Request.Method, path) {
		return access, nil
	}
	resourceType, resourceID := auditResource(c, route)
	metadata, _ := json.Marshal(map[string]any{"route": route})
	outcome := "success"
	if status >= http.StatusBadRequest {
		outcome = "failed"
	}
	return access, &model.AuditEvent{
		RequestID: requestID, ActorType: actorType,
		ActorUserID: userID, ActorTokenID: tokenID,
		Action:       c.Request.Method + " " + route,
		ResourceType: resourceType, ResourceID: resourceID,
		Outcome: outcome, HTTPStatus: status,
		IP: truncateAccessValue(c.ClientIP(), 64), Metadata: datatypes.JSON(metadata),
		CreatedAt: startedAt,
	}
}

func routeHasSensitiveParameter(route string) bool {
	for _, segment := range strings.Split(route, "/") {
		if strings.HasPrefix(segment, ":") && isSensitiveJSONKey(strings.TrimPrefix(segment, ":")) {
			return true
		}
	}
	return false
}

func accessErrorCode(c *gin.Context) string {
	if c == nil || c.Writer.Status() < http.StatusBadRequest {
		return ""
	}
	wrapper, ok := c.Writer.(*responseWriter)
	if !ok || wrapper.body == nil || wrapper.body.Len() == 0 {
		return ""
	}
	var body map[string]any
	if err := json.Unmarshal(wrapper.body.Bytes(), &body); err != nil {
		return ""
	}
	if nested, ok := body["error"].(map[string]any); ok {
		if value := firstAccessErrorValue(nested, "code", "type"); value != "" {
			return truncateAccessValue(value, 128)
		}
	}
	if value := firstAccessErrorValue(body, "code", "error"); value != "" {
		return truncateAccessValue(value, 128)
	}
	return ""
}

func firstAccessErrorValue(values map[string]any, keys ...string) string {
	for _, key := range keys {
		value, exists := values[key]
		if !exists || value == nil {
			continue
		}
		if text, ok := value.(string); ok {
			return strings.TrimSpace(text)
		}
		return fmt.Sprint(value)
	}
	return ""
}

func shouldPersistAccess(path string) bool {
	return path == "/api" || strings.HasPrefix(path, "/api/") ||
		path == "/v1" || strings.HasPrefix(path, "/v1/") ||
		path == "/internal" || strings.HasPrefix(path, "/internal/")
}

func shouldCreateAuditEvent(method, path string) bool {
	if method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions {
		return false
	}
	if path == "/api" || strings.HasPrefix(path, "/api/") {
		return true
	}
	if path == "/v1/files" || strings.HasPrefix(path, "/v1/files/") {
		return true
	}
	return method == http.MethodDelete || strings.HasSuffix(path, "/cancel")
}

func auditResource(c *gin.Context, route string) (string, string) {
	trimmed := strings.Trim(route, "/")
	parts := strings.Split(trimmed, "/")
	start := 0
	if len(parts) > 0 && (parts[0] == "api" || parts[0] == "v1") {
		start = 1
	}
	if len(parts) > start && parts[start] == "admin" {
		start++
	}
	resourceType := ""
	if len(parts) > start {
		resourceType = parts[start]
	}
	for _, name := range []string{"id", "code", "model_name", "task_no"} {
		if value := strings.TrimSpace(c.Param(name)); value != "" {
			return resourceType, truncateAccessValue(value, 128)
		}
	}
	return resourceType, ""
}

func sanitizeAccessQuery(values url.Values) string {
	if len(values) == 0 {
		return ""
	}
	copy := make(url.Values, len(values))
	for key, items := range values {
		if isSensitiveJSONKey(key) {
			copy[key] = []string{"[REDACTED]"}
			continue
		}
		copy[key] = make([]string, len(items))
		for index, item := range items {
			copy[key][index] = sanitizeAccessQueryValue(item)
		}
	}
	return copy.Encode()
}

func sanitizeAccessQueryValue(value string) string {
	trimmed := strings.TrimSpace(value)
	if json.Valid([]byte(trimmed)) {
		var decoded any
		if err := json.Unmarshal([]byte(trimmed), &decoded); err == nil {
			redactAccessQueryJSONValue(decoded)
			if encoded, err := json.Marshal(decoded); err == nil {
				return string(encoded)
			}
		}
	}
	return sanitizeAccessURL(value)
}

func redactAccessQueryJSONValue(value any) {
	switch current := value.(type) {
	case []any:
		for index := range current {
			if text, ok := current[index].(string); ok {
				current[index] = sanitizeAccessURL(text)
				continue
			}
			redactAccessQueryJSONValue(current[index])
		}
	case map[string]any:
		for key, child := range current {
			if isSensitiveJSONKey(key) {
				current[key] = "[REDACTED]"
				continue
			}
			if text, ok := child.(string); ok {
				current[key] = sanitizeAccessURL(text)
				continue
			}
			redactAccessQueryJSONValue(child)
		}
	}
}

func sanitizeAccessURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return value
	}
	changed := parsed.User != nil
	parsed.User = nil
	query := parsed.Query()
	for key := range query {
		if isSensitiveJSONKey(key) {
			query.Set(key, "[REDACTED]")
			changed = true
		}
	}
	if !changed {
		return value
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func truncateAccessValue(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	return value[:maxBytes]
}

package middleware

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func TestSanitizeAccessQueryRedactsNestedSignedURL(t *testing.T) {
	raw := sanitizeAccessQuery(url.Values{
		"redirect": {"https://user:password@storage.example/file?X-Amz-Signature=nested-secret&part=1"},
		"payload":  {`{"accessToken":"json-secret","url":"https://json-user:json-password@storage.example/file?sig=json-signature"}`},
	})
	values, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatal(err)
	}
	redirect := values.Get("redirect")
	nested, err := url.Parse(redirect)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(redirect, "nested-secret") || strings.Contains(redirect, "user") || nested.User != nil || nested.Query().Get("X-Amz-Signature") != "[REDACTED]" {
		t.Fatalf("nested signed URL was not sanitized: %q", redirect)
	}
	payload := values.Get("payload")
	for _, secret := range []string{"json-secret", "json-user", "json-password", "json-signature"} {
		if strings.Contains(payload, secret) {
			t.Fatalf("JSON query value retained %q: %s", secret, payload)
		}
	}
}

func TestPersistentAccessLoggerRedactsSensitivePathParameters(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:access-log-sensitive-path?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.APIAccessLog{}, &model.AuditEvent{}); err != nil {
		t.Fatal(err)
	}
	model.SetDB(db)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestID(), PersistentAccessLogger())
	router.POST("/internal/callback/v1/:channel_type/:task_no/:signature", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/internal/callback/v1/vendor/task-1/sensitive-signature", nil)
	router.ServeHTTP(recorder, request)

	var access model.APIAccessLog
	if err := db.First(&access).Error; err != nil {
		t.Fatal(err)
	}
	want := "/internal/callback/v1/:channel_type/:task_no/:signature"
	if access.Path != want || access.Route != want || strings.Contains(access.Path, "sensitive-signature") {
		t.Fatalf("sensitive path was persisted: %#v", access)
	}
}

func TestPersistentAccessLoggerRecordsRecoveredPanics(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:access-log-panic?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.APIAccessLog{}, &model.AuditEvent{}); err != nil {
		t.Fatal(err)
	}
	model.SetDB(db)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestID(), PersistentAccessLogger(), gin.Recovery())
	router.GET("/api/panic", func(*gin.Context) {
		panic("test panic")
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/panic", nil))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", recorder.Code)
	}

	var access model.APIAccessLog
	if err := db.First(&access).Error; err != nil {
		t.Fatal(err)
	}
	if access.StatusCode != http.StatusInternalServerError || access.Route != "/api/panic" {
		t.Fatalf("panic access log = %#v", access)
	}
}

func TestPersistentAccessLoggerStoresRedactedAccessAndAuditRecords(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:access-log?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.APIAccessLog{}, &model.AuditEvent{}); err != nil {
		t.Fatal(err)
	}
	model.SetDB(db)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestID(), PersistentAccessLogger())
	router.POST("/api/tokens/:id/recharge", func(c *gin.Context) {
		c.Set(ContextKeyUserID, uint(9))
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/tokens/12/recharge?apiKey=camel-secret&api_key=secret&mode=manual", nil)
	req.Header.Set(RequestIDKey, "req-access-1")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d", recorder.Code)
	}

	var access model.APIAccessLog
	if err := db.First(&access).Error; err != nil {
		t.Fatal(err)
	}
	if access.RequestID != "req-access-1" || access.UserID != 9 || access.Route != "/api/tokens/:id/recharge" {
		t.Fatalf("access = %#v", access)
	}
	if access.Query != "apiKey=%5BREDACTED%5D&api_key=%5BREDACTED%5D&mode=manual" {
		t.Fatalf("query = %q", access.Query)
	}

	var event model.AuditEvent
	if err := db.First(&event).Error; err != nil {
		t.Fatal(err)
	}
	if event.ActorUserID != 9 || event.ResourceType != "tokens" || event.ResourceID != "12" || event.Outcome != "success" {
		t.Fatalf("event = %#v", event)
	}
}

func TestPersistentAccessLoggerDoesNotAuditReadOnlyRequests(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:access-log-read?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.APIAccessLog{}, &model.AuditEvent{}); err != nil {
		t.Fatal(err)
	}
	model.SetDB(db)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestID(), PersistentAccessLogger())
	router.GET("/v1/files", func(c *gin.Context) { c.Status(http.StatusOK) })
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/files", nil))

	var accessCount, auditCount int64
	if err := db.Model(&model.APIAccessLog{}).Count(&accessCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.AuditEvent{}).Count(&auditCount).Error; err != nil {
		t.Fatal(err)
	}
	if accessCount != 1 || auditCount != 0 {
		t.Fatalf("access=%d audit=%d", accessCount, auditCount)
	}
}

func TestPersistentAccessLoggerSkipsMarkedAccessLogQueries(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:access-log-skip-query?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.APIAccessLog{}, &model.AuditEvent{}); err != nil {
		t.Fatal(err)
	}
	model.SetDB(db)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestID(), PersistentAccessLogger())
	router.GET("/api/observability/access-logs", SkipPersistentAccessLog(), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	router.GET("/api/observability/access-logs/:id", SkipPersistentAccessLog(), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	router.GET("/api/observability/audit-events", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	for _, path := range []string{
		"/api/observability/access-logs",
		"/api/observability/access-logs/12",
		"/api/observability/audit-events",
	} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d", path, recorder.Code)
		}
	}

	var logs []model.APIAccessLog
	if err := db.Find(&logs).Error; err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].Route != "/api/observability/audit-events" {
		t.Fatalf("persisted access logs = %#v", logs)
	}
}

func TestPersistentAccessLoggerExtractsStructuredErrorCode(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:access-log-error?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.APIAccessLog{}, &model.AuditEvent{}); err != nil {
		t.Fatal(err)
	}
	model.SetDB(db)
	previousLogger := logger.L
	logger.L = zap.NewNop()
	t.Cleanup(func() { logger.L = previousLogger })

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestID(), RequestLogger(), PersistentAccessLogger())
	router.POST("/v1/test", func(c *gin.Context) {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "invalid_value", "message": "bad"}})
	})
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/test", nil))

	var access model.APIAccessLog
	if err := db.First(&access).Error; err != nil {
		t.Fatal(err)
	}
	if access.ErrorCode != "invalid_value" {
		t.Fatalf("error code = %q", access.ErrorCode)
	}
}

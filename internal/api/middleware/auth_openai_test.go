package middleware

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/tokenauth"
	"gorm.io/gorm"
)

var authTestDBCounter int64

func TestAuthMissingHeaderUsesOpenAIError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(Auth())
	router.GET("/v1/models", func(c *gin.Context) { c.Status(http.StatusOK) })

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.Code)
	}
	body := response.Body.String()
	if !strings.Contains(body, `"type":"authentication_error"`) || !strings.Contains(body, `"code":"invalid_api_key"`) {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAuthSupportsAnthropicAPIKeyHeader(t *testing.T) {
	dsn := fmt.Sprintf("file:auth_anthropic_%d?mode=memory&cache=shared", atomic.AddInt64(&authTestDBCounter, 1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.Token{}); err != nil {
		t.Fatal(err)
	}
	model.SetDB(db)
	user := model.User{BaseModel: model.BaseModel{ID: 7}, Username: "anthropic-owner", Status: 1}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	rawKey, selector, digest, err := tokenauth.Generate()
	if err != nil {
		t.Fatal(err)
	}
	token := model.Token{UserID: 7, Selector: selector, SecretDigest: digest, SecretDigestVersion: tokenauth.DigestVersion, AuthVersion: 1, Status: 1}
	if err := db.Create(&token).Error; err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.Use(Auth())
	router.POST("/v1/messages", func(c *gin.Context) {
		if got := GetTokenID(c); got != token.ID {
			t.Fatalf("token id=%d, want %d", got, token.ID)
		}
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{}`))
	request.Header.Set("x-api-key", rawKey)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}

	now := time.Now().UTC()
	if err := db.Model(&model.Token{}).Where("id=?", token.ID).
		Updates(map[string]any{"status": 0, "revoked_at": now, "auth_version": gorm.Expr("auth_version + 1")}).Error; err != nil {
		t.Fatal(err)
	}
	revokedRequest := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{}`))
	revokedRequest.Header.Set("x-api-key", rawKey)
	revokedResponse := httptest.NewRecorder()
	router.ServeHTTP(revokedResponse, revokedRequest)
	if revokedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token status=%d body=%s", revokedResponse.Code, revokedResponse.Body.String())
	}
}

func TestAuthUsesAnthropicErrorShapeForMessages(t *testing.T) {
	router := gin.New()
	router.Use(Auth())
	router.POST("/v1/messages", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{}`)))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Type  string `json:"type"`
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Type != "error" || body.Error.Type != "authentication_error" {
		t.Fatalf("body=%s", response.Body.String())
	}
}

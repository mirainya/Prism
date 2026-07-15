package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/auth"
	"github.com/mirainya/Prism/pkg/cache"
	"github.com/mirainya/Prism/pkg/config"
	"gorm.io/gorm"
)

func TestJWTAuthRejectsPersistentlyRevokedSessionBeforeCacheLookup(t *testing.T) {
	previousCacheClient := cache.Client
	cache.Client = nil
	t.Cleanup(func() { cache.Client = previousCacheClient })

	database, err := gorm.Open(sqlite.Open("file:jwt_auth_session_version?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&model.User{}); err != nil {
		t.Fatal(err)
	}
	model.SetDB(database)

	user := &model.User{
		Username:       "revoked-session-user",
		Password:       "unused-test-hash",
		Role:           model.UserRoleUser,
		Status:         1,
		SessionVersion: 2,
	}
	if err := database.Create(user).Error; err != nil {
		t.Fatal(err)
	}

	previousConfig := config.C
	config.C = &config.Config{Server: config.ServerConfig{JWTSecret: "jwt-auth-version-test"}}
	t.Cleanup(func() { config.C = previousConfig })
	token, err := auth.GenerateTokenWithSessionVersion(user.ID, user.Username, string(user.Role), 1)
	if err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(JWTAuth())
	router.GET("/protected", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want %d", recorder.Code, http.StatusUnauthorized)
	}
	if !strings.Contains(recorder.Body.String(), "token has been revoked or expired") {
		t.Fatalf("response=%s", recorder.Body.String())
	}
}

package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/pkg/logger"
	"go.uber.org/zap"
)

func TestSetupRouterDoesNotServeSPAForLegacyCallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousLogger := logger.L
	logger.L = zap.NewNop()
	t.Cleanup(func() { logger.L = previousLogger })

	router := SetupRouter()
	request := httptest.NewRequest(http.MethodPost, "/internal/callback/legacy", strings.NewReader(`{"status":"SUCCESS"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusNotFound, response.Body.String())
	}
	if strings.Contains(strings.ToLower(response.Body.String()), "<html") {
		t.Fatalf("legacy callback received SPA HTML: %s", response.Body.String())
	}
}

func TestSetupRouterReturnsNotFoundForRetiredV2Chat(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousLogger := logger.L
	logger.L = zap.NewNop()
	t.Cleanup(func() { logger.L = previousLogger })

	router := SetupRouter()
	var hasV1Chat, hasV2Chat bool
	for _, route := range router.Routes() {
		if route.Method == http.MethodPost && route.Path == "/v1/chat/completions" {
			hasV1Chat = true
		}
		if route.Method == http.MethodPost && route.Path == "/v2/chat/completions" {
			hasV2Chat = true
		}
	}
	if !hasV1Chat {
		t.Fatal("/v1/chat/completions is not registered")
	}
	if hasV2Chat {
		t.Fatal("retired /v2/chat/completions is still registered")
	}

	request := httptest.NewRequest(http.MethodPost, "/v2/chat/completions", strings.NewReader(`{"model":"test","messages":[{"role":"user","content":"hi"}]}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusNotFound, response.Body.String())
	}
	if strings.Contains(strings.ToLower(response.Body.String()), "<html") {
		t.Fatalf("retired v2 route received SPA HTML: %s", response.Body.String())
	}
}

func TestSetupRouterRegistersResponsesAndFiles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousLogger := logger.L
	logger.L = zap.NewNop()
	t.Cleanup(func() { logger.L = previousLogger })
	router := SetupRouter()
	want := map[string]bool{
		"POST /v1/responses": false, "GET /v1/responses/:id": false, "DELETE /v1/responses/:id": false,
		"POST /v1/responses/:id/cancel": false, "GET /v1/responses/:id/input_items": false,
		"POST /v1/files": false, "GET /v1/files": false, "GET /v1/files/:id": false, "GET /v1/files/:id/content": false, "DELETE /v1/files/:id": false,
	}
	for _, route := range router.Routes() {
		key := route.Method + " " + route.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for route, registered := range want {
		if !registered {
			t.Errorf("route not registered: %s", route)
		}
	}
}

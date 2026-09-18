package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/gateway"
	"github.com/mirainya/Prism/pkg/logger"
	"go.uber.org/zap"
)

func TestSetupRouterDoesNotServeSPAForLegacyCallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousLogger := logger.L
	logger.L = zap.NewNop()
	t.Cleanup(func() { logger.L = previousLogger })

	router := setupRouterForTest(t)
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

func TestSetupRouterRedirectsLegacyUnifiedGatewayBrowserPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousLogger := logger.L
	logger.L = zap.NewNop()
	t.Cleanup(func() { logger.L = previousLogger })

	router := setupRouterForTest(t)
	request := httptest.NewRequest(http.MethodGet, "/unified-gateway/catalog?tab=pricing", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusFound)
	}
	if got := response.Header().Get("Location"); got != "/ops-console?tab=pricing" {
		t.Fatalf("location = %q, want %q", got, "/ops-console?tab=pricing")
	}
}

func TestSetupRouterReturnsNotFoundForRetiredV2Chat(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousLogger := logger.L
	logger.L = zap.NewNop()
	t.Cleanup(func() { logger.L = previousLogger })

	router := setupRouterForTest(t)
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
	router := setupRouterForTest(t)
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

func TestSetupRouterDoesNotExposeUnsupportedVideoMutations(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousLogger := logger.L
	logger.L = zap.NewNop()
	t.Cleanup(func() { logger.L = previousLogger })
	router := setupRouterForTest(t)
	unsupported := map[string]struct{}{
		"POST /v1/videos/generations/:id/cancel":                               {},
		"POST /v1/videos/generations/:id/priority-queue":                       {},
		"POST /api/playground/:token_id/videos/generations/:id/cancel":         {},
		"POST /api/playground/:token_id/videos/generations/:id/priority-queue": {},
	}
	for _, route := range router.Routes() {
		key := route.Method + " " + route.Path
		if _, found := unsupported[key]; found {
			t.Errorf("unsupported video mutation is registered: %s", key)
		}
	}
}

func setupRouterForTest(t *testing.T) *gin.Engine {
	t.Helper()
	executionEngine, err := gateway.NewV2Engine()
	if err != nil {
		t.Fatal(err)
	}
	return SetupRouter(executionEngine, func(context.Context) error { return nil })
}
